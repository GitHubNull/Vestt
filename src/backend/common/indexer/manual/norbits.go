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
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// NorBits ports Jackett.Common/Indexers/Definitions/NorBits.cs.
//
// mechanism: html-login (index+login page cookie simulation, POST
// takelogin.php, session refresh on missing "logout.php")
//
// Ported from: Definitions/NorBits.cs.
//
// 澄清记录:
//   - ValidateConfig error messages copied verbatim, including the trailing
//     space before "!" (upstream lines 478/488/504/509).
//   - PublishDate uses the upstream format "yyyy-MM-ddHH:mm:ss" — no space
//     between date and time (upstream line 285); a parse failure aborts the
//     whole term via OnParseError ("Parse error").
//   - Upstream Download builds an unused torrentid/_ form (comment "Strange,
//     blank param...") and then calls base.Download — the port keeps Base's
//     Download unchanged.
//   - ConfigureIfOK upstream first saves CookieHeader=response cookies and
//     then NorBits overwrites it with the combined
//     "indexCookies responseCookies ts_username=<user>" value (upstream line
//     196); the port mirrors that order, so the persisted config holds the
//     response cookies only (upstream quirk).
//   - The search term is UrlEncode'd with ISO-8859-1 (code page 28591):
//     runes above 0xFF fall back to '?' (.NET encoder fallback).
//   - OnParseError throws upstream, aborting the whole PerformQuery; the
//     port returns an IndexerError of KindParse with message "Parse error".
type NorBits struct {
	indexer.Base
	cfg *norbitsConfig
}

// norbitsConfig ports upstream ConfigurationDataNorbits
// (Bespoke/ConfigurationDataNorbits.cs): the three DisplayInfo blocks,
// Username/Password/2FA, freeleech/fullsearch toggles and the required
// Pages setting; declared as fields instead of base+dynamic items — same
// registration and ordering (item id = name lower-cased, spaces removed).
type norbitsConfig struct {
	configdata.ConfigData
	CredentialsWarning *configdata.InfoItem
	Username           *configdata.StringItem
	Password           *configdata.PasswordItem
	TwoFactorAuth      *configdata.StringItem
	Freeleech          *configdata.BoolItem
	PagesWarning       *configdata.InfoItem
	Pages              *configdata.StringItem
	UseFullSearch      *configdata.BoolItem
	AccountActivity    *configdata.InfoItem
}

const (
	norbitsCredentialsWarning = "<b>Credentials Configuration</b> (<i>Private Tracker</i>),<br /><br /> <ul><li><b>Username</b> is your account name on this tracker.</li><li><b>Password</b> is your password associated to your account name.</li></ul>"
	norbitsPagesWarning       = "<b>Preferences Configuration</b> (<i>Tweak your search settings</i>),<br /><br /> <ul><li><b>Max Pages to Process</b> let you specify how many page (max) Jackett can process when doing a search. Setting a value <b>higher than 4 is dangerous</b> for you account ! (<b>Result of too many requests to tracker...that <u>will be suspect</u></b>).</li></ul>"
	norbitsAccountActivity    = "Parasites are deactivated after 28 days. (7 days below 0.3 in ratio, 7 days with a warning, 14 days as a parasite). Accounts with nothing downloaded or uploaded will be deactivated after 28 days. This only applies to newly registered accounts without torrent activity (e.g. not passing the norbits test)."
)

func newNorBitsConfig() *norbitsConfig {
	return &norbitsConfig{
		ConfigData:         *configdata.NewConfigData(),
		CredentialsWarning: configdata.NewInfoItem("Credentials", norbitsCredentialsWarning),
		Username:           configdata.NewStringItem("Username"),
		Password:           configdata.NewPasswordItem("Password"),
		TwoFactorAuth:      configdata.NewStringItem("2FA Code (Optional)"),
		Freeleech:          configdata.NewBoolItem("Search freeleech only"),
		PagesWarning:       configdata.NewInfoItem("Preferences", norbitsPagesWarning),
		Pages:              configdata.NewStringItem("Max Pages to Process (Required)"),
		UseFullSearch:      configdata.NewBoolItem("Enable search in description."),
		AccountActivity:    configdata.NewInfoItem("Account Inactivity", norbitsAccountActivity),
	}
}

