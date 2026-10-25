// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
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

// IPTorrents ports Jackett.Common/Indexers/Definitions/IPTorrents.cs.
//
// mechanism: cookie html
//
// Correction to the SPEC-09 batch-2 table ("html-login（分类 URL 模板）"):
// upstream authenticates with a manually pasted cookie (+ optional
// User-Agent override) and scrapes the torrents HTML table; tracker
// categories become bare query-string path segments ("/t?72&…").
//
// 澄清记录:
//   - upstream constructor adds "sort"/"freeleech"/"Account Inactivity" via
//     AddDynamic; the Go port declares them as exported fields (auto-
//     registered, same ids derived from the item names).
//   - upstream throws a plain Exception when the page lacks "/lout.php"
//     (expired cookie); per SPEC-09 §4 the Go port maps it to
//     IndexerError{Kind: Auth} with the upstream message preserved.
//   - upstream's "No Torrents Found!" profile-config error maps to
//     IndexerError{Kind: Config}.
//   - upstream parses rows inside try/catch and only logs via OnParseError,
//     returning the releases accumulated so far; the Go port logs and
//     returns the partial set as well.
//
// Ported from: Definitions/IPTorrents.cs,
// Models/IndexerConfig/ConfigurationDataCookieUA.cs.
type IPTorrents struct {
	indexer.Base
	cfg *ipTorrentsConfig
}

type ipTorrentsConfig struct {
	configdata.ConfigData
	Cookie                *configdata.StringItem
	CookieInstructions    *configdata.InfoItem
	UserAgent             *configdata.StringItem
	UserAgentInstructions *configdata.InfoItem
	Instructions          *configdata.InfoItem
	Sort                  *configdata.SelectItem
	Freeleech             *configdata.BoolItem
	AccountInactivity     *configdata.InfoItem
}

const ipTorrentsInstructions = "In the website Settings:<ul><li>change the <b>Torrents per page</b> option to <b>100</b></li><li><b>Tick</b> the <b>Torrents - Show files count</b> checkbox</li><li>Set the <b>torrents category column</b> to <b>Icons</b></li></ul>Otherwise the indexer may not return results."

const ipTorrentsCookieInstructions = "Please enter the cookie for the site manually. <a href=\"https://github.com/Jackett/Jackett/wiki/Finding-cookies\" target=\"_blank\">See here</a> on how get the cookies." +
	"<br>Example cookie header (usually longer than this):<br><code>PHPSESSID=8rk27odm; ipsconnect_63ad9c=1; more_stuff=etc;</code>"

const ipTorrentsUserAgentInstructions = "<ol><li>From the same place you fetched the cookie,<li>Find <b>'user-agent:'</b> in the <b>Request Headers</b> section<li><b>Select</b>" +
	"and <b>Copy</b> the whole user-agent string <i>(everything after 'user-agent: ')</i> and <b>Paste</b> here.</ol>"

const ipTorrentsAccountInactivity = "All members are required to log into their account at least every 89 days or you will be deleted without warning."

func newIPTorrentsConfig() *ipTorrentsConfig {
	return &ipTorrentsConfig{
		ConfigData:            *configdata.NewConfigData(),
		Cookie:                configdata.NewStringItem("Cookie"),
		CookieInstructions:    configdata.NewInfoItem("Cookie Instructions", ipTorrentsCookieInstructions),
		UserAgent:             configdata.NewStringItem("User-Agent"),
		UserAgentInstructions: configdata.NewInfoItem("User Agent Instructions", ipTorrentsUserAgentInstructions),
		Instructions:          configdata.NewInfoItem("", ipTorrentsInstructions),
		Sort: configdata.NewSelectItem("Sort requested from site", []configdata.Option{
			{Value: "time", Label: "created"},
			{Value: "size", Label: "size"},
			{Value: "seeders", Label: "seeders"},
			{Value: "name", Label: "title"},
		}),
		Freeleech:         configdata.NewBoolItem("Search freeleech only"),
		AccountInactivity: configdata.NewInfoItem("Account Inactivity", ipTorrentsAccountInactivity),
	}
}

