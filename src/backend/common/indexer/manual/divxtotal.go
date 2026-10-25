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
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// DivxTotal ports Jackett.Common/Indexers/Definitions/DivxTotal.cs.
//
// mechanism: html (public WordPress-style "/page/N/?s=" search; the .torrent
// link of movies/programs is resolved from the details page at download time)
//
// Ported from: Definitions/DivxTotal.cs.
//
// 澄清记录:
//   - upstream CheckTitleMatchWords "accent removal" re-encodes each word via
//     ISO-8859-8 bytes and decodes it back as UTF-8; any char > 0xFF becomes
//     '?' and any lone byte >= 0x80 becomes U+FFFD. Both sides of the
//     comparison go through the same lossy transform, so the port keeps it
//     (divxFoldWord) instead of "fixing" it.
//   - upstream GetDownloadLink may return null; new Uri(null) then throws and
//     the series row is skipped (logged). The port returns an error instead,
//     which the row loop logs and skips identically.
//   - upstream ExceptionWithConfigData carries the response body as the
//     message; the port surfaces the body as a plain error.
//   - upstream OnParseError throws Exception("Parse error"); the port
//     returns an IndexerError of Kind Parse with the same message.
type DivxTotal struct {
	indexer.Base
	cfg *divxTotalConfig
}

type divxTotalConfig struct {
	configdata.ConfigData
	MatchWords   *configdata.BoolItem
	FlareSolverr *configdata.InfoItem
}

func newDivxTotalConfig() *divxTotalConfig {
	matchWords := configdata.NewBoolItem("Match words in title")
	matchWords.Value = true
	return &divxTotalConfig{
		ConfigData:   *configdata.NewConfigData(),
		MatchWords:   matchWords,
		FlareSolverr: configdata.NewInfoItem("FlareSolverr", "This site may use Cloudflare DDoS Protection, therefore Jackett requires <a href=\"https://github.com/Jackett/Jackett#configuring-flaresolverr\" target=\"_blank\">FlareSolverr</a> to access it."),
	}
}

const (
	divxDownloadLink   = "/download_tt.php"
	divxMaxNrOfResults = 100
	divxMaxPageLoads   = 3

	divxCatPeliculas     = "peliculas"
	divxCatPeliculasHD   = "peliculas-hd"
	divxCatPeliculas3D   = "peliculas-3-d"
	divxCatPeliculasDVDR = "peliculas-dvdr"
	divxCatSeries        = "series"
	divxCatProgramas     = "programas"
	divxCatOtros         = "otros"

	divxSizePeliculas     = 2147483648 // 2 GB
	divxSizePeliculasDVDR = 5368709120 // 5 GB
	divxSizeSeries        = 536870912  // 512 MB
	divxSizeOtros         = 536870912  // 512 MB
)

// NewDivxTotal builds the indexer (services.Factory shape).
func NewDivxTotal(deps services.Deps) (indexer.Indexer, error) {
	d := &DivxTotal{cfg: newDivxTotalConfig()}
	d.Base = indexer.Base{
		IDValue:          "divxtotal",
		NameValue:        "DivxTotal",
		DescriptionValue: "DivxTotal is a SPANISH site for Movies, TV series and Software",
		SiteLinkValue:    "https://divxtotal.foo/",
		DefaultSiteLink:  "https://divxtotal.foo/",
		LegacySiteLinks: []string{
			"https://www.divxtotal.ms/",
			"https://www.divxtotal.fi/",
			"https://www.divxtotal.cat/",
			"https://www.divxtotal.pl/",
			"https://www.divxtotal.wf/",
			"https://www.divxtotal.win/",
			"https://www1.divxtotal.zip/",
			"https://www2.divxtotal.zip/",
			"https://www2.divxtotal.mov/",
			"https://www3.divxtotal.mov/",
			"https://www4.divxtotal.mov/",
			"https://www5.divxtotal.mov/",
			"https://divxtotal.io/",
			"https://www1.divxtotal.lol/",
			"https://www2.divxtotal.lol/",
			"https://www3.divxtotal.lol/",
		},
		LangValue: "es-ES",
		TypeValue: "public",
		Caps:      divxCaps(),
		Config:    d.cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	// upstream hides the .torrent for non-series categories on the details
	// page; Base.ResolveDownload performs that extra step at download time.
	d.ResolveDownload = d.resolveDownload
	return d, nil
}

func divxCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	c := caps.Categories
	c.AddCategoryMapping(divxCatPeliculas, models.CatMoviesSD, "Peliculas")
	c.AddCategoryMapping(divxCatPeliculasHD, models.CatMoviesHD, "Peliculas HD")
	c.AddCategoryMapping(divxCatPeliculas3D, models.CatMovies3D, "Peliculas 3D")
	c.AddCategoryMapping(divxCatPeliculasDVDR, models.CatMoviesDVD, "Peliculas DVD-r")
	c.AddCategoryMapping(divxCatSeries, models.CatTVSD, "Series")
	c.AddCategoryMapping(divxCatProgramas, models.CatPC, "Programas")
	c.AddCategoryMapping(divxCatOtros, models.CatOtherMisc, "Otros")
	return caps
}

