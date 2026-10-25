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
	"golang.org/x/net/html"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// BrasilTracker ports Jackett.Common/Indexers/Definitions/BrasilTracker.cs.
//
// mechanism: html-login (private Gazelle-like tracker; cookie auth against
// torrents.php, group/torrent row parsing, freeleech flag)
//
// Ported from: Definitions/BrasilTracker.cs.
//
// 澄清记录:
//   - upstream BrasilTracker extends IndexerBase directly (NOT
//     PublicBrazilianIndexerBase); there is no shared base-class behaviour
//     to inline, so this file is self-contained.
//   - upstream `seasonEp ??= _EpisodeRegex.Match(qDetailsLink.TextContent).Value`
//     effectively means: use the torrentid-anchor match when it succeeds,
//     otherwise the details-anchor match (possibly ""). The port implements
//     exactly that.
//   - upstream release.Poster is set from groupPoster even for standalone
//     torrent rows (whatever the last group header set, possibly null);
//     replicated verbatim.
//   - upstream ExceptionWithConfigData/OnParseError semantics match the
//     other manual indexers: response body as error message / KindParse
//     "Parse error".
type BrasilTracker struct {
	indexer.Base
	cfg *brasilTrackerConfig
}

type brasilTrackerConfig struct {
	configdata.ConfigData
	Cookie            *configdata.StringItem
	FreeLeech         *configdata.BoolItem
	AccountInactivity *configdata.InfoItem
}

func newBrasilTrackerConfig() *brasilTrackerConfig {
	freeleech := configdata.NewBoolItem("Search freeleech only")
	freeleech.Value = false
	return &brasilTrackerConfig{
		ConfigData:        *configdata.NewConfigData(),
		Cookie:            configdata.NewStringItem("Cookie"),
		FreeLeech:         freeleech,
		AccountInactivity: configdata.NewInfoItem("Account Inactivity", "User: You must access the account at least once within 7 days, after this period the account will be considered inactive and automatically deactivated by the system. Member: You must access the account at least once every 90 days, after this period the account will be considered inactive and automatically deactivated by the system. Power User & Elite: Exempt."),
	}
}

// brEpisodeRegex mirrors upstream _EpisodeRegex.
var brEpisodeRegex = regexp.MustCompile(`(?:\[?[SsEe]\d{2,4}){1,2}\]?`)

// NewBrasilTracker builds the indexer (services.Factory shape).
func NewBrasilTracker(deps services.Deps) (indexer.Indexer, error) {
	b := &BrasilTracker{cfg: newBrasilTrackerConfig()}
	b.Base = indexer.Base{
		IDValue:          "brasiltracker",
		NameValue:        "BrasilTracker",
		DescriptionValue: "BrasilTracker is a BRAZILIAN Private Torrent Tracker for MOVIES / TV / GENERAL",
		SiteLinkValue:    "https://brasiltracker.org/",
		DefaultSiteLink:  "https://brasiltracker.org/",
		LangValue:        "pt-BR",
		TypeValue:        "private",
		Caps:             brCaps(),
		Config:           b.cfg,
		Client:           deps.Client,
		Saver:            deps.Saver,
		Cache:            deps.Cache,
	}
	return b, nil
}

func brCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamGenre}
	caps.MovieSearchParams = []string{models.MovieParamQ, models.MovieParamImdbID, models.MovieParamGenre}
	caps.MusicSearchParams = []string{models.MusicParamQ, models.MusicParamGenre}
	caps.BookSearchParams = []string{models.BookParamQ, models.BookParamGenre}
	c := caps.Categories
	c.AddCategoryMapping("16", models.CatAudioAudiobook, "Audiobooks")
	c.AddCategoryMapping("6", models.CatTVAnime, "Animes")
	c.AddCategoryMapping("11", models.CatPC0day, "Aplicativos")
	c.AddCategoryMapping("15", models.CatOther, "Cursos")
	c.AddCategoryMapping("8", models.CatTVDocumentary, "Documentários")
	c.AddCategoryMapping("14", models.CatTVSport, "Esportes")
	c.AddCategoryMapping("3", models.CatXXX, "Filmes XXX")
	c.AddCategoryMapping("1", models.CatMovies, "Filmes")
	c.AddCategoryMapping("12", models.CatBooksComics, "Histórias em Quadrinhos")
	c.AddCategoryMapping("9", models.CatPCGames, "Jogos")
	c.AddCategoryMapping("13", models.CatBooksEBook, "Livros")
	c.AddCategoryMapping("10", models.CatBooksMags, "Revistas")
	c.AddCategoryMapping("2", models.CatTV, "Séries")
	c.AddCategoryMapping("5", models.CatAudioVideo, "Show")
	c.AddCategoryMapping("7", models.CatTV, "Televisão")
	return caps
}

