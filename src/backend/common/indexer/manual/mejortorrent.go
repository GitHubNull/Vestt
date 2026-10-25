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
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/indexer/shared"
	"vestt/common/models"
	"vestt/common/services"
)

// MejorTorrent ports Jackett.Common/Indexers/Definitions/MejorTorrent.cs.
//
// mechanism: html (public Tailwind site: newest list or
// "busqueda/page/N?q=" search; series/movies resolved from the details
// page; inherits IndexerBase directly — NOT PublicBrazilianIndexerBase, so
// the implementation is fully self-contained)
//
// Ported from: Definitions/MejorTorrent.cs.
//
// 澄清记录:
//   - Upstream ParseTags mutates its by-value string parameters and is
//     therefore a no-op for its callers (upstream lines 609-630); the port
//     keeps the function with the same discarded-result semantics.
//   - ParseMovieRelease's else branch (query.Year fallback) is unreachable
//     upstream because AngleSharp TextContent is never null — the year from
//     the details page is always appended, even when it is the empty
//     string (upstream lines 388-396).
//   - quality is modeled as *string to mirror upstream's null checks; site
//     TextContent is never null, so in practice the null branches (SDTV /
//     "[720p]" fallbacks in ParseMejorTorrentSeriesTitle) are dead code.
//   - CheckTitleMatchWords is byte-identical with DivxTotal's (upstream
//     lines 443-458 vs DivxTotal.cs), including the lossy ISO-8859-8 →
//     UTF-8 fold; the port reuses divxCheckTitleMatchWords.
//   - ExceptionWithConfigData surfaces as IndexerError of KindConfig with
//     the upstream message ("HTTP 500 Internal Server Error" or the body).
//   - The search term has [\W]+ replaced with "%" and is then UrlEncode'd
//     by GetQueryString, so the wire value carries %25 for the separators.
type MejorTorrent struct {
	indexer.Base
	cfg *mejortorrentConfig
}

// mejortorrentConfig ports the upstream bare ConfigurationData + the
// dynamic "Match words in title" bool (default true, AddDynamic in the
// constructor); declared as a field instead — same registration. The
// commented-out FlareSolverr display item upstream is not ported.
type mejortorrentConfig struct {
	configdata.ConfigData
	MatchWords *configdata.BoolItem
}

func newMejorTorrentConfig() *mejortorrentConfig {
	matchWords := configdata.NewBoolItem("Match words in title")
	matchWords.Value = true
	return &mejortorrentConfig{
		ConfigData: *configdata.NewConfigData(),
		MatchWords: matchWords,
	}
}

// MejorTorrent virtual categories (upstream MejorTorrentCatType).
const (
	mejortorrentCatPelicula = "Película"
	mejortorrentCatSerie    = "Serie"
	mejortorrentCatSerieHD  = "SerieHD" // this category is created, doesn't exist in the site
	mejortorrentCatMusica   = "Música"
	mejortorrentCatOtro     = "Otro"
)

const (
	mejortorrentNewTorrentsURL = "torrents"
	mejortorrentSearchURL      = "busqueda/page/"

	mejortorrentPagesToSearch = 3
)