// ApplyConfiguration ports upstream: load values, run an empty query and
// require at least one release ("Could not find releases from this URL").
func (d *DivxTotal) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := d.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := d.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	d.SetConfigured(false)
	releases, err := d.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(releases) == 0 {
		return indexer.ConfigCompleted, errors.New("Could not find releases from this URL") //nolint:staticcheck // upstream DivxTotal.cs error text, kept 1:1
	}
	d.SetConfigured(true)
	d.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (d *DivxTotal) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return d.ResultsForQueryWith(ctx, q, isMeta, d.perform)
}

func (d *DivxTotal) Test(ctx context.Context) error {
	return d.Base.Test(ctx, d.perform)
}

func (d *DivxTotal) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	newQuery := query.Clone()

	var releases []*models.ReleaseInfo

	matchWords := d.cfg.MatchWords.Value
	matchWords = newQuery.SearchTerm != "" && matchWords

	// we remove parts from the original query
	newQuery = divxParseQuery(newQuery)

	page := 1
	var doc *goquery.Document
	for {
		url := d.SiteLink() + "page/" + strconv.Itoa(page) + "/?s=" + divxEncodeQuery(newQuery.SearchTerm)

		htmlString, err := d.loadWebPage(ctx, url)
		if err != nil {
			d.log().Error(fmt.Sprintf("DivxTotal: Failed to load url [%s]: %s", url, err.Error()))

			return releases, nil
		}

		doc, err = goquery.NewDocumentFromReader(strings.NewReader(htmlString))
		if err != nil {
			return nil, d.parseError(htmlString, err)
		}

		table := doc.Find("table.table")
		if table.Length() == 0 {
			break
		}

		table.Find("tbody > tr").Each(func(_ int, row *goquery.Selection) {
			rels, err := d.parseReleases(ctx, row, newQuery, matchWords)
			if err != nil {
				// upstream: row-level failures are logged and skipped
				d.log().Error(fmt.Sprintf("CardigannIndexer (%s): Error while parsing row '%s':\n\n%v", d.ID(), divxRowHTML(row), err))
			} else {
				releases = append(releases, rels...)
			}
		})

		page++

		if page > divxMaxPageLoads ||
			len(releases) >= divxMaxNrOfResults ||
			divxIsLastPage(doc) {
			break
		}
	}

	return releases, nil
}

// loadWebPage ports upstream LoadWebPageAsync: non-OK responses surface the
// body as the error message (ExceptionWithConfigData).
func (d *DivxTotal) loadWebPage(ctx context.Context, url string) (string, error) {
	if d.Client == nil {
		return "", errors.New("http client not configured")
	}
	res, err := d.Client.Do(ctx, d.ID(), &httpclient.Request{
		URL:     url,
		Cookies: d.CookieHeader(),
	})
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", errors.New(string(res.Body))
	}
	return string(res.Body), nil
}

