// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/dateutil"
	"vestt/common/utils/parseutil"
)

// PixelHD ports Jackett.Common/Indexers/Definitions/PixelHD.cs.
//
// mechanism: html-login (private tracker; captcha login against login.php,
// torrents.php group/poster browse parsing, "time ago" added dates)
//
// Ported from: Definitions/PixelHD.cs,
// Models/IndexerConfig/ConfigurationDataCaptchaLogin.cs.
//
// 澄清记录:
//   - upstream detects an expired session via results.IsRedirect (the Go
//     http stack follows redirects transparently) and re-logins; the port
//     cannot observe the redirect, so the re-login branch is not reachable
//     (same approximation as shared/gazelle.go).
//   - upstream `span.warning[id!="no-cookies"]:has(br)` uses AngleSharp-only
//     pseudo selectors; the port filters span.warning elements by id and
//     descendant <br> manually (equivalent for the flat login-error markup).
//   - upstream reads the error via .TextContent without trimming; the port
//     keeps the text as-is (entities decoded by the HTML parser).
//   - upstream `new Uri(SiteLink + href)` concatenates raw strings; the port
//     does the same (base.Parse(SiteLink + href)).
//   - upstream adds "groupname" unconditionally after the imdbid/keyword
//     branch (NameValueCollection allows duplicate keys, so a keyword search
//     emits groupname twice); the port keeps the duplicate via
//     shared.QueryParams.
type PixelHD struct {
	indexer.Base
	cfg *pixelHDConfig

	// input_captcha / input_username / input_password: form field names
	// discovered from the live login page (upstream private fields).
	inputCaptcha  string
	hasCaptcha    bool
	inputUsername string
	inputPassword string
}

type pixelHDConfig struct {
	configdata.ConfigData
	Username      *configdata.StringItem
	Password      *configdata.StringItem
	CaptchaImage  *configdata.ImageItem
	CaptchaText   *configdata.StringItem
	CaptchaCookie *configdata.HiddenItem
	Instructions  *configdata.InfoItem
}

func newPixelHDConfig() *pixelHDConfig {
	return &pixelHDConfig{
		ConfigData:    *configdata.NewConfigData(),
		Username:      configdata.NewStringItem("Username"),
		Password:      configdata.NewStringItem("Password"),
		CaptchaImage:  configdata.NewImageItem("Captcha Image"),
		CaptchaText:   configdata.NewStringItem("Captcha Text"),
		CaptchaCookie: configdata.NewHiddenItem("Captcha Cookie"),
		Instructions:  configdata.NewInfoItem("", ""),
	}
}

// NewPixelHD builds the indexer (services.Factory shape).
func NewPixelHD(deps services.Deps) (indexer.Indexer, error) {
	p := &PixelHD{cfg: newPixelHDConfig()}
	p.Base = indexer.Base{
		IDValue:          "pixelhd",
		NameValue:        "PiXELHD",
		DescriptionValue: "PixelHD (PxHD) is a Private Torrent Tracker for HD .MP4 MOVIES / TV",
		SiteLinkValue:    "https://pixelhd.me/",
		DefaultSiteLink:  "https://pixelhd.me/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             pixelHDCaps(),
		Config:           p.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return p, nil
}

func pixelHDCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	c := caps.Categories
	c.AddCategoryMapping("1", models.CatMoviesHD, "Movies HD")
	return caps
}

func (p *PixelHD) loginURL() string  { return p.SiteLink() + "login.php" }
func (p *PixelHD) browseURL() string { return p.SiteLink() + "torrents.php" }

// ConfigurationForSetup ports upstream GetConfigurationForSetup: fetch the
// login page, discover the (rotating) form field names and the captcha.
func (p *PixelHD) ConfigurationForSetup(ctx context.Context) ([]map[string]any, error) {
	if err := p.fetchLoginForm(ctx); err != nil {
		return nil, err
	}
	return p.Base.ConfigurationForSetup(ctx)
}

