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
	"time"

	"github.com/PuerkitoBio/goquery"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// XSpeeds ports Jackett.Common/Indexers/Definitions/XSpeeds.cs.
//
// mechanism: html-login (POST takelogin.php, "logout.php" check, optional
// captcha via GetConfigurationForSetup) with a POST browse.php search and
// HTML table scraping.
//
// 澄清记录:
//   - upstream re-logins when the search response IsRedirect (cs:271-275);
//     the Go client stack follows redirects transparently (see Libble port),
//     so that branch cannot be observed and is not ported.
//   - upstream registers the sort/order selects, the freeleech toggle and
//     "Account Inactivity" via AddDynamic (cs:60-73); the Go port declares
//     them as exported fields carrying the upstream display names ("Sort
//     by", "Order", "Filter freeleech only") — form ids are name-derived
//     (ConfigurationData.cs:105) and the AddDynamic keys are never
//     serialized.
//   - upstream GetConfigurationForSetup adds CaptchaText/CaptchaImage
//     dynamic items only when the login page shows img#regimage (cs:173-196);
//     the port mirrors this via AddDynamic on the embedded ConfigData.
//   - upstream `new Uri(href)` requires absolute hrefs (cs:314-316); relative
//     details/download hrefs are resolved against the site link instead of
//     crashing (established manual-package deviation, cf. BitHDTV).
//   - the whole parse block is wrapped in try/catch → OnParseError (cs:351-354):
//     a broken row aborts the query with a KindParse error.
//   - upstream sets Poster twice — first from td .tooltip-content img, then
//     unconditionally overwritten from td > div > img[src] when it starts
//     with "http" (cs:330-344); the port replicates the overwrite (the first
//     value is dead upstream too).
//
// Ported from: Definitions/XSpeeds.cs,
// Models/IndexerConfig/ConfigurationDataBasicLogin.cs.
type XSpeeds struct {
	indexer.Base
	cfg *xSpeedsConfig
}

// xSpeedsConfig ports upstream ConfigurationDataBasicLogin plus the dynamic
// sort/order selects, freeleech toggle and Account Inactivity info.
type xSpeedsConfig struct {
	configdata.ConfigData
	Username               *configdata.StringItem
	Password               *configdata.PasswordItem
	Instructions           *configdata.InfoItem
	SortRequestedFromSite  *configdata.SelectItem
	OrderRequestedFromSite *configdata.SelectItem
	Freeleech              *configdata.BoolItem
	AccountInactivity      *configdata.InfoItem
}

const xSpeedsAccountInactivity = "Accounts with no activity for 30 days will automatically be deleted. Note: The activity has to be a login on site, meaning that download client, IRC and Discord are NOT counted as site activity. If you have to be away from site for a prolonged time, you can park your account up to 60 days in the User CP area."

func newXSpeedsConfig() *xSpeedsConfig {
	return &xSpeedsConfig{
		ConfigData: *configdata.NewConfigData(),
		Username:   configdata.NewStringItem("Username"),
		Password:   configdata.NewPasswordItem("Password"),
		// upstream ConfigurationDataBasicLogin(instructionMessageOptional: null)
		Instructions: configdata.NewInfoItem("", ""),
		// upstream display names (XSpeeds.cs:51,63) — ids are name-derived
		// ("sortby"/"order", ConfigurationData.cs:105)
		SortRequestedFromSite: configdata.NewSelectItem("Sort by", []configdata.Option{
			{Value: "added", Label: "created"},
			{Value: "seeders", Label: "seeders"},
			{Value: "size", Label: "size"},
			{Value: "name", Label: "title"},
		}),
		OrderRequestedFromSite: configdata.NewSelectItem("Order", []configdata.Option{
			{Value: "desc", Label: "descending"},
			{Value: "asc", Label: "ascending"},
		}),
		Freeleech:         configdata.NewBoolItem("Filter freeleech only"),
		AccountInactivity: configdata.NewInfoItem("Account Inactivity", xSpeedsAccountInactivity),
	}
}

