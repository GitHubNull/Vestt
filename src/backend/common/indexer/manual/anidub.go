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
	"golang.org/x/net/html"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// formPairs is an ordered list of form fields (upstream
// List<KeyValuePair<string,string>> / Dictionary insertion order). The Go
// client stack's url.Values serialises alphabetically, which would not be
// 1:1 with the upstream wire body, so login/search posts go through
// RawBody + formPairs.encode() (WebUtility.UrlEncode-compatible encoding).
type formPairs [][2]string

// encode mirrors StringUtil.GetQueryString / WebUtility.UrlEncode
// ('+' for spaces, upper-case hex, order preserved).
func (p formPairs) encode() string {
	parts := make([]string, len(p))
	for i, kv := range p {
		parts[i] = shared.Encode(kv[0]) + "=" + shared.Encode(kv[1])
	}
	return strings.Join(parts, "&")
}

// formContentType is the header upstream WebClient sets for form posts.
var formContentType = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

// AniDUB ports Jackett.Common/Indexers/Definitions/AniDUB.cs.
//
// mechanism: html-login
//
// Ported from: Definitions/AniDUB.cs.
type AniDUB struct {
	indexer.Base
	cfg *anidubConfig
}

// anidubConfig ports upstream ConfigurationDataAniDub
// (ConfigurationDataBasicLogin + StripRussianTitle).
type anidubConfig struct {
	configdata.ConfigData
	Username          *configdata.StringItem
	Password          *configdata.StringItem
	Instructions      *configdata.InfoItem
	StripRussianTitle *configdata.BoolItem
}

func newAnidubConfig() *anidubConfig {
	return &anidubConfig{
		ConfigData:        *configdata.NewConfigData(),
		Username:          configdata.NewStringItem("Username"),
		Password:          configdata.NewStringItem("Password"),
		Instructions:      configdata.NewInfoItem("", ""),
		StripRussianTitle: configdata.NewBoolItem("Strip Russian Title"),
	}
}

var (
	// EpisodeInfoRegex: "[1-9 из 12]" / "[1 (something) из 12]" episode blocks.
	anidubEpisodeInfoRe = regexp.MustCompile(`(?i)\[(.*?)(?: \(.*?\))? из (.*?)\]$`)
	// SeasonInfoQueryRegex: "S2E1" → "TV-2" (tracker convention).
	anidubSeasonInfoQueryRe = regexp.MustCompile(`(?i)S(\d+)(?:E\d*)`)
	// SeasonInfoRegex: "TV-2" / "ТВ-2" season tags.
	anidubSeasonInfoRe = regexp.MustCompile(`(?i)(?:(?:TV-)|(?:ТВ-))(\d+)`)
	// StripRussianTitleRegex strips "Russian / " prefixes.
	anidubStripRussianTitleRe = regexp.MustCompile(`^.*?\/\s*`)
)

// anidubCategoriesMap mirrors upstream CategoriesMap. Order matters:
// the longest paths come first so StartsWith picks the most specific one.
var anidubCategoriesMap = []struct {
	path string
	cat  string
}{
	{"/anime_tv/full", "14"},
	{"/anime_tv/anime_ongoing", "10"},
	{"/anime_tv/shonen", "11"},
	{"/anime_tv", "2"},
	{"/xxx", "13"},
	{"/manga", "15"},
	{"/ost", "16"},
	{"/podcast", "17"},
	{"/anime_movie", "3"},
	{"/anime_ova/anime_ona", "5"},
	{"/anime_ova", "4"},
	{"/dorama/japan_dorama", "6"},
	{"/dorama/korea_dorama", "7"},
	{"/dorama/china_dorama", "8"},
	{"/dorama", "9"},
	{"/anons_ongoing", "12"},
}

