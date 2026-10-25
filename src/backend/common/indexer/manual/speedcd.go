// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// SpeedCD ports Jackett.Common/Indexers/Definitions/SpeedCD.cs.
//
// mechanism: html-login (two-step checkpoint form: username → token →
// password, browse/{cats}/q/{term} path-segment search, HTML table parsing)
//
// 澄清记录:
//   - upstream RequestLoginAndFollowRedirect follows the redirect chain;
//     the Go http stack follows redirects transparently, so the second-step
//     success check is ContentString.Contains("/browse.php")
//     (SpeedCD.cs:162) on the final body — no redirect observation needed.
//   - the stored login cookie header is only the redirect-hop Set-Cookie
//     pairs: upstream WebResult.Cookies extracts the Set-Cookie headers of a
//     single response (HttpWebClient.cs:220-238, with AllowAutoRedirect=false
//     at HttpWebClient.cs:85), so ConfigureIfOK(result.Cookies) at
//     SpeedCD.cs:162 saves e.g. "session=xyz"; the Go stack sees the final
//     (redirect-followed) response instead, whose Set-Cookie pairs are read.
//   - the login token regex matches the backslash-escaped quotes of the
//     JSON-encoded response body: name=\"a\" value=\"([^\"]+)\"
//     (SpeedCD.cs:140); ported verbatim.
//   - the login error message is the text of the first <h5> (SpeedCD.cs:166);
//     "Wrong Captcha!" in the body overrides it with the captcha message
//     (SpeedCD.cs:168-171). A missing <h5> falls back to "Login failed.".
//   - upstream sets wc.EmulateBrowser = false (SpeedCD.cs:72); the port
//     passes EmulateBrowser=false on the login requests only (the search
//     uses the default client behaviour, as upstream).
//   - the query term is a path segment: browse/{cat}/{cat}/[freeleech]
//     /[norar]/[deep]/q/{term}/[p/{page}] (SpeedCD.cs:181-239); the order of
//     the params matters and is preserved.
//   - upstream re-logins when the browse page lacks "/logout.php" and then
//     retries the search once (SpeedCD.cs:242-246); the port mirrors that.
//   - PublishDate uses the invariant layout "dddd, MMMM d, yyyy h:mmtt"
//     (title attribute minus " at") with AssumeUniversal → parsed as UTC
//     (SpeedCD.cs:261-272).
//   - the freeleech flag is a span containing the literal "[Freeleech]"
//     (AngleSharp :contains) — the port filters span texts manually.
//   - OnParseError aborts the whole query with "Parse error"
//     (SpeedCD.cs:286-289).
//
// Ported from: Definitions/SpeedCD.cs,
// Models/IndexerConfig/Bespoke/ConfigurationDataSpeedCD.cs.
type SpeedCD struct {
	indexer.Base
	cfg *speedCDConfig
}

type speedCDConfig struct {
	configdata.ConfigData
	Username        *configdata.StringItem
	Password        *configdata.StringItem
	Instructions    *configdata.InfoItem
	Freeleech       *configdata.BoolItem
	ExcludeArchives *configdata.BoolItem
	AccountActivity *configdata.InfoItem
}

const speedCDInstructions = `Speed.Cd have increased their security. If you are having problems please check the security tab in your
                    Speed.Cd profile. Eg. Geo Locking, your seedbox may be in a different country to the one where you login via your
                    web browser.<br><br>For best results, change the 'Torrents per page' setting to 100 in<br>'Profile Settings > Torrents'.
                    <br><br>This site may use Cloudflare DDoS Protection, therefore Jackett requires <a
                    href='https://github.com/Jackett/Jackett#configuring-flaresolverr' target='_blank'>FlareSolverr</a> to access it.`

const speedCDAccountActivity = "Accounts not being used for 3 months will be removed to make room for active members."