// NewXSpeeds builds the indexer (services.Factory shape).
func NewXSpeeds(deps services.Deps) (indexer.Indexer, error) {
	x := &XSpeeds{cfg: newXSpeedsConfig()}
	x.cfg.SortRequestedFromSite.Value = "added" // upstream { Value = "added" }
	x.cfg.OrderRequestedFromSite.Value = "desc"
	x.Base = indexer.Base{
		IDValue:          "xspeeds",
		NameValue:        "XSpeeds",
		DescriptionValue: "XSpeeds (XS) is a Private Torrent Tracker for MOVIES / TV / GENERAL",
		SiteLinkValue:    "https://www.xspeeds.eu/",
		DefaultSiteLink:  "https://www.xspeeds.eu/",
		LangValue:        "en-US",
		TypeValue:        "private",
		Caps:             xSpeedsCaps(),
		Config:           x.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return x, nil
}

func xSpeedsCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories

	c.AddCategoryMapping("70", models.CatTVAnime, "Anime")
	c.AddCategoryMapping("113", models.CatTVAnime, "Anime Boxsets")
	c.AddCategoryMapping("112", models.CatMoviesOther, "Anime Movies")
	c.AddCategoryMapping("111", models.CatMoviesOther, "Anime TV")
	c.AddCategoryMapping("150", models.CatPC, "Apps")
	c.AddCategoryMapping("166", models.CatPCMac, "Apps MacOS")
	c.AddCategoryMapping("153", models.CatBooks, "Books")
	c.AddCategoryMapping("154", models.CatAudioAudiobook, "Books Audiobooks")
	c.AddCategoryMapping("155", models.CatBooks, "Books eBooks & Magazines")
	c.AddCategoryMapping("68", models.CatMoviesOther, "Cams/TS")
	c.AddCategoryMapping("140", models.CatTVDocumentary, "Documentary")
	c.AddCategoryMapping("10", models.CatMoviesDVD, "DVDR")
	c.AddCategoryMapping("109", models.CatMoviesBluRay, "DVDR Bluray Disc")
	c.AddCategoryMapping("131", models.CatTVSport, "Fighting")
	c.AddCategoryMapping("134", models.CatTVSport, "Fighting Boxing")
	c.AddCategoryMapping("133", models.CatTVSport, "Fighting MMA")
	c.AddCategoryMapping("132", models.CatTVSport, "Fighting Wrestling")
	c.AddCategoryMapping("72", models.CatMoviesForeign, "Foreign")
	c.AddCategoryMapping("116", models.CatTVForeign, "Foreign Boxsets")
	c.AddCategoryMapping("114", models.CatMoviesForeign, "Foreign Movies")
	c.AddCategoryMapping("115", models.CatTVForeign, "Foreign TV")
	c.AddCategoryMapping("103", models.CatConsoleOther, "Games Console")
	c.AddCategoryMapping("105", models.CatConsoleOther, "Games Console Nintendo")
	c.AddCategoryMapping("104", models.CatConsolePS4, "Games Console Playstation")
	c.AddCategoryMapping("106", models.CatConsoleXBox, "Games Console XBOX")
	c.AddCategoryMapping("6", models.CatPCGames, "Games PC")
	c.AddCategoryMapping("108", models.CatPC, "Games PC Linux")
	c.AddCategoryMapping("107", models.CatPCMac, "Games PC Mac")
	c.AddCategoryMapping("11", models.CatMovies, "Movie Boxsets")
	c.AddCategoryMapping("118", models.CatMoviesUHD, "Movie Boxsets 4K")
	c.AddCategoryMapping("162", models.CatMoviesHD, "Movie Boxsets AV1")
	c.AddCategoryMapping("143", models.CatMoviesHD, "Movie Boxsets HD")
	c.AddCategoryMapping("119", models.CatMoviesHD, "Movie Boxsets HEVC")
	c.AddCategoryMapping("144", models.CatMoviesSD, "Movie Boxsets SD")
	c.AddCategoryMapping("12", models.CatMovies, "Movies")
	c.AddCategoryMapping("117", models.CatMoviesUHD, "Movies 4K")
	c.AddCategoryMapping("163", models.CatMoviesHD, "Movies AV1")
	c.AddCategoryMapping("145", models.CatMoviesHD, "Movies HD")
	c.AddCategoryMapping("100", models.CatMoviesHD, "Movies HEVC")
	c.AddCategoryMapping("146", models.CatMoviesSD, "Movies SD")
	c.AddCategoryMapping("13", models.CatAudio, "Music")
	c.AddCategoryMapping("135", models.CatAudioLossless, "Music FLAC")
	c.AddCategoryMapping("151", models.CatAudio, "Music Karaoke")
	c.AddCategoryMapping("136", models.CatAudio, "Music Boxset")
	c.AddCategoryMapping("148", models.CatAudioVideo, "Music Videos")
	c.AddCategoryMapping("9", models.CatOther, "Other")
	c.AddCategoryMapping("125", models.CatOther, "Other Pictures")
	c.AddCategoryMapping("54", models.CatTVOther, "Other Soaps")
	c.AddCategoryMapping("83", models.CatTVOther, "Other Specials")
	// upstream maps TOTM/TOTW to both TV and Movies (cs:143-146)
	c.AddCategoryMapping("139", models.CatTV, "TOTM (Freeleech)")
	c.AddCategoryMapping("138", models.CatTV, "TOTW (x2 upload)")
	c.AddCategoryMapping("139", models.CatMovies, "TOTM (Freeleech)")
	c.AddCategoryMapping("138", models.CatMovies, "TOTW (x2 upload)")
	c.AddCategoryMapping("167", models.CatMovies, "Weekend Deluxe 4K pack")
	c.AddCategoryMapping("20", models.CatTVSport, "Sports")
	c.AddCategoryMapping("88", models.CatTVSport, "Sports/Football")
	c.AddCategoryMapping("86", models.CatTVSport, "Sports/MotorSports")
	c.AddCategoryMapping("89", models.CatTVSport, "Sports/Olympics")
	c.AddCategoryMapping("126", models.CatTV, "TV")
	c.AddCategoryMapping("127", models.CatTVUHD, "TV 4K")
	c.AddCategoryMapping("164", models.CatTVHD, "TV AV1")
	c.AddCategoryMapping("129", models.CatTVHD, "TV HD")
	c.AddCategoryMapping("130", models.CatTVHD, "TV HEVC")
	c.AddCategoryMapping("128", models.CatTVSD, "TV SD")
	c.AddCategoryMapping("149", models.CatTV, "TV Specials")
	c.AddCategoryMapping("21", models.CatTVSD, "TV Boxsets")
	c.AddCategoryMapping("120", models.CatTVUHD, "TV Boxset 4K")
	c.AddCategoryMapping("165", models.CatTVUHD, "TV Boxset AV1")
	c.AddCategoryMapping("76", models.CatTVHD, "TV Boxset HD")
	c.AddCategoryMapping("97", models.CatTVHD, "TV Boxset HEVC")
	c.AddCategoryMapping("147", models.CatTVSD, "TV Boxset SD")
	return caps
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (x *XSpeeds) log() *slog.Logger {
	if x.Logger != nil {
		return x.Logger
	}
	return slog.Default()
}

func (x *XSpeeds) landingURL() string { return x.SiteLink() + "login.php" }
func (x *XSpeeds) loginURL() string   { return x.SiteLink() + "takelogin.php" }
func (x *XSpeeds) searchURL() string  { return x.SiteLink() + "browse.php" }

// ConfigurationForSetup ports upstream GetConfigurationForSetup (cs:173-196):
// fetch the login page and, when it shows img#regimage, append the captcha
// text/image dynamic items.
func (x *XSpeeds) ConfigurationForSetup(ctx context.Context) ([]map[string]any, error) {
	if x.Client != nil {
		loginPage, err := x.Client.Do(ctx, x.ID(), &httpclient.Request{
			URL:     x.landingURL(),
			Cookies: x.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(loginPage.Body)))
		if err != nil {
			return nil, err
		}
		if qCaptchaImg := doc.Find("img#regimage"); qCaptchaImg.Length() > 0 {
			captchaURL, _ := qCaptchaImg.Attr("src")
			// upstream passes the raw src to the client (cs:181-182) where a
			// relative href cannot be requested (HttpWebClient.cs:68
			// `new Uri(webRequest.Url)`); resolve against the site link per
			// the established manual-package deviation (cf. the
			// details/download hrefs note, cs:314-316).
			captchaImgURL, err := xsResolve(x.SiteLink(), captchaURL)
			if err != nil {
				return nil, err
			}
			captchaImageResponse, err := x.Client.Do(ctx, x.ID(), &httpclient.Request{
				URL:     captchaImgURL.String(),
				Cookies: setCookiePairs(loginPage.Headers),
				Referer: x.landingURL(),
			})
			if err != nil {
				return nil, err
			}
			if x.cfg.GetDynamic("CaptchaText") == nil {
				x.cfg.AddDynamic("CaptchaText", configdata.NewStringItem("Captcha Text"))
				image := configdata.NewImageItem("Captcha Image")
				image.Value = captchaImageResponse.Body
				x.cfg.AddDynamic("CaptchaImage", image)
			}
		} else {
			x.log().Debug(fmt.Sprintf("%s: No captcha image found", x.ID()))
		}
	}
	return x.Base.ConfigurationForSetup(ctx)
}

// ApplyConfiguration ports upstream (cs:198-230): POST takelogin.php and
// check the followed page for "logout.php"; on failure extract the site
// error message. RequiresTesting (the manager runs the test afterwards).
func (x *XSpeeds) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := x.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := x.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if x.Client == nil {
		return indexer.ConfigCompleted, errors.New("http client not configured")
	}

	pairs := formPairs{
		{"username", x.cfg.Username.Value},
		{"password", x.cfg.Password.Value},
	}
	if captchaText, ok := x.cfg.GetDynamic("CaptchaText").(*configdata.StringItem); ok && captchaText != nil {
		pairs = append(pairs, [2]string{"imagestring", captchaText.Value})
	}

	res, err := x.Client.Do(ctx, x.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     x.loginURL(),
		RawBody: pairs.encode(),
		Headers: formContentType,
		Cookies: x.CookieHeader(),
		Referer: x.landingURL(),
	})
	if err != nil {
		return indexer.ConfigCompleted, err
	}

	if !strings.Contains(string(res.Body), "logout.php") {
		errorMessage := ""
		if doc, docErr := goquery.NewDocumentFromReader(strings.NewReader(string(res.Body))); docErr == nil {
			errorMessage = strings.TrimSpace(strings.ReplaceAll(doc.Find(".left_side table:nth-of-type(1) tr:nth-of-type(2)").First().Text(), "\n\t", " "))
			if errorMessage == "" {
				errorMessage = strings.TrimSpace(strings.ReplaceAll(doc.Find("div.notification-body").First().Text(), "\n\t", " "))
			}
		}
		if errorMessage == "" {
			errorMessage = "Login failed."
		}
		return indexer.ConfigCompleted, &indexer.IndexerError{IndexerID: x.ID(), Kind: indexer.KindConfig, Message: errorMessage}
	}

	if u, err := url.Parse(x.SiteLink()); err == nil {
		if cookies := x.Client.CookieHeader(x.ID(), u); cookies != "" {
			x.SetCookieHeader(cookies)
		}
	}
	x.SetConfigured(true)
	x.SaveConfig(ctx)
	return indexer.ConfigRequiresTesting, nil
}