// NewAniDUB builds the indexer (services.Factory shape).
func NewAniDUB(deps services.Deps) (indexer.Indexer, error) {
	cfg := newAnidubConfig()
	// upstream: StripRussianTitle = true.
	cfg.StripRussianTitle.Value = true
	a := &AniDUB{cfg: cfg}
	a.Base = indexer.Base{
		IDValue:          "anidub",
		NameValue:        "AniDUB",
		DescriptionValue: "AniDUB is a RUSSIAN Semi-Private tracker and release group for Anime",
		SiteLinkValue:    "https://tr.anidub.com/",
		DefaultSiteLink:  "https://tr.anidub.com/",
		LangValue:        "ru-RU",
		TypeValue:        "semi-private",
		Caps:             anidubCaps(),
		Config:           cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return a, nil
}

func anidubCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	c := caps.Categories
	c.AddCategoryMapping("2", models.CatTVAnime, "Аниме TV")
	c.AddCategoryMapping("14", models.CatTVAnime, "Аниме TV / Законченные сериалы")
	c.AddCategoryMapping("10", models.CatTVAnime, "Аниме TV / Аниме Ongoing")
	c.AddCategoryMapping("11", models.CatTVAnime, "Аниме TV / Многосерийный сёнэн")
	c.AddCategoryMapping("13", models.CatXXX, "18+")
	c.AddCategoryMapping("15", models.CatBooksComics, "Манга")
	c.AddCategoryMapping("16", models.CatAudio, "OST")
	c.AddCategoryMapping("17", models.CatAudio, "Подкасты")
	c.AddCategoryMapping("3", models.CatTVAnime, "Аниме Фильмы")
	c.AddCategoryMapping("4", models.CatTVAnime, "Аниме OVA")
	c.AddCategoryMapping("5", models.CatTVAnime, "Аниме OVA |- Аниме ONA")
	c.AddCategoryMapping("9", models.CatTV, "Дорамы")
	c.AddCategoryMapping("6", models.CatTV, "Дорамы / Японские Сериалы и Фильмы")
	c.AddCategoryMapping("7", models.CatTV, "Дорамы / Корейские Сериалы и Фильмы")
	c.AddCategoryMapping("8", models.CatTV, "Дорамы / Китайские Сериалы и Фильмы")
	c.AddCategoryMapping("12", models.CatOther, "Аниме Ongoing Анонсы")
	c.AddCategoryMapping("1", models.CatOther, "Новости проекта Anidub")
	return caps
}

// log is a nil-safe logger (Base.Logger may be nil).
func (a *AniDUB) log() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

func (a *AniDUB) loginURL() string  { return a.SiteLink() + "index.php" }
func (a *AniDUB) searchURL() string { return a.SiteLink() + "index.php?do=search" }

// ApplyConfiguration ports upstream AniDUB.ApplyConfiguration: post the
// login form, then ConfigureIfOK against the "index.php?action=logout"
// marker (upstream returns IndexerConfigurationStatus.Completed).
func (a *AniDUB) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := a.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := a.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := a.doLogin(ctx); err != nil {
		return indexer.ConfigCompleted, err
	}
	return indexer.ConfigCompleted, nil
}

// doLogin ports the upstream login POST (RequestLoginAndFollowRedirect
// with returnCookiesFromFirstCall: true) + ConfigureIfOK.
func (a *AniDUB) doLogin(ctx context.Context) error {
	if a.Client == nil {
		return errors.New("http client not configured")
	}
	pairs := formPairs{
		{"login_name", a.cfg.Username.Value},
		{"login_password", a.cfg.Password.Value},
		{"login", "submit"},
	}
	res, err := a.Client.Do(ctx, a.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     a.loginURL(),
		RawBody: pairs.encode(),
		Headers: formContentType,
		Cookies: a.CookieHeader(),
	})
	if err != nil {
		return err
	}
	if !anidubIsAuthorized(res.Body) {
		doc, docErr := goquery.NewDocumentFromReader(bytes.NewReader(res.Body))
		message := ""
		if docErr == nil {
			message = strings.TrimSpace(doc.Find("#content .berror .berror_c").First().Text())
		}
		return &indexer.IndexerError{IndexerID: a.ID(), Kind: indexer.KindConfig, Message: message}
	}
	if u, err := url.Parse(a.loginURL()); err == nil {
		a.SetCookieHeader(a.Client.CookieHeader(a.ID(), u))
	}
	a.SetConfigured(true)
	a.SaveConfig(ctx)
	return nil
}

// Download ports upstream AniDUB.Download: EnsureAuthorized then base.
func (a *AniDUB) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	if err := a.ensureAuthorized(ctx); err != nil {
		return nil, err
	}
	return a.Base.Download(ctx, link)
}

func (a *AniDUB) ensureAuthorized(ctx context.Context) error {
	res, err := a.get(ctx, a.SiteLink())
	if err != nil {
		return err
	}
	if !anidubIsAuthorized(res.Body) {
		return a.doLogin(ctx)
	}
	return nil
}

func anidubIsAuthorized(body []byte) bool {
	return bytes.Contains(body, []byte("index.php?action=logout"))
}