// NewIPTorrents builds the indexer (services.Factory shape).
func NewIPTorrents(deps services.Deps) (indexer.Indexer, error) {
	i := &IPTorrents{cfg: newIPTorrentsConfig()}
	i.cfg.Sort.Value = "time" // upstream { Value = "time" }
	i.Base = indexer.Base{
		IDValue:          "iptorrents",
		NameValue:        "IPTorrents",
		DescriptionValue: "IPTorrents is a Private site. Always a step ahead.",
		SiteLinkValue:    "https://iptorrents.com/",
		DefaultSiteLink:  "https://iptorrents.com/",
		AltSiteLinks: []string{
			"https://iptorrents.com/",
			"https://www.iptorrents.com/",
			"https://iptorrents.me/",
			"https://nemo.iptorrents.com/",
			"https://ip.findnemo.net/",
			"https://ip.getcrazy.me/",
			"https://ip.workisboring.net/",
			"https://ipt.cool/",
			"https://ipt.lol/",
			"https://ipt.world/",
			"https://ipt.octopus.town/",
		},
		LegacySiteLinks: []string{
			"https://ipt-update.com/",
			"http://ipt.read-books.org/",
			"http://alien.eating-organic.net/",
			"http://kong.net-freaks.com/",
			"http://ghost.cable-modem.org/",
			"http://logan.unusualperson.com/",
			"https://ipt.rocks/",
			"http://baywatch.workisboring.com/",
			"https://iptorrents.eu/",
			"https://ipt.getcrazy.me/",
			"https://ipt.findnemo.net/",
			"https://ipt.beelyrics.net/",
			"https://ipt.venom.global/",
			"https://ipt.workisboring.net/",
			"https://ip.venom.global/",
		},
		LangValue: "en-US",
		TypeValue: "private",
		Caps:      ipTorrentsCaps(),
		Config:    i.cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	return i, nil
}

func ipTorrentsCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{
		models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID, models.TVParamGenre,
	}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID, models.MovieParamGenre}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.BookSearchParams = []string{models.BookParamQ}
	caps.TVSearchImdbAvailable = true

	c := caps.Categories
	c.AddCategoryMapping("72", models.CatMovies, "Movies")
	c.AddCategoryMapping("87", models.CatMovies3D, "Movie/3D")
	c.AddCategoryMapping("77", models.CatMoviesSD, "Movie/480p")
	c.AddCategoryMapping("101", models.CatMoviesUHD, "Movie/4K")
	c.AddCategoryMapping("89", models.CatMoviesBluRay, "Movie/BD-R")
	c.AddCategoryMapping("90", models.CatMoviesHD, "Movie/BD-Rip")
	c.AddCategoryMapping("96", models.CatMoviesSD, "Movie/Cam")
	c.AddCategoryMapping("6", models.CatMoviesDVD, "Movie/DVD-R")
	c.AddCategoryMapping("48", models.CatMoviesHD, "Movie/HD/Bluray")
	c.AddCategoryMapping("54", models.CatMovies, "Movie/Kids")
	c.AddCategoryMapping("62", models.CatMoviesSD, "Movie/MP4")
	c.AddCategoryMapping("38", models.CatMoviesForeign, "Movie/Non-English")
	c.AddCategoryMapping("68", models.CatMovies, "Movie/Packs")
	c.AddCategoryMapping("20", models.CatMoviesWEBDL, "Movie/Web-DL")
	c.AddCategoryMapping("100", models.CatMoviesHD, "Movie/x265")
	c.AddCategoryMapping("7", models.CatMoviesSD, "Movie/Xvid")

	c.AddCategoryMapping("73", models.CatTV, "TV")
	c.AddCategoryMapping("26", models.CatTVDocumentary, "TV/Documentaries")
	c.AddCategoryMapping("55", models.CatTVSport, "Sports")
	c.AddCategoryMapping("78", models.CatTVSD, "TV/480p")
	c.AddCategoryMapping("23", models.CatTVHD, "TV/BD")
	c.AddCategoryMapping("24", models.CatTVSD, "TV/DVD-R")
	c.AddCategoryMapping("25", models.CatTVSD, "TV/DVD-Rip")
	c.AddCategoryMapping("66", models.CatTVSD, "TV/Mobile")
	c.AddCategoryMapping("82", models.CatTVForeign, "TV/Non-English")
	c.AddCategoryMapping("65", models.CatTV, "TV/Packs")
	c.AddCategoryMapping("83", models.CatTVForeign, "TV/Packs/Non-English")
	c.AddCategoryMapping("79", models.CatTVSD, "TV/SD/x264")
	c.AddCategoryMapping("22", models.CatTVWEBDL, "TV/Web-DL")
	c.AddCategoryMapping("5", models.CatTVHD, "TV/x264")
	c.AddCategoryMapping("99", models.CatTVHD, "TV/x265")
	c.AddCategoryMapping("4", models.CatTVSD, "TV/Xvid")

	c.AddCategoryMapping("74", models.CatConsole, "Games")
	c.AddCategoryMapping("2", models.CatConsoleOther, "Games/Mixed")
	c.AddCategoryMapping("47", models.CatConsoleOther, "Games/Nintendo")
	c.AddCategoryMapping("43", models.CatPCGames, "Games/PC-ISO")
	c.AddCategoryMapping("45", models.CatPCGames, "Games/PC-Rip")
	c.AddCategoryMapping("71", models.CatConsolePS4, "Games/Playstation")
	c.AddCategoryMapping("50", models.CatConsoleWii, "Games/Wii")
	c.AddCategoryMapping("44", models.CatConsoleXBox, "Games/Xbox")

	c.AddCategoryMapping("75", models.CatAudio, "Music")
	c.AddCategoryMapping("3", models.CatAudioMP3, "Music/Audio")
	c.AddCategoryMapping("80", models.CatAudioLossless, "Music/Flac")
	c.AddCategoryMapping("93", models.CatAudio, "Music/Packs")
	c.AddCategoryMapping("37", models.CatAudioVideo, "Music/Video")
	c.AddCategoryMapping("21", models.CatAudioOther, "Podcast")

	c.AddCategoryMapping("76", models.CatOther, "Miscellaneous")
	c.AddCategoryMapping("60", models.CatTVAnime, "Anime")
	c.AddCategoryMapping("1", models.CatPC0day, "Appz")
	c.AddCategoryMapping("86", models.CatPC0day, "Appz/Non-English")
	c.AddCategoryMapping("64", models.CatAudioAudiobook, "AudioBook")
	c.AddCategoryMapping("35", models.CatBooks, "Books")
	c.AddCategoryMapping("102", models.CatBooks, "Books/Non-English")
	c.AddCategoryMapping("94", models.CatBooksComics, "Comics")
	c.AddCategoryMapping("95", models.CatBooksOther, "Educational")
	c.AddCategoryMapping("98", models.CatOther, "Fonts")
	c.AddCategoryMapping("69", models.CatPCMac, "Mac")
	c.AddCategoryMapping("92", models.CatBooksMags, "Magazines / Newspapers")
	c.AddCategoryMapping("58", models.CatPCMobileOther, "Mobile")
	c.AddCategoryMapping("36", models.CatOther, "Pics/Wallpapers")

	c.AddCategoryMapping("88", models.CatXXX, "XXX")
	c.AddCategoryMapping("85", models.CatXXXOther, "XXX/Magazines")
	c.AddCategoryMapping("8", models.CatXXX, "XXX/Movie")
	c.AddCategoryMapping("81", models.CatXXX, "XXX/Movie/0Day")
	c.AddCategoryMapping("91", models.CatXXXPack, "XXX/Packs")
	c.AddCategoryMapping("84", models.CatXXXImageSet, "XXX/Pics/Wallpapers")
	return caps
}

