// Package manual holds the hand-written indexers (one file per site).
package manual

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// DonTorrent ports Jackett.Common/Indexers/Definitions/DonTorrent.cs.
//
// mechanism: html (public Spanish tracker; POST /buscar form search with
// detail-page expansion; protected downloads require a proof-of-work
// challenge solved at download time)
//
// Ported from: Definitions/DonTorrent.cs.
//
// 澄清记录:
//   - upstream CheckTitleMatchWords uses the same lossy ISO-8859-8 → UTF-8
//     word folding as DivxTotal; kept verbatim (donFoldWord).
//   - upstream ParseSeriesReleaseAsync filters scene-episode queries against
//     ParseSeriesEpisodeTitle(info) — the episode NAME, which never contains
//     the SxxEyy string — instead of the raw episode info. That bug is
//     replicated verbatim (see parseSeriesRelease).
//   - upstream ProcessTags assigns (not appends) ".HC" for "Subs. integrados",
//     discarding previously collected tags; replicated verbatim.
//   - upstream parses the newest-list publish date into a local variable that
//     is never used (dead store); DateTime.TryParse cannot throw, so the port
//     documents and skips it.
//   - upstream searches POST to SiteLink + "buscar" (no trailing slash) even
//     though the SearchUrl constant carries one; the port posts to "buscar".
type DonTorrent struct {
	indexer.Base
	cfg *donTorrentConfig
}

type donTorrentConfig struct {
	configdata.ConfigData
	MatchWords *configdata.BoolItem
}

func newDonTorrentConfig() *donTorrentConfig {
	matchWords := configdata.NewBoolItem("Match words in title")
	matchWords.Value = true
	return &donTorrentConfig{
		ConfigData: *configdata.NewConfigData(),
		MatchWords: matchWords,
	}
}

const (
	donNewTorrentsURL = "ultimos"
	donSearchURL      = "buscar/"

	donCatPelicula   = "pelicula"
	donCatSerie      = "serie"
	donCatDocumental = "documental"
)

// donCategoriesMap mirrors upstream CategoriesMap insertion order
// (FirstOrDefault over the dictionary).
var donCategoriesMap = []struct {
	key string
	cat string
}{
	{"/pelicula/", donCatPelicula},
	{"/serie/", donCatSerie},
	{"/documental/", donCatDocumental},
}

// NewDonTorrent builds the indexer (services.Factory shape).
func NewDonTorrent(deps services.Deps) (indexer.Indexer, error) {
	d := &DonTorrent{cfg: newDonTorrentConfig()}
	d.Base = indexer.Base{
		IDValue:          "dontorrent",
		NameValue:        "DonTorrent",
		DescriptionValue: "DonTorrent is a SPANISH Public tracker for MOVIES / TV / GENERAL",
		SiteLinkValue:    "https://dontorrent.channel/",
		DefaultSiteLink:  "https://dontorrent.channel/",
		LegacySiteLinks: []string{
			"https://dontorrent.promo/",
			"https://dontorrent.photos/",
			"https://dontorrent.cfd/",
			"https://verdetorrent.com/",
			"https://naranjatorrent.com/",
			"https://todotorrents.org/",
			"https://tomadivx.net/",
			"https://seriesblanco.one/",
			"https://dontorrent.pink/",
			"https://dontorrent.reisen/",
			"https://dontorrent.racing/",
			"https://dontorrent.rocks/",
			"https://dontorrent.science/",
			"https://dontorrent.support/",
			"https://dontorrent.review/",
			"https://dontorrent.management/",
			"https://dontorrent.soccer/",
			"https://dontorrent.supply/",
			"https://dontorrent.moi/",
			"https://dontorrent.meme/",
		},
		LangValue: "es-ES",
		TypeValue: "public",
		Caps:      donCaps(),
		Config:    d.cfg,
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	// upstream: webclient.requestDelay = 2.1 (avoid Cloudflare rate limiter).
	if deps.Client != nil {
		deps.Client.SetRequestDelay(d.IDValue, 2100*time.Millisecond)
	}
	d.ResolveDownload = d.resolveDownload
	return d, nil
}

func donCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.MovieSearchParams = []string{models.MovieParamQ}
	// Raw search shows better results
	caps.SupportsRawSearch = true
	c := caps.Categories
	c.AddCategoryMapping(donCatPelicula, models.CatMovies, "Movies")
	c.AddCategoryMapping(donCatSerie, models.CatTV, "TV")
	c.AddCategoryMapping(donCatDocumental, models.CatTVDocumentary, "TV/Documentary")
	return caps
}