// NewMejorTorrent builds the indexer (services.Factory shape).
func NewMejorTorrent(deps services.Deps) (indexer.Indexer, error) {
	m := &MejorTorrent{cfg: newMejorTorrentConfig()}
	m.Base = indexer.Base{
		IDValue:          "mejortorrent",
		NameValue:        "MejorTorrent",
		DescriptionValue: "MejorTorrent is a Public site - Hay veces que un torrent viene mejor! :)",
		SiteLinkValue:    "https://www47.mejortorrent.eu/",
		DefaultSiteLink:  "https://www47.mejortorrent.eu/",
		LegacySiteLinks: []string{
			"https://www31.mejortorrent.eu/",
			"https://www32.mejortorrent.eu/",
			"https://www34.mejortorrent.eu/",
			"https://www35.mejortorrent.eu/",
			"https://www36.mejortorrent.eu/",
			"https://www37.mejortorrent.eu/",
			"https://www38.mejortorrent.eu/",
			"https://www39.mejortorrent.eu/",
			"https://www40.mejortorrent.eu/",
			"https://www41.mejortorrent.eu/",
			"https://www42.mejortorrent.eu/",
			"https://www43.mejortorrent.eu/",
			"https://www44.mejortorrent.eu/",
			"https://www45.mejortorrent.eu/",
			"https://www46.mejortorrent.eu/",
		},
		LangValue: "es-ES",
		TypeValue: "public",
		Caps:      mejortorrentCaps(),
		Config:    m.cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	return m, nil
}

func mejortorrentCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	caps.MusicSearchParams = []string{models.MusicParamQ}
	caps.SupportsRawSearch = true
	c := caps.Categories
	c.AddCategoryMapping(mejortorrentCatPelicula, models.CatMovies, "Pelicula")
	c.AddCategoryMapping(mejortorrentCatSerie, models.CatTVSD, "Serie")
	c.AddCategoryMapping(mejortorrentCatSerieHD, models.CatTVHD, "Serie HD")
	c.AddCategoryMapping(mejortorrentCatMusica, models.CatAudio, "Musica")
	// Other category is disabled because we have problems parsing documentaries
	//c.AddCategoryMapping(mejortorrentCatOtro, models.CatOther, "Otro")
	return caps
}

// log is a nil-safe logger (Base.Logger may be nil).
func (m *MejorTorrent) log() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// ApplyConfiguration ports upstream: load values, run an empty query and
// require at least one release ("Could not find releases from this URL").
func (m *MejorTorrent) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := m.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := m.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	m.SetConfigured(false)
	releases, err := m.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(releases) == 0 {
		return indexer.ConfigCompleted, errors.New("Could not find releases from this URL") //nolint:staticcheck // upstream MejorTorrent.cs error text, kept 1:1
	}
	m.SetConfigured(true)
	m.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (m *MejorTorrent) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return m.ResultsForQueryWith(ctx, q, isMeta, m.perform)
}

func (m *MejorTorrent) Test(ctx context.Context) error {
	return m.Base.Test(ctx, m.perform)
}

// perform ports upstream PerformQuery: strip the query parts, then newest
// or search depending on the remaining search term.
func (m *MejorTorrent) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	matchWords := m.cfg.MatchWords.Value
	matchWords = query.SearchTerm != "" && matchWords

	// we remove parts from the original query
	query = mejortorrentParseQuery(query.Clone())

	if strings.TrimSpace(query.SearchTerm) == "" {
		return m.performNewest(ctx, query)
	}
	return m.performSearch(ctx, query, matchWords)
}

// loadWebPage ports the upstream status handling of the list/search GET:
// non-OK surfaces "HTTP 500 Internal Server Error" or the response body.
func (m *MejorTorrent) loadWebPage(ctx context.Context, target string) (string, error) {
	if m.Client == nil {
		return "", errors.New("http client not configured")
	}
	res, err := m.Client.Do(ctx, m.ID(), &httpclient.Request{
		URL:     target,
		Cookies: m.CookieHeader(),
	})
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == http.StatusInternalServerError {
			return "", &indexer.IndexerError{IndexerID: m.ID(), Kind: indexer.KindConfig, Message: "HTTP 500 Internal Server Error"}
		}
		return "", &indexer.IndexerError{IndexerID: m.ID(), Kind: indexer.KindConfig, Message: string(res.Body)}
	}
	return string(res.Body), nil
}

// loadDetailsPage ports the upstream details-page GET: HTTP 500 is a
// warning and the row is skipped (skipped=true, err=nil); any other
// non-OK status throws the body.
func (m *MejorTorrent) loadDetailsPage(ctx context.Context, target string) (body string, skipped bool, err error) {
	if m.Client == nil {
		return "", false, errors.New("http client not configured")
	}
	res, err := m.Client.Do(ctx, m.ID(), &httpclient.Request{
		URL:     target,
		Cookies: m.CookieHeader(),
	})
	if err != nil {
		return "", false, err
	}
	if res.StatusCode != http.StatusOK {
		if res.StatusCode == http.StatusInternalServerError {
			m.log().Warn(fmt.Sprintf("Fetching %s returned HTTP 500", target))
			return "", true, nil
		}
		return "", false, &indexer.IndexerError{IndexerID: m.ID(), Kind: indexer.KindConfig, Message: string(res.Body)}
	}
	return string(res.Body), false, nil
}