func divxIsLastPage(doc *goquery.Document) bool {
	if doc == nil {
		return true
	}
	return doc.Find("ul.pagination > li.active + li > a").Length() == 0
}

// resolveDownload ports upstream Download: series already carry the
// .torrent link; other categories need one extra details-page fetch.
func (d *DivxTotal) resolveDownload(ctx context.Context, link *url.URL) (string, error) {
	downloadURL := link.String()
	if !strings.Contains(downloadURL, divxDownloadLink) {
		htmlString, err := d.loadWebPage(ctx, downloadURL)
		if err != nil {
			return "", err
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString))
		if err != nil {
			return "", err
		}
		downloadURL = divxGetDownloadLink(doc.Selection)
		if downloadURL == "" {
			return "", errors.New("DivxTotal: could not find download link on details page")
		}
	}
	return downloadURL, nil
}

func (d *DivxTotal) parseReleases(ctx context.Context, row *goquery.Selection, query *models.TorznabQuery, matchWords bool) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo

	anchor := row.Find("a").First()
	title := strings.TrimSpace(anchor.Text())

	// match the words in the query with the titles
	if matchWords && !divxCheckTitleMatchWords(query.SearchTerm, title) {
		return releases, nil
	}

	detailsStr, _ := anchor.Attr("href")
	parts := strings.Split(detailsStr, "/")
	if len(parts) < 4 {
		return nil, fmt.Errorf("DivxTotal: cannot extract category from details link %q", detailsStr)
	}
	cat := parts[3]

	// return results only for requested categories
	if query.HasSpecifiedCategories() && !divxContains(d.Caps.Categories.MapTorznabCapsToTrackers(query, false), cat) {
		return releases, nil
	}

	tds := row.Find("td")
	if tds.Length() < 4 {
		return nil, errors.New("DivxTotal: row has fewer than 4 cells")
	}
	publishStr := strings.TrimSpace(tds.Eq(2).Text())
	publishDate := divxTryToParseDate(publishStr, time.Now())
	sizeStr := strings.TrimSpace(tds.Eq(3).Text())

	// parsing is different for each category
	if cat == divxCatSeries {
		seriesReleases, err := d.parseSeriesRelease(ctx, query, detailsStr, cat, publishDate)
		if err != nil {
			return nil, err
		}
		releases = append(releases, seriesReleases...)
	} else if query.Episode == "" { // if it's scene series, we don't return other categories
		if cat == divxCatPeliculas || cat == divxCatPeliculasHD ||
			cat == divxCatPeliculas3D || cat == divxCatPeliculasDVDR {
			movieRelease := d.parseMovieRelease(query, title, detailsStr, cat, publishDate, sizeStr)
			releases = append(releases, movieRelease)
		} else {
			size := divxTryToParseSize(sizeStr, divxSizeOtros)
			releases = append(releases, d.generateRelease(title, detailsStr, detailsStr, cat, publishDate, size))
		}
	}

	return releases, nil
}

func (d *DivxTotal) parseSeriesRelease(ctx context.Context, query *models.TorznabQuery, detailsStr, cat string, publishDate time.Time) ([]*models.ReleaseInfo, error) {
	var seriesReleases []*models.ReleaseInfo

	htmlString, err := d.loadWebPage(ctx, detailsStr)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlString))
	if err != nil {
		return nil, err
	}

	doc.Find("table.rwd-table").Each(func(_ int, table *goquery.Selection) {
		table.Find("tbody > tr").Each(func(_ int, row *goquery.Selection) {
			anchor := row.Find("a").First()
			episodeTitle := strings.TrimSpace(anchor.Text())

			// Convert the title to Scene format
			episodeTitle = divxParseSeriesTitle(episodeTitle, query)

			// if the original query was in scene format, we filter the results to match episode
			if query.Episode != "" && !strings.Contains(episodeTitle, query.GetEpisodeSearchString()) {
				return
			}

			downloadLink := divxGetDownloadLink(row)

			seriesReleases = append(seriesReleases, d.generateRelease(episodeTitle, detailsStr, downloadLink, cat, publishDate, divxSizeSeries))
		})
	})

	return seriesReleases, nil
}