// NewNorBits builds the indexer (services.Factory shape).
func NewNorBits(deps services.Deps) (indexer.Indexer, error) {
	n := &NorBits{cfg: newNorBitsConfig()}
	n.cfg.Pages.Value = "4"
	n.Base = indexer.Base{
		IDValue:          "norbits",
		NameValue:        "NorBits",
		DescriptionValue: "NorBits is a NORWEGIAN Private site for MOVIES / TV / GENERAL",
		SiteLinkValue:    "https://norbits.net/",
		DefaultSiteLink:  "https://norbits.net/",
		LangValue:        "nb-NO",
		TypeValue:        "private",
		Caps:             norbitsCaps(),
		Config:           n.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return n, nil
}

func norbitsCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories
	c.AddCategoryMapping("main_cat[]=1", models.CatMovies, "Filmer")
	c.AddCategoryMapping("main_cat[]=1&sub2_cat[]=49", models.CatMoviesUHD, "Filmer - UHD-2160p")
	c.AddCategoryMapping("main_cat[]=1&sub2_cat[]=19", models.CatMoviesHD, "Filmer - HD-1080p/i")
	c.AddCategoryMapping("main_cat[]=1&sub2_cat[]=20", models.CatMoviesHD, "Filmer - HD-720p")
	c.AddCategoryMapping("main_cat[]=1&sub2_cat[]=22", models.CatMoviesSD, "Filmer - SD")
	c.AddCategoryMapping("main_cat[]=2", models.CatTV, "TV")
	c.AddCategoryMapping("main_cat[]=2&sub2_cat[]=49", models.CatTVUHD, "TV - UHD-2160p")
	c.AddCategoryMapping("main_cat[]=2&sub2_cat[]=19", models.CatTVHD, "TV - HD-1080p/i")
	c.AddCategoryMapping("main_cat[]=2&sub2_cat[]=20", models.CatTVHD, "TV - HD-720p")
	c.AddCategoryMapping("main_cat[]=2&sub2_cat[]=22", models.CatTVSD, "TV - SD")
	c.AddCategoryMapping("main_cat[]=3", models.CatPC, "Programmer")
	c.AddCategoryMapping("main_cat[]=4", models.CatConsole, "Spill")
	c.AddCategoryMapping("main_cat[]=5", models.CatAudio, "Musikk")
	c.AddCategoryMapping("main_cat[]=5&sub2_cat[]=42", models.CatAudioMP3, "Musikk - 192")
	c.AddCategoryMapping("main_cat[]=5&sub2_cat[]=43", models.CatAudioMP3, "Musikk - 256")
	c.AddCategoryMapping("main_cat[]=5&sub2_cat[]=44", models.CatAudioMP3, "Musikk - 320")
	c.AddCategoryMapping("main_cat[]=5&sub2_cat[]=45", models.CatAudioMP3, "Musikk - VBR")
	c.AddCategoryMapping("main_cat[]=5&sub2_cat[]=46", models.CatAudioLossless, "Musikk - Lossless")
	c.AddCategoryMapping("main_cat[]=6", models.CatBooks, "Tidsskrift")
	c.AddCategoryMapping("main_cat[]=7", models.CatAudioAudiobook, "Lydbøker")
	c.AddCategoryMapping("main_cat[]=8", models.CatAudioVideo, "Musikkvideoer")
	c.AddCategoryMapping("main_cat[]=8&sub2_cat[]=19", models.CatAudioVideo, "Musikkvideoer - HD-1080p/i")
	c.AddCategoryMapping("main_cat[]=8&sub2_cat[]=20", models.CatAudioVideo, "Musikkvideoer - HD-720p")
	c.AddCategoryMapping("main_cat[]=8&sub2_cat[]=22", models.CatAudioVideo, "Musikkvideoer - SD")
	c.AddCategoryMapping("main_cat[]=40", models.CatAudioOther, "Podcasts")
	return caps
}

// log is a nil-safe logger (Base.Logger may be nil).
func (n *NorBits) log() *slog.Logger {
	if n.Logger != nil {
		return n.Logger
	}
	return slog.Default()
}