func (m *MejorTorrent) performNewest(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo
	url := m.SiteLink() + mejortorrentNewTorrentsURL
	result, err := m.loadWebPage(ctx, url)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(result))
	if err != nil {
		return nil, m.parseFailure(result, err)
	}

	container := doc.Find(".gap-y-3 > div:nth-child(1) > div:nth-child(1)").First()
	parsedDetailsLink := map[string]bool{}
	var parseErr error
	container.Children().EachWithBreak(func(_ int, row *goquery.Selection) bool {
		cells := row.Children()
		if cells.Length() < 2 || cells.Eq(1).Children().Length() == 0 ||
			cells.Eq(1).Children().Eq(0).Children().Length() == 0 {
			parseErr = errors.New("MejorTorrent: malformed newest row")
			return false
		}
		rowPublishDate := cells.Eq(0).Text()
		inner := cells.Eq(1).Children().Eq(0)
		rowQuality := inner.Children().Eq(0).Text()
		rowTitle := strings.ReplaceAll(inner.Text(), rowQuality, "")
		rowTitle = strings.TrimSpace(rowTitle)
		rowDetailsLink, _ := cells.Eq(1).Attr("href")
		// we add parsed items to rowDetailsLink to avoid duplicates in newest torrents list results
		if !parsedDetailsLink[rowDetailsLink] {
			releases, parseErr = m.parseRelease(ctx, releases, rowTitle, rowDetailsLink, nil,
				rowPublishDate, &rowQuality, query, false)
			if parseErr != nil {
				return false
			}
			parsedDetailsLink[rowDetailsLink] = true
		}
		return true
	})
	if parseErr != nil {
		return nil, m.parseFailure(result, parseErr)
	}

	return releases, nil
}

func (m *MejorTorrent) performSearch(ctx context.Context, query *models.TorznabQuery, matchWords bool) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo

	// non-word runs (Unicode-aware, mirroring .NET \W) become "%"
	qc := shared.QueryParams{}
	qc.Add("q", mejortorrentNonWordRe.ReplaceAllString(strings.TrimSpace(query.SearchTerm), "%"))

	// We search in the first "PagesToSearch" pages
	for i := 1; i <= mejortorrentPagesToSearch; i++ {
		url := m.SiteLink() + mejortorrentSearchURL + strconv.Itoa(i) + "?" + qc.GetQueryString()
		result, err := m.loadWebPage(ctx, url)
		if err != nil {
			return nil, err
		}

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(result))
		if err != nil {
			return nil, m.parseFailure(result, err)
		}

		table := doc.Find(".w-11\\/12").First()
		// check the search term is valid
		if table.Length() > 0 && table.Find("div.flex-row:nth-child(1)").Length() > 0 {
			// check there are results
			var parseErr error
			table.Children().EachWithBreak(func(_ int, row *goquery.Selection) bool {
				cells := row.Children()
				if cells.Length() < 2 || cells.Eq(0).Children().Length() == 0 ||
					cells.Eq(0).Children().Eq(0).Children().Length() == 0 {
					parseErr = errors.New("MejorTorrent: malformed search row")
					return false
				}
				anchor := cells.Eq(0)
				inner := anchor.Children().Eq(0)
				rowQuality := inner.Children().Eq(0).Text()
				rowTitle := strings.TrimSpace(strings.ReplaceAll(inner.Text(), rowQuality, ""))
				rowDetailsLink, _ := anchor.Attr("href")
				rowMejortorrentCat := cells.Eq(1).Text()
				releases, parseErr = m.parseRelease(ctx, releases, rowTitle, rowDetailsLink, &rowMejortorrentCat,
					"", &rowQuality, query, matchWords)
				return parseErr == nil
			})
			if parseErr != nil {
				return nil, m.parseFailure(result, parseErr)
			}
		} else {
			break
		}

		// Avoid extraneous requests if the next page link is missing.
		if doc.Find(`span.relative a.relative[rel="next"]`).Length() == 0 {
			break
		}
	}

	return releases, nil
}