func (d *DivxTotal) parseMovieRelease(query *models.TorznabQuery, title, detailsStr, cat string, publishDate time.Time, sizeStr string) *models.ReleaseInfo {
	// parse tags in title, we need to put the year after the real title (before the tags)
	tags := ""
	for _, m := range divxTagRe.FindAllStringSubmatch(title, -1) {
		tags += " " + strings.TrimSpace(m[1])
		title = strings.ReplaceAll(title, m[0], "")
	}
	title = strings.TrimSpace(title)
	title = divxFourKRe.ReplaceAllString(title, "")

	// add the year
	if query.Year != nil {
		title += " " + strconv.Itoa(*query.Year)
	}

	// add the tags
	title += strings.ToUpper(tags)

	// add suffix and calculate the size
	size := divxTryToParseSize(sizeStr, divxSizePeliculas)
	switch cat {
	case divxCatPeliculas:
		title += " SPANISH DVDRip XviD"
	case divxCatPeliculasHD, divxCatPeliculas3D:
		title += " SPANISH BDRip x264"
	case divxCatPeliculasDVDR:
		title += " SPANISH DVDR"
		size = divxTryToParseSize(sizeStr, divxSizePeliculasDVDR)
	}

	return d.generateRelease(title, detailsStr, detailsStr, cat, publishDate, size)
}

func (d *DivxTotal) generateRelease(title, detailsStr, downloadLink, cat string, publishDate time.Time, size int64) *models.ReleaseInfo {
	link, _ := url.Parse(downloadLink)
	details, _ := url.Parse(detailsStr)
	files := int64(1)
	seeders := int64(1)
	peers := int64(2)
	dvf := float64(0)
	uvf := float64(1)
	return &models.ReleaseInfo{
		Title:                title,
		Details:              details,
		Link:                 link,
		Guid:                 link,
		Category:             d.Caps.Categories.MapTrackerCatToNewznab(cat),
		PublishDate:          publishDate,
		Size:                 &size,
		Files:                &files,
		Seeders:              &seeders,
		Peers:                &peers,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &uvf,
	}
}

// divxGetDownloadLink mirrors upstream GetDownloadLink ("" when absent).
func divxGetDownloadLink(dom *goquery.Selection) string {
	href, _ := dom.Find(`a[href*="` + divxDownloadLink + `"]`).First().Attr("href")
	return href
}

// divxWordRe mirrors upstream \b[\w']*\b (C# \w is Unicode-aware).
var divxWordRe = regexp.MustCompile(`[\p{L}\p{N}_']+`)

// divxFoldWord mirrors the upstream ISO-8859-8 → UTF-8 round-trip "accent
// removal": chars above 0xFF become '?', lone high bytes become U+FFFD.
func divxFoldWord(word string) string {
	word = strings.ToLower(word)
	buf := make([]byte, 0, len(word))
	for _, r := range word {
		if r <= 0xFF {
			buf = append(buf, byte(r))
		} else {
			buf = append(buf, '?')
		}
	}
	if !utf8.Valid(buf) {
		return strings.ToValidUTF8(string(buf), "�")
	}
	return string(buf)
}

// divxCheckTitleMatchWords ports upstream CheckTitleMatchWords.
func divxCheckTitleMatchWords(queryStr, title string) bool {
	// this code splits the words, removes words with 2 letters or less,
	// removes accents and lowercase
	queryWords := map[string]bool{}
	for _, w := range divxWordRe.FindAllString(queryStr, -1) {
		if len(w) > 2 {
			queryWords[divxFoldWord(w)] = true
		}
	}
	titleWords := map[string]bool{}
	for _, w := range divxWordRe.FindAllString(title, -1) {
		if len(w) > 2 {
			titleWords[divxFoldWord(w)] = true
		}
	}
	for word := range queryWords {
		if !titleWords[word] {
			return false
		}
	}
	return true
}