// ApplyConfiguration ports upstream: apply the cookie, run an empty query;
// any failure (including zero results) is wrapped in "Your cookie did not
// work: ".
func (b *BrasilTracker) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := b.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := b.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	b.SetCookieHeader(b.cfg.Cookie.Value)
	b.SetConfigured(false)
	results, err := b.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, fmt.Errorf("Your cookie did not work: %s", err.Error()) //nolint:staticcheck // upstream BrasilTracker.cs error text, kept 1:1
	}
	if len(results) == 0 {
		return indexer.ConfigCompleted, errors.New("Your cookie did not work: Found 0 results in the tracker") //nolint:staticcheck // upstream BrasilTracker.cs error text, kept 1:1
	}
	b.SetConfigured(true)
	b.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (b *BrasilTracker) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return b.ResultsForQueryWith(ctx, q, isMeta, b.perform)
}

func (b *BrasilTracker) Test(ctx context.Context) error {
	return b.Base.Test(ctx, b.perform)
}

func (b *BrasilTracker) browseURL() string { return b.SiteLink() + "torrents.php" }

// brFixSearchTerm ports upstream FixSearchTerm.
func brFixSearchTerm(query *models.TorznabQuery) string {
	if query.IsImdbQuery() {
		return query.ImdbID
	}
	return query.GetQueryString()
}

// brStripSearchString ports upstream StripSearchString.
func brStripSearchString(term string) string {
	// Search does not support searching with episode numbers so strip it if
	// we have one; we will AND filter the result later to achieve the proper result
	term = brEpisodeRegex.ReplaceAllString(term, "")
	return strings.TrimRight(term, " \t\r\n")
}

func (b *BrasilTracker) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo
	searchURL := b.browseURL()
	searchTerm := brFixSearchTerm(query)

	// ordered query collection (upstream NameValueCollection order)
	params := make([][2]string, 0, 8)
	params = append(params,
		[2]string{"searchstr", brStripSearchString(searchTerm)},
		[2]string{"order_by", "time"},
		[2]string{"order_way", "desc"},
		[2]string{"group_results", "1"},
		[2]string{"action", "basic"},
		[2]string{"searchsubmit", "1"},
	)
	for _, cat := range b.Caps.Categories.MapTorznabCapsToTrackers(query, false) {
		params = append(params, [2]string{"filter_cat[" + cat + "]", "1"})
	}

	if query.IsGenreQuery() {
		params = append(params, [2]string{"taglist", query.Genre})
	}

	if b.cfg.FreeLeech.Value {
		params = append(params, [2]string{"freetorrent", "1"})
	}

	searchURL += "?" + brQueryString(params)

	if b.Client == nil {
		return nil, errors.New("http client not configured")
	}
	results, err := b.Client.Do(ctx, b.ID(), &httpclient.Request{
		URL:     searchURL,
		Cookies: b.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	if results.StatusCode != http.StatusOK {
		return nil, errors.New(string(results.Body))
	}
	body := string(results.Body)

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		b.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, body))
		return nil, &indexer.IndexerError{IndexerID: b.ID(), Kind: indexer.KindParse, Message: "Parse error"}
	}

	// upstream rowsSelector: "table.torrent_table > tbody > tr:not(tr.colhead)"
	var (
		groupTitle   string
		groupYearStr string
		groupPoster  *url.URL
		category     string
		imdbLink     string
		tmdbLink     string
		genres       string
	)
	rows := doc.Find("table.torrent_table > tbody > tr").FilterFunction(func(_ int, s *goquery.Selection) bool {
		return !s.HasClass("colhead")
	})
	rows.Each(func(_ int, row *goquery.Selection) {
		// upstream: row-level failures are logged and skipped
		if rel := b.parseRow(row, &groupTitle, &groupYearStr, &groupPoster, &category, &imdbLink, &tmdbLink, &genres, query, searchTerm); rel != nil {
			releases = append(releases, rel)
		}
	})

	return releases, nil
}