func newSpeedCDConfig() *speedCDConfig {
	freeleech := configdata.NewBoolItem("Search freeleech only")
	freeleech.Value = false
	excludeArchives := configdata.NewBoolItem("Exclude torrents with RAR files")
	excludeArchives.Value = false
	return &speedCDConfig{
		ConfigData:      *configdata.NewConfigData(),
		Username:        configdata.NewStringItem("Username"),
		Password:        configdata.NewStringItem("Password"),
		Instructions:    configdata.NewInfoItem("", speedCDInstructions),
		Freeleech:       freeleech,
		ExcludeArchives: excludeArchives,
		AccountActivity: configdata.NewInfoItem("Account Inactivity", speedCDAccountActivity),
	}
}

// NewSpeedCD builds the indexer (services.Factory shape).
func NewSpeedCD(deps services.Deps) (indexer.Indexer, error) {
	s := &SpeedCD{cfg: newSpeedCDConfig()}
	s.Base = indexer.Base{
		IDValue:          "speedcd",
		NameValue:        "Speed.cd",
		DescriptionValue: "Speed.cd is a Private site. Your home now!",
		SiteLinkValue:    "https://speed.cd/",
		DefaultSiteLink:  "https://speed.cd/",
		AltSiteLinks: []string{
			"https://speed.cd/",
			"https://speed.click/",
			"https://speeders.me/",
		},
		LangValue: "en-US",
		TypeValue: "private",
		Caps:      speedCDCaps(),
		Config:    s.cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	return s, nil
}

func speedCDCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{
		models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID,
	}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}

	c := caps.Categories
	c.AddCategoryMapping("1", models.CatMoviesOther, "Movies/XviD")
	c.AddCategoryMapping("42", models.CatMovies, "Movies/Packs")
	c.AddCategoryMapping("32", models.CatMovies, "Movies/Kids")
	c.AddCategoryMapping("43", models.CatMoviesHD, "Movies/HD")
	c.AddCategoryMapping("47", models.CatMovies, "Movies/DiVERSiTY")
	c.AddCategoryMapping("28", models.CatMoviesBluRay, "Movies/B-Ray")
	c.AddCategoryMapping("48", models.CatMovies3D, "Movies/3D")
	c.AddCategoryMapping("40", models.CatMoviesDVD, "Movies/DVD-R")
	c.AddCategoryMapping("56", models.CatMovies, "Movies/Anime")
	c.AddCategoryMapping("50", models.CatTVSport, "TV/Sports")
	c.AddCategoryMapping("52", models.CatTVHD, "TV/B-Ray")
	c.AddCategoryMapping("53", models.CatTVSD, "TV/DVD-R")
	c.AddCategoryMapping("41", models.CatTV, "TV/Packs")
	c.AddCategoryMapping("55", models.CatTV, "TV/Kids")
	c.AddCategoryMapping("57", models.CatTV, "TV/DiVERSiTY")
	c.AddCategoryMapping("49", models.CatTVHD, "TV/HD")
	c.AddCategoryMapping("2", models.CatTVSD, "TV/Episodes")
	c.AddCategoryMapping("30", models.CatTVAnime, "TV/Anime")
	c.AddCategoryMapping("25", models.CatPCISO, "Games/PC ISO")
	c.AddCategoryMapping("39", models.CatConsoleWii, "Games/Wii")
	c.AddCategoryMapping("45", models.CatConsolePS3, "Games/PS3")
	c.AddCategoryMapping("35", models.CatConsole, "Games/Nintendo")
	c.AddCategoryMapping("33", models.CatConsoleXBox360, "Games/XboX360")
	c.AddCategoryMapping("46", models.CatPCMobileOther, "Mobile")
	c.AddCategoryMapping("24", models.CatPC0day, "Apps/0DAY")
	c.AddCategoryMapping("51", models.CatPCMac, "Mac")
	c.AddCategoryMapping("54", models.CatBooks, "Educational")
	c.AddCategoryMapping("27", models.CatBooks, "Books-Mags")
	c.AddCategoryMapping("26", models.CatAudio, "Music/Audio")
	c.AddCategoryMapping("3", models.CatAudio, "Music/Flac")
	c.AddCategoryMapping("44", models.CatAudio, "Music/Pack")
	c.AddCategoryMapping("29", models.CatAudioVideo, "Music/Video")
	return caps
}