func (a *AniDUB) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return a.ResultsForQueryWith(ctx, q, isMeta, a.perform)
}

func (a *AniDUB) Test(ctx context.Context) error {
	return a.Base.Test(ctx, a.perform)
}

// perform ports upstream PerformQuery: an empty search term (or a self-test)
// browses the latest releases, anything else runs the search POST.
func (a *AniDUB) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if query.IsTest || strings.TrimSpace(query.SearchTerm) == "" {
		return a.fetchNewReleases(ctx)
	}
	return a.performSearch(ctx, query)
}

const anidubSiteError = "There is a problem at the AniDUB WEB site, view the Jackett logs for more details."

func anidubSiteBroken(body []byte) bool {
	return bytes.Contains(body, []byte("Fatal error: Uncaught exception")) ||
		bytes.Contains(body, []byte("MySQL Error!"))
}

func (a *AniDUB) fetchNewReleases(ctx context.Context) ([]*models.ReleaseInfo, error) {
	const releaseLinksSelector = "#dle-content > .story > .story_h > .lcol > h2 > a"
	res, err := a.get(ctx, a.SiteLink())
	if err != nil {
		return nil, err
	}
	if anidubSiteBroken(res.Body) {
		a.log().Error("[AniDUB]" + string(res.Body))
		return nil, errors.New(anidubSiteError) //nolint:staticcheck // upstream AniDUB.cs siteError text, kept 1:1
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(res.Body))
	if err != nil {
		return nil, a.parseFailure(string(res.Body), err)
	}
	var releases []*models.ReleaseInfo
	var fetchErr error
	doc.Find(releaseLinksSelector).EachWithBreak(func(_ int, linkNode *goquery.Selection) bool {
		showURL := linkNode.AttrOr("href", "")
		showReleases, err := a.fetchShowReleases(ctx, showURL)
		if err != nil {
			fetchErr = err
			return false
		}
		releases = append(releases, showReleases...)
		return true
	})
	if fetchErr != nil {
		return nil, fetchErr
	}
	return releases, nil
}

// fetchShowReleases ports upstream FetchShowReleases: one show page holds
// every quality release under #tabs .torrent_c.
func (a *AniDUB) fetchShowReleases(ctx context.Context, showURL string) ([]*models.ReleaseInfo, error) {
	const releasesSelector = "#tabs .torrent_c > div"

	uri, err := url.Parse(showURL)
	if err != nil {
		return nil, a.parseFailure(showURL, err)
	}
	categories := a.parseCategories(uri)
	if categories == nil {
		// If no category then it should be a news topic (doesn't happen often).
		return nil, nil
	}

	res, err := a.get(ctx, showURL)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(res.Body))
	if err != nil {
		return nil, a.parseFailure(string(res.Body), err)
	}
	content := doc.Find("#dle-content").First()
	if content.Length() == 0 {
		return nil, a.parseFailure(string(res.Body), errors.New("missing #dle-content"))
	}

	date := a.dateFromShowPage(showURL, content)

	baseTitle, err := a.baseTitle(categories, content)
	if err != nil {
		return nil, a.parseFailure(string(res.Body), err)
	}
	poster, err := a.poster(showURL, content)
	if err != nil {
		return nil, a.parseFailure(string(res.Body), err)
	}

	var releases []*models.ReleaseInfo
	var parseErr error
	content.Find(releasesSelector).EachWithBreak(func(_ int, releaseNode *goquery.Selection) bool {
		children := releaseNode.Children()
		hasTorrentH := children.FilterFunction(func(_ int, n *goquery.Selection) bool {
			return strings.Contains(n.AttrOr("class", ""), "torrent_h")
		}).Length() > 0

		var tabNode *goquery.Selection
		if hasTorrentH {
			// No quality, one tab, seems like a buggy page
			tabNode = releaseNode
		} else {
			// First child div (upstream: TagName equals "div", case-insensitive).
			tabNode = children.Filter("div").First()
			if tabNode.Length() == 0 {
				parseErr = errors.New("missing tab div")
				return false
			}
		}

		release, err := a.buildRelease(showURL, uri, categories, baseTitle, poster, date, tabNode)
		if err != nil {
			parseErr = err
			return false
		}
		releases = append(releases, release)
		return true
	})
	if parseErr != nil {
		return nil, a.parseFailure(string(res.Body), parseErr)
	}
	return releases, nil
}