// fetchLoginForm ports the upstream GetConfigurationForSetup body (also used
// by the session-refresh path).
func (p *PixelHD) fetchLoginForm(ctx context.Context) error {
	if p.Client == nil {
		return errors.New("http client not configured")
	}
	loginRes, err := p.Client.Do(ctx, p.ID(), &httpclient.Request{URL: p.loginURL()})
	if err != nil {
		return err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(loginRes.Body)))
	if err != nil {
		return err
	}

	p.cfg.CaptchaCookie.Value = pixCookieHeader(loginRes)

	captchaImg := doc.Find(`img[alt="FuckOff Image"]`).First()
	if captchaImg.Length() > 0 {
		captchaInput := doc.Find(`input[maxlength="6"]`).First()
		if captchaInput.Length() == 0 {
			return errors.New("PixelHD: captcha input not found")
		}
		p.inputCaptcha, _ = captchaInput.Attr("name")
		p.hasCaptcha = true

		src, _ := captchaImg.Attr("src")
		captchaRes, err := p.Client.Do(ctx, p.ID(), &httpclient.Request{
			URL:     p.SiteLink() + src,
			Referer: p.loginURL(),
			Cookies: p.cfg.CaptchaCookie.Value,
		})
		if err != nil {
			return err
		}
		p.cfg.CaptchaImage.Value = captchaRes.Body
	} else {
		p.hasCaptcha = false
		p.inputCaptcha = ""
		p.cfg.CaptchaImage.Value = nil
	}

	usernameInput := doc.Find(`input[maxlength="20"]`).First()
	if usernameInput.Length() == 0 {
		return errors.New("PixelHD: username input not found")
	}
	p.inputUsername, _ = usernameInput.Attr("name")

	passwordInput := doc.Find(`input[maxlength="40"]`).First()
	if passwordInput.Length() == 0 {
		return errors.New("PixelHD: password input not found")
	}
	p.inputPassword, _ = passwordInput.Attr("name")
	return nil
}

// ApplyConfiguration ports upstream: post the login form (username, password,
// keeplogged=1, optional captcha) and check for logout.php.
func (p *PixelHD) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := p.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := p.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	return p.applyLogin(ctx)
}

func (p *PixelHD) applyLogin(ctx context.Context) (indexer.ConfigStatus, error) {
	if p.inputUsername == "" {
		// field names are only known after fetching the login page
		if err := p.fetchLoginForm(ctx); err != nil {
			return indexer.ConfigCompleted, err
		}
	}
	pairs := url.Values{}
	pairs.Set(p.inputUsername, p.cfg.Username.Value)
	pairs.Set(p.inputPassword, p.cfg.Password.Value)
	pairs.Set("keeplogged", "1")
	if p.hasCaptcha {
		pairs.Set(p.inputCaptcha, p.cfg.CaptchaText.Value)
	}

	res, err := p.Client.Do(ctx, p.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     p.loginURL(),
		Body:    pairs,
		Cookies: p.cfg.CaptchaCookie.Value,
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	content := string(res.Body)

	if strings.Contains(content, "logout.php") {
		// see immortalseed.go: union of final-response Set-Cookie and the jar
		cookies := pixCookieHeader(res)
		if site, err := url.Parse(p.SiteLink()); err == nil {
			cookies = imsMergeCookies(cookies, p.Client.CookieHeader(p.ID(), site))
		}
		p.SetCookieHeader(cookies)
		p.SetConfigured(true)
		p.SaveConfig(ctx)
		return indexer.ConfigRequiresTesting, nil
	}

	return indexer.ConfigCompleted, errors.New(pixLoginError(content))
}

// pixLoginError ports upstream `span.warning[id!="no-cookies"]:has(br)`.
func pixLoginError(content string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return ""
	}
	message := ""
	doc.Find("span.warning").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if id, _ := s.Attr("id"); id == "no-cookies" {
			return true
		}
		if s.Find("br").Length() == 0 {
			return true
		}
		message = s.Text()
		return false
	})
	return message
}

func (p *PixelHD) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return p.ResultsForQueryWith(ctx, q, isMeta, p.perform)
}

func (p *PixelHD) Test(ctx context.Context) error {
	return p.Base.Test(ctx, p.perform)
}

var pixImdbRe = regexp.MustCompile(`tt(\d+)`)