// ApplyConfiguration ports upstream: load values, run an empty query and
// require at least one release ("Could not find releases from this URL").
func (d *DonTorrent) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
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
		return indexer.ConfigCompleted, errors.New("Could not find releases from this URL") //nolint:staticcheck // upstream DonTorrent.cs error text, kept 1:1
	}
	d.SetConfigured(true)
	d.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (d *DonTorrent) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return d.ResultsForQueryWith(ctx, q, isMeta, d.perform)
}

func (d *DonTorrent) Test(ctx context.Context) error {
	return d.Base.Test(ctx, d.perform)
}

func (d *DonTorrent) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	matchWords := d.cfg.MatchWords.Value
	matchWords = query.SearchTerm != "" && matchWords

	// we remove parts from the original query
	query = donParseQuery(query.Clone())

	if strings.TrimSpace(query.SearchTerm) == "" {
		return d.performNewest(ctx, query)
	}
	return d.performSearch(ctx, query, matchWords)
}

// resolveDownload ports upstream Download: links of the form
// ".../<tabla>/<contentId>" go through the protected-download
// proof-of-work flow; anything else is handed to Base.Download as-is.
func (d *DonTorrent) resolveDownload(ctx context.Context, link *url.URL) (string, error) {
	downloadLink := ""
	cleanLink := strings.TrimRight(link.String(), "/")

	lastSlash := strings.LastIndex(cleanLink, "/")
	if lastSlash > 0 {
		contentIDStr := cleanLink[lastSlash+1:]
		aux := cleanLink[:lastSlash]
		secondLastSlash := strings.LastIndex(aux, "/")
		if secondLastSlash > 0 {
			tabla := aux[secondLastSlash+1:]
			rawHref := aux[:secondLastSlash]

			if contentIDStr != "" && tabla != "" {
				if _, err := strconv.Atoi(contentIDStr); err == nil {
					// Protected downloads challenge solver
					protectedURL := d.getProtectedDownloadURL(ctx, contentIDStr, tabla)
					if protectedURL != "" {
						downloadLink = protectedURL
					} else {
						if strings.HasPrefix(rawHref, "//") {
							downloadLink = "https:" + rawHref
						} else {
							downloadLink = rawHref
						}
					}
				}
			}
		}
	}

	if downloadLink == "" {
		return "", errors.New("DonTorrent: could not resolve download link")
	}
	return downloadLink, nil
}

func (d *DonTorrent) performNewest(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo

	target := d.SiteLink() + donNewTorrentsURL

	body, err := d.getOK(ctx, target)
	if err != nil {
		return nil, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return nil, d.parseError(body, err)
	}

	parsedDetailsLink := map[string]bool{}

	doc.Find("div.seccion#ultimos_torrents a.text-primary").Each(func(_ int, row *goquery.Selection) {
		rowTitle := strings.TrimSpace(row.Text())
		href, _ := row.Attr("href")
		rowDetailsLink := fmt.Sprintf("%s%s", d.SiteLink(), href)

		// upstream parses the previous sibling as a date into rowPublishDate,
		// which is a dead store (never passed downstream); DateTime.TryParse
		// cannot throw, so the port documents and skips it.
		rowQuality := ""
		if next := row.Next(); next.Length() > 0 {
			nextText := strings.TrimSpace(next.Text())
			if donParensRe.MatchString(nextText) {
				rowQuality = strings.ReplaceAll(strings.Trim(strings.Trim(nextText, "("), ")"), "-", ".")
			}
		}

		// we add parsed items to parsedDetailsLink to avoid duplicates in the
		// newest torrents list results
		if !parsedDetailsLink[rowDetailsLink] {
			cat := donGetCategoryFromURL(rowDetailsLink)
			d.parseRelease(ctx, &releases, rowDetailsLink, rowTitle, cat, rowQuality, query, false)
			parsedDetailsLink[rowDetailsLink] = true
		}
	})

	return releases, nil
}