func (a *AniDUB) buildRelease(showURL string, uri *url.URL, categories []int, baseTitle string, poster *url.URL, date time.Time, tabNode *goquery.Selection) (*models.ReleaseInfo, error) {
	seeders, err := anidubIntSelection(tabNode, ".list.down > .li_distribute_m")
	if err != nil {
		return nil, err
	}
	leechers, err := anidubIntSelection(tabNode, ".list.down > .li_swing_m")
	if err != nil {
		return nil, err
	}
	grabs, err := anidubIntSelection(tabNode, ".list.down > .li_download_m")
	if err != nil {
		return nil, err
	}

	torrentID, err := anidubTorrentID(tabNode)
	if err != nil {
		return nil, err
	}
	guid, err := url.Parse(anidubAddQueryString(showURL, "id", torrentID))
	if err != nil {
		return nil, err
	}
	link, err := url.Parse(a.SiteLink() + "engine/download.php?id=" + torrentID)
	if err != nil {
		return nil, err
	}

	sizeSel := tabNode.Find(".list.down > .red").First()
	if sizeSel.Length() == 0 {
		return nil, errors.New("missing size node")
	}
	size := parseutil.GetBytes(sizeSel.Text())

	// Appending id to differentiate between different quality versions
	dvf := 0.0
	uvf := 0.0
	peers := leechers + seeders
	return &models.ReleaseInfo{
		Title:                anidubBuildTitle(baseTitle, tabNode.Parent()),
		Guid:                 guid,
		Details:              uri,
		Link:                 link,
		PublishDate:          date,
		Category:             categories,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &uvf,
		Size:                 &size,
		Grabs:                &grabs,
		Description:          anidubDescription(tabNode),
		Seeders:              &seeders,
		Peers:                &peers,
		Poster:               poster,
	}, nil
}

// anidubIntSelection mirrors the upstream int.TryParse(...) selector reads
// (a missing node is a NullReferenceException upstream → parse error here).
func anidubIntSelection(tabNode *goquery.Selection, selector string) (int64, error) {
	sel := tabNode.Find(selector).First()
	if sel.Length() == 0 {
		return 0, fmt.Errorf("missing node %q", selector)
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(sel.Text()), 10, 64)
	return n, nil
}

func anidubTorrentID(tabNode *goquery.Selection) (string, error) {
	nodeID := tabNode.AttrOr("id", "")
	if nodeID == "" {
		return "", errors.New("missing torrent id")
	}
	// Format is "torrent_{id}_info"
	return strings.ReplaceAll(strings.ReplaceAll(nodeID, "torrent_", ""), "_info", ""), nil
}

func anidubDescription(tabNode *goquery.Selection) string {
	return strings.TrimSpace(tabNode.Find(".tech > pre").First().Text())
}

func anidubBuildTitle(baseTitle string, releaseNode *goquery.Selection) string {
	quality := anidubQuality(releaseNode)
	if strings.TrimSpace(quality) != "" {
		return fmt.Sprintf("%s [%s]", baseTitle, quality)
	}
	return baseTitle
}

func anidubQuality(releaseNode *goquery.Selection) string {
	// For some releases there's no block with quality
	if releaseNode == nil || releaseNode.Length() == 0 {
		return ""
	}
	nodeID := strings.TrimSpace(releaseNode.AttrOr("id", ""))
	if nodeID == "" {
		return ""
	}
	switch strings.ToLower(nodeID) {
	case "tv720":
		return "HDTV 720p"
	case "tv1080":
		return "HDTV 1080p"
	case "bd720":
		return "BDRip 720p"
	case "bd1080":
		return "BDRip 1080p"
	case "hwp":
		return "SDTV"
	default:
		return strings.ToUpper(nodeID)
	}
}

func (a *AniDUB) poster(showURL string, content *goquery.Selection) (*url.URL, error) {
	posterNode := content.Find(".poster_bg .poster img").First()
	if posterNode.Length() == 0 {
		return nil, errors.New("missing poster node")
	}
	posterSrc := posterNode.AttrOr("src", "")
	poster, err := url.Parse(posterSrc)
	if err == nil && poster.IsAbs() {
		return poster, nil
	}
	a.log().Warn(fmt.Sprintf("[AniDub] Poster URL couldn't be parsed on '%s'. Poster node src: %s", showURL, posterSrc))
	return nil, nil
}

