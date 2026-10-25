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

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// RevoPeers ports Jackett.Common/Indexers/Definitions/RevoPeers.cs.
//
// mechanism: html-login (login.php session seeding, takelogin.php form
// login, #torrents-table browse parsing, [REQ] tag stripping, imdb link
// scraping, PAY2DL ratio/seed-time stamps)
//
// Ported from: Definitions/RevoPeers.cs,
// Models/IndexerConfig/ConfigurationDataBasicLogin.cs.
//
// 澄清记录:
//   - upstream `Replaces => {"revolutiontt", "gimmepeers"}` has no equivalent
//     in the current indexer.Base port (no Replaces field yet); not ported.
//   - upstream ApplyConfiguration returns IndexerConfigurationStatus.Completed
//     (not RequiresTesting) after a successful login (RevoPeers.cs:135);
//     ported verbatim.
//   - upstream re-logins when the search response IsRedirect
//     (RevoPeers.cs:169-173); the Go client stack follows redirects
//     transparently, so this branch is not ported.
//   - upstream OnParseError throws, aborting PerformQuery; the port returns
//     an IndexerError of KindParse ("Parse error") instead.
//   - upstream rows.Skip(1) skips the first table row (RevoPeers.cs:181);
//     a qLink-less row is the support/donation banner and is skipped
//     (RevoPeers.cs:191-195).
type RevoPeers struct {
	indexer.Base
	cfg *revoPeersConfig
}

// revoPeersConfig ports upstream ConfigurationDataBasicLogin with the
// "For best results..." instruction message.
type revoPeersConfig struct {
	configdata.ConfigData
	Username     *configdata.StringItem
	Password     *configdata.StringItem
	Instructions *configdata.InfoItem
}

const revoPeersInstructions = "For best results, change the 'Torrents per page' setting to 100 in your Profile."

func newRevoPeersConfig() *revoPeersConfig {
	return &revoPeersConfig{
		ConfigData:   *configdata.NewConfigData(),
		Username:     configdata.NewStringItem("Username"),
		Password:     configdata.NewStringItem("Password"),
		Instructions: configdata.NewInfoItem("", revoPeersInstructions),
	}
}

// NewRevoPeers builds the indexer (services.Factory shape).
func NewRevoPeers(deps services.Deps) (indexer.Indexer, error) {
	r := &RevoPeers{cfg: newRevoPeersConfig()}
	r.Base = indexer.Base{
		IDValue:          "revopeers",
		NameValue:        "RevoPeers",
		DescriptionValue: "RevoPeers is a Private PAY2DL Torrent Tracker for MOVIES / TV / GENERAL",
		SiteLinkValue:    "https://revopeers.me/",
		DefaultSiteLink:  "https://revopeers.me/",
		LegacySiteLinks: []string{
			"https://revott.me/",
			"https://revolutiontt.me/",
			"https://www.gimmepeers.com/",
		},
		LangValue: "en-US",
		TypeValue: "private",
		Caps:      revoPeersCaps(),
		Config:    r.cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	return r, nil
}

func revoPeersCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories
	c.AddCategoryMapping("23", models.CatTVAnime, "Anime")
	c.AddCategoryMapping("2", models.CatPCMac, "Appz-Mac/Linux")
	c.AddCategoryMapping("1", models.CatPCISO, "Appz-Win/PC")
	c.AddCategoryMapping("22", models.CatAudioAudiobook, "Book-Audio")
	c.AddCategoryMapping("36", models.CatBooksEBook, "Book-Ebook")
	c.AddCategoryMapping("40", models.CatConsoleWii, "Games-Nin")
	c.AddCategoryMapping("16", models.CatConsolePS3, "Games-PS")
	c.AddCategoryMapping("4", models.CatPCGames, "Games-Win")
	c.AddCategoryMapping("39", models.CatConsoleXBox360, "Games-XBOX")
	c.AddCategoryMapping("35", models.CatConsoleNDS, "Handheld/NDS")
	c.AddCategoryMapping("34", models.CatPCMobileOther, "Mobile")
	c.AddCategoryMapping("51", models.CatMoviesUHD, "Movies/4K")
	c.AddCategoryMapping("10", models.CatMoviesBluRay, "Movies/BluRay")
	c.AddCategoryMapping("20", models.CatMoviesDVD, "Movies/DVDR")
	c.AddCategoryMapping("12", models.CatMoviesHD, "Movies/HDx264")
	c.AddCategoryMapping("44", models.CatMoviesOther, "Movies/Packs")
	c.AddCategoryMapping("11", models.CatMoviesHD, "Movies/x265")
	c.AddCategoryMapping("19", models.CatMoviesSD, "Movies/XviD")
	c.AddCategoryMapping("6", models.CatAudio, "Music")
	c.AddCategoryMapping("8", models.CatAudioLossless, "Music/FLAC")
	c.AddCategoryMapping("46", models.CatAudioOther, "Music/Packs")
	c.AddCategoryMapping("29", models.CatAudioVideo, "MusicVideos")
	c.AddCategoryMapping("21", models.CatTVSport, "Sports")
	c.AddCategoryMapping("70", models.CatTVUHD, "TV/4K")
	c.AddCategoryMapping("43", models.CatTVOther, "TV/DVDR")
	c.AddCategoryMapping("42", models.CatTVHD, "TV/HDx264")
	c.AddCategoryMapping("45", models.CatTVOther, "TV/Packs")
	c.AddCategoryMapping("41", models.CatTVSD, "TV/SDx264")
	c.AddCategoryMapping("52", models.CatTVHD, "TV/x265")
	c.AddCategoryMapping("7", models.CatTVSD, "TV/XViD")
	c.AddCategoryMapping("9", models.CatXXX, "XXX")
	c.AddCategoryMapping("49", models.CatXXX, "XXX/0DAY")
	c.AddCategoryMapping("47", models.CatXXXDVD, "XXX/DVDR")
	c.AddCategoryMapping("48", models.CatXXX, "XXX/HDx264")
	return caps
}

