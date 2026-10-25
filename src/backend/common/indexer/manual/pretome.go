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
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/text/encoding/htmlindex"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/dateutil"
	"vestt/common/utils/parseutil"
)

// PreToMe ports Jackett.Common/Indexers/Definitions/PreToMe.cs.
//
// mechanism: html-login (form login with a PIN number)
//
// 澄清记录:
//   - upstream Encoding = iso-8859-1: response bodies are decoded from
//     ISO-8859-1 before parsing (mirrors engine cardigann handling).
//   - upstream distinguishes "POST did not redirect at all" (→ "Login failed.
//     Did you use the PIN number…") from "redirected but no logout link"
//     (→ red-font table error or "Login failed"); the Go client stack
//     follows redirects transparently, so the two cases are collapsed:
//     red-font message when present, otherwise the PIN error message.
//   - upstream re-logins when the search response IsRedirect; the Go client
//     stack follows redirects transparently, so this branch is not ported.
//
// Ported from: Definitions/PreToMe.cs.
type PreToMe struct {
	indexer.Base
	cfg *preToMeConfig
}

// preToMeConfig ports upstream ConfigurationDataPinNumber (Username +
// Password + Pin + Instructions) and the dynamic "Account Inactivity"
// display info (declared as fields instead of AddDynamic).
type preToMeConfig struct {
	configdata.ConfigData
	Username          *configdata.StringItem
	Password          *configdata.StringItem
	Pin               *configdata.StringItem
	Instructions      *configdata.InfoItem
	AccountInactivity *configdata.InfoItem
}

const preToMeInstructions = "For best results, change the 'Torrents per page' setting to 100 in 'Profile => Torrent browse settings'."

func newPreToMeConfig() *preToMeConfig {
	return &preToMeConfig{
		ConfigData:   *configdata.NewConfigData(),
		Username:     configdata.NewStringItem("Username"),
		Password:     configdata.NewStringItem("Password"),
		Pin:          configdata.NewStringItem("Login Pin Number"),
		Instructions: configdata.NewInfoItem("", preToMeInstructions),
		AccountInactivity: configdata.NewInfoItem("Account Inactivity",
			"Inactive accounts are removed after 28 days"),
	}
}