func (d *DonTorrent) performSearch(ctx context.Context, query *models.TorznabQuery, matchWords bool) ([]*models.ReleaseInfo, error) {
	// Found release result
	var releases []*models.ReleaseInfo

	searchTerm := query.SearchTerm

	// Search results
	page := 1
	totalResults := -1
	processedResults := 0
	endOfSearch := false

	if len(searchTerm) >= 2 {
		for !endOfSearch && (processedResults < totalResults || totalResults == -1) {
			// Perform the search query using POST
			formData := url.Values{}
			formData.Set("valor", searchTerm)
			formData.Set("Buscar", "Buscar")
			formData.Set("p", strconv.Itoa(page))

			if d.Client == nil {
				return nil, errors.New("http client not configured")
			}
			res, err := d.Client.Do(ctx, d.ID(), &httpclient.Request{
				Method:  http.MethodPost,
				URL:     d.SiteLink() + "buscar",
				Referer: d.SiteLink(),
				Body:    formData,
				Cookies: d.CookieHeader(),
			})
			if err != nil {
				return nil, err
			}
			if res.StatusCode != http.StatusOK {
				return nil, errors.New(string(res.Body))
			}
			body := string(res.Body)

			doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
			if err != nil {
				return nil, d.parseError(body, err)
			}

			rows := doc.Find("div.seccion#buscador > div.card > div.card-body > ul > li")

			if rows.Length() == 0 {
				endOfSearch = true
			} else if page == 1 && strings.Contains(rows.First().Text(), "Introduce alguna palabra para buscar con al menos 2 letras.") {
				endOfSearch = true
			} else {
				if page == 1 {
					leadBTagElements := doc.Find("div.seccion#buscador > div.card > div.card-body > p.lead > b")
					totalResults = -1
					if leadBTagElements.Length() >= 2 {
						if n, err := strconv.Atoi(strings.TrimSpace(leadBTagElements.Eq(1).Text())); err == nil && n > 0 {
							totalResults = n
						}
					}
					if totalResults <= 0 {
						endOfSearch = true
					}
				}

				if !endOfSearch {
					rows.Each(func(_ int, row *goquery.Selection) {
						processedResults++

						// href=/pelicula/6981/Saga-Spiderman
						anchor := row.Find("li > span > a").First()

						if anchor.Length() > 0 {
							href, _ := anchor.Attr("href")
							link := fmt.Sprintf("%s%s", strings.TrimRight(d.SiteLink(), "/"), href)
							title := anchor.Text()
							cat := donGetCategoryFromURL(link)
							quality := ""
							if q := row.Find("li > span > span").First(); q.Length() > 0 {
								quality = strings.ReplaceAll(strings.Trim(strings.Trim(strings.TrimSpace(q.Text()), "("), ")"), "-", ".")
							}

							d.parseRelease(ctx, &releases, link, title, cat, quality, query, matchWords)
						}
					})

					// Stop pagination if all items are processed or no more rows are returned
					if processedResults >= totalResults || rows.Length() == 0 {
						endOfSearch = true
					} else {
						page++
					}
				}
			}
		}
	} else if len(searchTerm) == 0 {
		return d.performNewest(ctx, query)
	}

	return releases, nil
}