func (s *SpeedCD) searchURL() string { return s.SiteLink() + "browse/" }

// ApplyConfiguration ports upstream SpeedCD.ApplyConfiguration
// (SpeedCD.cs:119-124): login, then RequiresTesting.
func (s *SpeedCD) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := s.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := s.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := s.doLogin(ctx); err != nil {
		return indexer.ConfigCompleted, err
	}
	return indexer.ConfigRequiresTesting, nil
}

var (
	// upstream: @"name=\""a\"" value=\""([^""]+)\""" — the response body
	// contains backslash-escaped quotes (JSON-encoded HTML).
	scdTokenRe = regexp.MustCompile(`name=\\"a\\" value=\\"([^"]+)\\"`)
	scdReqTag  = regexp.MustCompile(`(?i)\[REQ(UEST)?\]`)
)

func scdBoolPtr(b bool) *bool { return &b }

// doLogin ports upstream DoLogin (SpeedCD.cs:126-175): first request with
// username, token extraction, second request with token + password.
func (s *SpeedCD) doLogin(ctx context.Context) error {
	if s.Client == nil {
		return errors.New("http client not configured")
	}
	noBrowser := scdBoolPtr(false)

	// first request with username
	result, err := s.Client.Do(ctx, s.ID(), &httpclient.Request{
		Method:         http.MethodPost,
		URL:            s.SiteLink() + "checkpoint/API",
		Body:           scdFormPairs{{"username", s.cfg.Username.Value}}.values(),
		Headers:        scdFormContentType,
		Referer:        s.SiteLink(),
		EmulateBrowser: noBrowser,
	})
	if err != nil {
		return err
	}

	tokenMatch := scdTokenRe.FindStringSubmatch(string(result.Body))
	if tokenMatch == nil {
		return errors.New("Error parsing the login form") //nolint:staticcheck // upstream SpeedCD.cs error text, kept 1:1
	}
	token := tokenMatch[1]

	// second request with token and password
	result, err = s.Client.Do(ctx, s.ID(), &httpclient.Request{
		Method:         http.MethodPost,
		URL:            s.SiteLink() + "checkpoint/",
		Body:           scdFormPairs{{"pwd", s.cfg.Password.Value}, {"a", token}}.values(),
		Headers:        scdFormContentType,
		Cookies:        scdSetCookiePairs(result.Headers),
		Referer:        s.SiteLink(),
		EmulateBrowser: noBrowser,
	})
	if err != nil {
		return err
	}

	// upstream ConfigureIfOK: login is OK when the (redirect-followed) body
	// contains "/browse.php".
	if bytes.Contains(result.Body, []byte("/browse.php")) {
		s.SetCookieHeader(scdSetCookiePairs(result.Headers))
		s.SetConfigured(true)
		s.SaveConfig(ctx)
		return nil
	}

	doc, docErr := goquery.NewDocumentFromReader(strings.NewReader(string(result.Body)))
	errorMessage := ""
	if docErr == nil {
		errorMessage = strings.TrimSpace(doc.Find("h5").First().Text())
	}
	if bytes.Contains(result.Body, []byte("Wrong Captcha!")) {
		errorMessage = "Captcha required due to a failed login attempt. Login via a browser to whitelist your IP and then reconfigure Jackett."
	}
	if errorMessage == "" {
		errorMessage = "Login failed."
	}
	s.SetConfigured(false)
	return &indexer.IndexerError{IndexerID: s.ID(), Kind: indexer.KindConfig, Message: errorMessage}
}