// NewPreToMe builds the indexer (services.Factory shape).
func NewPreToMe(deps services.Deps) (indexer.Indexer, error) {
	p := &PreToMe{cfg: newPreToMeConfig()}
	p.Base = indexer.Base{
		IDValue:          "pretome",
		NameValue:        "PreToMe",
		DescriptionValue: "PreToMe is a Private site for High Quality, High Definition (HD) MOVIES and TV Shows",
		SiteLinkValue:    "https://pretome.info/",
		DefaultSiteLink:  "https://pretome.info/",
		LangValue:        "en-US",
		TypeValue:        "private",
		EncodingName:     "iso-8859-1",
		Caps:             preToMeCaps(),
		Config:           p.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return p, nil
}

func preToMeCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories

	// Unfortunately most of them are tags not categories and they return the parent category
	// we have to re-add the tags with the parent category so the results are not removed with the filtering

	// Applications
	c.AddCategoryMapping("cat[]=22", models.CatPC, "Applications")
	c.AddCategoryMapping("cat[]=22&tags=Windows", models.CatPC0day, "Applications/Windows")
	c.AddCategoryMapping("cat[]=22&tags=MAC", models.CatPCMac, "Applications/MAC")
	c.AddCategoryMapping("cat[]=22&tags=Linux", models.CatPC, "Applications/Linux")

	// Ebooks
	c.AddCategoryMapping("cat[]=27", models.CatBooksEBook, "Ebooks")

	// Games
	// NOTE: Console 1000 category is not working well because it contains pc and console results mixed
	c.AddCategoryMapping("cat[]=4", models.CatConsole, "Games")
	c.AddCategoryMapping("cat[]=4&tags=PC", models.CatPCGames, "Games/PC")
	c.AddCategoryMapping("cat[]=4&tags=RIP", models.CatPCGames, "Games/RIP")
	c.AddCategoryMapping("cat[]=4&tags=ISO", models.CatPCGames, "Games/ISO")
	c.AddCategoryMapping("cat[]=4&tags=NSW", models.CatConsoleOther, "Games/NSW")
	c.AddCategoryMapping("cat[]=4&tags=GAMES-NSW", models.CatConsoleOther, "Games/NSW")
	c.AddCategoryMapping("cat[]=4&tags=XBOX360", models.CatConsoleXBox360, "Games/XBOX360")
	c.AddCategoryMapping("cat[]=4&tags=PS3", models.CatConsolePS3, "Games/PS3")
	c.AddCategoryMapping("cat[]=4&tags=Wii", models.CatConsoleWii, "Games/Wii")
	c.AddCategoryMapping("cat[]=4&tags=PSP", models.CatConsolePSP, "Games/PSP")
	c.AddCategoryMapping("cat[]=4&tags=NDS", models.CatConsoleNDS, "Games/NDS")
	c.AddCategoryMapping("cat[]=4&tags=Xbox", models.CatConsoleXBox, "Games/Xbox")
	c.AddCategoryMapping("cat[]=4&tags=PS2", models.CatConsoleOther, "Games/PS2")

	// Miscellaneous
	c.AddCategoryMapping("cat[]=31", models.CatOther, "Miscellaneous")
	c.AddCategoryMapping("cat[]=31&tags=Ebook", models.CatBooksEBook, "Miscellaneous/Ebook")
	c.AddCategoryMapping("cat[]=31&tags=RARFiX", models.CatOther, "Miscellaneous/RARFiX")

	// Movies
	c.AddCategoryMapping("cat[]=19", models.CatMovies, "Movies")
	c.AddCategoryMapping("cat[]=19&tags=1080p", models.CatMoviesHD, "Movies/1080p")
	c.AddCategoryMapping("cat[]=19&tags=720p", models.CatMoviesHD, "Movies/720p")
	c.AddCategoryMapping("cat[]=19&tags=2160p", models.CatMoviesUHD, "Movies/2160p")
	c.AddCategoryMapping("cat[]=19&tags=x264", models.CatMoviesHD, "Movies/x264")
	c.AddCategoryMapping("cat[]=19&tags=x265", models.CatMoviesHD, "Movies/x265")
	c.AddCategoryMapping("cat[]=19&tags=BluRay", models.CatMoviesHD, "Movies/BluRay")
	c.AddCategoryMapping("cat[]=19&tags=XviD", models.CatMoviesSD, "Movies/XviD")
	c.AddCategoryMapping("cat[]=19&tags=DVDRiP", models.CatMoviesSD, "Movies/DVDRiP")
	c.AddCategoryMapping("cat[]=19&tags=DVD", models.CatMoviesSD, "Movies/DVD")
	c.AddCategoryMapping("cat[]=19&tags=DVDR", models.CatMoviesSD, "Movies/DVDR")
	c.AddCategoryMapping("cat[]=19&tags=WMV", models.CatMovies, "Movies/WMV")
	c.AddCategoryMapping("cat[]=19&tags=CAM", models.CatMovies, "Movies/CAM")
	c.AddCategoryMapping("cat[]=19&tags=DolbyVision", models.CatMovies, "Movies/DolbyVision")

	// Music
	c.AddCategoryMapping("cat[]=6", models.CatAudio, "Music")
	c.AddCategoryMapping("cat[]=6&tags=MP3", models.CatAudioMP3, "Music/MP3")
	c.AddCategoryMapping("cat[]=6&tags=V2", models.CatAudioMP3, "Music/V2")
	c.AddCategoryMapping("cat[]=6&tags=FLAC", models.CatAudioLossless, "Music/FLAC")
	c.AddCategoryMapping("cat[]=6&tags=320kbps", models.CatAudioMP3, "Music/320kbps")

	// Sport
	c.AddCategoryMapping("cat[]=52", models.CatTVSport, "Sport")
	c.AddCategoryMapping("cat[]=52&tags=1080p", models.CatTVSport, "Sport/1080p")
	c.AddCategoryMapping("cat[]=52&tags=720p", models.CatTVSport, "Sport/720p")
	c.AddCategoryMapping("cat[]=52&tags=2160p", models.CatTVSport, "Sport/2160p")

	// TV
	c.AddCategoryMapping("cat[]=7", models.CatTV, "TV")
	c.AddCategoryMapping("cat[]=7&tags=1080p", models.CatTVHD, "TV/1080p")
	c.AddCategoryMapping("cat[]=7&tags=720p", models.CatTVHD, "TV/720p")
	c.AddCategoryMapping("cat[]=7&tags=2160p", models.CatTVUHD, "TV/2160p")
	c.AddCategoryMapping("cat[]=7&tags=x264", models.CatTVHD, "TV/x264")
	c.AddCategoryMapping("cat[]=7&tags=x265", models.CatTVHD, "TV/x265")
	c.AddCategoryMapping("cat[]=7&tags=HDTV", models.CatTVHD, "TV/HDTV")
	c.AddCategoryMapping("cat[]=7&tags=Documentary", models.CatTVDocumentary, "TV/Documentary")
	c.AddCategoryMapping("cat[]=7&tags=PDTV", models.CatTVSD, "TV/PDTV")
	c.AddCategoryMapping("cat[]=7&tags=BluRay", models.CatTVHD, "TV/BluRay")
	c.AddCategoryMapping("cat[]=7&tags=DolbyVision", models.CatTVUHD, "TV/DolbyVision")
	c.AddCategoryMapping("cat[]=7&tags=DVDRiP", models.CatTVSD, "TV/DVDRiP")
	c.AddCategoryMapping("cat[]=7&tags=XviD", models.CatTVSD, "TV/XviD")
	c.AddCategoryMapping("cat[]=7&tags=DVD", models.CatTVSD, "TV/DVD")
	c.AddCategoryMapping("cat[]=7&tags=HD-DVD", models.CatTVSD, "TV/HD-DVD")

	// XXX
	c.AddCategoryMapping("cat[]=51", models.CatXXX, "XXX")
	c.AddCategoryMapping("cat[]=51&tags=1080p", models.CatXXXx264, "XXX/1080p")
	c.AddCategoryMapping("cat[]=51&tags=720p", models.CatXXXx264, "XXX/720p")
	c.AddCategoryMapping("cat[]=51&tags=2160p", models.CatXXXUHD, "XXX/2160p")
	c.AddCategoryMapping("cat[]=51&tags=VR180", models.CatXXXOther, "XXX/VR180")
	c.AddCategoryMapping("cat[]=51&tags=XXX-VR180", models.CatXXXOther, "XXX/VR180")
	c.AddCategoryMapping("cat[]=51&tags=x264", models.CatXXXx264, "XXX/x264")
	c.AddCategoryMapping("cat[]=51&tags=x265", models.CatXXXx264, "XXX/x265")
	c.AddCategoryMapping("cat[]=51&tags=XviD", models.CatXXXXviD, "XXX/XviD")
	c.AddCategoryMapping("cat[]=51&tags=DVDRiP", models.CatXXXDVD, "XXX/DVDRiP")

	return caps
}