// parseRelease ports upstream ParseRelease.
func (m *MejorTorrent) parseRelease(ctx context.Context, releases []*models.ReleaseInfo, title, detailsStr string,
	mejortorrentCat *string, publishStr string, quality *string, query *models.TorznabQuery, matchWords bool) ([]*models.ReleaseInfo, error) {
	// Remove trailing dot. Eg Harry Potter Y La Orden Del Fénix.
	title = strings.TrimSpace(title)
	if strings.HasSuffix(title, ".") {
		title = strings.TrimSpace(title[:len(title)-1])
	}

	cat := mejortorrentGetCategory(mejortorrentCat, detailsStr, title, quality)
	if cat == mejortorrentCatOtro {
		return releases, nil // skip releases from this category
	}

	categories := m.Caps.Categories.MapTrackerCatToNewznab(cat)
	publishDate := mejortorrentTryToParseDate(publishStr, time.Now())

	// return results only for requested categories
	if query.HasSpecifiedCategories() && (len(categories) == 0 || !mejortorrentContainsInt(query.Categories, categories[0])) {
		return releases, nil
	}

	// match the words in the query with the titles
	if matchWords && !divxCheckTitleMatchWords(query.SearchTerm, title) {
		return releases, nil
	}

	// parsing is different for each category
	if cat == mejortorrentCatSerie || cat == mejortorrentCatSerieHD {
		return m.parseSeriesRelease(ctx, releases, query, title, detailsStr, cat, publishDate, quality)
	}
	if query.Episode == "" { // if it's scene series, we don't return other categories
		if cat == mejortorrentCatPelicula {
			return m.parseMovieRelease(ctx, releases, query, title, detailsStr, cat, publishDate, quality)
		}
		size := int64(104857600) // 100 MB
		releases = append(releases, m.generateRelease(title, detailsStr, detailsStr, cat, publishDate, size))
	}
	return releases, nil
}

func (m *MejorTorrent) parseSeriesRelease(ctx context.Context, releases []*models.ReleaseInfo, query *models.TorznabQuery,
	title, detailsStr, cat string, publishDate time.Time, quality *string) ([]*models.ReleaseInfo, error) {
	result, skipped, err := m.loadDetailsPage(ctx, detailsStr)
	if err != nil {
		return nil, err
	}
	if skipped {
		return releases, nil
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(result))
	if err != nil {
		return nil, m.parseFailure(result, err)
	}

	rows := doc.Find("tr.border")
	quality = mejortorrentCleanQuality(quality)
	mejortorrentParseTags(title, derefQuality(quality))
	var parseErr error
	rows.EachWithBreak(func(_ int, row *goquery.Selection) bool {
		cells := row.Children()
		if cells.Length() < 3 {
			parseErr = errors.New("MejorTorrent: malformed episode row")
			return false
		}
		episodeTitle := strings.ReplaceAll(cells.Eq(1).Text(), "\n", "")
		downloadLink := cells.Eq(cells.Length()-1).Children().First().AttrOr("href", "")
		episodePublishStr := strings.ReplaceAll(cells.Eq(2).Text(), "\n", "")
		episodePublish := mejortorrentTryToParseDate(episodePublishStr, publishDate)

		// Convert the title to Scene format
		episodeTitle = mejortorrentParseSeriesTitle(title, episodeTitle, quality, query)

		// if the original query was in scene format, we filter the results to match episode
		// query.Episode != null means scene title
		if query.Episode != "" && !strings.Contains(episodeTitle, query.GetEpisodeSearchString()) {
			return true
		}

		// guess size
		size := int64(536870912) // 512 MB
		if strings.Contains(strings.ToLower(title), "720p") {
			size = 1073741824 // 1 GB
		}

		releases = append(releases, m.generateRelease(episodeTitle, detailsStr, downloadLink, cat, episodePublish, size))
		return true
	})
	if parseErr != nil {
		return nil, m.parseFailure(result, parseErr)
	}

	return releases, nil
}

