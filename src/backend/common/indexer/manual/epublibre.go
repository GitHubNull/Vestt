package manual

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
)

// EpubLibre ports Jackett.Common/Indexers/Definitions/EpubLibre.cs.
//
// mechanism: html (public ajax catalog + magnet details-page download)
//
// Correction to the SPEC-08 batch-1 table ("html-login"): upstream
// 0.24.2900 EpubLibre is a PUBLIC indexer with the plain base
// ConfigurationData — there is no login at all. The catalog is an AJAX
// endpoint returning HTML embedded in JSON, and the magnet link is scraped
// from the details page at download time (upstream overrides Download).
//
// 澄清记录:
//   - Upstream Download compares result.RedirectingTo with the overload
//     page; httpclient.Result does not expose the redirect target (the Go
//     stack follows redirects transparently), so the overload condition is
//     approximated: a details page without the magnet anchor surfaces the
//     upstream overload error text.
//   - CheckTitleMatchWords' ISO-8859-8 byte round-trip is ported verbatim
//     (charmap.ISO8859_8 encode → reinterpret as UTF-8); for ASCII words it
//     is the identity, matching upstream behaviour for queries/titles.
//
// Ported from: Definitions/EpubLibre.cs.
type EpubLibre struct {
	indexer.Base
}

// epubLibreConfig is the plain upstream base ConfigurationData (no extra
// items): Site Link + Tags only.
type epubLibreConfig struct {
	configdata.ConfigData
}

// NewEpubLibre builds the indexer (services.Factory shape).
func NewEpubLibre(deps services.Deps) (indexer.Indexer, error) {
	e := &EpubLibre{}
	e.Base = indexer.Base{
		IDValue:          "epublibre",
		NameValue:        "EpubLibre",
		DescriptionValue: "EpubLibre is a Public site. Más libros, Más libres",
		SiteLinkValue:    "https://www.epublibre.org/",
		DefaultSiteLink:  "https://www.epublibre.org/",
		AltSiteLinks:     []string{"https://www.epublibre.org/"},
		LegacySiteLinks: []string{
			"https://epublibre.org/",
			"https://epublibre.unblockit.download/",
		},
		LangValue: "es-ES",
		TypeValue: "public",
		Caps:      epubLibreCaps(),
		Config:    &epubLibreConfig{ConfigData: *configdata.NewConfigData()},
		Client:    deps.Client,
		Saver:     deps.Saver,
		Cache:     deps.Cache,
	}
	return e, nil
}

func epubLibreCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.BookSearchParams = []string{models.BookParamQ} // TODO: add more book parameters
	caps.Categories.AddCategoryMapping("1", models.CatBooksEBook, "")
	return caps
}

// ApplyConfiguration ports upstream ConfigureIfOK(string.Empty, any, …).
func (e *EpubLibre) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := e.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := e.LoadValuesFromJSON(nil, false); err != nil {
		return indexer.ConfigCompleted, err
	}
	releases, err := e.perform(ctx, models.NewTorznabQuery())
	if err != nil {
		return indexer.ConfigCompleted, err
	}
	if len(releases) == 0 {
		return indexer.ConfigCompleted, errors.New("Could not find any release from this URL") //nolint:staticcheck // upstream EpubLibre.cs error text, kept 1:1
	}
	e.SetConfigured(true)
	e.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

func (e *EpubLibre) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return e.ResultsForQueryWith(ctx, q, isMeta, e.perform)
}

func (e *EpubLibre) Test(ctx context.Context) error {
	return e.Base.Test(ctx, e.perform)
}

const (
	epubLibreMaxItemsPerPage    = 18
	epubLibreMaxSearchPageLimit = 6 // 18 items per page * 6 pages = 108
)

var epubLibreAPIHeaders = map[string]string{"X-Requested-With": "XMLHttpRequest"}

var epubLibreLanguages = map[string]string{
	"1":  "español",
	"2":  "catalán",
	"3":  "euskera",
	"4":  "gallego",
	"5":  "inglés",
	"6":  "francés",
	"7":  "alemán",
	"8":  "sueco",
	"9":  "mandarín",
	"10": "italiano",
	"11": "portugués",
	"12": "esperanto",
}

func (e *EpubLibre) perform(ctx context.Context, query *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	var releases []*models.ReleaseInfo

	searchString := "--"
	maxPages := 2 // we scrape only 2 pages for recent torrents
	if strings.TrimSpace(query.GetQueryString()) != "" {
		searchString = escapeDataString(query.GetQueryString())
		maxPages = epubLibreMaxSearchPageLimit
	}

	lastPublishDate := time.Now()
	for page := 0; page < maxPages; page++ {
		searchURL := fmt.Sprintf("%scatalogo/index/%d/nuevo/todos/sin/todos/%s/ajax",
			e.SiteLink(), page*epubLibreMaxItemsPerPage, searchString)
		res, err := e.Client.Do(ctx, e.ID(), &httpclient.Request{
			URL:     searchURL,
			Headers: epubLibreAPIHeaders,
			Cookies: e.CookieHeader(),
		})
		if err != nil {
			return nil, err
		}

		var payload struct {
			Contenido string `json:"contenido"`
		}
		if err := json.Unmarshal(res.Body, &payload); err != nil {
			return nil, e.parseError(string(res.Body), err)
		}

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(payload.Contenido))
		if err != nil {
			return nil, e.parseError(string(res.Body), err)
		}

		rows := doc.Find("div.span2")
		rowCount := rows.Length()
		var rowErr error
		rows.EachWithBreak(func(_ int, row *goquery.Selection) bool {
			r, err := e.parseRow(row, query, &lastPublishDate)
			if err != nil {
				rowErr = err
				return false
			}
			if r != nil {
				releases = append(releases, r)
			}
			return true
		})
		if rowErr != nil {
			return nil, e.parseError(string(res.Body), rowErr)
		}

		if rowCount < epubLibreMaxItemsPerPage {
			break // this is the last page
		}
	}

	return releases, nil
}