func (n *NorBits) loginURL() string      { return n.SiteLink() + "login.php" }
func (n *NorBits) loginCheckURL() string { return n.SiteLink() + "takelogin.php" }
func (n *NorBits) searchURL() string     { return n.SiteLink() + "browse.php" }

// ApplyConfiguration ports upstream NorBits.ApplyConfiguration: validate,
// login, RequiresTesting (the manager runs the test afterwards).
func (n *NorBits) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := n.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := n.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := n.validateConfig(); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := n.doLogin(ctx); err != nil {
		return indexer.ConfigCompleted, err
	}
	return indexer.ConfigRequiresTesting, nil
}

// validateConfig ports upstream ValidateConfig (messages verbatim).
func (n *NorBits) validateConfig() error {
	n.log().Debug("NorBits - Validating Settings ... \n")

	if n.cfg.Username.Value == "" {
		return &indexer.IndexerError{IndexerID: n.ID(), Kind: indexer.KindConfig, Message: "You must provide a username for this tracker to login !"}
	}
	n.log().Debug("NorBits - Validated Setting -- Username (auth) => " + n.cfg.Username.Value)

	if n.cfg.Password.Value == "" {
		return &indexer.IndexerError{IndexerID: n.ID(), Kind: indexer.KindConfig, Message: "You must provide a password with your username for this tracker to login !"}
	}
	n.log().Debug("NorBits - Validated Setting -- Password (auth) => " + n.cfg.Password.Value)

	if n.cfg.Pages.Value != "" {
		// upstream: Convert.ToInt32(Pages.Value) throws FormatException on
		// non-numeric input
		if _, err := strconv.Atoi(strings.TrimSpace(n.cfg.Pages.Value)); err != nil {
			return &indexer.IndexerError{IndexerID: n.ID(), Kind: indexer.KindConfig, Message: "Please enter a numeric maximum number of pages to crawl !"}
		}
		n.log().Debug("NorBits - Validated Setting -- Max Pages => " + n.cfg.Pages.Value)
	} else {
		return &indexer.IndexerError{IndexerID: n.ID(), Kind: indexer.KindConfig, Message: "Please enter a maximum number of pages to crawl !"}
	}
	return nil
}

// doLogin ports upstream DoLoginAsync: GET the index page (cookies), GET the
// login page (tracker security simulation), POST takelogin.php; login is OK
// when the response sets a "uid" cookie.
func (n *NorBits) doLogin(ctx context.Context) error {
	if n.Client == nil {
		return errors.New("http client not configured")
	}

	// Get index page for cookies
	n.log().Debug("NorBits - Getting index page (for cookies).. with " + n.SiteLink())
	indexPage, err := n.Client.Do(ctx, n.ID(), &httpclient.Request{
		URL:     n.SiteLink(),
		Cookies: n.CookieHeader(),
	})
	if err != nil {
		return err
	}
	indexCookies := setCookiePairs(indexPage.Headers)

	pairs := formPairs{
		{"username", n.cfg.Username.Value},
		{"password", n.cfg.Password.Value},
		{"logout", "no"},
		{"returnto", "/"},
	}

	// Use 2FA code if defined
	if n.cfg.TwoFactorAuth.Value != "" {
		pairs = append(pairs, [2]string{"code", n.cfg.TwoFactorAuth.Value})
	}

	// Get login page -- (not used, but simulation needed by tracker security's checks)
	n.log().Debug("NorBits - Getting login page (user simulation).. with " + n.loginURL())
	if _, err := n.Client.Do(ctx, n.ID(), &httpclient.Request{
		URL:     n.loginURL(),
		Cookies: indexCookies,
		Referer: n.SiteLink(),
	}); err != nil {
		return err
	}

	// Build WebRequest for submitting authentication
	n.log().Debug("Perform login with " + n.loginCheckURL())
	response, err := n.Client.Do(ctx, n.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     n.loginCheckURL(),
		RawBody: pairs.encode(),
		Headers: formContentType,
		Cookies: indexCookies,
		Referer: n.loginURL(),
	})
	if err != nil {
		return err
	}
	responseCookies := setCookiePairs(response.Headers)

	// Test if we are logged in (upstream: response.Cookies.Contains("uid="))
	if responseCookies == "" || !strings.Contains(responseCookies, "uid=") {
		// Oops, unable to login
		message := "Error during attempt !"
		n.log().Debug("NorBits - Login failed: " + message)
		return &indexer.IndexerError{IndexerID: n.ID(), Kind: indexer.KindConfig, Message: "Login failed: " + message}
	}

	// upstream ConfigureIfOK: CookieHeader = response cookies, configured,
	// saved — then NorBits overwrites CookieHeader with the combined value.
	n.SetCookieHeader(responseCookies)
	n.SetConfigured(true)
	n.SaveConfig(ctx)
	n.log().Debug("NorBits - Cookies saved for future uses...")
	n.SetCookieHeader(indexCookies + " " + responseCookies + " ts_username=" + n.cfg.Username.Value)
	n.log().Debug("NorBits - Login Success\n")
	return nil
}