// brQueryString mirrors upstream NameValueCollection.GetQueryString
// (WebUtility.UrlEncode: '+' for spaces, everything else %XX).
func brQueryString(params [][2]string) string {
	parts := make([]string, 0, len(params))
	for _, kv := range params {
		parts = append(parts, brEncodeQuery(kv[0])+"="+brEncodeQuery(kv[1]))
	}
	return strings.Join(parts, "&")
}

func brEncodeQuery(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "%20", "+")
}

// parseRow ports the per-row logic of upstream PerformQuery. It returns the
// release for torrent rows and nil for headers/edition rows/skipped rows,
// updating the group context pointers for group headers.
func (b *BrasilTracker) parseRow(row *goquery.Selection, groupTitle, groupYearStr *string, groupPoster **url.URL, category, imdbLink, tmdbLink, genres *string, query *models.TorznabQuery, searchTerm string) *models.ReleaseInfo {
	// ignore sub groups info row, it's just a row with an info about the next
	// section, something like "Dual Áudio" or "Legendado"
	if row.Find(".edition_info").Length() > 0 {
		return nil
	}

	// some torrents have more than one link, and the one with .tooltip is the
	// wrong one in that case, so let's try to pick up first without the
	// .tooltip class; if nothing is found, then we try again without that
	// filter
	qDetailsLink := row.Find(`a[href^="torrents.php?id="]`).Not(".tooltip").First()
	if qDetailsLink.Length() == 0 {
		qDetailsLink = row.Find(`a[href^="torrents.php?id="]`).First()
		// if still can't find the right link, skip it
		if qDetailsLink.Length() == 0 {
			b.log().Error(fmt.Sprintf("%s: Error while parsing row '%s': Can't find the right details link", b.ID(), brRowHTML(row)))
			return nil
		}
	}
	title := brStripSearchString(strings.TrimSpace(qDetailsLink.Text()))

	seasonEp := ""
	if seasonEl := row.Find(`a[href^="torrents.php?torrentid="]`).First(); seasonEl.Length() > 0 {
		if m := brEpisodeRegex.FindString(seasonEl.Text()); m != "" {
			seasonEp = m
		} else {
			seasonEp = brEpisodeRegex.FindString(qDetailsLink.Text())
		}
	} else {
		seasonEp = brEpisodeRegex.FindString(qDetailsLink.Text())
	}

	yearStr := ""
	if row.HasClass("group") || row.HasClass("torrent") { // group/ungrouped headers
		_ = row.Find(`a[href^="/torrents.php?filter_cat"]`).First()

		torrentInfoEl := row.Find("div.torrent_info").First()
		if torrentInfoEl.Length() > 0 {
			// valid for torrent grouped but that has only 1 episode yet
			if dy, _ := torrentInfoEl.Attr("data-year"); dy != "" {
				yearStr = dy
			}
		}
		if yearStr == "" {
			yearStr = brNextSiblingText(qDetailsLink)
		}
		*category = brCategoryFromYearStr(yearStr)
		if *category == "XXX" {
			// some 3x titles end with [XXX] which needs to be renamed
			*category = strings.ReplaceAll(*category, "XXX", "Filmes XXX")
		}

		if src, _ := row.Find(`img[alt="Cover"]`).First().Attr("src"); src != "" {
			if posterURI, err := url.Parse(src); err == nil && posterURI.Scheme != "" && posterURI.Host != "" {
				*groupPoster = posterURI
			}
		}
		if row.HasClass("group") { // group headers
			*groupTitle = title
			*groupYearStr = yearStr
			if href, ok := row.Find(`a[href*="imdb.com/title/tt"]`).First().Attr("href"); ok {
				*imdbLink = href
			} else {
				*imdbLink = ""
			}
			if href, ok := row.Find(`a[href*="themoviedb.org/"]`).First().Attr("href"); ok {
				*tmdbLink = href
			} else {
				*tmdbLink = ""
			}
			if tags := row.Find("div.tags").First(); tags.Length() > 0 {
				*genres = tags.Text()
			} else {
				*genres = ""
			}
			return nil
		}
	}

	minimumRatio := 1.0
	minimumSeedTime := int64(172800)
	release := &models.ReleaseInfo{
		MinimumRatio:    &minimumRatio,
		MinimumSeedTime: &minimumSeedTime,
	}

	qDlLink := row.Find(`a[href^="torrents.php?action=download"]`).First()
	qSize := row.Find("td:nth-last-child(4)").First()
	qGrabs := row.Find("td:nth-last-child(3)").First()
	qSeeders := row.Find("td:nth-last-child(2)").First()
	qLeechers := row.Find("td:nth-last-child(1)").First()
	qFreeLeech := row.Find("strong").FilterFunction(func(_ int, s *goquery.Selection) bool {
		return strings.Contains(s.Text(), "Free")
	}).First()

	if row.HasClass("group_torrent") { // torrents belonging to a group
		release.Description = qDetailsLink.Text()
		release.Title = brParseTitle(*groupTitle, seasonEp, *groupYearStr)
	} else if row.HasClass("torrent") { // standalone/ungrouped torrents
		release.Description = row.Find("div.torrent_info").First().Text()
		release.Title = brParseTitle(title, seasonEp, yearStr)
		if href, ok := row.Find(`a[href*="imdb.com/title/tt"]`).First().Attr("href"); ok {
			*imdbLink = href
		} else {
			*imdbLink = ""
		}
		if href, ok := row.Find(`a[href*="themoviedb.org/"]`).First().Attr("href"); ok {
			*tmdbLink = href
		} else {
			*tmdbLink = ""
		}
		if tags := row.Find("div.tags").First(); tags.Length() > 0 {
			*genres = tags.Text()
		} else {
			*genres = ""
		}
	}
	release.Poster = *groupPoster
	if imdb, ok := parseutil.GetLongFromString(*imdbLink); ok {
		release.Imdb = &imdb
	}
	if tmdb, ok := parseutil.GetLongFromString(*tmdbLink); ok {
		release.TMDb = &tmdb
	}
	if *genres != "" {
		for _, g := range strings.Split(strings.ReplaceAll(*genres, ", ", ","), ",") {
			found := false
			for _, existing := range release.Genres {
				if existing == g {
					found = true
					break
				}
			}
			if !found {
				release.Genres = append(release.Genres, g)
			}
		}
	}
	release.Category = b.Caps.Categories.MapTrackerCatDescToNewznab(*category)
	release.Description = strings.ReplaceAll(release.Description, " / Free", "")         // Remove Free Tag
	release.Description = strings.ReplaceAll(release.Description, "/ WEB ", "/ WEB-DL ") // Fix web/web-dl
	release.Description = strings.ReplaceAll(release.Description, "Full HD", "1080p")
	// Handles HDR conflict
	release.Description = strings.ReplaceAll(release.Description, "/ HD /", "/ 720p /")
	release.Description = strings.ReplaceAll(release.Description, "/ HD]", "/ 720p]")
	release.Description = strings.ReplaceAll(release.Description, "4K", "2160p")
	release.Description = strings.ReplaceAll(release.Description, "SD", "480p")
	release.Description = strings.ReplaceAll(release.Description, "Dual Áudio", "Dual")
	release.Description = strings.ReplaceAll(release.Description, "Dual Audio", "Dual")

	// Adjust the description in order to can be read by Radarr and Sonarr
	cleanDescription := strings.TrimRight(strings.TrimLeft(strings.TrimSpace(release.Description), "["), "]")

	// Formats the title so it can be parsed later
	titleElements := strings.Split(cleanDescription, " / ")
	release.Title = strings.TrimSpace(release.Title)
	if len(titleElements) < 6 {
		// Usually non movies / series could have less than 6 elements, eg: Books.
		release.Title += " " + strings.Join(titleElements, " ")
	} else {
		release.Title += " " + titleElements[5] + " " + titleElements[3] + " " + titleElements[1] + " " +
			titleElements[2] + " " + titleElements[4] + " " + strings.Join(titleElements[6:], " ")
	}

	if brBrazilianRe.MatchString(release.Description) {
		release.Title += " Brazilian"
	}

	// This tracker does not provide a publish date to search terms (only on
	// the last 24h page)
	release.PublishDate = brToday()

	// check for previously stripped search terms
	if !query.IsImdbQuery() && !query.MatchQueryStringAND(release.Title, 0, searchTerm) {
		return nil
	}

	size := parseutil.GetBytes(qSize.Text())
	release.Size = &size
	dlHref, _ := qDlLink.Attr("href")
	detailsHref, _ := qDetailsLink.Attr("href")
	release.Link, _ = url.Parse(b.SiteLink() + dlHref)
	release.Details, _ = url.Parse(b.SiteLink() + detailsHref)
	release.Guid = release.Link
	grabs := parseutil.MustCoerceLong(strings.TrimSpace(qGrabs.Text()))
	release.Grabs = &grabs
	seeders := parseutil.MustCoerceLong(strings.TrimSpace(qSeeders.Text()))
	release.Seeders = &seeders
	leechers := parseutil.MustCoerceLong(strings.TrimSpace(qLeechers.Text()))
	peers := leechers + seeders
	release.Peers = &peers
	dvf := float64(1)
	if qFreeLeech.Length() > 0 {
		dvf = 0
	}
	release.DownloadVolumeFactor = &dvf
	uvf := float64(1)
	release.UploadVolumeFactor = &uvf
	return release
}