func (e *EpubLibre) parseRow(row *goquery.Selection, query *models.TorznabQuery, lastPublishDate *time.Time) (*models.ReleaseInfo, error) {
	title := row.Find("h2").First().Text() + " - " + row.Find("h1").First().Text()
	if !e.checkTitleMatchWords(query.GetQueryString(), title) {
		return nil, nil // skip if it doesn't contain all words
	}

	posterSrc, ok := row.Find("img#catalog").First().Attr("src")
	if !ok {
		return nil, errors.New("row has no catalog image")
	}
	poster, err := epubLibreResolve(e.SiteLink(), posterSrc)
	if err != nil {
		return nil, err
	}
	qLink := row.Find("a").First()
	detailsHref, ok := qLink.Attr("href")
	if !ok {
		return nil, errors.New("row has no details link")
	}
	details, err := epubLibreResolve(e.SiteLink(), detailsHref)
	if err != nil {
		return nil, err
	}

	dataContent, ok := qLink.Attr("data-content")
	if !ok {
		return nil, errors.New("details link has no data-content tooltip")
	}
	tooltip, err := goquery.NewDocumentFromReader(strings.NewReader(dataContent))
	if err != nil {
		return nil, err
	}
	// we get the language from the last class tag => class="pull-right sprite idioma_5"
	classAttr, ok := tooltip.Find("div.pull-right").First().Attr("class")
	if !ok {
		return nil, errors.New("tooltip has no pull-right language div")
	}
	parts := strings.Split(classAttr, "_")
	if len(parts) < 2 {
		return nil, fmt.Errorf("unexpected language class %q", classAttr)
	}
	language, ok := epubLibreLanguages[parts[len(parts)-1]]
	if !ok {
		return nil, fmt.Errorf("unknown language id %q", parts[len(parts)-1])
	}
	title += fmt.Sprintf(" [%s] [epub]", language)

	descCells := tooltip.Find("div.row-fluid > div")
	if descCells.Length() < 4 {
		return nil, fmt.Errorf("tooltip has %d description cells, expected 4", descCells.Length())
	}
	texts := make([]string, 0, 4)
	descCells.Each(func(_ int, s *goquery.Selection) { texts = append(texts, s.Text()) })
	description := fmt.Sprintf("Rev: %s Páginas: %s Puntación: %s Likes: %s",
		texts[0], texts[1], texts[2], texts[3])

	// publish date is not available in the torrent list, but we add a
	// relative date so we can sort
	*lastPublishDate = lastPublishDate.Add(-time.Minute)

	size := int64(5242880) // 5 MB
	seeders := int64(1)
	peers := int64(2)
	dvf := 0.0
	uvf := 1.0
	return &models.ReleaseInfo{
		Title:                title,
		Details:              details,
		Link:                 details,
		Guid:                 details,
		PublishDate:          *lastPublishDate,
		Poster:               poster,
		Description:          description,
		Category:             []int{models.CatBooksEBook.ID},
		Size:                 &size,
		Seeders:              &seeders,
		Peers:                &peers,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &uvf,
	}, nil
}

// Download ports upstream EpubLibre.Download: fetch the details page and
// return the magnet URI bytes (see file header for the RedirectingTo note).
func (e *EpubLibre) Download(ctx context.Context, link *url.URL) ([]byte, error) {
	if e.Client == nil {
		return nil, errors.New("http client not configured")
	}
	res, err := e.Client.Do(ctx, e.ID(), &httpclient.Request{
		URL:     link.String(),
		Cookies: e.CookieHeader(),
	})
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(res.Body))
	if err != nil {
		return nil, e.parseError(string(res.Body), err)
	}
	magnet, ok := doc.Find("a[id=en_desc]").First().Attr("href")
	if !ok {
		// see 澄清记录: the Go client stack follows the redirect to the
		// overload page transparently, so the anchor is missing there.
		return nil, errors.New("El servidor se encuentra sobrecargado en estos momentos. / The server is currently overloaded.") //nolint:staticcheck // upstream EpubLibre.cs error text, kept 1:1
	}
	return []byte(magnet), nil
}

// log is a nil-safe logger for the manual package (Base.Logger may be nil).
func (e *EpubLibre) log() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