func (m *MejorTorrent) parseMovieRelease(ctx context.Context, releases []*models.ReleaseInfo, query *models.TorznabQuery,
	title, detailsStr, cat string, publishDate time.Time, quality *string) ([]*models.ReleaseInfo, error) {
	result, skipped, err := m.loadDetailsPage(ctx, detailsStr)
	if err != nil {
		return nil, err
	}
	if skipped {
		return releases, nil
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(result))
	if err != nil {
		return nil, m.parseFailure(result, err)
	}

	downloadSel := doc.Find(".ml-2").First()
	if downloadSel.Length() == 0 {
		return nil, m.parseFailure(result, errors.New("MejorTorrent: missing .ml-2 download link"))
	}
	downloadLink := downloadSel.AttrOr("href", "")

	// clean quality
	quality = mejortorrentCleanQuality(quality)

	// add the year: upstream checks `detailsYear != null`, but AngleSharp
	// TextContent is never null, so the year node text (possibly empty) is
	// always appended and the query.Year fallback is unreachable.
	detailsYear := doc.Find("div.py-4:nth-child(2) > p:nth-child(2) > a:nth-child(2)").First().Text()
	title = title + " " + detailsYear

	mejortorrentParseTags(title, derefQuality(quality))

	// add spanish
	title += " SPANISH"

	// add quality
	if quality != nil {
		title += " " + *quality
	}

	// guess size 1.5 GB
	size := mejortorrentGuessSize(title, 1610612736)

	releases = append(releases, m.generateRelease(title, detailsStr, downloadLink, cat, publishDate, size))
	return releases, nil
}

func (m *MejorTorrent) generateRelease(title, detailsStr, downloadLink, cat string, publishDate time.Time, size int64) *models.ReleaseInfo {
	if strings.HasPrefix(downloadLink, "/") {
		downloadLink = m.SiteLink() + downloadLink[1:]
	}
	link, _ := url.Parse(downloadLink)
	details, _ := url.Parse(detailsStr)
	files := int64(1)
	seeders := int64(1)
	peers := int64(2)
	dvf := 0.0
	uvf := 1.0
	return &models.ReleaseInfo{
		Title:                title,
		Details:              details,
		Link:                 link,
		Guid:                 link,
		Category:             m.Caps.Categories.MapTrackerCatToNewznab(cat),
		PublishDate:          publishDate,
		Size:                 &size,
		Files:                &files,
		Seeders:              &seeders,
		Peers:                &peers,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &uvf,
	}
}

var (
	mejortorrentNonWordRe   = regexp.MustCompile(`[^\p{L}\p{N}_]+`) // upstream \W (Unicode-aware)
	mejortorrentSeasonEpRe  = regexp.MustCompile(`[S|s]\d+[E|e]\d+`)
	mejortorrentNonLatinRe  = regexp.MustCompile(`[^\x01-ÿ]+`)
	mejortorrentSpacesRe    = regexp.MustCompile(`\s+`)
	mejortorrentYearRe      = regexp.MustCompile(`(?i)( +([0-9]{4}))$`)
	mejortorrentLangWordsRe = regexp.MustCompile(`(?i)\b(espa[ñn]ol|spanish|castellano|spa)\b`)

	mejortorrentSeriesEpAllRe    = regexp.MustCompile(`(?i)([0-9]+)x([0-9]+)`)
	mejortorrentSeriesEpRangeRe  = regexp.MustCompile(`(?i)^([0-9]+)x([0-9]+)[^0-9]+([0-9]+)[.]?$`)
	mejortorrentSeriesEpSingleRe = regexp.MustCompile(`(?i)^([0-9]+)x([0-9]+)(.*)$`)

	mejortorrentTagRe     = regexp.MustCompile(`[\[\(]([^\]\)]+)[\]\)]`)
	mejortorrentHDRipRe   = regexp.MustCompile(`(?i)HDRip`)
	mejortorrentQualityRe = regexp.MustCompile(`[\[\(]([^\]\)]+)[\]\)]`)
)

