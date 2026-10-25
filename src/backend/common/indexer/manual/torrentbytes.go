package manual

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/text/encoding/htmlindex"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// TorrentBytes ports Jackett.Common/Indexers/Definitions/TorrentBytes.cs.
//
// mechanism: html-login (browse.php seeding for cookies, takelogin.php form
// login, iso-8859-1 responses, colhead-table browse parsing, FL green-font
// detection)
//
// Ported from: Definitions/TorrentBytes.cs,
// Models/IndexerConfig/ConfigurationDataBasicLogin.cs.
//
// 澄清记录:
//   - upstream Encoding = iso-8859-1: response bodies are decoded from
//     ISO-8859-1 before parsing.
//   - upstream reads the category href with Substring(15)
//     ("browse.php?cat=" prefix, TorrentBytes.cs:185); the port uses
//     strings.TrimPrefix with the same length.
//   - upstream re-logins when the search response IsRedirect
//     (TorrentBytes.cs:153-157); the Go client stack follows redirects
//     transparently, so this branch is not ported.
//   - upstream OnParseError throws, aborting PerformQuery; the port returns
//     an IndexerError of KindParse ("Parse error") instead.
type TorrentBytes struct {
	indexer.Base
	cfg *torrentBytesConfig
}

// torrentBytesConfig ports upstream ConfigurationDataBasicLogin with the
// "For best results..." instruction message.
type torrentBytesConfig struct {
	configdata.ConfigData
	Username     *configdata.StringItem
	Password     *configdata.StringItem
	Instructions *configdata.InfoItem
}

const torrentBytesInstructions = "For best results, change the 'Torrents per page' setting to 100 in your profile on the TorrentBytes webpage."

func newTorrentBytesConfig() *torrentBytesConfig {
	return &torrentBytesConfig{
		ConfigData:   *configdata.NewConfigData(),
		Username:     configdata.NewStringItem("Username"),
		Password:     configdata.NewStringItem("Password"),
		Instructions: configdata.NewInfoItem("", torrentBytesInstructions),
	}
}

// NewTorrentBytes builds the indexer (services.Factory shape).
func NewTorrentBytes(deps services.Deps) (indexer.Indexer, error) {
	t := &TorrentBytes{cfg: newTorrentBytesConfig()}
	t.Base = indexer.Base{
		IDValue:          "torrentbytes",
		NameValue:        "TorrentBytes",
		DescriptionValue: "TorrentBytes is a Private site. A decade of TorrentBytes",
		SiteLinkValue:    "https://www.torrentbytes.net/",
		DefaultSiteLink:  "https://www.torrentbytes.net/",
		LangValue:        "en-US",
		TypeValue:        "private",
		EncodingName:     "iso-8859-1",
		Caps:             torrentBytesCaps(),
		Config:           t.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return t, nil
}

func torrentBytesCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	c := caps.Categories
	c.AddCategoryMapping("23", models.CatTVAnime, "Anime")
	c.AddCategoryMapping("52", models.CatPCMac, "Apple/All")
	c.AddCategoryMapping("22", models.CatPC, "Apps/misc")
	c.AddCategoryMapping("1", models.CatPC, "Apps/PC")
	c.AddCategoryMapping("28", models.CatTVForeign, "Foreign Titles")
	c.AddCategoryMapping("50", models.CatConsole, "Games/Consoles")
	c.AddCategoryMapping("42", models.CatPCGames, "Games/Pack")
	c.AddCategoryMapping("4", models.CatPCGames, "Games/PC")
	c.AddCategoryMapping("51", models.CatPC, "Linux/All")
	c.AddCategoryMapping("31", models.CatOtherMisc, "Misc")
	c.AddCategoryMapping("20", models.CatMoviesDVD, "Movies/DVD-R")
	c.AddCategoryMapping("12", models.CatMoviesBluRay, "Movies/Full Blu-ray")
	c.AddCategoryMapping("5", models.CatMoviesHD, "Movies/HD")
	c.AddCategoryMapping("40", models.CatMovies, "Movies/Pack")
	c.AddCategoryMapping("19", models.CatMoviesSD, "Movies/SD")
	c.AddCategoryMapping("49", models.CatMoviesUHD, "Movies/UHD")
	c.AddCategoryMapping("25", models.CatAudio, "Music/DVDR")
	c.AddCategoryMapping("48", models.CatAudioLossless, "Music/Flac")
	c.AddCategoryMapping("6", models.CatAudioMP3, "Music/MP3")
	c.AddCategoryMapping("43", models.CatAudio, "Music/Pack")
	c.AddCategoryMapping("34", models.CatAudioVideo, "Music/Videos")
	c.AddCategoryMapping("45", models.CatMoviesBluRay, "NonScene/BRrip")
	c.AddCategoryMapping("46", models.CatMoviesHD, "NonScene/x264")
	c.AddCategoryMapping("44", models.CatMoviesSD, "NonScene/Xvid")
	c.AddCategoryMapping("37", models.CatTVHD, "TV/BRrip")
	c.AddCategoryMapping("38", models.CatTVHD, "TV/HD")
	c.AddCategoryMapping("41", models.CatTV, "TV/Pack")
	c.AddCategoryMapping("33", models.CatTVSD, "TV/SD")
	c.AddCategoryMapping("32", models.CatTVUHD, "TV/UHD")
	c.AddCategoryMapping("39", models.CatXXXx264, "XXX/HD")
	c.AddCategoryMapping("24", models.CatXXXImageSet, "XXX/IMGSET")
	c.AddCategoryMapping("21", models.CatXXXPack, "XXX/Pack")
	c.AddCategoryMapping("9", models.CatXXXXviD, "XXX/SD")
	c.AddCategoryMapping("29", models.CatXXX, "XXX/Web")
	return caps
}