func (a *AniDUB) baseTitle(categories []int, content *goquery.Selection) (string, error) {
	domTitle := content.Find("#news-title").First()
	if domTitle.Length() == 0 {
		return "", errors.New("missing #news-title")
	}

	baseTitle := strings.TrimSpace(domTitle.Text())
	if a.cfg.StripRussianTitle.Value {
		baseTitle = anidubStripRussianTitleRe.ReplaceAllString(baseTitle, "")
	}
	baseTitle = anidubFixBookInfo(baseTitle)

	isShow := anidubContainsInt(categories, models.CatTVAnime.ID)
	if isShow {
		baseTitle = anidubFixShowTitle(baseTitle)
	} else {
		// Just fix TV-\d to S\d and [\d+] to E\d
		baseTitle = anidubFixSeasonInfo(baseTitle)
		baseTitle = anidubFixEpisodeInfo(baseTitle)
	}

	baseTitle = anidubFixMovieInfo(baseTitle)

	// Mostly audio is in original name, which can't be known during parsing
	// Skipping appending russing language tag
	isAudio := anidubContainsInt(categories, models.CatAudio.ID)
	if !isAudio {
		baseTitle = anidubAppendRussianLanguageTag(baseTitle)
	}

	return strings.TrimSpace(baseTitle), nil
}

func anidubContainsInt(list []int, v int) bool {
	for _, n := range list {
		if n == v {
			return true
		}
	}
	return false
}

func anidubFixShowTitle(title string) string {
	seasonNum := anidubSeasonNum(title)

	// Remove season info
	title = anidubSeasonInfoRe.ReplaceAllString(title, "")

	// Normalize for parsing usages: should look like S01E01-E09
	return anidubEpisodeInfoRe.ReplaceAllStringFunc(title, func(m string) string {
		g := anidubEpisodeInfoRe.FindStringSubmatch(m)
		return fmt.Sprintf("S%02dE01-E%s", seasonNum, g[1])
	})
}

func anidubSeasonNum(title string) int {
	// First season is often skipped so return 1 if nothing matched
	if m := anidubSeasonInfoRe.FindStringSubmatch(title); m != nil {
		if seasonNum, err := strconv.Atoi(m[1]); err == nil {
			return seasonNum
		}
	}
	return 1
}

func anidubFixBookInfo(title string) string {
	return strings.ReplaceAll(title, "[Главы ", "[")
}

func anidubFixEpisodeInfo(title string) string {
	return anidubEpisodeInfoRe.ReplaceAllStringFunc(title, func(m string) string {
		g := anidubEpisodeInfoRe.FindStringSubmatch(m)
		return fmt.Sprintf("E01-E%s", g[1])
	})
}

func anidubFixMovieInfo(title string) string {
	return strings.ReplaceAll(title, " [Movie]", "")
}

func anidubFixSeasonInfo(title string) string {
	return anidubSeasonInfoRe.ReplaceAllStringFunc(title, func(m string) string {
		g := anidubSeasonInfoRe.FindStringSubmatch(m)
		n, _ := strconv.Atoi(g[1])
		return fmt.Sprintf("S%02d", n)
	})
}

func anidubAppendRussianLanguageTag(title string) string { return title + " [RUS]" }

// dateFromShowPage ports upstream GetDateFromShowPage (Russian Standard
// Time is +03:00, no DST; AssumeUniversal parsing).
func (a *AniDUB) dateFromShowPage(showURL string, content *goquery.Selection) time.Time {
	const dateLayout = "2-01-2006, 15:04"

	// Russian Standard Time is +03:00, no DST
	russianStandardTimeDiff := 3 * time.Hour
	nowLocal := time.Now().UTC().Add(russianStandardTimeDiff)

	dateText := anidubDateText(content)
	dateText = strings.ReplaceAll(dateText, "Вчера", nowLocal.Add(-24*time.Hour).Format("2-01-2006"))
	dateText = strings.ReplaceAll(dateText, "Сегодня", nowLocal.Format("2-01-2006"))

	if date, err := time.ParseInLocation(dateLayout, dateText, time.UTC); err == nil {
		return date.Add(-russianStandardTimeDiff)
	}

	a.log().Warn(fmt.Sprintf("[AniDub] Date time couldn't be parsed on '%s'. Date text: %s", showURL, dateText))
	return time.Now().UTC()
}

func anidubDateText(content *goquery.Selection) string {
	const dateSelector = ".story_inf > li:nth-child(2)"

	li := content.Find(dateSelector).First()
	if li.Length() == 0 {
		return ""
	}
	last := li.Get(0).LastChild
	if last == nil || last.Type != html.TextNode {
		return ""
	}
	return strings.TrimSpace(last.Data)
}