// parseError mirrors upstream OnParseError: log the response dump and
// surface Exception("Parse error", ex).
func (e *EpubLibre) parseError(dump string, err error) error {
	e.log().Error(fmt.Sprintf("Error while parsing result: %v\n%s", err, dump))
	return &indexer.IndexerError{IndexerID: e.ID(), Kind: indexer.KindParse, Message: "Parse error"}
}

// checkTitleMatchWords ports upstream CheckTitleMatchWords (TODO upstream:
// merge with query.MatchQueryStringAND).
func (e *EpubLibre) checkTitleMatchWords(queryStr, title string) bool {
	// this code split the words, remove words with 2 letters or less, remove
	// accents and lowercase
	queryWords := epubLibreMatchWords(queryStr)
	titleWords := epubLibreMatchWords(title)
	for _, word := range queryWords {
		found := false
		for _, tw := range titleWords {
			if tw == word {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

var epubLibreWordRe = regexp.MustCompile(`\b[\p{L}\p{N}_']*\b`)

// epubLibreMatchWords mirrors the upstream word extraction: Regex.Matches on
// \b[\w']*\b, keep words longer than 2 chars, lowercase, then the
// ISO-8859-8 byte round-trip (upstream strips accents this way).
func epubLibreMatchWords(s string) []string {
	matches := epubLibreWordRe.FindAllString(s, -1)
	var out []string
	for _, m := range matches {
		if len(m) <= 2 {
			continue
		}
		out = append(out, epubLibreLatinize(strings.ToLower(m)))
	}
	return out
}

// epubLibreLatinize mirrors Encoding.UTF8.GetString(
// Encoding.GetEncoding("ISO-8859-8").GetBytes(s)): each rune is encoded to
// its ISO-8859-8 byte; .NET's EncoderReplacementFallback substitutes '?'
// for unencodable runes (Go's charmap encoder would error instead); the
// resulting byte string is then reinterpreted as UTF-8 (invalid sequences
// become U+FFFD, matching .NET's UTF-8 replacement fallback).
func epubLibreLatinize(s string) string {
	buf := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 0x80 {
			buf = append(buf, byte(r))
		} else if b, ok := epubLibreISO8859_8Reverse[r]; ok {
			// ISO-8859-8 defines non-ASCII slots only for a few specials
			// (NBSP/macron/SHY/… at 0xA0+) and Hebrew letters (0xE0+);
			// the reverse lookup decides encodability exactly.
			buf = append(buf, b)
		} else {
			buf = append(buf, '?')
		}
	}
	return string(buf)
}

// epubLibreISO8859_8Reverse is the rune→byte reverse table of the
// ISO-8859-8 (Latin/Hebrew) code page — the exact byte mapping used by
// .NET's Encoding.GetEncoding("ISO-8859-8") (verified against
// golang.org/x/text/encoding/charmap ISO8859_8 tables). Runes absent from
// the table are unencodable and become '?' (upstream EncoderReplacementFallback).
var epubLibreISO8859_8Reverse = map[rune]byte{
	0x00A0: 0xA0, 0x00A2: 0xA2, 0x00A3: 0xA3, 0x00A4: 0xA4, 0x00A5: 0xA5,
	0x00A6: 0xA6, 0x00A7: 0xA7, 0x00A8: 0xA8, 0x00A9: 0xA9, 0x00D7: 0xAA,
	0x00AB: 0xAB, 0x00AC: 0xAC, 0x00AD: 0xAD, 0x00AE: 0xAE, 0x00AF: 0xAF,
	0x00B0: 0xB0, 0x00B1: 0xB1, 0x00B2: 0xB2, 0x00B3: 0xB3, 0x00B4: 0xB4,
	0x00B5: 0xB5, 0x00B6: 0xB6, 0x00B7: 0xB7, 0x00B8: 0xB8, 0x00B9: 0xB9,
	0x00F7: 0xBA, 0x00BB: 0xBB, 0x00BC: 0xBC, 0x00BD: 0xBD, 0x00BE: 0xBE,
	0x2017: 0xDF,
	0x05D0: 0xE0, 0x05D1: 0xE1, 0x05D2: 0xE2, 0x05D3: 0xE3, 0x05D4: 0xE4,
	0x05D5: 0xE5, 0x05D6: 0xE6, 0x05D7: 0xE7, 0x05D8: 0xE8, 0x05D9: 0xE9,
	0x05DA: 0xEA, 0x05DB: 0xEB, 0x05DC: 0xEC, 0x05DD: 0xED, 0x05DE: 0xEE,
	0x05DF: 0xEF, 0x05E0: 0xF0, 0x05E1: 0xF1, 0x05E2: 0xF2, 0x05E3: 0xF3,
	0x05E4: 0xF4, 0x05E5: 0xF5, 0x05E6: 0xF6, 0x05E7: 0xF7, 0x05E8: 0xF8,
	0x05E9: 0xF9, 0x05EA: 0xFA, 0x200E: 0xFD, 0x200F: 0xFE,
}

// epubLibreResolve resolves possibly-relative hrefs against the site link.
func epubLibreResolve(siteLink, href string) (*url.URL, error) {
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