func (n *NorBits) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return n.ResultsForQueryWith(ctx, q, isMeta, n.perform)
}

func (n *NorBits) Test(ctx context.Context) error {
	return n.Base.Test(ctx, n.perform)
}

// perform ports upstream PerformQuery: search the exact term and, when it
// contains diacritics, once more with the diacritics stripped.
func (n *NorBits) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo

	exactSearchTerm := query.GetQueryString()
	searchTerms := []string{exactSearchTerm}

	// duplicate search without diacritics
	baseSearchTerm := norbitsRemoveDiacritics(exactSearchTerm)
	if baseSearchTerm != exactSearchTerm {
		searchTerms = append(searchTerms, baseSearchTerm)
	}

	for _, searchTerm := range searchTerms {
		// Build our query
		request := n.buildQuery(searchTerm, query)

		// Getting results & Store content
		response, err := n.searchWithLoginRetry(ctx, request)
		if err != nil {
			return nil, err
		}

		pageReleases, found, err := n.parseSearchPage(response.Body)
		if err != nil {
			return nil, err
		}
		if !found {
			// No result found for this query
			n.log().Debug("NorBits - No result found for your query, please try another search term ...\n")
			break
		}
		n.log().Debug(fmt.Sprintf("NorBits - Found %d result(s) in 1 page(s) for this query !", len(pageReleases)))
		releases = append(releases, pageReleases...)
	}
	// Return found releases
	return releases, nil
}

// searchWithLoginRetry ports upstream SearchWithLoginRetryAsync: when the
// response does not carry the "logout.php" marker the session is refreshed
// and the search retried once.
func (n *NorBits) searchWithLoginRetry(ctx context.Context, request string) (*httpclient.Result, error) {
	response, err := n.searchRequest(ctx, request)
	if err != nil {
		return nil, err
	}

	if bytes.Contains(response.Body, []byte("logout.php")) {
		return response, nil
	}

	n.log().Debug("NorBits: Invalid session, logging in and retrying search")

	if err := n.doLogin(ctx); err != nil {
		return nil, err
	}

	response, err = n.searchRequest(ctx, request)
	if err != nil {
		return nil, err
	}

	if !bytes.Contains(response.Body, []byte("logout.php")) {
		return nil, errors.New("NorBits login succeeded, but the retried search is not authenticated.") //nolint:staticcheck // upstream NorBits.cs error text, kept 1:1
	}

	return response, nil
}

// searchRequest ports upstream QueryTrackerAsync
// (RequestWithCookiesAndRetryAsync with the search URL as referer).
func (n *NorBits) searchRequest(ctx context.Context, request string) (*httpclient.Result, error) {
	if n.Client == nil {
		return nil, errors.New("http client not configured")
	}
	return n.Client.Do(ctx, n.ID(), &httpclient.Request{
		URL:     request,
		Cookies: n.CookieHeader(),
		Referer: n.searchURL(),
	})
}

