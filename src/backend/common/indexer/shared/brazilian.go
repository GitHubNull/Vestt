// mechanism: html (public Brazilian WordPress "?s=" search)
//
// Ported from: Abstract/PublicBrazilianIndexerBase.cs (+ SimpleRequestGenerator
// and RowParsingExtensions in the same upstream file, and
// PublicBrazilianParser).
//
// Brazilian is the shared base for the public Brazilian trackers
// (Blaze, ComandoTorrent, etc.). Concrete indexers embed it, set identity
// via indexer.Base, use NewBrazilianCaps for the shared capabilities and
// implement their own row selection; the field extractors here operate on
// pre-extracted row text / HTML fragments (stdlib-only HTML handling).
//
// 澄清记录:
//   - upstream ExtractFromRow runs CSS selectors ("span:contains(...)") via
//     AngleSharp; the Go port (no goquery dependency allowed) applies the
//     same label regexes to the tag-stripped row text, which is equivalent
//     for the flat label:value markup these trackers use.
//   - upstream ExtractTitleOrDefault reads only regex group 1 of
//     "&dn=(.+?)&|&dn=(.+?)$"; when the dn parameter is last (group 2
//     matches) upstream keeps an empty title. That bug is replicated
//     verbatim (see ExtractTitleOrDefault).
//   - upstream sets webclient.requestDelay = .5 (seconds) in the
//     constructor; use BrazilianRequestDelay + (*Brazilian).WireDelay.
package shared

import (
	"context"
	"html"
	"net/url"
	"regexp"
	"strings"
	"time"

	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/utils/parseutil"
)

// BrazilianRequestDelay mirrors upstream webclient.requestDelay = .5s.
const BrazilianRequestDelay = 500 * time.Millisecond

// BrazilianDescription mirrors the upstream Description template.
func BrazilianDescription(name string) string {
	return name + " is a Public Torrent Tracker for Movies and TV Shows dubbed in Brazilian Portuguese"
}

// NewBrazilianCaps mirrors upstream PublicBrazilianIndexerBase.SetCapabilities
// (private and identical for every Brazilian tracker).
func NewBrazilianCaps() *models.Capabilities {
	caps := models.NewCapabilities()
	caps.MovieSearchParams = []string{models.MovieParamQ}
	caps.TVSearchParams = []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	caps.Categories.AddCategoryMapping("filmes", models.CatMovies, "")
	caps.Categories.AddCategoryMapping("series", models.CatTV, "")
	return caps
}

// Brazilian ports upstream PublicBrazilianIndexerBase (+ the
// PublicBrazilianParser abstract). The config is the plain base
// ConfigurationData (configdata.NewConfigData).
type Brazilian struct {
	indexer.Base

	// SearchQueryParamsKey mirrors SimpleRequestGenerator's key
	// (default "?s=").
	SearchQueryParamsKey string

	// GetTitleElement resolves the description/title element text from the
	// download-button HTML fragment (upstream abstract
	// GetTitleElementOrNull); nil = no description element.
	GetTitleElement func(downloadButtonHTML string) string
}

// WireDelay applies the upstream requestDelay (0.5s) to this indexer on the
// wired client; call once from the factory after Client is set.
func (b *Brazilian) WireDelay() {
	if b.Client != nil {
		b.Client.SetRequestDelay(b.ID(), BrazilianRequestDelay)
	}
}

// ApplyConfiguration mirrors upstream: these public indexers need no login,
// configuration always succeeds (ConfigureIfOK with isLoggedin=true).
func (b *Brazilian) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := applyPostedItems(&b.Base, items); err != nil {
		return indexer.ConfigCompleted, err
	}
	b.SetConfigured(true)
	b.SaveConfig(ctx)
	return indexer.ConfigCompleted, nil
}