// parseRelease ports upstream ParseReleaseAsync.
func (d *DonTorrent) parseRelease(ctx context.Context, releases *[]*models.ReleaseInfo, link, title, category, quality string, query *models.TorznabQuery, matchWords bool) {
	// Remove trailing dot if there's one.
	title = strings.TrimSpace(title)
	if strings.HasSuffix(title, ".") {
		title = strings.TrimSpace(title[:len(title)-1])
	}

	// return results only for requested categories
	if query.HasSpecifiedCategories() {
		mapped := d.Caps.Categories.MapTrackerCatToNewznab(category)
		first := 0
		if len(mapped) > 0 {
			first = mapped[0]
		}
		if !donContainsInt(query.Categories, first) {
			return
		}
	}

	if (matchWords && donCheckTitleMatchWords(query.SearchTerm, title)) || !matchWords {
		switch category {
		case donCatPelicula:
			d.parseMovieRelease(ctx, releases, link, query, title, quality, category)
		case donCatDocumental, donCatSerie:
			d.parseSeriesRelease(ctx, releases, link, query, title, quality, category)
		}
	}
}

func (d *DonTorrent) parseMovieRelease(ctx context.Context, releases *[]*models.ReleaseInfo, link string, query *models.TorznabQuery, title, quality, category string) {
	cleanReleaseTitle := donCleanReleaseTitle(title)
	tags := donProcessTags(title)
	lang := ".SPANISH"
	downloadLink := link
	var size int64

	body, err := d.getOK(ctx, link)
	if err != nil {
		d.log().Error(fmt.Sprintf("DonTorrent: failed to load movie details [%s]: %v", link, err))
		return
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		d.log().Error(fmt.Sprintf("DonTorrent: failed to parse movie details [%s]: %v", link, err))
		return
	}

	year := strings.TrimSpace(doc.Find("div.d-inline-block.ml-2 > p.m-1 > a").First().Text())

	// add the year
	if year != "" && donYearRe.MatchString(year) {
		year = "." + year
	} else {
		year = ""
	}

	info := doc.Find("div.descargar > div.card > div.card-body").First()

	downloadBtn := info.Find("a.protected-download").First()
	if downloadBtn.Length() == 0 {
		downloadBtn = info.Find("div.text-center a").First()
	}

	if downloadBtn.Length() > 0 {
		contentIDStr, _ := downloadBtn.Attr("data-content-id")
		tabla, _ := downloadBtn.Attr("data-tabla")

		if contentIDStr != "" && tabla != "" {
			if _, err := strconv.Atoi(contentIDStr); err == nil {
				downloadLink = link + "/" + tabla + "/" + contentIDStr
			}
		}
	}

	moreinfo := info.Find("div.text-center > div.d-inline-block")

	// guess size
	if moreinfo.Length() == 2 {
		size = parseutil.GetBytes(moreinfo.Eq(1).Find("p").First().Text())
	} else {
		size = donGuessSize(quality, category)
	}

	*releases = append(*releases, d.generateRelease(cleanReleaseTitle+year+"."+quality+tags+lang, link, downloadLink, category, time.Now(), size))
}