// mejortorrentParseQuery ports upstream ParseQuery (mutates the cloned query).
func mejortorrentParseQuery(query *models.TorznabQuery) *models.TorznabQuery {
	// Eg. Doctor.Who.2005.(Доктор.Кто).S02E08 — the season/episode part is
	// already parsed by Jackett; GetQueryString re-adds it, we strip it again.
	searchTerm := query.GetQueryString()
	// remove the season/episode from the query as MejorTorrent only wants the series name
	searchTerm = mejortorrentSeasonEpRe.ReplaceAllString(searchTerm, "")

	// Server returns a 500 error if a UTF character higher than ÿ is included,
	// so we need to strip them
	searchTerm = mejortorrentNonLatinRe.ReplaceAllString(searchTerm, " ")
	searchTerm = mejortorrentSpacesRe.ReplaceAllString(searchTerm, " ")
	searchTerm = strings.TrimSpace(searchTerm)

	// we parse the year and remove it from search
	if m := mejortorrentYearRe.FindStringSubmatch(searchTerm); m != nil {
		if y, err := strconv.Atoi(m[2]); err == nil {
			query.Year = &y
		}
		searchTerm = strings.ReplaceAll(searchTerm, m[1], "")
	}

	// remove some words
	searchTerm = mejortorrentLangWordsRe.ReplaceAllString(searchTerm, "")

	query.SearchTerm = searchTerm
	return query
}

// mejortorrentParseSeriesTitle ports upstream ParseMejorTorrentSeriesTitle (static).
func mejortorrentParseSeriesTitle(title, episodeTitle string, quality *string, query *models.TorznabQuery) string {
	// parse title
	// title = The Mandalorian - 1ª Temporada
	// title = The Mandalorian - 1ª Temporada [720p]
	// title = Grace and Frankie - 5ª Temporada [720p]: 5x08 al 5x13.
	var newTitle string
	for _, part := range strings.Split(title, " - ") {
		if strings.TrimSpace(part) != "" {
			newTitle = strings.TrimSpace(part)
			break
		}
	}

	// parse episode title
	// episodeTitle = 5x08 al 5x13.
	// episodeTitle = 2x01 - 2x02 - 2x03.
	newEpisodeTitle := strings.TrimSpace(episodeTitle)
	matches := mejortorrentSeriesEpAllRe.FindAllStringSubmatch(newEpisodeTitle, -1)
	if len(matches) > 1 {
		var sb strings.Builder
		for i, m := range matches {
			if i == 0 {
				sb.WriteString("S" + pad2(mustAtoi(m[1])) + "E" + pad2(mustAtoi(m[2])))
			} else {
				sb.WriteString("-E" + pad2(mustAtoi(m[2])))
			}
		}
		// newEpisodeTitle = S05E08-E13 / S02E01-E02-E03
		newEpisodeTitle = sb.String()
	} else {
		// episodeTitle = 1x04 - 05.
		if m := mejortorrentSeriesEpRangeRe.FindStringSubmatch(newEpisodeTitle); m != nil {
			newEpisodeTitle = "S" + pad2(mustAtoi(m[1])) + "E" + pad2(mustAtoi(m[2])) + "-" +
				"E" + pad2(mustAtoi(m[3]))
			// newEpisodeTitle = S01E04-E05
		} else {
			// episodeTitle = 1x02
			// episodeTitle = 1x02 -
			// episodeTitle = 1x08 - CONTRASEÑA: WWW.PCTNEW ORG bebe
			if m := mejortorrentSeriesEpSingleRe.FindStringSubmatch(newEpisodeTitle); m != nil {
				newEpisodeTitle = "S" + pad2(mustAtoi(m[1])) + "E" + pad2(mustAtoi(m[2]))
				// newEpisodeTitle = S01E02
				if m[3] != "" {
					newEpisodeTitle += " " + strings.TrimSpace(strings.ReplaceAll(m[3], " -", ""))
					// newEpisodeTitle = S01E08 CONTRASEÑA: WWW.PCTNEW ORG bebe
				}
			}
		}
	}

	// if the original query was in scene format, we have to put the year back
	// query.Episode != null means scene title
	year := ""
	if query.Episode != "" && query.Year != nil {
		year = " " + strconv.Itoa(*query.Year)
	}
	newTitle += year + " " + newEpisodeTitle

	// add quality
	if quality != nil {
		newTitle += " SPANISH " + *quality
	} else if strings.Contains(strings.ToLower(title), "[720p]") {
		newTitle += " SPANISH 720p HDTV x264"
	} else {
		newTitle += " SPANISH SDTV XviD"
	}

	// return The Mandalorian S01E04 SPANISH 720p HDTV x264
	return newTitle
}