// ApplyConfiguration ports upstream: load the cookie, test-query the tracker,
// and wrap every failure in "Your cookie did not work, …".
func (i *IPTorrents) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := i.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := i.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}

	i.SetCookieHeader(i.cfg.Cookie.Value)

	results, err := i.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		i.SetConfigured(false)
		return indexer.ConfigCompleted, fmt.Errorf("Your cookie did not work, make sure the user agent matches your computer: %s", err.Error()) //nolint:staticcheck // upstream IPTorrents.cs error text, kept 1:1
	}
	if len(results) == 0 {
		i.SetConfigured(false)
		return indexer.ConfigCompleted, errors.New("Your cookie did not work, make sure the user agent matches your computer: Found 0 results in the tracker") //nolint:staticcheck // upstream IPTorrents.cs error text, kept 1:1
	}

	i.SetConfigured(true)
	i.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (i *IPTorrents) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return i.ResultsForQueryWith(ctx, q, isMeta, i.perform)
}

func (i *IPTorrents) Test(ctx context.Context) error {
	return i.Base.Test(ctx, i.perform)
}

// ipValidTagList mirrors upstream validTagList.
var ipValidTagList = []string{
	"action",
	"adventure",
	"animation",
	"biography",
	"comedy",
	"crime",
	"documentary",
	"drama",
	"family",
	"fantasy",
	"game-show",
	"history",
	"horror",
	"music",
	"musical",
	"mystery",
	"news",
	"reality-tv",
	"romance",
	"sci-fi",
	"sitcom",
	"sport",
	"talk-show",
	"thriller",
	"war",
	"western",
}