func (p *PixelHD) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if p.Client == nil {
		return nil, errors.New("http client not configured")
	}
	searchString := query.GetQueryString()

	qc := shared.QueryParams{}
	qc.Add("order_by", "time")
	qc.Add("order_way", "desc")

	if strings.TrimSpace(query.ImdbID) != "" {
		qc.Add("imdbid", query.ImdbID)
	} else if strings.TrimSpace(searchString) != "" {
		qc.Add("groupname", searchString)
	}
	// upstream comment: "Add anyway after checking above?"
	qc.Add("groupname", searchString)

	searchURL := p.browseURL() + "?" + qc.GetQueryString()

	res, err := p.Client.Do(ctx, p.ID(), &httpclient.Request{
		URL:     searchURL,
		Cookies: p.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	return p.parseBrowse(string(res.Body))
}

// parseBrowse ports the upstream PerformQuery parsing block (whole block is
// inside one try/catch → OnParseError, mirrored here).
func (p *PixelHD) parseBrowse(content string) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return nil, p.parseError(content, err)
	}

	groups := doc.Find("div.browsePoster")
	for gi := 0; gi < groups.Length(); gi++ {
		group := groups.Eq(gi)

		poster := (*url.URL)(nil)
		if src, ok := group.Find("img.classBrowsePoster").First().Attr("src"); ok {
			poster, _ = url.Parse(p.SiteLink() + src)
		}

		var imdbID *int64
		if imdbLink := group.Find(`a[href*="www.imdb.com/title/tt"]`).First(); imdbLink.Length() > 0 {
			href, _ := imdbLink.Attr("href")
			if m := pixImdbRe.FindStringSubmatch(href); m != nil {
				if id, err := parseutil.CoerceInt(m[1]); err == nil {
					imdbID = &id
				}
			}
		}

		// upstream: strong:has(a[title="View Torrent"]) (AngleSharp :has)
		groupTitle := ""
		group.Find("strong").EachWithBreak(func(_ int, s *goquery.Selection) bool {
			if s.Find(`a[title="View Torrent"]`).Length() > 0 {
				groupTitle = strings.ReplaceAll(s.Text(), " ]", "]")
				return false
			}
			return true
		})

		// upstream: tr.group_torrent:has(a[href^="torrents.php?id="])
		rows := group.Find("tr.group_torrent").FilterFunction(func(_ int, s *goquery.Selection) bool {
			return s.Find(`a[href^="torrents.php?id="]`).Length() > 0
		})
		for ri := 0; ri < rows.Length(); ri++ {
			row := rows.Eq(ri)
			rel, err := p.parseRow(row, groupTitle, poster, imdbID)
			if err != nil {
				return nil, p.parseError(content, err)
			}
			releases = append(releases, rel)
		}
	}
	return releases, nil
}

func (p *PixelHD) parseRow(row *goquery.Selection, groupTitle string, poster *url.URL, imdbID *int64) (*models.ReleaseInfo, error) {
	title := row.Find(`a[href^="torrents.php?id="]`).First()
	if title.Length() == 0 {
		return nil, errors.New("row has no torrents.php?id link")
	}
	added := row.Find("td:nth-child(3)").First()
	sizeCell := row.Find("td:nth-child(4)").First()
	grabsCell := row.Find("td:nth-child(6)").First()
	seedersCell := row.Find("td:nth-child(7)").First()
	leechersCell := row.Find("td:nth-child(8)").First()
	download := row.Find(`a[href^="torrents.php?action=download"]`).First()
	if download.Length() == 0 {
		return nil, errors.New("row has no download link")
	}

	seeders := parseutil.MustCoerceLong(strings.TrimSpace(seedersCell.Text()))
	leechers := parseutil.MustCoerceLong(strings.TrimSpace(leechersCell.Text()))
	size := parseutil.GetBytes(sizeCell.Text())
	grabs := parseutil.MustCoerceLong(strings.TrimSpace(grabsCell.Text()))
	publishDate, err := dateutil.FromTimeAgo(added.Text())
	if err != nil {
		return nil, err
	}

	detailsHref, _ := title.Attr("href")
	details, err := url.Parse(p.SiteLink() + detailsHref)
	if err != nil {
		return nil, err
	}
	downloadHref, _ := download.Attr("href")
	link, err := url.Parse(p.SiteLink() + downloadHref)
	if err != nil {
		return nil, err
	}

	minimumRatio := 1.0
	minimumSeedTime := int64(72 * 60 * 60)
	peers := leechers + seeders
	return &models.ReleaseInfo{
		MinimumRatio:    &minimumRatio,
		MinimumSeedTime: &minimumSeedTime,
		Title:           groupTitle + " " + strings.TrimSpace(title.Text()),
		Category:        []int{models.CatMoviesHD.ID},
		Link:            link,
		Details:         details,
		Guid:            link,
		Size:            &size,
		Seeders:         &seeders,
		Peers:           &peers,
		Grabs:           &grabs,
		PublishDate:     publishDate,
		Poster:          poster,
		Imdb:            imdbID,
	}, nil
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (p *PixelHD) log() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// parseError mirrors upstream OnParseError.
func (p *PixelHD) parseError(dump string, err error) error {
	p.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: p.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// pixCookieHeader renders a response's Set-Cookie headers in
// "name=value; name2=value2" Cookie-header form (upstream WebResult.Cookies).
func pixCookieHeader(res *httpclient.Result) string {
	var parts []string
	for _, line := range res.Headers.Values("Set-Cookie") {
		if i := strings.IndexByte(line, ';'); i >= 0 {
			line = line[:i]
		}
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "; ")
}
