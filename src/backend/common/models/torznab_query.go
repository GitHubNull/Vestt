package models

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// TorznabQuery is the parsed Torznab request (upstream TorznabQuery.cs).
//
// Ported from: Jackett.Common/Models/TorznabQuery.cs and
// Models/DTO/TorznabRequest.cs (ToTorznabQuery).
type TorznabQuery struct {
	InteractiveSearch bool
	QueryType         string
	Categories        []int
	Extended          int
	ApiKey            string
	Limit             int
	Offset            int
	RageID            *int
	TvdbID            *int
	ImdbID            string
	TmdbID            *int
	TvmazeID          *int
	TraktID           *int
	DoubanID          *int

	// Cache defaults to true (upstream property initializer).
	Cache bool

	Season     *int
	Episode    string
	SearchTerm string

	Album  string
	Artist string
	Label  string
	Track  string
	Year   *int
	Genre  string

	Author    string
	Title     string
	Publisher string

	IsTest bool
}

// NewTorznabQuery returns a query with upstream constructor defaults.
func NewTorznabQuery() *TorznabQuery {
	return &TorznabQuery{Categories: []int{}, Cache: true}
}

var (
	standardizeDashesRegex       = regexp.MustCompile(`\p{Pd}+`)
	standardizeSingleQuotesRegex = regexp.MustCompile("[\u0060\u00B4\u2018\u2019]")
)

func isNotNullOrWhiteSpace(s string) bool { return strings.TrimSpace(s) != "" }

// ImdbIDShort is the numeric part of the imdb id (upstream ImdbIDShort).
func (q *TorznabQuery) ImdbIDShort() string { return strings.TrimLeft(q.ImdbID, "t") }

func (q *TorznabQuery) IsSearch() bool   { return q.QueryType == "search" }
func (q *TorznabQuery) IsTVSearch() bool { return q.QueryType == "tvsearch" }

// IsMovieSearch mirrors upstream (TorrentPotato with a term counts as movie).
func (q *TorznabQuery) IsMovieSearch() bool {
	return q.QueryType == "movie" || (q.QueryType == "TorrentPotato" && isNotNullOrWhiteSpace(q.SearchTerm))
}
func (q *TorznabQuery) IsMusicSearch() bool { return q.QueryType == "music" }
func (q *TorznabQuery) IsBookSearch() bool  { return q.QueryType == "book" }

func (q *TorznabQuery) IsTVRageQuery() bool { return q.RageID != nil }
func (q *TorznabQuery) IsTvdbQuery() bool   { return q.TvdbID != nil }
func (q *TorznabQuery) IsImdbQuery() bool   { return q.ImdbID != "" }
func (q *TorznabQuery) IsTmdbQuery() bool   { return q.TmdbID != nil }
func (q *TorznabQuery) IsTvmazeQuery() bool { return q.TvmazeID != nil }
func (q *TorznabQuery) IsTraktQuery() bool  { return q.TraktID != nil }
func (q *TorznabQuery) IsDoubanQuery() bool { return q.DoubanID != nil }
func (q *TorznabQuery) IsGenreQuery() bool  { return q.Genre != "" }

// IsIdSearch mirrors upstream (Episode/Season/any ID/any music-book param).
func (q *TorznabQuery) IsIdSearch() bool {
	return isNotNullOrWhiteSpace(q.Episode) ||
		(q.Season != nil && *q.Season > 0) ||
		q.IsImdbQuery() || q.IsTvdbQuery() || q.IsTVRageQuery() || q.IsTraktQuery() ||
		q.IsTvmazeQuery() || q.IsTmdbQuery() || q.IsDoubanQuery() ||
		isNotNullOrWhiteSpace(q.Album) || isNotNullOrWhiteSpace(q.Artist) ||
		isNotNullOrWhiteSpace(q.Label) || isNotNullOrWhiteSpace(q.Genre) ||
		isNotNullOrWhiteSpace(q.Track) || isNotNullOrWhiteSpace(q.Author) ||
		isNotNullOrWhiteSpace(q.Title) || isNotNullOrWhiteSpace(q.Publisher) ||
		q.Year != nil
}

// IsRssSearch mirrors upstream: no search term and no IDs.
func (q *TorznabQuery) IsRssSearch() bool {
	return !isNotNullOrWhiteSpace(q.SearchTerm) && !q.IsIdSearch()
}