// buildQuery ports upstream BuildQuery: the term is either an imdbsearch,
// a latin-1-encoded search or an empty "search="; parameters are
// incldead/fullsearch/scenerelease (+FL for freeleech); tracker categories
// are appended raw (upstream string.Join, no re-encoding).
func (n *NorBits) buildQuery(term string, query *models.TorznabQuery) string {
	searchURL := n.searchURL()

	// Building our tracker query
	parameters := shared.QueryParams{}
	parameters.Add("incldead", "1")
	parameters.Add("fullsearch", map[bool]string{true: "1", false: "0"}[n.cfg.UseFullSearch.Value])
	parameters.Add("scenerelease", "0")

	var searchterm string
	if strings.TrimSpace(query.ImdbID) != "" {
		searchterm = "imdbsearch=" + query.ImdbID
	} else if strings.TrimSpace(term) != "" {
		searchterm = "search=" + norbitsEncodeLatin1(term)
	} else {
		// Showing all torrents (just for output function)
		searchterm = "search="
	}

	if n.cfg.Freeleech.Value {
		parameters.Add("FL", "1")
	}

	// Building our query
	searchURL += "?" + searchterm + "&" + parameters.GetQueryString()

	categoriesList := n.Caps.Categories.MapTorznabCapsToTrackers(query, false)
	if len(categoriesList) > 0 {
		searchURL += "&" + strings.Join(categoriesList, "&")
	}

	n.log().Debug("Built query for \"" + term + "\"... " + searchURL)

	return searchURL
}

// parseSearchPage ports the upstream row loop. found=false mirrors the
// "no rows → break" path; a row parse failure mirrors OnParseError throwing
// ("Parse error" for the whole query).
func (n *NorBits) parseSearchPage(body []byte) (releases []*models.ReleaseInfo, found bool, err error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, false, n.parseFailure(string(body), err)
	}

	// FindTorrentRows: #torrentTable > tbody > tr, header row skipped
	all := doc.Find("#torrentTable > tbody > tr")
	rows := all.Slice(1, all.Length())
	if rows.Length() == 0 {
		return nil, false, nil
	}

	var parseErr error
	rows.EachWithBreak(func(_ int, row *goquery.Selection) bool {
		release, err := n.parseRow(row)
		if err != nil {
			parseErr = err
			return false
		}
		releases = append(releases, release)
		return true
	})
	if parseErr != nil {
		return nil, false, n.parseFailure(string(body), parseErr)
	}
	return releases, true, nil
}