func (d *DonTorrent) parseSeriesRelease(ctx context.Context, releases *[]*models.ReleaseInfo, link string, query *models.TorznabQuery, title, quality, category string) {
	cleanReleaseTitle := donCleanReleaseTitle(title)
	tags := donProcessTags(title)
	lang := ".SPANISH"
	season := donParseSeriesSeason(title)

	body, err := d.getOK(ctx, link)
	if err != nil {
		d.log().Error(fmt.Sprintf("DonTorrent: failed to load series details [%s]: %v", link, err))
		return
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		d.log().Error(fmt.Sprintf("DonTorrent: failed to parse series details [%s]: %v", link, err))
		return
	}

	data := doc.Find("div.descargar > div.card > div.card-body > div.d-inline-block > table.table > tbody > tr")

	data.Each(func(_ int, row *goquery.Selection) {
		tds := row.Find("td")
		if tds.Length() < 3 {
			return
		}
		info := strings.TrimSpace(tds.Eq(0).Text())
		episodeNumber := donParseSeriesEpisodeNumber(info)
		episodeTitle := donParseSeriesEpisodeTitle(info)
		episodePublishDate := time.Now()
		if t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(tds.Eq(2).Text()), time.Local); err == nil {
			episodePublishDate = t
		}
		size := donGuessSize(quality, category)
		size *= donGetEpisodeCountFromTitle(episodeNumber)

		downloadLink := ""
		downloadBtn := row.Find("td > a.protected-download").First()

		if downloadBtn.Length() > 0 {
			contentIDStr, _ := downloadBtn.Attr("data-content-id")
			tabla, _ := downloadBtn.Attr("data-tabla")

			if contentIDStr != "" && tabla != "" {
				if _, err := strconv.Atoi(contentIDStr); err == nil {
					downloadLink = link + "/" + tabla + "/" + contentIDStr
				}
			}
		}

		// if the original query was in scene format, we filter the results to
		// match episode. Upstream checks the parsed episode NAME here, which
		// never contains the SxxEyy string (upstream bug, replicated).
		if query.Episode != "" && !strings.Contains(episodeTitle, query.GetEpisodeSearchString()) {
			return
		}

		if strings.TrimSpace(episodeTitle) == "" {
			*releases = append(*releases, d.generateRelease(cleanReleaseTitle+"."+season+episodeNumber+"."+quality+tags+lang, link, downloadLink, category, episodePublishDate, size))
		} else {
			*releases = append(*releases, d.generateRelease(cleanReleaseTitle+"."+season+episodeNumber+"."+episodeTitle+"."+quality+tags+lang, link, downloadLink, category, episodePublishDate, size))
		}
	})
}

func (d *DonTorrent) generateRelease(title, link, downloadLink, cat string, publishDate time.Time, size int64) *models.ReleaseInfo {
	dl, _ := url.Parse(downloadLink)
	lnk, _ := url.Parse(link)
	seeders := int64(1)
	peers := int64(2)
	dvf := float64(0)
	uvf := float64(1)
	return &models.ReleaseInfo{
		Title:                title,
		Details:              lnk,
		Link:                 dl,
		Guid:                 dl,
		Category:             d.Caps.Categories.MapTrackerCatToNewznab(cat),
		PublishDate:          publishDate,
		Size:                 &size,
		Seeders:              &seeders,
		Peers:                &peers,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &uvf,
	}
}