func (s *SpeedCD) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return s.ResultsForQueryWith(ctx, q, isMeta, s.perform)
}

func (s *SpeedCD) Test(ctx context.Context) error {
	return s.Base.Test(ctx, s.perform)
}

// perform ports upstream PerformQuery (SpeedCD.cs:177-291).
func (s *SpeedCD) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if s.Client == nil {
		return nil, errors.New("http client not configured")
	}

	// the order of the params is important!
	var qc []string

	qc = append(qc, s.Caps.Categories.MapTorznabCapsToTrackers(query, false)...)

	if s.cfg.Freeleech.Value {
		qc = append(qc, "freeleech")
	}

	if s.cfg.ExcludeArchives.Value {
		qc = append(qc, "norar")
	}

	if query.IsImdbQuery() {
		term := query.ImdbID

		if strings.TrimSpace(query.GetEpisodeSearchString()) != "" {
			term += " " + query.GetEpisodeSearchString()

			if query.Season != nil && *query.Season > 0 && query.Episode == "" {
				term += "*"
			}
		}

		qc = append(qc, "deep", "q", scdUrlEncode(strings.TrimSpace(term)))
	} else {
		term := query.GetQueryString()

		if strings.TrimSpace(query.GetEpisodeSearchString()) != "" && query.Season != nil && *query.Season > 0 && query.Episode == "" {
			term += "*"
		}

		qc = append(qc, "q", scdUrlEncode(strings.TrimSpace(term)))
	}

	if query.Limit > 0 && query.Offset > 0 {
		page := query.Offset/query.Limit + 1

		qc = append(qc, "p", strconv.Itoa(page))
	}

	requestURL := s.searchURL() + strings.Join(qc, "/")
	response, err := s.Client.Do(ctx, s.ID(), &httpclient.Request{
		URL:     requestURL,
		Cookies: s.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	if !bytes.Contains(response.Body, []byte("/logout.php")) { // re-login
		if err := s.doLogin(ctx); err != nil {
			return nil, err
		}
		response, err = s.Client.Do(ctx, s.ID(), &httpclient.Request{
			URL:     requestURL,
			Cookies: s.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
	}

	return s.parseSearchPage(response.Body)
}

// parseSearchPage ports the upstream row loop (SpeedCD.cs:248-291); a row
// failure aborts the query with "Parse error" (upstream OnParseError).
func (s *SpeedCD) parseSearchPage(body []byte) ([]*models.ReleaseInfo, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, s.parseFailure(string(body), err)
	}

	var releases []*models.ReleaseInfo
	var parseErr error
	doc.Find("div.boxContent > table > tbody > tr").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		release, err := s.parseRow(row)
		if err != nil {
			parseErr = err
			return false
		}
		releases = append(releases, release)
		return true
	})
	if parseErr != nil {
		return nil, s.parseFailure(string(body), parseErr)
	}
	return releases, nil
}