// mejortorrentGetCategory ports upstream GetMejortorrentCategory (static).
func mejortorrentGetCategory(mejortorrentCat *string, detailsStr, title string, quality *string) string {
	// get root category
	cat := mejortorrentCatOtro
	if mejortorrentCat == nil {
		switch {
		case strings.Contains(detailsStr, "pelicula"):
			cat = mejortorrentCatPelicula
		case strings.Contains(detailsStr, "serie"):
			cat = mejortorrentCatSerie
		case strings.Contains(detailsStr, "musica"):
			cat = mejortorrentCatMusica
		}
	} else {
		switch *mejortorrentCat {
		case mejortorrentCatPelicula, mejortorrentCatSerie, mejortorrentCatMusica:
			cat = *mejortorrentCat
		case "peliculas":
			cat = mejortorrentCatPelicula
		case "series", "documentales":
			cat = mejortorrentCatSerie
		}
	}

	// hack to separate SD & HD series
	if cat == mejortorrentCatSerie {
		t := strings.ToLower(title)
		q := ""
		if quality != nil {
			q = strings.ToLower(*quality)
		}
		if strings.Contains(t, "720p") || strings.Contains(t, "1080p") ||
			strings.Contains(q, "720p") || strings.Contains(q, "1080p") {
			cat = mejortorrentCatSerieHD
		}
	}

	return cat
}

// mejortorrentParseTags ports upstream ParseTags. Upstream passes title and
// quality by value, so the parsed tags/4K rewrites are discarded — the
// function is a no-op for its callers (kept for parity; the ineffectual
// rewrites are elided for ineffassign/staticcheck).
func mejortorrentParseTags(title, quality string) {
	title = strings.TrimSpace(title)

	// parse tags in title, we need to put the year after the real title (before the tags)
	// Harry Potter And The Deathly Hallows: Part 1 [subs. Integrados]
	for _, m := range mejortorrentTagRe.FindAllStringSubmatch(title, -1) {
		title = strings.ReplaceAll(title, m[0], "")
	}
}

// mejortorrentGuessSize ports upstream GuessSize (initial quality hint is
// refined by title keywords; the if/else order matters: 2160p beats bluray,
// microhd wins over everything).
func mejortorrentGuessSize(title string, initialQuality int64) int64 {
	size := initialQuality
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "microhd"):
		size = 7516192768 // 7 GB
	case strings.Contains(t, "complete bluray") || strings.Contains(t, "2160p"):
		size = 53687091200 // 50 GB
	case strings.Contains(t, "bluray"):
		size = 17179869184 // 16 GB
	case strings.Contains(t, "bdremux"):
		size = 21474836480 // 20 GB
	}
	return size
}

// mejortorrentCleanQuality ports upstream CleanQuality (static): the
// bracketed/parenthesised part becomes the quality, '-' becomes ' ' and
// HDRip is rewritten to BDRip for Radarr. nil in → nil out.
func mejortorrentCleanQuality(quality *string) *string {
	if quality == nil {
		return nil
	}
	q := *quality
	if m := mejortorrentQualityRe.FindStringSubmatch(q); m != nil {
		q = m[1]
	}
	q = strings.TrimSpace(strings.ReplaceAll(q, "-", " "))
	q = mejortorrentHDRipRe.ReplaceAllString(q, "BDRip") // fix for Radarr
	return &q
}

// mejortorrentTryToParseDate ports upstream TryToParseDate (yyyy-MM-dd,
// falls back to the provided default).
func mejortorrentTryToParseDate(dateToParse string, dateDefault time.Time) time.Time {
	if t, err := time.Parse("2006-01-02", dateToParse); err == nil {
		return t
	}
	return dateDefault
}

func derefQuality(q *string) string {
	if q == nil {
		return ""
	}
	return *q
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func mejortorrentContainsInt(list []int, v int) bool {
	for _, n := range list {
		if n == v {
			return true
		}
	}
	return false
}

func (m *MejorTorrent) parseFailure(dump string, err error) error {
	m.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: m.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}