func (n *NorBits) parseRow(row *goquery.Selection) (*models.ReleaseInfo, error) {
	linkHref := row.Find(`td:nth-of-type(2) > a[href*="download.php?id="]`).First().AttrOr("href", "")
	link, err := url.Parse(n.SiteLink() + strings.TrimLeft(linkHref, "/"))
	if err != nil {
		return nil, err
	}
	qDetails := row.Find(`td:nth-of-type(2) > a[href*="details.php?id="]`).First()
	if qDetails.Length() == 0 {
		return nil, errors.New("missing details link")
	}

	title := strings.TrimSpace(qDetails.AttrOr("title", ""))
	details, err := url.Parse(n.SiteLink() + strings.TrimLeft(qDetails.AttrOr("href", ""), "/"))
	if err != nil {
		return nil, err
	}

	mainCategory := norbitsRowCategory(row, "main_cat[]")
	secondCategory := norbitsRowCategory(row, "sub2_cat[]")

	var catParts []string
	if strings.TrimSpace(mainCategory) != "" {
		catParts = append(catParts, mainCategory)
	}
	if strings.TrimSpace(secondCategory) != "" {
		catParts = append(catParts, secondCategory)
	}
	cat := strings.Join(catParts, "&")

	seeders, _ := parseutil.CoerceInt(row.Find("td:nth-of-type(9)").Text())
	leechers, _ := parseutil.CoerceInt(row.Find("td:nth-of-type(10)").Text())

	size := parseutil.GetBytes(row.Find("td:nth-of-type(7)").Text())

	files, _ := parseutil.CoerceInt(strings.TrimSpace(row.Find("td:nth-of-type(3) > a").First().Text()))

	// grabs: the leading text node of the 8th cell
	// (upstream: td.FirstChild.TextContent.Trim())
	grabsText := ""
	if cell := row.Find("td:nth-of-type(8)").First(); cell.Length() > 0 {
		if node := cell.Get(0); node != nil && node.FirstChild != nil && node.FirstChild.Type == html.TextNode {
			grabsText = strings.TrimSpace(node.FirstChild.Data)
		}
	}
	grabs, _ := parseutil.CoerceInt(grabsText)

	// upstream format "yyyy-MM-ddHH:mm:ss" — no space between date and time
	publishDate, err := time.Parse("2006-01-0215:04:05", strings.TrimSpace(row.Find("td:nth-of-type(5)").Text()))
	if err != nil {
		return nil, err
	}

	minimumRatio := 1.0
	minimumSeedTime := int64(172800) // 48 hours
	dvf := 1.0
	uvf := 1.0
	peers := seeders + leechers

	release := &models.ReleaseInfo{
		Guid:                 details,
		Details:              details,
		Link:                 link,
		Title:                title,
		Category:             n.Caps.Categories.MapTrackerCatToNewznab(cat),
		Size:                 &size,
		Files:                &files,
		Grabs:                &grabs,
		Seeders:              &seeders,
		Peers:                &peers,
		PublishDate:          publishDate,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &uvf,
		MinimumRatio:         &minimumRatio,
		MinimumSeedTime:      &minimumSeedTime,
	}

	var genres []string
	if g := row.Find("span.genres").First(); g.Length() > 0 {
		genresText := strings.TrimSpace(g.Text())
		if genresText != "" {
			genresText = strings.ReplaceAll(genresText, "\u00A0", " ")
			genresText = strings.ReplaceAll(genresText, "(", "")
			genresText = strings.ReplaceAll(genresText, ")", "")
			genresText = strings.ReplaceAll(genresText, " | ", ",")
			release.Description = genresText
			// upstream Union(split(',')) keeps the raw parts (spaces and
			// empties included), de-duplicated
			for _, genre := range strings.Split(genresText, ",") {
				if !norbitsContains(genres, genre) {
					genres = append(genres, genre)
				}
			}
			release.Genres = genres
		}
	}

	// IMDB
	imdbLink := row.Find(`a[href*="imdb.com/title/tt"]`).First().AttrOr("href", "")
	if imdb, ok := parseutil.GetLongFromString(imdbLink); ok {
		release.Imdb = &imdb
	}

	if row.Find(`img[title="100% freeleech"]`).Length() > 0 {
		dvf = 0
		release.DownloadVolumeFactor = &dvf
	} else if row.Find(`img[title="Halfleech"]`).Length() > 0 {
		dvf = 0.5
		release.DownloadVolumeFactor = &dvf
	} else if row.Find(`img[title="90% Freeleech"]`).Length() > 0 {
		dvf = 0.1
		release.DownloadVolumeFactor = &dvf
	}

	return release, nil
}

// norbitsRowCategory ports the category extraction: the href of the
// main_cat[]/sub2_cat[] anchor split on '?', last part.
func norbitsRowCategory(row *goquery.Selection, marker string) string {
	href, ok := row.Find("td:nth-of-type(1) a[href*=\"" + marker + "\"]").First().Attr("href")
	if !ok {
		return ""
	}
	parts := strings.Split(href, "?")
	return parts[len(parts)-1]
}

func (n *NorBits) parseFailure(dump string, err error) error {
	n.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: n.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

func norbitsContains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// norbitsEncodeLatin1 mirrors WebUtilityHelpers.UrlEncode(term,
// Encoding.GetEncoding(28591)): the string is first encoded to ISO-8859-1
// bytes (runes above 0xFF become '?', the .NET encoder fallback) and the
// bytes are URL-encoded with '+' for spaces.
func norbitsEncodeLatin1(s string) string {
	buf := make([]byte, 0, len(s))
	for _, r := range s {
		if r <= 0xFF {
			buf = append(buf, byte(r))
		} else {
			buf = append(buf, '?')
		}
	}
	return shared.Encode(string(buf))
}

// norbitsRemoveDiacritics mirrors StringUtil.RemoveDiacritics: NFD
// decomposition, non-spacing marks dropped. Upstream does not recompose to
// NFC afterwards, so neither do we.
func norbitsRemoveDiacritics(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)))
	out, _, err := transform.String(t, s)
	if err != nil {
		return s
	}
	return out
}

// setCookiePairs mirrors upstream HttpWebClient2's WebResult.Cookies
// construction: every Set-Cookie header is reduced to its name=value pair,
// grouped by name with only the last value kept, space-joined.
func setCookiePairs(h http.Header) string {
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