// log is a nil-safe logger (Base.Logger may be nil).
func (t *TorrentBytes) log() *slog.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return slog.Default()
}

func (t *TorrentBytes) loginURL() string  { return t.SiteLink() + "takelogin.php" }
func (t *TorrentBytes) searchURL() string { return t.SiteLink() + "browse.php" }

// ApplyConfiguration ports upstream TorrentBytes.ApplyConfiguration: seed the
// cookie jar with a GET of the site, POST username/password (+returnto/login)
// to takelogin.php, then verify the response contains "my.php".
// RequiresTesting (the manager runs the test afterwards).
func (t *TorrentBytes) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := t.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := t.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if t.Client == nil {
		return indexer.ConfigCompleted, errors.New("http client not configured")
	}

	// upstream requests the site first to seed the session cookie
	// (TorrentBytes.cs:113)
	loginPage, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     t.SiteLink(),
		Cookies: t.CookieHeader(),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}

	pairs := url.Values{}
	pairs.Set("username", t.cfg.Username.Value)
	pairs.Set("password", t.cfg.Password.Value)
	pairs.Set("returnto", "/")
	pairs.Set("login", "Log in!")

	res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     t.loginURL(),
		Body:    pairs,
		Referer: t.SiteLink(),
		Cookies: torrentBytesCookies(t.CookieHeader(), loginPage),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	content := string(res.Body)

	if strings.Contains(content, "my.php") {
		cookies := torrentBytesCookieHeaderValue(res)
		if site, err := url.Parse(t.SiteLink()); err == nil {
			cookies = torrentBytesMergeCookies(cookies, t.Client.CookieHeader(t.ID(), site))
		}
		t.SetCookieHeader(cookies)
		t.SetConfigured(true)
		t.SaveConfig(ctx)
		return indexer.ConfigRequiresTesting, nil
	}

	// upstream: td.embedded text, else the raw response
	errMessage := content
	if doc, docErr := goquery.NewDocumentFromReader(bytes.NewReader(res.Body)); docErr == nil {
		if embedded := strings.TrimSpace(doc.Find("td.embedded").First().Text()); embedded != "" {
			errMessage = embedded
		}
	}
	return indexer.ConfigCompleted, &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindConfig, Message: errMessage}
}