// HasSpecifiedCategories mirrors upstream Categories is { Length: > 0 }.
func (q *TorznabQuery) HasSpecifiedCategories() bool { return len(q.Categories) > 0 }

// SanitizedSearchTerm mirrors the upstream property (dash/quote
// standardisation, then a restrictive allow-list of characters).
func (q *TorznabQuery) SanitizedSearchTerm() string {
	term := q.SearchTerm
	term = standardizeDashesRegex.ReplaceAllString(term, "-")
	term = standardizeSingleQuotesRegex.ReplaceAllString(term, "'")

	var b strings.Builder
	for _, c := range term {
		if c < 128 && (isASCIILetterDigit(byte(c)) ||
			c == ' ' || c == '\t' || c == '\n' || c == '\r' ||
			c == '-' || c == '.' || c == '_' || c == '(' || c == ')' ||
			c == '@' || c == '/' || c == '\'' || c == '[' || c == ']' ||
			c == '+' || c == '%') {
			b.WriteRune(c)
		} else if c >= 128 && (isUnicodeLetterDigit(c) || isUnicodeSpace(c)) {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func isASCIILetterDigit(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func isUnicodeLetterDigit(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isUnicodeSpace(r rune) bool {
	return unicode.IsSpace(r)
}

// GetQueryString is SanitizedSearchTerm + the episode string (upstream).
func (q *TorznabQuery) GetQueryString() string {
	return strings.TrimSpace(q.SanitizedSearchTerm() + " " + q.GetEpisodeSearchString())
}

// GetEpisodeSearchString mirrors upstream: airdate style (yyyy MM/dd),
// Sxx, or SxxEyy (numeric episode coerced, fallback to raw).
func (q *TorznabQuery) GetEpisodeSearchString() string {
	if q.Season == nil || *q.Season == 0 {
		return ""
	}
	season := *q.Season
	if isAirDateFormat(season, q.Episode) {
		month, day := parseAirDateParts(q.Episode)
		return pad4(season) + "." + pad2(month) + "." + pad2(day)
	}
	if !isNotNullOrWhiteSpace(q.Episode) {
		return "S" + pad2(season)
	}
	if ep, err := strconv.Atoi(q.Episode); err == nil {
		return "S" + pad2(season) + "E" + pad2(ep)
	}
	return "S" + pad2(season) + "E" + q.Episode
}

// isAirDateFormat replicates DateTime.TryParseExact($"{Season} {Episode}",
// "yyyy MM/dd"): the episode must look like "MM/dd".
func isAirDateFormat(season int, episode string) bool {
	parts := strings.Split(episode, "/")
	if len(parts) != 2 {
		return false
	}
	m, err1 := strconv.Atoi(parts[0])
	d, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return season >= 0 && m >= 1 && m <= 12 && d >= 1 && d <= 31
}

func parseAirDateParts(episode string) (month, day int) {
	parts := strings.Split(episode, "/")
	m, _ := strconv.Atoi(parts[0])
	d, _ := strconv.Atoi(parts[1])
	return m, d
}

func pad2(n int) string {
	s := strconv.Itoa(n)
	if len(s) < 2 {
		return "0" + s
	}
	return s
}

func pad4(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// Clone deep-copies the query (upstream Clone).
func (q *TorznabQuery) Clone() *TorznabQuery {
	ret := *q
	if q.Categories != nil {
		ret.Categories = make([]int, len(q.Categories))
		copy(ret.Categories, q.Categories)
	}
	return &ret
}

// CreateFallback mirrors upstream: default movie categories when none are
// specified, then replaces the search term.
func (q *TorznabQuery) CreateFallback(search string) *TorznabQuery {
	ret := q.Clone()
	if len(ret.Categories) == 0 {
		ret.Categories = []int{
			CatMovies.ID, CatMoviesForeign.ID, CatMoviesOther.ID, CatMoviesSD.ID,
			CatMoviesHD.ID, CatMovies3D.ID, CatMoviesBluRay.ID, CatMoviesDVD.ID,
			CatMoviesWEBDL.ID, CatMoviesUHD.ID,
		}
	}
	ret.SearchTerm = search
	return ret
}

// MatchQueryStringAND mirrors upstream AND-filtering of a title against the
// query string (common words and single characters excluded).
func (q *TorznabQuery) MatchQueryStringAND(title string, limit int, queryStringOverride string) bool {
	commonWords := map[string]bool{"and": true, "the": true, "an": true}

	queryString := q.GetQueryString()
	if isNotNullOrWhiteSpace(queryStringOverride) {
		queryString = queryStringOverride
	}
	if limit > 0 {
		if limit > len(queryString) {
			limit = len(queryString)
		}
		queryString = queryString[:limit]
	}

	splitRegex := regexp.MustCompile(`[^\w]+`)
	parts := splitRegex.Split(queryString, -1)
	var filtered []string
	for _, p := range parts {
		lp := strings.ToLower(p)
		if p == "" || len(p) <= 1 || commonWords[lp] {
			continue
		}
		filtered = append(filtered, p)
	}

	lt := strings.ToLower(title)
	for _, p := range filtered {
		if !strings.Contains(lt, strings.ToLower(p)) {
			return false
		}
	}
	return true
}

// ParseTorznabQuery parses query parameters into a TorznabQuery, following
// upstream ToTorznabQuery (int parse failures are tolerated: the entry is
// skipped, SPEC-03 §3.1.1).
func ParseTorznabQuery(v url.Values) *TorznabQuery {
	q := NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = v.Get("q")
	q.ImdbID = v.Get("imdbid")
	q.Episode = v.Get("ep")
	if t := v.Get("t"); t != "" {
		q.QueryType = t
	}
	if s := v.Get("extended"); isNotNullOrWhiteSpace(s) {
		q.Extended = coerceInt(s, 0)
	}
	if s := v.Get("limit"); isNotNullOrWhiteSpace(s) {
		q.Limit = coerceInt(s, 0)
	}
	if s := v.Get("offset"); isNotNullOrWhiteSpace(s) {
		q.Offset = coerceInt(s, 0)
	}
	if b, ok := parseBool(v.Get("cache")); ok {
		q.Cache = b
	}

	if cat := v.Get("cat"); cat != "" {
		q.Categories = ParseTorznabCategories(cat)
	} else {
		if q.QueryType == "movie" && isNotNullOrWhiteSpace(q.ImdbID) {
			q.Categories = []int{CatMovies.ID}
		} else {
			q.Categories = []int{}
		}
	}

	if s := v.Get("season"); isNotNullOrWhiteSpace(s) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			q.Season = &n
		}
	}
	if s := v.Get("rid"); isNotNullOrWhiteSpace(s) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			q.RageID = &n
		}
	}
	if s := v.Get("tvdbid"); isNotNullOrWhiteSpace(s) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			q.TvdbID = &n
		}
	}
	if s := v.Get("tvmazeid"); isNotNullOrWhiteSpace(s) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			q.TvmazeID = &n
		}
	}
	if s := v.Get("tmdbid"); isNotNullOrWhiteSpace(s) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			q.TmdbID = &n
		}
	}
	if s := v.Get("traktid"); isNotNullOrWhiteSpace(s) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			q.TraktID = &n
		}
	}
	if s := v.Get("doubanid"); isNotNullOrWhiteSpace(s) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			q.DoubanID = &n
		}
	}
	if s := v.Get("album"); isNotNullOrWhiteSpace(s) {
		q.Album = s
	}
	if s := v.Get("artist"); isNotNullOrWhiteSpace(s) {
		q.Artist = s
	}
	if s := v.Get("label"); isNotNullOrWhiteSpace(s) {
		q.Label = s
	}
	if s := v.Get("track"); isNotNullOrWhiteSpace(s) {
		q.Track = s
	}
	if s := v.Get("year"); isNotNullOrWhiteSpace(s) {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			q.Year = &n
		}
	}
	if s := v.Get("genre"); isNotNullOrWhiteSpace(s) {
		q.Genre = s
	}
	if s := v.Get("title"); isNotNullOrWhiteSpace(s) {
		q.Title = s
	}
	if s := v.Get("author"); isNotNullOrWhiteSpace(s) {
		q.Author = s
	}
	if s := v.Get("publisher"); isNotNullOrWhiteSpace(s) {
		// upstream bug preserved: publisher is assigned to Author
		q.Author = s
	}
	return q
}

// coerceInt mirrors ParseUtil.CoerceInt (invalid → fallback).
func coerceInt(s string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return fallback
}

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}