func (x *XSpeeds) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return x.ResultsForQueryWith(ctx, q, isMeta, x.perform)
}

func (x *XSpeeds) Test(ctx context.Context) error {
	return x.Base.Test(ctx, x.perform)
}

var (
	// upstream _dateMatchRegex (cs:36).
	xsDateMatchRe = regexp.MustCompile(`\d{2}-\d{2}-\d{4} \d{2}:\d{2}`)
	// upstream search-string whitespace collapsing (cs:256).
	xsSearchSpaceRe = regexp.MustCompile(`[ -._]+`)
	// upstream CleanTitle's "[R]" suffix strip (cs:371).
	xsReqTagRe = regexp.MustCompile(`(?i)\[R\]$`)
)

// perform ports upstream PerformQuery (cs:232-362): a POST search with the
// single-category restriction and sort/freeleech handling.
func (x *XSpeeds) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if x.Client == nil {
		return nil, errors.New("http client not configured")
	}
	prevCook := x.CookieHeader() + ""

	categoryMapping := distinct(x.Caps.Categories.MapTorznabCapsToTrackers(query, false))

	searchParams := formPairs{
		{"do", "search"},
		{"category", xsFirstIfSingleOrDefault(categoryMapping, "0")}, // multi category search not supported
		{"include_dead_torrents", "yes"},
	}

	if x.cfg.Freeleech.Value {
		searchParams = append(searchParams, [2]string{"sort", "free"})
		searchParams = append(searchParams, [2]string{"order", "desc"})
	} else {
		searchParams = append(searchParams, [2]string{"sort", x.cfg.SortRequestedFromSite.Value})
		searchParams = append(searchParams, [2]string{"order", x.cfg.OrderRequestedFromSite.Value})
	}

	searchString := strings.TrimSpace(xsSearchSpaceRe.ReplaceAllString(query.GetQueryString(), " "))

	if query.IsImdbQuery() {
		searchParams = append(searchParams, [2]string{"keywords", query.ImdbID})
		searchParams = append(searchParams, [2]string{"search_type", "t_both"})
	} else if searchString != "" {
		searchParams = append(searchParams, [2]string{"keywords", searchString})
		searchParams = append(searchParams, [2]string{"search_type", "t_name"})
	}

	res, err := x.Client.Do(ctx, x.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     x.searchURL(),
		RawBody: searchParams.encode(),
		Headers: formContentType,
		Cookies: x.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}

	releases, err := x.parseSearchPage(string(res.Body))
	if err != nil {
		return nil, err
	}

	if !strings.EqualFold(strings.TrimSpace(x.CookieHeader()), strings.TrimSpace(prevCook)) {
		x.SaveConfig(ctx)
	}

	return releases, nil
}