// torrentBytesCookieHeaderValue renders a response's Set-Cookie headers in
// Cookie-header form.
func torrentBytesCookieHeaderValue(res *httpclient.Result) string {
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

// torrentBytesMergeCookies unions two Cookie-header strings (first wins).
func torrentBytesMergeCookies(a, b string) string {
	seen := map[string]bool{}
	var parts []string
	for _, s := range append(strings.Split(a, "; "), strings.Split(b, "; ")...) {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := s
		if i := strings.IndexByte(s, '='); i >= 0 {
			key = s[:i]
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// torrentBytesCookies resolves the seed-page cookies the same way upstream
// passes loginPage.Cookies (null-safe).
func torrentBytesCookies(current string, loginPage *httpclient.Result) string {
	if loginPage == nil {
		return current
	}
	return torrentBytesMergeCookies(torrentBytesCookieHeaderValue(loginPage), current)
}

func (t *TorrentBytes) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return t.ResultsForQueryWith(ctx, q, isMeta, t.perform)
}

func (t *TorrentBytes) Test(ctx context.Context) error {
	return t.Base.Test(ctx, t.perform)
}

// perform ports upstream TorrentBytes.PerformQuery (TorrentBytes.cs:127-215).
func (t *TorrentBytes) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if t.Client == nil {
		return nil, errors.New("http client not configured")
	}

	qc := shared.QueryParams{}
	qc.Add("incldead", "1")

	if query.IsImdbQuery() {
		qc.Add("search", query.ImdbID)
		qc.Add("sc", "2") // search in description
	} else {
		qc.Add("search", query.GetQueryString())
		qc.Add("sc", "1") // search in title
	}

	for _, cat := range t.Caps.Categories.MapTorznabCapsToTrackers(query, false) {
		qc.Add("c"+cat, "1")
	}

	searchURL := t.searchURL() + "?" + qc.GetQueryString()

	enc, err := htmlindex.Get("iso-8859-1")
	if err != nil {
		return nil, err
	}

	res, err := t.Client.Do(ctx, t.ID(), &httpclient.Request{
		URL:     searchURL,
		Referer: t.searchURL(),
		Cookies: t.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	body, err := enc.NewDecoder().Bytes(res.Body)
	if err != nil {
		return nil, err
	}

	return t.parseBrowse(body)
}

// parseBrowse ports the upstream parsing block
// (selector: table > tbody:has(tr > td.colhead) > tr:not(:has(td.colhead))).
func (t *TorrentBytes) parseBrowse(body []byte) ([]*models.ReleaseInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, t.parseFailure(string(body), err)
	}

	var releases []*models.ReleaseInfo
	var parseErr error
	doc.Find("table > tbody").EachWithBreak(func(_ int, tbody *goquery.Selection) bool {
		// tbody must directly contain a tr > td.colhead header row
		if tbody.Find("tr > td.colhead").Length() == 0 {
			return true
		}
		tbody.Find("tr").EachWithBreak(func(_ int, row *goquery.Selection) bool {
			if row.Find("td.colhead").Length() > 0 {
				return true
			}
			release, ok, err := t.parseRow(row)
			if err != nil {
				parseErr = err
				return false
			}
			if ok {
				releases = append(releases, release)
			}
			return true
		})
		return parseErr == nil
	})
	if parseErr != nil {
		return nil, t.parseFailure(string(body), parseErr)
	}
	return releases, nil
}

// parseRow ports one browse row. The second return value is false when the
// row carries no title (upstream breaks out of the loop, TorrentBytes.cs:177-179).
func (t *TorrentBytes) parseRow(row *goquery.Selection) (*models.ReleaseInfo, bool, error) {
	release := &models.ReleaseInfo{}

	link := row.Find("td:nth-of-type(2) a:nth-of-type(2)").First()
	if link.Length() == 0 {
		// upstream: link.GetAttribute on null → NRE → OnParseError
		return nil, false, errors.New("release row has no details link")
	}
	guid, err := url.Parse(t.SiteLink() + link.AttrOr("href", ""))
	if err != nil {
		return nil, false, err
	}
	release.Guid = guid
	release.Details = guid
	release.Title = link.AttrOr("title", "")

	// There isn't a title attribute if the release name isn't truncated.
	if strings.TrimSpace(release.Title) == "" {
		release.Title = strings.TrimSpace(link.Contents().First().Text())
	}
	release.Description = release.Title

	// If we search an get no results, we still get a table just with no info.
	if strings.TrimSpace(release.Title) == "" {
		return nil, false, nil
	}

	// Check if the release has been assigned a category
	if qCat := row.Find("td:nth-of-type(1) a").First(); qCat.Length() > 0 {
		// upstream: qCat.GetAttribute("href").Substring(15) ("browse.php?cat=")
		cat := strings.TrimPrefix(qCat.AttrOr("href", ""), "browse.php?cat=")
		release.Category = t.Caps.Categories.MapTrackerCatToNewznab(cat)
	}

	qLink := row.Find("td:nth-of-type(2) a").First()
	linkURL, err := url.Parse(t.SiteLink() + qLink.AttrOr("href", ""))
	if err != nil {
		return nil, false, err
	}
	release.Link = linkURL

	added := strings.TrimSpace(row.Find("td:nth-of-type(5)").Text())
	publishDate, err := parseTBDate(added)
	if err != nil {
		return nil, false, err
	}
	release.PublishDate = publishDate

	size := parseutil.GetBytes(row.Find("td:nth-of-type(7)").Text())
	release.Size = &size

	seeders, _ := parseutil.CoerceInt(strings.TrimSpace(row.Find("td:nth-of-type(9)").Text()))
	release.Seeders = &seeders
	peers, _ := parseutil.CoerceInt(strings.TrimSpace(row.Find("td:nth-of-type(10)").Text()))
	peers += seeders
	release.Peers = &peers

	files, _ := parseutil.CoerceInt(row.Find("td:nth-child(3)").Text())
	release.Files = &files

	grabsText := row.Find("td:nth-child(8)").Text()
	if grabsText != "----" {
		grabs, _ := parseutil.CoerceInt(grabsText)
		release.Grabs = &grabs
	}

	dvf := 1.0
	if row.Find(`font[color="green"]`).FilterFunction(func(_ int, s *goquery.Selection) bool {
		return strings.Contains(s.Text(), "F") && strings.Contains(s.Text(), "L")
	}).Length() > 0 {
		dvf = 0
	}
	release.DownloadVolumeFactor = &dvf
	release.UploadVolumeFactor = torrentBytesF64(1)
	return release, true, nil
}

// parseTBDate mirrors upstream DateTime.ParseExact(added, "yyyy-MM-ddHH:mm:ss"):
// .NET tolerates a space between the date and time parts, so both shapes are
// accepted (the site's markup renders them adjacently).
func parseTBDate(s string) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-0215:04:05", s, time.Local); err == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
}

// parseFailure mirrors upstream OnParseError: log the response dump and
// surface Exception("Parse error", ex).
func (t *TorrentBytes) parseFailure(dump string, err error) error {
	t.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: t.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

func torrentBytesF64(v float64) *float64 { return &v }