func (i *IPTorrents) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	/* notes:
	 * IPTorrents can search for genre (tags) using the default title&tags search
	 * qf=
	 * "" = Title and Tags
	 * ti = Title
	 * ta = Tags
	 * all = Title, Tags & Description
	 * adv = Advanced
	 *
	 * But only movies and tv have tags.
	 */

	headers := map[string]string{}
	if i.cfg.UserAgent.Value != "" {
		headers["User-Agent"] = i.cfg.UserAgent.Value
	}

	qc := shared.QueryParams{}

	for _, cat := range i.Caps.Categories.MapTorznabCapsToTrackers(query, false) {
		qc.Set(cat, "")
	}

	if i.cfg.Freeleech.Value {
		qc.Set("free", "on")
	}

	var searchQuery []string

	// IPT uses sphinx, which supports boolean operators and grouping
	if query.IsImdbQuery() {
		searchQuery = append(searchQuery, "+("+query.ImdbID+")")

		// search in description
		qc.Set("qf", "all")
	} else if query.IsGenreQuery() {
		searchQuery = append(searchQuery, "+("+query.Genre+")")
	}

	searchTerm := query.GetQueryString()

	if strings.TrimSpace(searchTerm) != "" {
		if strings.TrimSpace(query.GetEpisodeSearchString()) != "" && query.Season != nil && *query.Season > 0 && strings.TrimSpace(query.Episode) == "" {
			searchTerm += "*"
		}

		searchQuery = append(searchQuery, "+("+searchTerm+")")
	}

	if len(searchQuery) > 0 {
		qc.Set("q", strings.Join(searchQuery, " "))
	}

	qc.Set("o", i.cfg.Sort.Value)

	if query.Limit > 0 && query.Offset > 0 {
		page := query.Offset/query.Limit + 1
		qc.Add("p", strconv.Itoa(page))
	}

	searchURL := i.SiteLink() + "t?" + qc.GetQueryString()

	if i.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := i.Client.Do(ctx, i.ID(), &httpclient.Request{
		URL:     searchURL,
		Referer: i.SiteLink() + "t",
		Headers: headers,
		Cookies: i.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	results := string(res.Body)

	if !strings.Contains(results, "/lout.php") {
		return nil, &indexer.IndexerError{
			IndexerID: i.ID(),
			Kind:      indexer.KindAuth,
			Message:   "The user is not logged in. It is possible that the cookie has expired or you made a mistake when copying it. Please check the settings.",
		}
	}

	if strings.TrimSpace(query.ImdbID) == "" && strings.TrimSpace(query.SearchTerm) == "" && strings.Contains(results, "No Torrents Found!") {
		return nil, &indexer.IndexerError{
			IndexerID: i.ID(),
			Kind:      indexer.KindConfig,
			Message:   "Got No Torrents Found! Make sure your IPTorrents profile config contain proper default category settings.",
		}
	}

	parsed, err := i.parseSearchPage(results, query)
	if err != nil {
		// upstream OnParseError: log the dump and return the partial set
		i.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, results))
		return parsed, nil
	}
	return parsed, nil
}

// ipColDelimiters mirrors upstream char[] delimiters.
var ipColDelimiters = []byte{',', ' ', '/', ')', '(', '.', ';', '[', ']', '"', '|', ':'}