func (p *PreToMe) loginURL() string  { return p.SiteLink() + "takelogin.php" }
func (p *PreToMe) searchURL() string { return p.SiteLink() + "browse.php" }

// ApplyConfiguration ports upstream: GET the login page (seed cookies),
// POST the credentials + PIN, then verify the response carries the logout
// link. RequiresTesting (the manager runs the test afterwards).
func (p *PreToMe) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := p.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := p.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if p.Client == nil {
		return indexer.ConfigCompleted, errors.New("http client not configured")
	}

	if _, err := p.Client.Do(ctx, p.ID(), &httpclient.Request{
		URL:     p.loginURL(),
		Cookies: p.CookieHeader(),
	}); err != nil {
		return indexer.ConfigCompleted, err
	}

	pairs := formPairs{
		{"returnto", "%2F"},
		{"login_pin", p.cfg.Pin.Value},
		{"username", p.cfg.Username.Value},
		{"password", p.cfg.Password.Value},
		{"login", "Login"},
	}

	res, err := p.Client.Do(ctx, p.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     p.loginURL(),
		RawBody: pairs.encode(),
		Headers: formContentType,
		Cookies: p.CookieHeader(),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}

	if !bytes.Contains(res.Body, []byte("logout.php")) {
		errMessage := ""
		if doc, docErr := goquery.NewDocumentFromReader(bytes.NewReader(res.Body)); docErr == nil {
			errMessage = strings.TrimSpace(doc.Find(`table.body_table font[color~="red"]`).First().Text())
		}
		if errMessage == "" {
			errMessage = "Login failed. Did you use the PIN number that PreToMe emailed you?"
		}
		return indexer.ConfigCompleted, &indexer.IndexerError{IndexerID: p.ID(), Kind: indexer.KindConfig, Message: errMessage}
	}

	if u, err := url.Parse(p.SiteLink()); err == nil {
		p.SetCookieHeader(p.Client.CookieHeader(p.ID(), u))
	}
	p.SetConfigured(true)
	p.SaveConfig(ctx)
	return indexer.ConfigRequiresTesting, nil
}