var (
	divxPunctRe     = regexp.MustCompile(`[-._\(\)@/\\\[\]\+\%]`)
	divxSpacesRe    = regexp.MustCompile(`\s+`)
	divxYearRe      = regexp.MustCompile(`([ ]+([0-9]{4}))$`)
	divxLangWordsRe = regexp.MustCompile(`(?i)\b(espa[ñn]ol|spanish|castellano|spa)\b`)
	divxTagRe       = regexp.MustCompile(`[\[\(]([^\]\)]+)[\]\)]`)
	divxFourKRe     = regexp.MustCompile(`(?i) 4K$`)
)

// divxEncodeQuery mirrors WebUtility.UrlEncode ('+' for spaces).
func divxEncodeQuery(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "%20", "+")
}

// divxParseQuery ports upstream ParseQuery (mutates the cloned query).
func divxParseQuery(query *models.TorznabQuery) *models.TorznabQuery {
	// Eg. Marco.Polo.2014.S02E08 — season/episode already parsed by Jackett
	searchTerm := query.SanitizedSearchTerm()

	// replace punctuation symbols with spaces
	searchTerm = divxPunctRe.ReplaceAllString(searchTerm, " ")
	searchTerm = divxSpacesRe.ReplaceAllString(searchTerm, " ")
	searchTerm = strings.TrimSpace(searchTerm)

	// we parse the year and remove it from search
	if m := divxYearRe.FindStringSubmatch(searchTerm); m != nil {
		if y, err := strconv.Atoi(m[2]); err == nil {
			query.Year = &y
		}
		searchTerm = strings.ReplaceAll(searchTerm, m[1], "")
	}

	// remove some words
	searchTerm = divxLangWordsRe.ReplaceAllString(searchTerm, "")

	query.SearchTerm = searchTerm
	return query
}

var divxSeriesEpRe = regexp.MustCompile(`(([0-9]+)x([0-9]+)[^0-9]*)$`)

// divxParseSeriesTitle ports upstream ParseDivxTotalSeriesTitle
// (episodeTitle = American Horror Story6x04 → American Horror Story S06E04 SPANISH SDTV XviD).
func divxParseSeriesTitle(episodeTitle string, query *models.TorznabQuery) string {
	newTitle := episodeTitle
	if m := divxSeriesEpRe.FindStringSubmatch(newTitle); m != nil {
		season := "S" + divxPadLeft2(m[2])
		episode := "E" + divxPadLeft2(m[3])
		// if the original query was in scene format, we have to put the year back
		year := ""
		if query.Episode != "" && query.Year != nil {
			year = " " + strconv.Itoa(*query.Year)
		}
		// upstream string.Replace drops the FIRST occurrence of the matched tail
		newTitle = strings.Replace(newTitle, m[1], year+" "+season+episode, 1)
	}
	newTitle += " SPANISH SDTV XviD"
	return newTitle
}

func divxPadLeft2(s string) string {
	for len(s) < 2 {
		s = "0" + s
	}
	return s
}

// divxTryToParseDate ports upstream TryToParseDate (dd-MM-yyyy, default today).
func divxTryToParseDate(dateToParse string, dateDefault time.Time) time.Time {
	if t, err := time.ParseInLocation("02-01-2006", dateToParse, time.Local); err == nil {
		return t
	}
	return dateDefault
}

// divxTryToParseSize ports upstream TryToParseSize (ParseUtil.GetBytes never
// throws; values <= 0 fall back to the category default).
func divxTryToParseSize(sizeToParse string, sizeDefault int64) int64 {
	parsedSize := parseutil.GetBytes(sizeToParse)
	if parsedSize > 0 {
		return parsedSize
	}
	return sizeDefault
}

func (d *DivxTotal) parseError(dump string, err error) error {
	d.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: d.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// divxRowHTML renders a row as HTML for the upstream error log line.
func divxRowHTML(row *goquery.Selection) string {
	html, err := row.Html()
	if err != nil {
		return ""
	}
	return html
}

func divxContains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (d *DivxTotal) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}