func (i *IPTorrents) parseSearchPage(results string, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(results))
	if err != nil {
		return nil, err
	}

	var headerColumns []string
	doc.Find(`table[id="torrents"] > thead > tr > th`).Each(func(_ int, s *goquery.Selection) {
		headerColumns = append(headerColumns, strings.TrimSpace(s.Text()))
	})
	sizeIndex := ipFindColumnIndexOrDefault(headerColumns, "Sort by size", 5)
	filesIndex := ipFindColumnIndexOrDefault(headerColumns, "Sort by files", -1)

	var parseErr error
	doc.Find(`table[id="torrents"] > tbody > tr`).EachWithBreak(func(_ int, row *goquery.Selection) bool {
		release, err := i.parseRow(row, sizeIndex, filesIndex, headerColumns, query)
		if err != nil {
			if errors.Is(err, errIPNoResults) {
				return true // no results row
			}
			parseErr = err
			return false
		}
		releases = append(releases, release)
		return true
	})
	if parseErr != nil {
		return releases, parseErr
	}
	return releases, nil
}

// errIPNoResults marks a row without a title link (upstream `continue`).
var errIPNoResults = errors.New("row has no title link")

func (i *IPTorrents) parseRow(row *goquery.Selection, sizeIndex, filesIndex int, headerColumns []string, query *models.TorznabQuery) (*models.ReleaseInfo, error) {
	qTitleLink := row.Find("a.hv").First()
	if qTitleLink.Length() == 0 {
		return nil, errIPNoResults
	}

	title := ipCleanTitle(qTitleLink.Text())

	if !query.IsImdbQuery() && !query.IsGenreQuery() && !query.MatchQueryStringAND(title, 0, "") {
		return nil, errIPNoResults
	}

	details, err := ipResolve(i.SiteLink(), strings.TrimLeft(qTitleLink.AttrOr("href", ""), "/"))
	if err != nil {
		return nil, err
	}

	qLink := row.Find(`a[href^="/download.php/"]`).First()
	link, err := ipResolve(i.SiteLink(), strings.TrimLeft(qLink.AttrOr("href", ""), "/"))
	if err != nil {
		return nil, err
	}

	subSel := row.Find("div.sub").First()
	descrSplit := strings.Split(subSel.Text(), "|")
	dateSplit := strings.Split(descrSplit[len(descrSplit)-1], " by ")
	publishDate, err := dateutil.FromTimeAgo(strings.TrimSpace(dateSplit[0]))
	if err != nil {
		return nil, err
	}
	description := ""
	if len(descrSplit) > 1 {
		description = "Tags: " + strings.TrimSpace(descrSplit[0])
	}
	if len(dateSplit) > 1 {
		description += " Uploaded by: " + strings.TrimSpace(dateSplit[len(dateSplit)-1])
	}

	// Torrents - Category column == Text or Code
	// release.Category = MapTrackerCatDescToNewznab(row text) // Works for "Text" but only contains the parent category
	catIcon := row.Find(`td:nth-of-type(1) a[href^="?"]`).First()
	if catIcon.Length() == 0 {
		return nil, errors.New("Please, change the 'Torrents - Category column' option to 'Icons' in the website Settings. Wait a minute (cache) and then try again.") //nolint:staticcheck // upstream IPTorrents.cs error text, kept 1:1
	}

	// Torrents - Category column == Icons
	cat := i.Caps.Categories.MapTrackerCatToNewznab(strings.TrimPrefix(catIcon.AttrOr("href", ""), "?"))

	cells := row.Children()
	if sizeIndex < 0 || sizeIndex >= cells.Length() {
		return nil, fmt.Errorf("row has %d cells, size index %d out of range", cells.Length(), sizeIndex)
	}
	size := parseutil.GetBytes(cells.Eq(sizeIndex).Text())

	var files *int64

	if filesIndex != -1 {
		if filesIndex < 0 || filesIndex >= cells.Length() {
			return nil, fmt.Errorf("row has %d cells, files index %d out of range", cells.Length(), filesIndex)
		}
		// files column is enabled in the site settings
		f := parseutil.MustCoerceLong(strings.ReplaceAll(cells.Eq(filesIndex).Text(), "Go to files", ""))
		files = &f
	}

	colIndex := 6
	if cells.Length() == 10 {
		colIndex = 7
	}

	grabsIndex := ipFindColumnIndexOrDefault(headerColumns, "Sort by snatches", colIndex)
	colIndex++
	seedersIndex := ipFindColumnIndexOrDefault(headerColumns, "Sort by seeders", colIndex)
	colIndex++
	leechersIndex := ipFindColumnIndexOrDefault(headerColumns, "Sort by leechers", colIndex)

	if grabsIndex < 0 || grabsIndex >= cells.Length() ||
		seedersIndex < 0 || seedersIndex >= cells.Length() ||
		leechersIndex < 0 || leechersIndex >= cells.Length() {
		return nil, fmt.Errorf("row has %d cells, stat index out of range", cells.Length())
	}

	grabs := parseutil.MustCoerceLong(cells.Eq(grabsIndex).Text())
	seeders := parseutil.MustCoerceLong(cells.Eq(seedersIndex).Text())
	leechers := parseutil.MustCoerceLong(cells.Eq(leechersIndex).Text())

	// validTagList.Intersect(description.ToLower().Split(delimiters))
	descriptionTokens := map[string]bool{}
	for _, tok := range strings.FieldsFunc(strings.ToLower(description), func(r rune) bool {
		return strings.IndexByte(string(ipColDelimiters), byte(r)) >= 0
	}) {
		descriptionTokens[tok] = true
	}
	var releaseGenres []string
	for _, tag := range ipValidTagList {
		if descriptionTokens[tag] {
			releaseGenres = append(releaseGenres, tag)
		}
	}

	peers := seeders + leechers
	downloadVolumeFactor := 1.0
	if row.Find("span.free").Length() != 0 {
		downloadVolumeFactor = 0
	}
	uploadVolumeFactor := 1.0
	minimumRatio := 1.0
	minimumSeedTime := int64(1209600) // 336 hours

	return &models.ReleaseInfo{
		Guid:                 details,
		Link:                 link,
		Details:              details,
		Title:                title,
		Category:             cat,
		Description:          description,
		Genres:               releaseGenres,
		Size:                 &size,
		Files:                files,
		Grabs:                &grabs,
		Seeders:              &seeders,
		Peers:                &peers,
		PublishDate:          publishDate,
		DownloadVolumeFactor: &downloadVolumeFactor,
		UploadVolumeFactor:   &uploadVolumeFactor,
		MinimumRatio:         &minimumRatio,
		MinimumSeedTime:      &minimumSeedTime,
	}, nil
}