// log is a nil-safe logger (Base.Logger may be nil).
func (r *RevoPeers) log() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func (r *RevoPeers) landingPageURL() string { return r.SiteLink() + "login.php" }
func (r *RevoPeers) loginURL() string       { return r.SiteLink() + "takelogin.php" }
func (r *RevoPeers) searchURL() string      { return r.SiteLink() + "browse.php" }

// ApplyConfiguration ports upstream RevoPeers.ApplyConfiguration: GET the
// landing page for the PHP session cookie, POST username/password to
// takelogin.php, then verify the response contains "/logout.php". Returns
// Completed (upstream quirk, not RequiresTesting).
func (r *RevoPeers) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := r.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := r.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if r.Client == nil {
		return indexer.ConfigCompleted, errors.New("http client not configured")
	}

	// need to do an initial request to get PHP session cookie
	// (upstream comment, RevoPeers.cs:128-129)
	homePageLoad, err := r.Client.Do(ctx, r.ID(), &httpclient.Request{
		URL:     r.landingPageURL(),
		Referer: r.SiteLink(),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}

	pairs := url.Values{}
	pairs.Set("username", r.cfg.Username.Value)
	pairs.Set("password", r.cfg.Password.Value)

	res, err := r.Client.Do(ctx, r.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     r.loginURL(),
		Body:    pairs,
		Referer: r.landingPageURL(),
		Cookies: revoPeersCookies(homePageLoad),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	content := string(res.Body)

	if strings.Contains(content, "/logout.php") {
		cookies := revoPeersCookieHeaderValue(res)
		if site, err := url.Parse(r.SiteLink()); err == nil {
			cookies = revoPeersMergeCookies(cookies, r.Client.CookieHeader(r.ID(), site))
		}
		r.SetCookieHeader(cookies)
		r.SetConfigured(true)
		r.SaveConfig(ctx)
		return indexer.ConfigCompleted, nil
	}

	return indexer.ConfigCompleted, &indexer.IndexerError{
		IndexerID: r.ID(),
		Kind:      indexer.KindConfig,
		Message:   "Login failed! Check the username and password. If they are ok, try logging on the website.",
	}
}

// revoPeersCookieHeaderValue renders a response's Set-Cookie headers in
// Cookie-header form.
func revoPeersCookieHeaderValue(res *httpclient.Result) string {
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

// revoPeersMergeCookies unions two Cookie-header strings (first wins).
func revoPeersMergeCookies(a, b string) string {
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

// revoPeersCookies resolves the landing-page cookies (upstream passes
// homePageLoad.Cookies, null-safe).
func revoPeersCookies(homePageLoad *httpclient.Result) string {
	if homePageLoad == nil {
		return ""
	}
	return revoPeersCookieHeaderValue(homePageLoad)
}

func (r *RevoPeers) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return r.ResultsForQueryWith(ctx, q, isMeta, r.perform)
}

func (r *RevoPeers) Test(ctx context.Context) error {
	return r.Base.Test(ctx, r.perform)
}

// perform ports upstream RevoPeers.PerformQuery (RevoPeers.cs:138-242).
func (r *RevoPeers) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if r.Client == nil {
		return nil, errors.New("http client not configured")
	}

	qc := shared.QueryParams{}
	qc.Add("incldead", "1")

	if query.IsImdbQuery() {
		qc.Add("titleonly", "0")
		qc.Add("search", query.ImdbID)
	} else {
		qc.Add("titleonly", "1")
		qc.Add("search", query.GetQueryString())
	}

	for _, cat := range r.Caps.Categories.MapTorznabCapsToTrackers(query, false) {
		qc.Add("c"+cat, "1")
	}

	searchURL := r.searchURL() + "?" + qc.GetQueryString()
	res, err := r.Client.Do(ctx, r.ID(), &httpclient.Request{
		URL:     searchURL,
		Cookies: r.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	return r.parseBrowse(res.Body)
}

// parseBrowse ports the upstream parsing block (rows.Skip(1) over
// #torrents-table > tbody > tr).
func (r *RevoPeers) parseBrowse(body []byte) ([]*models.ReleaseInfo, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, r.parseFailure(string(body), err)
	}

	var releases []*models.ReleaseInfo
	var parseErr error
	rows := doc.Find("#torrents-table > tbody > tr")
	rows.EachWithBreak(func(i int, row *goquery.Selection) bool {
		if i == 0 {
			return true // upstream rows.Skip(1)
		}
		release, ok, err := r.parseRow(row)
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
		return nil, r.parseFailure(string(body), parseErr)
	}
	return releases, nil
}

// parseRow ports one browse row. The second return value is false when the
// row is the support/donation banner (no download link).
func (r *RevoPeers) parseRow(row *goquery.Selection) (*models.ReleaseInfo, bool, error) {
	qDetails := row.Find(".br_right > a").First()
	if qDetails.Length() == 0 {
		// upstream: qDetails.GetAttribute on null → NRE → OnParseError
		return nil, false, errors.New("release row has no details link")
	}
	details, err := url.Parse(r.SiteLink() + qDetails.AttrOr("href", ""))
	if err != nil {
		return nil, false, err
	}
	title := strings.TrimSpace(qDetails.Find("b").Text())
	// Remove auto-generated [REQ] tag from fulfilled requests
	title = strings.TrimPrefix(title, "[REQ] ")

	qLink := row.Find("td:nth-child(4) > a").First()
	if qLink.Length() == 0 {
		return nil, false, nil // support/donation banner
	}
	link, err := url.Parse(r.SiteLink() + qLink.AttrOr("href", ""))
	if err != nil {
		return nil, false, err
	}

	// dateString format "yyyy-MMM-dd hh:mm:ss" => eg "2015-04-25 23:38:12"
	dateString := strings.TrimSpace(row.Find("td:nth-child(6) nobr").Text())
	publishDate, err := parseRVDate(dateString)
	if err != nil {
		return nil, false, err
	}

	sizeHTML, _ := row.Find("td:nth-child(7)").Html()
	sizeStr := strings.TrimSpace(strings.SplitN(sizeHTML, "<", 2)[0])
	size := parseutil.GetBytes(sizeStr)
	files, _ := parseutil.GetLongFromString(row.Find("td:nth-child(7) > a").Text())
	grabs, _ := parseutil.GetLongFromString(row.Find("td:nth-child(8)").Text())
	seeders, _ := parseutil.CoerceInt(row.Find("td:nth-child(9)").Text())
	leechers, _ := parseutil.CoerceInt(row.Find("td:nth-child(10)").Text())
	peers := seeders + leechers

	category := strings.ReplaceAll(row.Find(".br_type > a").First().AttrOr("href", ""), "browse.php?cat=", "")

	var imdb *int64
	if qImdb := row.Find(`a[href*="www.imdb.com/"]`).First(); qImdb.Length() > 0 {
		parts := strings.Split(qImdb.AttrOr("href", ""), "/")
		if id, ok := parseutil.GetImdbID(parts[len(parts)-1]); ok {
			imdb = &id
		}
	}

	minimumRatio := 1.0
	minimumSeedTime := int64(172800) // 48 hours

	return &models.ReleaseInfo{
		Details:              details,
		Guid:                 details,
		Title:                title,
		Link:                 link,
		PublishDate:          publishDate,
		Size:                 &size,
		Seeders:              &seeders,
		Peers:                &peers,
		Grabs:                &grabs,
		Files:                &files,
		Category:             r.Caps.Categories.MapTrackerCatToNewznab(category),
		Imdb:                 imdb,
		MinimumRatio:         &minimumRatio,
		MinimumSeedTime:      &minimumSeedTime,
		UploadVolumeFactor:   revoPeersF64(1),
		DownloadVolumeFactor: revoPeersF64(1),
	}, true, nil
}

// parseRVDate mirrors upstream DateTime.ParseExact(dateString,
// "yyyy-MM-ddHH:mm:ss"): .NET tolerates a space between the date and time
// parts, so both shapes are accepted.
func parseRVDate(s string) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-0215:04:05", s, time.Local); err == nil {
		return t, nil
	}
	return time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
}

// parseFailure mirrors upstream OnParseError: log the response dump and
// surface Exception("Parse error", ex).
func (r *RevoPeers) parseFailure(dump string, err error) error {
	r.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: r.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

func revoPeersF64(v float64) *float64 { return &v }