func (a *AniDUB) parseCategories(showURI *url.URL) []int {
	path := strings.ToLower(showURI.Path)
	for _, m := range anidubCategoriesMap {
		if strings.HasPrefix(path, m.path) {
			return a.Caps.Categories.MapTrackerCatToNewznab(m.cat)
		}
	}
	return nil
}

func (a *AniDUB) performSearch(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	const searchLinkSelector = "#dle-content > .searchitem > h3 > a"

	res, err := a.Client.Do(ctx, a.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     a.searchURL(),
		RawBody: a.preparePostData(query).encode(),
		Headers: formContentType,
		Cookies: a.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	if anidubSiteBroken(res.Body) {
		a.log().Error("[AniDUB]" + string(res.Body))
		return nil, errors.New(anidubSiteError) //nolint:staticcheck // upstream AniDUB.cs siteError text, kept 1:1
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(res.Body))
	if err != nil {
		return nil, a.parseFailure(string(res.Body), err)
	}
	var releases []*models.ReleaseInfo
	var fetchErr error
	doc.Find(searchLinkSelector).EachWithBreak(func(_ int, linkNode *goquery.Selection) bool {
		link := linkNode.AttrOr("href", "")
		showReleases, err := a.fetchShowReleases(ctx, link)
		if err != nil {
			fetchErr = err
			return false
		}
		releases = append(releases, showReleases...)
		return true
	})
	if fetchErr != nil {
		return nil, fetchErr
	}

	var filtered []*models.ReleaseInfo
	for _, release := range releases {
		if query.MatchQueryStringAND(release.Title, 0, "") {
			filtered = append(filtered, release)
		}
	}
	return filtered, nil
}

func (a *AniDUB) preparePostData(query *models.TorznabQuery) formPairs {
	pairs := formPairs{
		{"do", "search"},
		{"subaction", "search"},
		{"search_start", "1"},
		{"full_search", "1"},
		{"result_from", "1"},
		{"story", a.normalizeSearchQuery(query)},
		{"titleonly", "0"},
		{"searchuser", ""},
		{"replyless", "0"},
		{"replylimit", "0"},
		{"searchdate", "0"},
		{"beforeafter", "after"},
		{"sortby", ""},
		{"resorder", "desc"},
		{"showposts", "1"},
	}
	pairs = append(pairs, a.prepareCategoriesQuery(query)...)
	return pairs
}

func (a *AniDUB) prepareCategoriesQuery(query *models.TorznabQuery) formPairs {
	categories := []string{"0"}
	if query.HasSpecifiedCategories() {
		categories = a.Caps.Categories.MapTorznabCapsToTrackers(query, false)
	}
	var pairs formPairs
	for _, category := range categories {
		pairs = append(pairs, [2]string{"catlist[]", category})
	}
	return pairs
}

func (a *AniDUB) normalizeSearchQuery(query *models.TorznabQuery) string {
	searchQuery := query.SanitizedSearchTerm()

	// Convert S\dE\d to TV-{Season} because of the convention on the tracker
	searchQuery = anidubSeasonInfoQueryRe.ReplaceAllStringFunc(searchQuery, func(m string) string {
		g := anidubSeasonInfoQueryRe.FindStringSubmatch(m)
		season, _ := strconv.Atoi(g[1])
		return fmt.Sprintf("TV-%d", season)
	})

	if query.Season != nil && *query.Season > 0 {
		// Replace "TV- " with season from query
		searchQuery = anidubSeasonInfoRe.ReplaceAllString(searchQuery, "")
		searchQuery += fmt.Sprintf(" TV-%d", *query.Season)
	}

	return strings.ToLower(searchQuery)
}

// anidubAddQueryString mirrors QueryHelpers.AddQueryString(url, key, value).
func anidubAddQueryString(u, key, value string) string {
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + key + "=" + shared.Encode(value)
}

func (a *AniDUB) parseFailure(dump string, err error) error {
	a.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: a.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

func (a *AniDUB) get(ctx context.Context, target string) (*httpclient.Result, error) {
	if a.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := a.Client.Do(ctx, a.ID(), &httpclient.Request{
		URL:     target,
		Cookies: a.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, &httpclient.ErrSiteDown{URL: target, Status: res.StatusCode}
	}
	return res, nil
}