// ipFindColumnIndexOrDefault mirrors upstream (ordinal match, -1 when absent
// unless a default index is given).
func ipFindColumnIndexOrDefault(columns []string, name string, defaultIndex int) int {
	for index, col := range columns {
		if col == name {
			return index
		}
	}
	return defaultIndex
}

var (
	// upstream [\u0000-\u0008\u000A-\u001F\u0100-\uFFFF] (RE2 wants \x{…})
	ipInvalidCharsRe = regexp.MustCompile(`[\x00-\x08\x0A-\x1F\x{0100}-\x{FFFF}]`)
	// upstream: RegexOptions.Compiled | RegexOptions.IgnoreCase
	ipReqTagRe  = regexp.MustCompile(`(?i)[\(\[\{]REQ(UEST(ED)?)?[\)\]\}]`)
	ipLangTagRe = regexp.MustCompile(`(?i)^\[[a-z0-9 ._-]+\][-._ ](.*-[a-z0-9]+)$`)
)

// ipCleanTitle ports upstream CleanTitle (including the upstream quirks:
// the character-class ranges and the anime release-group strip).
func ipCleanTitle(title string) string {
	// Drop invalid chars that seems to have cropped up in some titles. #6582
	title = ipInvalidCharsRe.ReplaceAllString(title, "")
	title = ipReqTagRe.ReplaceAllString(title, "")

	// Drop languages between brackets conflicting with anime release group parsing
	title = ipLangTagRe.ReplaceAllString(title, "$1")

	return strings.Trim(title, " -:")
}

// ipResolve mirrors upstream `new Uri(SiteLink + href.TrimStart('/'))`.
func ipResolve(siteLink, href string) (*url.URL, error) {
	u, err := url.Parse(siteLink + href)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (i *IPTorrents) log() *slog.Logger {
	if i.Logger != nil {
		return i.Logger
	}
	return slog.Default()
}