func (p *PreToMe) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return p.ResultsForQueryWith(ctx, q, isMeta, p.perform)
}

func (p *PreToMe) Test(ctx context.Context) error {
	return p.Base.Test(ctx, p.perform)
}

var pretomeDateBrRe = regexp.MustCompile(`\<br[\s]{0,1}[\/]{0,1}\>`)

// perform ports upstream PreToMe.PerformQuery.
func (p *PreToMe) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if p.Client == nil {
		return nil, errors.New("http client not configured")
	}

	qc := shared.QueryParams{}
	qc.Add("st", "1") // search in title

	if query.IsImdbQuery() {
		qc.Add("search", query.ImdbID)
		qc.Add("sd", "1") // search in description
	} else {
		qc.Add("search", query.GetQueryString())
	}

	// parse categories and tags
	var catGroups []string
	var tagGroups []string
	for _, cat := range p.Caps.Categories.MapTorznabCapsToTrackers(query, false) {
		// "cat[]=7&tags=x264"
		cSplit := strings.SplitN(cat, "&", 2)

		gSplit := strings.SplitN(cSplit[0], "=", 2)
		if len(gSplit) > 1 && !pretomeContainsString(catGroups, gSplit[1]) {
			catGroups = append(catGroups, gSplit[1]) // category = 7
		}

		if len(cSplit) > 1 {
			tSplit := strings.SplitN(cSplit[1], "=", 2)
			if len(tSplit) > 1 && !pretomeContainsString(tagGroups, tSplit[1]) {
				tagGroups = append(tagGroups, tSplit[1]) // tag = x264
			}
		}
	}

	// add categories
	for _, cat := range catGroups {
		qc.Add("cat[]", cat)
	}

	// do not include too many tags as it'll mess with their servers
	if len(tagGroups) < 7 {
		qc.Add("tags", strings.Join(tagGroups, ","))
		// if tags are specified match any
		// if no tags are specified match all, with any we get random results
		tf := "all"
		if len(tagGroups) > 0 {
			tf = "any"
		}
		qc.Add("tf", tf)
	}

	enc, err := htmlindex.Get("iso-8859-1")
	if err != nil {
		return nil, err
	}

	res, err := p.Client.Do(ctx, p.ID(), &httpclient.Request{
		URL:     p.searchURL() + "?" + qc.GetQueryString(),
		Cookies: p.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	body, err := enc.NewDecoder().Bytes(res.Body)
	if err != nil {
		return nil, err
	}

	return p.parseSearchPage(query, body)
}

// parseSearchPage ports the upstream per-response parsing block
// (including OnParseError semantics).
func (p *PreToMe) parseSearchPage(query *models.TorznabQuery, body []byte) ([]*models.ReleaseInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, p.parseFailure(string(body), err)
	}

	var releases []*models.ReleaseInfo
	var parseErr error
	doc.Find("table > tbody > tr.browse").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		release, ok, err := p.parseRow(query, row)
		if err != nil {
			parseErr = err
			return false
		}
		if ok {
			releases = append(releases, release)
		}
		return true
	})
	if parseErr != nil {
		return nil, p.parseFailure(string(body), parseErr)
	}
	return releases, nil
}