// brCategoryFromYearStr mirrors upstream
// Regex.Replace(yearStr, @".+ \[(.+)\]", "$1").
var brYearCatRe = regexp.MustCompile(`.+ \[(.+)\]`)

func brCategoryFromYearStr(yearStr string) string {
	return brYearCatRe.ReplaceAllString(yearStr, "$1")
}

var brBrazilianRe = regexp.MustCompile(`(Dual|[Nn]acional|[Dd]ublado)`)

var brIntlTitleRe = regexp.MustCompile(`.* \[(.*\/?)\]`)

// brInternationalTitle ports upstream InternationalTitle.
func brInternationalTitle(title string) string {
	if m := brIntlTitleRe.FindStringSubmatch(title); m != nil {
		return strings.Split(m[1], "/")[0]
	}
	return title
}

// brParseTitle ports upstream ParseTitle.
func brParseTitle(title, seasonEp, year string) string {
	// Removes the SxxExx if it comes on the title
	cleanTitle := brEpisodeRegex.ReplaceAllString(title, "")
	// Removes the year if it comes on the title
	// The space is added because on daily releases the date will be XX/XX/YYYY
	if year != "" {
		cleanTitle = strings.ReplaceAll(cleanTitle, " "+year, "")
	}
	cleanTitle = brParseTitleTrimRe.ReplaceAllString(cleanTitle, "")

	// Get international title if available, or use the full title if not
	cleanTitle = brInternationalTitle(cleanTitle)
	cleanTitle += " " + year + " " + seasonEp
	cleanTitle = strings.TrimSpace(cleanTitle)
	return cleanTitle
}

// brParseTitleTrimRe mirrors upstream @"^\s*|[\s-]*$" (leading whitespace
// and trailing spaces/dashes removed).
var brParseTitleTrimRe = regexp.MustCompile(`^\s*|[\s-]*$`)

// brToday mirrors DateTime.Today (local midnight).
func brToday() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

// brNextSiblingText mirrors upstream qDetailsLink.NextSibling.TextContent
// (text node as-is, element sibling's text content).
func brNextSiblingText(sel *goquery.Selection) string {
	for _, n := range sel.Nodes {
		sib := n.NextSibling
		if sib == nil {
			continue
		}
		if sib.Type == html.TextNode {
			return strings.TrimSpace(sib.Data)
		}
		return strings.TrimSpace((&goquery.Selection{Nodes: []*html.Node{sib}}).Text())
	}
	return ""
}

// brRowHTML renders a row as HTML for the upstream error log line.
func brRowHTML(row *goquery.Selection) string {
	h, err := row.Html()
	if err != nil {
		return ""
	}
	return h
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (b *BrasilTracker) log() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}