// SearchURL ports SimpleRequestGenerator.GetSearchRequests: "?s=" + term
// (+ " S<season>"), or the bare site link for empty searches.
func (b *Brazilian) SearchURL(q *models.TorznabQuery) string {
	key := b.SearchQueryParamsKey
	if key == "" {
		key = "?s="
	}
	searchURL := b.SiteLinkValue + key
	if strings.TrimSpace(q.SearchTerm) != "" {
		searchURL += Encode(q.SearchTerm)
		if q.Season != nil {
			searchURL += Encode(" " + itoa(*q.Season))
		}
	} else {
		searchURL = b.SiteLinkValue
	}
	return searchURL
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// --- RowParsingExtensions ---------------------------------------------------
// Upstream operates on AngleSharp IElements; the Go port takes the row's
// visible text (see TextOf) or raw HTML fragment where an attribute is read.

// TextOf renders an HTML fragment as visible text (tags stripped, entities
// decoded, whitespace collapsed).
var whitespaceRe = regexp.MustCompile(`\s+`)

// TextOf renders an HTML fragment as the upstream TextContent would (with
// whitespace collapsed for regex matching).
func TextOf(fragment string) string {
	return strings.TrimSpace(whitespaceRe.ReplaceAllString(stripTags(fragment), " "))
}

var hrefRe = regexp.MustCompile(`(?is)\bhref\s*=\s*"([^"]*)"`)

// ExtractMagnet mirrors upstream ExtractMagnet: the download button's href
// (entities decoded, as a DOM attribute read would be), or nil when
// empty/missing.
func ExtractMagnet(downloadButtonHTML string) *url.URL {
	href := hrefValue(downloadButtonHTML)
	if href == "" {
		return nil
	}
	u, err := url.Parse(href)
	if err != nil {
		return nil
	}
	return u
}

// extractPattern mirrors upstream ExtractPattern: the first capture group of
// pattern against text.
func extractPattern(text, pattern string) (string, bool) {
	m := regexp.MustCompile(pattern).FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// extractByLabel finds "label value" in raw row HTML, capturing the value up
// to the next tag (upstream reads the TextContent of the span containing the
// label; the flat label:value markup these trackers emit is equivalent).
func extractByLabel(rowHTML, label string) (string, bool) {
	return extractPattern(rowHTML, regexp.QuoteMeta(label)+`\s*([^<]+)`)
}

// ExtractGenres mirrors upstream ExtractGenres ("Gênero:" label); rowHTML
// is the raw row markup.
func ExtractGenres(rowHTML string) []string {
	var genres []string
	if value, ok := extractByLabel(rowHTML, "Gênero:"); ok {
		genres = extractMultiValuesFromField(value)
	}
	return genres
}

// ExtractSubtitles mirrors upstream ExtractSubtitles ("Legenda:" label).
func ExtractSubtitles(rowHTML string) []string {
	var subtitles []string
	if value, ok := extractByLabel(rowHTML, "Legenda:"); ok {
		subtitles = extractMultiValuesFromField(value)
	}
	return subtitles
}

// ExtractLanguages mirrors upstream ExtractLanguages: "Áudio:" label with
// "Idioma:" fallback.
func ExtractLanguages(rowHTML string) []string {
	var languages []string
	if value, ok := extractByLabel(rowHTML, "Áudio:"); ok {
		languages = extractMultiValuesFromField(value)
	}
	if len(languages) == 0 {
		if value, ok := extractByLabel(rowHTML, "Idioma:"); ok {
			languages = extractMultiValuesFromField(value)
		}
	}
	return languages
}

// extractMultiValuesFromField mirrors upstream: '|' split, ',' split, or a
// single value; every token trimmed.
func extractMultiValuesFromField(field string) []string {
	if strings.Contains(field, "|") {
		return splitTrim(field, "|")
	}
	if strings.Contains(field, ",") {
		return splitTrim(field, ",")
	}
	return []string{field}
}

func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// ExtractCategory mirrors upstream ExtractCategory: TV when the row title
// link text (or the fallback title) carries season info, else Movies.
// rowTitleText is the visible text of the row's "div.title > a" element.
func ExtractCategory(rowTitleText, title string) []int {
	category := models.CatMovies.ID
	if text, ok := extractTitleForCategory(rowTitleText); ok {
		category = categoryIDFromText(text)
	}
	if category != models.CatTV.ID && strings.TrimSpace(title) != "" {
		category = categoryIDFromText(title)
	}
	return []int{category}
}

func extractTitleForCategory(rowTitleText string) (string, bool) {
	if strings.TrimSpace(rowTitleText) == "" {
		return "", false
	}
	return rowTitleText, true
}

var seasonInfoRe = regexp.MustCompile(`(?i)\bS\d{1,2}(?:E\d{1,2})?\b`)

// categoryIDFromText mirrors upstream ExtractCategory(string): temporada /
// season / Sxx(Eyy) → TV, else Movies.
func categoryIDFromText(text string) int {
	if strings.Contains(strings.ToLower(text), "temporada") ||
		strings.Contains(strings.ToLower(text), "season") ||
		seasonInfoRe.MatchString(text) {
		return models.CatTV.ID
	}
	return models.CatMovies.ID
}

// ExtractReleaseDate mirrors upstream ExtractReleaseDate: "Lançamento:" year
// (yyyy), defaulting to today when absent/unparseable.
func ExtractReleaseDate(rowHTML string) time.Time {
	result := time.Now()
	if value, ok := extractByLabel(rowHTML, "Lançamento:"); ok {
		if t, err := time.ParseInLocation("2006", strings.TrimSpace(value), time.Local); err == nil {
			result = t
		}
	}
	return result
}

// ExtractSize mirrors upstream ExtractSize ("Tamanho:" label).
func ExtractSize(rowHTML string) int64 {
	var result int64
	if value, ok := extractByLabel(rowHTML, "Tamanho:"); ok {
		result = GetBytes(value)
	}
	return result
}

// sizeUnitWordRe finds standalone size units (upstream `\b[GTKP]?B\b`).
var sizeUnitWordRe = regexp.MustCompile(`(?i)\b[GTKP]?B\b`)

// sizeRangeRe mirrors the upstream RightToLeft match: a unit followed by a
// run of digits/separators and another unit.
var sizeRangeRe = regexp.MustCompile(`(?i)[GTKP]?B([.,| \d]+[GTKP]?B)`)

// GetBytes mirrors upstream RowParsingExtensions.GetBytes: when the text
// mentions more than one size unit, keep the last "X…B" range, then
// ParseUtil.GetBytes.
func GetBytes(text string) int64 {
	if len(sizeUnitWordRe.FindAllString(text, -1)) > 1 {
		matches := sizeRangeRe.FindAllStringSubmatch(text, -1)
		if len(matches) > 0 {
			text = matches[len(matches)-1][1]
		}
	}
	return parseutil.GetBytes(text)
}

// --- PublicBrazilianParser --------------------------------------------------

var dnRe = regexp.MustCompile(`&dn=(.+?)&|&dn=(.+?)$`)
var resolutionRe = regexp.MustCompile(`\b(\d{3,4}p)\b`)

// ExtractTitleOrDefault ports upstream PublicBrazilianParser
// .ExtractTitleOrDefault.
//
// 上游 bug 复刻: upstream reads only regex group 1, so a &dn= parameter that
// is last in the magnet link (group 2 match) is discarded upstream too.
func (b *Brazilian) ExtractTitleOrDefault(downloadButtonHTML, defaultTitle string) string {
	magnetTitle := ""
	if m := dnRe.FindStringSubmatch(hrefValue(downloadButtonHTML)); m != nil {
		if decoded, err := url.QueryUnescape(m[1]); err == nil {
			magnetTitle = decoded
		}
	}
	if strings.TrimSpace(magnetTitle) != "" {
		return formatTitle(CleanTitle(magnetTitle), extractResolution(magnetTitle))
	}

	description := ""
	if b.GetTitleElement != nil {
		description = b.GetTitleElement(downloadButtonHTML)
	}
	resolution := ""
	if strings.TrimSpace(description) != "" {
		resolution = extractResolution(description)
	} else {
		resolution = extractResolution(defaultTitle)
	}
	title := defaultTitle
	switch {
	case strings.TrimSpace(defaultTitle) != "":
		title = CleanTitle(defaultTitle)
	case strings.TrimSpace(description) != "":
		title = CleanTitle(description)
	}
	return formatTitle(title, resolution)
}

// hrefValue extracts the href attribute of the first anchor in the fragment
// (empty when absent), with HTML entities decoded (upstream reads the
// DOM attribute value, not the raw markup).
func hrefValue(fragment string) string {
	m := hrefRe.FindStringSubmatch(fragment)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}

func extractResolution(text string) string {
	resolution := ""
	if m := resolutionRe.FindStringSubmatch(text); m != nil {
		resolution = m[1]
	}
	return resolution
}

func formatTitle(title, resolution string) string {
	if strings.TrimSpace(resolution) == "" {
		return title
	}
	return title + " " + resolution
}

// ExtractSizeByResolution ports upstream ExtractSizeByResolution.
func ExtractSizeByResolution(title string) int64 {
	resolution := "Other"
	if m := resolutionRe.FindStringSubmatch(title); m != nil {
		resolution = m[1]
	}

	size := "512MB"
	switch resolution {
	case "720p":
		size = "1GB"
	case "1080p":
		size = "2.5GB"
	case "2160p":
		size = "5GB"
	}
	return parseutil.GetBytes(size)
}

var cleanTitleRes = []*regexp.Regexp{
	// Remove size info in parentheses
	regexp.MustCompile(`(?i)\(\d+(?:\.\d+)?\s*(?:GB|MB)\)`),
	// Remove quality info
	regexp.MustCompile(`(?i)\b(?:720p|1080p|2160p|4K)\b`),
	// Remove source info
	regexp.MustCompile(`(?i)\b(?:WEB-DL|BRRip|HDRip|WEBRip|BluRay|Torrent|Download)\b`),
	// Remove language info
	regexp.MustCompile(`(?i)\b(?:Legendado|Leg|Dublado|Dub|[AÁ]udio)\b`),
	// Clean up torrent group names
	regexp.MustCompile(`(?i)HIDRATORRENTS\.ORG|\[?Erai-raws\]?|\[?Anime Time\]?|COMANDO4K\.COM|COMANDO\.TO|VEMTORRENT\.COM|VACATORRENT\.COM`),
	// Remove brackets/parentheses content
	regexp.MustCompile(`(?i)\[(?:.*?)\]|\((?:.*?)\)`),
	// Remove dangling punctuation and separators
	regexp.MustCompile(`(?i)[\\/,|~_-]+\s*|\s*[\\/,|~_-]+`),
	// Clean up multiple spaces
	regexp.MustCompile(`(?i)\s+`),
	// Remove file extension from the beginning of title
	regexp.MustCompile(`(?i)MKV|MP4`),
	// (the final step, dots between words, is RE2-incompatible and runs via
	// replaceDotsBetweenWords instead)
}

// cleanTitleTrimCuts mirrors the upstream final Trim(' ', '.', ',', '-',
// '_', '~', '/', '\\', '|') + Trim().
var cleanTitleTrimCuts = " .,-_~/\\|"

// CleanTitle ports upstream PublicBrazilianParser.CleanTitle.
//
// 注意: RE2 不支持后行断言；`(?<!\d)\.(?!\d)` 以手工扫描实现相同语义
// (仅当点号两侧都不是数字时替换为空格)。
func CleanTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return ""
	}

	// Remove size info in parentheses
	title = cleanTitleRes[0].ReplaceAllString(title, "")
	// Remove quality info
	title = cleanTitleRes[1].ReplaceAllString(title, "")
	// Remove source info
	title = cleanTitleRes[2].ReplaceAllString(title, "")
	// Remove language info
	title = cleanTitleRes[3].ReplaceAllString(title, "")
	// Clean up torrent group names
	title = cleanTitleRes[4].ReplaceAllString(title, "")
	// Remove brackets/parentheses content
	title = cleanTitleRes[5].ReplaceAllString(title, "")
	// Remove dangling punctuation and separators
	title = cleanTitleRes[6].ReplaceAllString(title, " ")
	// Clean up multiple spaces
	title = cleanTitleRes[7].ReplaceAllString(title, " ")
	// Remove file extension from the beginning of title
	title = cleanTitleRes[8].ReplaceAllString(title, "")
	// Remove dots between words but keep dots in version numbers
	title = replaceDotsBetweenWords(title)
	// Remove any remaining punctuation at start/end
	return strings.TrimSpace(strings.Trim(title, cleanTitleTrimCuts))
}

// replaceDotsBetweenWords mirrors .NET `(?<!\d)\.(?!\d)` (a dot with a
// non-digit on both sides becomes a space).
func replaceDotsBetweenWords(title string) string {
	runes := []rune(title)
	var b strings.Builder
	for i, r := range runes {
		if r != '.' {
			b.WriteRune(r)
			continue
		}
		beforeDigit := i > 0 && runes[i-1] >= '0' && runes[i-1] <= '9'
		afterDigit := i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9'
		if beforeDigit || afterDigit {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return b.String()
}