// parseRow ports one browse row. The second return value is false when the
// row is skipped (missing title link / AND-filter mismatch).
func (p *PreToMe) parseRow(query *models.TorznabQuery, row *goquery.Selection) (*models.ReleaseInfo, bool, error) {
	qDetails := row.Find(`a[href^="details.php?id="]`).First()
	if qDetails.Length() == 0 {
		return nil, false, nil
	}
	title, ok := qDetails.Attr("title")
	if !ok || strings.TrimSpace(title) == "" {
		return nil, false, nil
	}

	// we have to skip bad titles due to tags + any word search
	if !query.MatchQueryStringAND(title, 0, "") {
		return nil, false, nil
	}

	details, err := url.Parse(p.SiteLink() + qDetails.AttrOr("href", ""))
	if err != nil {
		return nil, false, err
	}
	downloadHref := row.Find(`a[href^="download.php"]`).First().AttrOr("href", "")
	link, err := url.Parse(p.SiteLink() + downloadHref)
	if err != nil {
		return nil, false, err
	}

	innerHTML, _ := row.Find("td:nth-of-type(6)").Html()
	dateStr := strings.TrimSpace(pretomeDateBrRe.ReplaceAllString(innerHTML, " "))
	publishDate, err := dateutil.FromTimeAgo(dateStr)
	if err != nil {
		return nil, false, err
	}

	seeders := parseutil.MustCoerceLong(strings.TrimSpace(row.Find("td:nth-of-type(10)").Text()))
	leechers := parseutil.MustCoerceLong(strings.TrimSpace(row.Find("td:nth-of-type(11)").Text()))
	size := parseutil.GetBytes(row.Find("td:nth-of-type(8)").Text())
	files := parseutil.MustCoerceLong(row.Find("td:nth-of-type(4)").Text())
	grabs := parseutil.MustCoerceLong(row.Find("td:nth-of-type(9)").Text())
	peers := leechers + seeders

	cat := ""
	if catHref, ok := row.Find(`td:nth-of-type(1) a[href^="browse.php"]`).First().Attr("href"); ok {
		parts := strings.Split(catHref, "?")
		cat = parts[len(parts)-1]
	}

	minimumRatio := 0.75
	minimumSeedTime := int64(216000) // 60 hours
	return &models.ReleaseInfo{
		Title:                title,
		Details:              details,
		Guid:                 details,
		Link:                 link,
		PublishDate:          publishDate,
		Size:                 &size,
		Category:             p.Caps.Categories.MapTrackerCatToNewznab(cat),
		Files:                &files,
		Grabs:                &grabs,
		Seeders:              &seeders,
		Peers:                &peers,
		MinimumRatio:         &minimumRatio,
		MinimumSeedTime:      &minimumSeedTime,
		DownloadVolumeFactor: manualF64(0), // ratioless
		UploadVolumeFactor:   manualF64(1),
	}, true, nil
}

// log is a nil-safe logger (Base.Logger may be nil).
func (p *PreToMe) log() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// parseFailure mirrors upstream OnParseError.
func (p *PreToMe) parseFailure(dump string, err error) error {
	p.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: p.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

func pretomeContainsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