// getOK performs a GET requiring a 200 response (upstream
// RequestWithCookiesAsync + ExceptionWithConfigData on other statuses).
func (d *DonTorrent) getOK(ctx context.Context, target string) (string, error) {
	if d.Client == nil {
		return "", errors.New("http client not configured")
	}
	res, err := d.Client.Do(ctx, d.ID(), &httpclient.Request{
		URL:     target,
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

var (
	donSeasonRe     = regexp.MustCompile(`^(.*?)\s*-\s*(\d+)[ªº]?\s+Temporada`)
	donMiniserieRe  = regexp.MustCompile(`^(.*?)\s*-\s*Miniserie`)
	donEpisodeNumRe = regexp.MustCompile(`\d+x(\d+)(?:\s*(?:-|al)\s*(?:\d+x)?(\d+))?`)
	donTagRe        = regexp.MustCompile(`[\[\(]([^\]\)]+)[\]\)]`)
	donYearRe       = regexp.MustCompile(`^((?:19|20)\d{2})$`)
	donWordRe       = regexp.MustCompile(`[\p{L}\p{N}_']+`)
	donParensRe     = regexp.MustCompile(`[()]`)
	donCleanTitleRe = regexp.MustCompile(`[\s:\-\._]+`)
	donEpisodeCntRe = regexp.MustCompile(`E[0-9+]`)
)

// donParseSeriesSeason ports upstream ParseSeriesSeason.
func donParseSeriesSeason(title string) string {
	result := ""

	if m := donSeasonRe.FindStringSubmatch(title); m != nil {
		seasonNumber, _ := strconv.Atoi(m[2])
		result = fmt.Sprintf("S%02d", seasonNumber)
	} else if donMiniserieRe.MatchString(title) {
		result = "S01"
	}

	return result
}

// donParseSeriesEpisodeNumber ports upstream ParseSeriesEpisodeNumber.
func donParseSeriesEpisodeNumber(episodeTitle string) string {
	result := ""

	if m := donEpisodeNumRe.FindStringSubmatch(episodeTitle); m != nil {
		epStart, _ := strconv.Atoi(m[1])
		if m[2] != "" {
			epEnd, _ := strconv.Atoi(m[2])
			result = fmt.Sprintf("E%02d-E%02d", epStart, epEnd)
		} else {
			result = fmt.Sprintf("E%02d", epStart)
		}
	}

	return result
}

// donParseSeriesEpisodeTitle ports upstream ParseSeriesEpisodeTitle.
func donParseSeriesEpisodeTitle(episodeTitle string) string {
	result := ""

	if loc := donEpisodeNumRe.FindStringSubmatchIndex(episodeTitle); loc != nil {
		endIndex := loc[1]
		if endIndex < len(episodeTitle) {
			remain := strings.TrimSpace(episodeTitle[endIndex:])

			if strings.HasPrefix(remain, "-") {
				rawTitle := strings.TrimSpace(remain[1:])

				titleName := strings.TrimRight(rawTitle, "….")

				if titleName != "" {
					result = strings.ReplaceAll(titleName, " ", ".")
				}
			}
		}
	}

	return result
}

// donGetEpisodeCountFromTitle ports upstream GetEpisodeCountFromTitle.
func donGetEpisodeCountFromTitle(title string) int64 {
	matches := donEpisodeCntRe.FindAllStringIndex(title, -1)
	count := int64(len(matches))
	if count == 0 {
		return 0 // no episodes in title
	}

	// eg E1-E9
	if count == 2 {
		first := title[matches[0][0] : matches[1][0]-1]
		last := title[matches[1][0]:min(matches[1][0]+3, len(title))] // "Exx"
		if strings.HasPrefix(first, "E") && strings.HasPrefix(last, "E") {
			firstEp, err1 := strconv.ParseInt(first[1:min(3, len(first))], 10, 64)
			lastEp, err2 := strconv.ParseInt(last[1:min(3, len(last))], 10, 64)
			if err1 == nil && err2 == nil {
				return lastEp - firstEp + 1 // E01-E03 -> 3 episodes
			}
		}
	}

	return count
}

// donGetCategoryFromURL ports upstream GetCategoryFromURL (first map entry
// whose key the URL contains, else "").
func donGetCategoryFromURL(rawURL string) string {
	for _, e := range donCategoriesMap {
		if strings.Contains(rawURL, e.key) {
			return e.cat
		}
	}
	return ""
}

// donCleanReleaseTitle ports upstream CleanReleaseTitle.
func donCleanReleaseTitle(title string) string {
	result := ""

	if m := donSeasonRe.FindStringSubmatch(title); m != nil {
		result = strings.TrimSpace(m[1])
	}

	if m := donMiniserieRe.FindStringSubmatch(title); result == "" && m != nil {
		result = strings.TrimSpace(m[1])
	}

	if result == "" {
		result = title
	}

	if index := strings.IndexAny(result, "(["); index >= 0 {
		result = result[:index]
	}

	result = donCleanTitleRe.ReplaceAllString(strings.TrimSpace(result), ".")
	result = strings.Trim(result, ".")

	return result
}

// donGuessSize ports upstream GuessSize (quality/category lookup table).
func donGuessSize(quality, category string) int64 {
	var size int64
	qualityL := strings.ToLower(strings.TrimSpace(quality))

	switch strings.ToLower(strings.TrimSpace(category)) {
	case "pelicula":
		switch qualityL {
		case "fullbluray":
			size = 48318382080 // 45 GB
		case "4k":
			size = 21474836480 // 20 GB
		case "bdremux", "bdremux.1080p":
			size = 21474836480 // 20 GB
		case "bluray", "bluray.1080p":
			size = 9663676416 // 9 GB
		case "microhd", "microhd.1080p":
			size = 4831838208 // 4.5 GB
		case "bluray.720p":
			size = 4294967296 // 4 GB
		case "microhd.720p", "hdrip":
			size = 2147483648 // 2 GB
		case "dvdrip", "hdtv":
			size = 1503238554 // 1.4 GB
		case "screener", "scr":
			size = 1073741824 // 1 GB
		case "cam", "telesync", "ts", "tc":
			size = 734003200 // 700 MB
		default:
			size = 524288000 // 500 MB
		}
	case "documental", "serie":
		switch qualityL {
		case "fullbluray":
			size = 10737418240 // 10 GB
		case "4k":
			size = 10737418240 // 10 GB
		case "bdremux", "bdremux.1080p":
			size = 8589934592 // 8 GB
		case "bluray", "bluray.1080p":
			size = 3221225472 // 3 GB
		case "microhd", "microhd.1080p":
			size = 1610612736 // 1.5 GB
		case "bluray.720p":
			size = 2147483648 // 2 GB
		case "microhd.720p", "hdtv.720p", "hdrip":
			size = 1073741824 // 1 GB
		case "dvdrip", "hdtv":
			size = 536870912 // 500 MB
		case "screener", "scr":
			size = 419430400 // 400 MB
		case "cam", "telesync", "ts", "tc":
			size = 314572800 // 300 MB
		default:
			size = 524288000 // 500 MB
		}
	}

	return size
}

// donProcessTags ports upstream ProcessTags (note: "Subs. integrados"
// ASSIGNS ".HC", discarding previously collected tags — upstream bug kept).
func donProcessTags(title string) string {
	tags := ""

	for _, m := range donTagRe.FindAllStringSubmatch(title, -1) {
		tag := strings.ToUpper(strings.TrimSpace(m[1]))

		if strings.ToLower(tag) == "v. extendida" {
			tags += ".Directors.Cut"
		} else if strings.ToLower(tag) == "subs. integrados" {
			tags = ".HC"
		} else {
			tags += "." + tag
		}
	}

	return tags
}

// getProtectedDownloadURL ports upstream GetProtectedDownloadUrlAsync: a
// generate/validate proof-of-work handshake against api_validate_pow.php.
// Returns "" when any step fails (upstream returns null).
func (d *DonTorrent) getProtectedDownloadURL(ctx context.Context, contentID, contentType string) string {
	apiURL := strings.TrimRight(d.SiteLink(), "/") + "/api_validate_pow.php"
	headers := map[string]string{"Content-Type": "application/json; charset=utf-8"}

	generateData := map[string]string{
		"action":     "generate",
		"content_id": contentID,
		"tabla":      contentType,
	}
	generateJSON, _ := json.Marshal(generateData)

	genResponse, err := d.postJSON(ctx, apiURL, headers, string(generateJSON))
	if err != nil {
		return ""
	}

	var genResult map[string]any
	if err := json.Unmarshal([]byte(genResponse), &genResult); err != nil {
		return ""
	}
	if success, _ := genResult["success"].(bool); !success {
		return ""
	}

	challenge, _ := genResult["challenge"].(string)
	if challenge == "" {
		return ""
	}

	nonce := donComputeProofOfWork(challenge, 3)

	validateData := map[string]string{
		"action":    "validate",
		"challenge": challenge,
		"nonce":     strconv.FormatInt(nonce, 10),
	}
	validateJSON, _ := json.Marshal(validateData)

	valResponse, err := d.postJSON(ctx, apiURL, headers, string(validateJSON))
	if err != nil {
		return ""
	}

	var valResult map[string]any
	if err := json.Unmarshal([]byte(valResponse), &valResult); err != nil {
		return ""
	}
	if success, _ := valResult["success"].(bool); !success {
		return ""
	}

	relativeURL, _ := valResult["download_url"].(string)
	if relativeURL == "" {
		return ""
	}

	relativeURL = strings.ReplaceAll(relativeURL, "\\/", "/")
	if strings.HasPrefix(relativeURL, "//") {
		relativeURL = "https:" + relativeURL
	}

	return relativeURL
}

func (d *DonTorrent) postJSON(ctx context.Context, target string, headers map[string]string, rawBody string) (string, error) {
	if d.Client == nil {
		return "", errors.New("http client not configured")
	}
	res, err := d.Client.Do(ctx, d.ID(), &httpclient.Request{
		Method:  http.MethodPost,
		URL:     target,
		Headers: headers,
		RawBody: rawBody,
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

// donComputeProofOfWork ports upstream ComputeProofOfWork: the first nonce
// whose SHA-256 of challenge+nonce hex starts with `difficulty` zeroes.
func donComputeProofOfWork(challenge string, difficulty int) int64 {
	var nonce int64
	target := strings.Repeat("0", difficulty)

	for {
		sum := sha256.Sum256([]byte(challenge + strconv.FormatInt(nonce, 10)))
		hashHex := hex.EncodeToString(sum[:])

		if strings.HasPrefix(hashHex, target) {
			return nonce
		}

		nonce++
	}
}

// donFoldWord mirrors the upstream ISO-8859-8 → UTF-8 word folding (see
// divxFoldWord; both trackers share the upstream helper).
func donFoldWord(word string) string {
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

// donCheckTitleMatchWords ports upstream CheckTitleMatchWords.
func donCheckTitleMatchWords(queryStr, title string) bool {
	queryWords := map[string]bool{}
	for _, w := range donWordRe.FindAllString(queryStr, -1) {
		if len(w) > 2 {
			queryWords[donFoldWord(w)] = true
		}
	}
	titleWords := map[string]bool{}
	for _, w := range donWordRe.FindAllString(title, -1) {
		if len(w) > 2 {
			titleWords[donFoldWord(w)] = true
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
	donPunctRe     = regexp.MustCompile(`[-._\(\)@/\\\[\]\+\%]`)
	donSpacesRe    = regexp.MustCompile(`\s+`)
	donYearTermRe  = regexp.MustCompile(`([ ]+([0-9]{4}))$`)
	donLangWordsRe = regexp.MustCompile(`(?i)\b(espa[ñn]ol|spanish|castellano|spa)\b`)
)

// donParseQuery ports upstream ParseQuery (mutates the cloned query).
func donParseQuery(query *models.TorznabQuery) *models.TorznabQuery {
	// Eg. Marco.Polo.2014.S02E08 — season/episode already parsed by Jackett
	searchTerm := query.SanitizedSearchTerm()

	// replace punctuation symbols with spaces
	searchTerm = donPunctRe.ReplaceAllString(searchTerm, " ")
	searchTerm = donSpacesRe.ReplaceAllString(searchTerm, " ")
	searchTerm = strings.TrimSpace(searchTerm)

	// we parse the year and remove it from search
	if m := donYearTermRe.FindStringSubmatch(searchTerm); m != nil {
		if y, err := strconv.Atoi(m[2]); err == nil {
			query.Year = &y
		}
		searchTerm = strings.ReplaceAll(searchTerm, m[1], "")
	}

	// remove some words
	searchTerm = donLangWordsRe.ReplaceAllString(searchTerm, "")

	query.SearchTerm = searchTerm
	return query
}

func (d *DonTorrent) parseError(dump string, err error) error {
	d.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: d.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

func donContainsInt(list []int, v int) bool {
	for _, n := range list {
		if n == v {
			return true
		}
	}
	return false
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (d *DonTorrent) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}