// xsFirstIfSingleOrDefault mirrors upstream IEnumerableExtensions
// FirstIfSingleOrDefault.
func xsFirstIfSingleOrDefault(list []string, def string) string {
	if len(list) == 1 {
		return list[0]
	}
	return def
}

func distinct(list []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// parseSearchPage ports the upstream parsing block (cs:279-350); any row
// failure aborts the query via OnParseError.
func (x *XSpeeds) parseSearchPage(content string) ([]*models.ReleaseInfo, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		x.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, content))
		return nil, &indexer.IndexerError{IndexerID: x.ID(), Kind: indexer.KindParse, Message: "Parse error"}
	}

	var releases []*models.ReleaseInfo
	var parseErr error
	doc.Find(`table#sortabletable > tbody > tr:has(div > a[href*="details.php?id="])`).EachWithBreak(func(_ int, row *goquery.Selection) bool {
		release, err := x.parseRow(row)
		if err != nil {
			parseErr = err
			return false
		}
		if release != nil {
			releases = append(releases, release)
		}
		return true
	})
	if parseErr != nil {
		x.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", parseErr, content))
		return nil, &indexer.IndexerError{IndexerID: x.ID(), Kind: indexer.KindParse, Message: "Parse error"}
	}
	return releases, nil
}

// parseRow ports one result row (cs:284-349). A nil release with nil error
// means the row was skipped (freeleech-only filter).
func (x *XSpeeds) parseRow(row *goquery.Selection) (*models.ReleaseInfo, error) {
	release := &models.ReleaseInfo{}

	if row.Find(`img[alt^="Free Torrent"], img[alt^="Sitewide Free Torrent"]`).Length() > 0 {
		release.DownloadVolumeFactor = xsF64(0)
	} else if row.Find(`img[alt^="Silver Torrent"]`).Length() > 0 {
		release.DownloadVolumeFactor = xsF64(0.5)
	} else {
		release.DownloadVolumeFactor = xsF64(1)
	}

	if x.cfg.Freeleech.Value && *release.DownloadVolumeFactor != 0 {
		return nil, nil
	}

	if row.Find(`img[alt^="x2 Torrent"]`).Length() > 0 {
		release.UploadVolumeFactor = xsF64(2)
	} else {
		release.UploadVolumeFactor = xsF64(1)
	}

	qDetails := row.Find(`div > a[href*="details.php?id="]`).First()
	release.Title = xsCleanTitle(strings.TrimSpace(qDetails.Text()))

	guidHref, ok := row.Find("td:nth-of-type(3) a").First().Attr("href")
	if !ok {
		return nil, errors.New("row has no download link")
	}
	guid, err := xsResolve(x.SiteLink(), guidHref)
	if err != nil {
		return nil, err
	}
	release.Guid = guid
	release.Link = guid

	detailsHref, ok := qDetails.Attr("href")
	if !ok {
		return nil, errors.New("row has no details link")
	}
	details, err := xsResolve(x.SiteLink(), detailsHref)
	if err != nil {
		return nil, err
	}
	release.Details = details

	// 08-08-2015 12:51
	// requests can be 'Pre Release Time: 25-04-2021 15:00 Uploaded: 3 Weeks, 2 Days, 23 Hours, 53 Minutes, 39 Seconds after Pre'
	dateText := strings.TrimSpace(row.Find("td:nth-of-type(2) > div:last-child").Text())
	if m := xsDateMatchRe.FindString(dateText); m != "" {
		publishDate, err := time.ParseInLocation("02-01-2006 15:04", m, time.Local)
		if err != nil {
			return nil, err
		}
		release.PublishDate = publishDate
	}

	seeders := parseutil.MustCoerceLong(strings.TrimSpace(row.Find("td:nth-of-type(7)").Text()))
	leechers := parseutil.MustCoerceLong(strings.TrimSpace(row.Find("td:nth-of-type(8)").Text()))
	release.Seeders = &seeders
	peers := seeders + leechers
	release.Peers = &peers
	size := parseutil.GetBytes(strings.TrimSpace(row.Find("td:nth-of-type(5)").Text()))
	release.Size = &size

	if qPoster := row.Find("td:nth-of-type(2) .tooltip-content img"); qPoster.Length() > 0 {
		if src, ok := qPoster.Attr("src"); ok {
			release.Poster, _ = url.Parse(src)
		}
	}

	categoryLink, ok := row.Find("td:nth-of-type(1) a").First().Attr("href")
	if !ok {
		return nil, errors.New("row has no category link")
	}
	cat := parseutil.GetArgumentFromQueryString(categoryLink, "category")
	release.Category = x.Caps.Categories.MapTrackerCatToNewznab(cat)

	grabs := parseutil.MustCoerceLong(row.Find("td:nth-child(6)").Text())
	release.Grabs = &grabs

	// upstream quirk: this unconditionally overwrites the tooltip poster
	// (cs:343-344) and resets it to nil when the cover is not absolute.
	cover := strings.TrimSpace(row.Find("td:nth-of-type(2) > div > img[src]").First().AttrOr("src", ""))
	if cover != "" && strings.HasPrefix(cover, "http") {
		release.Poster, _ = url.Parse(cover)
	} else {
		release.Poster = nil
	}

	minimumRatio := 0.8
	release.MinimumRatio = &minimumRatio

	return release, nil
}

func xsF64(v float64) *float64 { return &v }

// xsCleanTitle ports upstream CleanTitle (cs:364-375).
func xsCleanTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return ""
	}
	title = xsReqTagRe.ReplaceAllString(title, "")
	return strings.TrimSpace(title)
}

// xsResolve parses href and resolves relative references against the site
// link (see file header).
func xsResolve(siteLink, href string) (*url.URL, error) {
	u, err := url.Parse(href)
	if err != nil {
		return nil, err
	}
	if u.IsAbs() {
		return u, nil
	}
	base, err := url.Parse(siteLink)
	if err != nil {
		return nil, err
	}
	return base.ResolveReference(u), nil
}