func (s *SpeedCD) parseRow(row *goquery.Selection) (*models.ReleaseInfo, error) {
	titleSel := row.Find(`td:nth-child(2) > div > a[href^="/t/"]`).First()
	title := scdCleanTitle(titleSel.Text())

	downloadHref := row.Find(`td:nth-child(4) a[href^="/download/"]`).First().AttrOr("href", "")
	link, err := url.Parse(s.SiteLink() + strings.TrimLeft(downloadHref, "/"))
	if err != nil {
		return nil, err
	}
	details, err := url.Parse(s.SiteLink() + strings.TrimLeft(titleSel.AttrOr("href", ""), "/"))
	if err != nil {
		return nil, err
	}
	seeders, _ := parseutil.CoerceInt(row.Find("td:nth-child(8)").Text())
	leechers, _ := parseutil.CoerceInt(row.Find("td:nth-child(9)").Text())
	pubDateStr := strings.ReplaceAll(row.Find(`td:nth-child(2) span[class^="elapsedDate"]`).First().AttrOr("title", ""), " at", "")
	catHref := row.Find("td:nth-child(1) a").First().AttrOr("href", "")
	parts := strings.Split(catHref, "/")
	cat := parts[len(parts)-1]

	// upstream: span:contains("[Freeleech]")
	downloadVolumeFactor := 1.0
	row.Find("td:nth-child(2) span").EachWithBreak(func(_ int, sp *goquery.Selection) bool {
		if strings.Contains(sp.Text(), "[Freeleech]") {
			downloadVolumeFactor = 0
			return false
		}
		return true
	})

	publishDate, err := time.Parse("Monday, January 2, 2006 3:04PM", pubDateStr)
	if err != nil {
		return nil, err
	}
	size := parseutil.GetBytes(row.Find("td:nth-child(6)").Text())
	grabs, _ := parseutil.CoerceInt(row.Find("td:nth-child(7)").Text())
	peers := seeders + leechers
	uploadVolumeFactor := 1.0
	minimumRatio := 1.0
	minimumSeedTime := int64(259200) // 72 hours

	return &models.ReleaseInfo{
		Guid:                 link,
		Link:                 link,
		Details:              details,
		Title:                title,
		Category:             s.Caps.Categories.MapTrackerCatToNewznab(cat),
		PublishDate:          publishDate,
		Size:                 &size,
		Grabs:                &grabs,
		Seeders:              &seeders,
		Peers:                &peers,
		DownloadVolumeFactor: &downloadVolumeFactor,
		UploadVolumeFactor:   &uploadVolumeFactor,
		MinimumRatio:         &minimumRatio,
		MinimumSeedTime:      &minimumSeedTime,
	}, nil
}

// parseFailure mirrors upstream OnParseError: log the dump, surface
// "Parse error".
func (s *SpeedCD) parseFailure(dump string, err error) error {
	s.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: s.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// scdCleanTitle ports upstream CleanTitle (SpeedCD.cs:293-298).
func scdCleanTitle(title string) string {
	title = scdReqTag.ReplaceAllString(title, "")
	return strings.Trim(title, " .")
}

// scdUrlEncode mirrors upstream WebUtilityHelpers.UrlEncode(term, Encoding)
// (SpeedCD.cs:216, SpeedCD.cs:228): UTF-8 bytes URL-encoded with the
// WebUtility.UrlEncode safe set — A-Za-z0-9 and -_.!'()* pass through
// (note the '*': shared.Encode would escape it as %2A), space becomes '+',
// everything else uppercase %XX (WebUtilityHelpers.cs:8-17).
func scdUrlEncode(s string) string {
	var b strings.Builder
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '!', c == '*', c == '\'', c == '(', c == ')':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0F])
		}
	}
	return b.String()
}

// --- form helpers (batch-scoped names: file-list test mode) -----------------

type scdFormPairs [][2]string

func (p scdFormPairs) values() url.Values {
	v := url.Values{}
	for _, kv := range p {
		v.Add(kv[0], kv[1])
	}
	return v
}

var scdFormContentType = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

// scdSetCookiePairs mirrors upstream WebResult.Cookies: every Set-Cookie
// header reduced to its name=value pair, last value per name, space-joined.
func scdSetCookiePairs(h http.Header) string {
	if h == nil {
		return ""
	}
	values := h.Values("Set-Cookie")
	last := map[string]string{}
	var order []string
	for _, v := range values {
		eq := strings.Index(v, "=")
		if eq < 0 {
			continue
		}
		name := v[:eq]
		pair := v
		if semi := strings.Index(v, ";"); semi >= 0 {
			pair = v[:semi]
		}
		if _, seen := last[name]; !seen {
			order = append(order, name)
		}
		last[name] = pair
	}
	pairs := make([]string, 0, len(order))
	for _, name := range order {
		pairs = append(pairs, last[name])
	}
	return strings.Join(pairs, " ")
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (s *SpeedCD) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
