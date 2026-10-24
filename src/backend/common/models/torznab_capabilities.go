package models

import (
	"strings"
)

// Search parameter names (upstream TvSearchParam etc. enums, lower-case
// wire format).
//
// Ported from: Jackett.Common/Models/TorznabCapabilities.cs.
const (
	TVParamQ        = "q"
	TVParamSeason   = "season"
	TVParamEp       = "ep"
	TVParamImdbID   = "imdbid"
	TVParamTvdbID   = "tvdbid"
	TVParamRID      = "rid"
	TVParamTmdbID   = "tmdbid"
	TVParamTvmazeID = "tvmazeid"
	TVParamTraktID  = "traktid"
	TVParamDoubanID = "doubanid"
	TVParamYear     = "year"
	TVParamGenre    = "genre"

	MovieParamQ        = "q"
	MovieParamImdbID   = "imdbid"
	MovieParamTmdbID   = "tmdbid"
	MovieParamTraktID  = "traktid"
	MovieParamDoubanID = "doubanid"
	MovieParamYear     = "year"
	MovieParamGenre    = "genre"

	MusicParamQ      = "q"
	MusicParamAlbum  = "album"
	MusicParamArtist = "artist"
	MusicParamLabel  = "label"
	MusicParamTrack  = "track"
	MusicParamYear   = "year"
	MusicParamGenre  = "genre"

	BookParamQ         = "q"
	BookParamTitle     = "title"
	BookParamAuthor    = "author"
	BookParamPublisher = "publisher"
	BookParamYear      = "year"
	BookParamGenre     = "genre"
)

// CapabilitiesCategories holds the category tree and tracker mappings.
//
// Ported from: Jackett.Common/Models/TorznabCapabilitiesCategories.cs.
type CapabilitiesCategories struct {
	tree []*Category
	// mappings mirrors upstream _categoryMapping (ordered, duplicates
	// allowed) — see category_mapping.go.
	mappings []CategoryMapping
}

// NewCapabilitiesCategories returns an empty category container.
func NewCapabilitiesCategories() *CapabilitiesCategories {
	return &CapabilitiesCategories{}
}

// Tree returns the (unsorted) category tree.
func (c *CapabilitiesCategories) Tree() []*Category { return c.tree }

// AddTreeCategory appends a top-level category (used by indexers/engine).
func (c *CapabilitiesCategories) AddTreeCategory(cat *Category) { c.tree = append(c.tree, cat) }

func copyWithoutSubs(cat *Category) *Category {
	return &Category{ID: cat.ID, Name: cat.Name}
}

// AddCategoryTree mirrors upstream AddTorznabCategoryTree: parents are added
// bare; children attach under their parent node (creating it if needed);
// anything else becomes a custom top-level entry.
func (c *CapabilitiesCategories) AddCategoryTree(cat *Category) {
	for _, p := range ParentCats {
		if p.ID == cat.ID {
			// parent cat
			for _, n := range c.tree {
				if n.ID == cat.ID {
					return
				}
			}
			c.tree = append(c.tree, copyWithoutSubs(cat))
			return
		}
	}
	// child or custom cat
	var parentCat *Category
	for _, p := range ParentCats {
		if p.Contains(cat) {
			parentCat = p
			break
		}
	}
	if parentCat == nil {
		c.tree = append(c.tree, cat)
		return
	}
	for _, n := range c.tree {
		if n.ID == parentCat.ID {
			if !n.Contains(cat) {
				n.SubCategories = append(n.SubCategories, cat)
			}
			return
		}
	}
	node := copyWithoutSubs(parentCat)
	node.SubCategories = []*Category{cat}
	c.tree = append(c.tree, node)
}

// SupportedCategories mirrors upstream: exact-ID matches against parents
// and their direct sub-categories.
func (c *CapabilitiesCategories) SupportedCategories(categories []int) []int {
	if len(categories) == 0 {
		return []int{}
	}
	var out []int
	for _, node := range c.tree {
		all := append([]*Category{node}, node.SubCategories...)
		for _, cat := range all {
			for _, id := range categories {
				if cat.ID == id {
					out = append(out, id)
				}
			}
		}
	}
	return out
}

// ExpandTorznabQueryCategories mirrors upstream: parent categories expand to
// include their children; optionally children map back to their parent.
func (c *CapabilitiesCategories) ExpandTorznabQueryCategories(q *TorznabQuery, mapChildrenCatsToParent bool) []int {
	var out []int
	seen := map[int]bool{}
	add := func(id int) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, queryCategory := range q.Categories {
		add(queryCategory)
		if queryCategory >= 100000 {
			continue
		}
		var parentCat *Category
		for _, n := range c.tree {
			if n.ID == queryCategory {
				parentCat = n
				break
			}
		}
		if parentCat != nil {
			for _, sc := range parentCat.SubCategories {
				add(sc.ID)
			}
		} else if mapChildrenCatsToParent {
			queryCat := &Category{ID: queryCategory}
			for _, n := range c.tree {
				if n.Contains(queryCat) {
					add(n.ID)
					break
				}
			}
		}
	}
	return out
}

// SortedTree mirrors GetTorznabCategoryTree(sorted=true): parents sorted by
// id (custom cats ≥100000 sort last by name), sub-categories sorted by id.
func (c *CapabilitiesCategories) SortedTree() []*Category {
	out := make([]*Category, 0, len(c.tree))
	for _, cat := range c.tree {
		subs := make([]*Category, len(cat.SubCategories))
		copy(subs, cat.SubCategories)
		sortCatsByID(subs)
		out = append(out, &Category{ID: cat.ID, Name: cat.Name, SubCategories: subs})
	}
	// stable sort: key = id for regular cats, "zzz"+name for custom (>=100000)
	sortCatTree(out)
	return out
}

// FlatList mirrors GetTorznabCategoryList: parents then sub-cats, without
// nested sub-categories.
func (c *CapabilitiesCategories) FlatList(sorted bool) []*Category {
	tree := c.tree
	if sorted {
		tree = c.SortedTree()
	}
	var out []*Category
	for _, cat := range tree {
		out = append(out, &Category{ID: cat.ID, Name: cat.Name})
		out = append(out, cat.SubCategories...)
	}
	return out
}

func sortCatsByID(cats []*Category) {
	for i := 1; i < len(cats); i++ {
		for j := i; j > 0 && cats[j-1].ID > cats[j].ID; j-- {
			cats[j-1], cats[j] = cats[j], cats[j-1]
		}
	}
}

func sortCatTree(cats []*Category) {
	for i := 1; i < len(cats); i++ {
		for j := i; j > 0 && catSortKey(cats[j-1]) > catSortKey(cats[j]); j-- {
			cats[j-1], cats[j] = cats[j], cats[j-1]
		}
	}
}

func catSortKey(c *Category) string {
	if c.ID >= 100000 {
		return "zzz" + c.Name
	}
	return pad10(c.ID)
}

func pad10(n int) string {
	s := itoa(n)
	for len(s) < 10 {
		s = "0" + s
	}
	return s
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

// Capabilities is the indexer capability model (upstream TorznabCapabilities).
type Capabilities struct {
	LimitsMax     *int
	LimitsDefault *int

	SearchAvailable   bool
	SupportsRawSearch bool

	TVSearchParams    []string
	MovieSearchParams []string
	MusicSearchParams []string
	BookSearchParams  []string

	// TVSearchImdbAvailable mirrors the upstream property (default false,
	// disabled due to upstream issue #8107).
	TVSearchImdbAvailable bool

	Categories *CapabilitiesCategories
}

// NewCapabilities returns defaults: search on, limits 100/100, empty param
// lists, fresh categories (upstream constructor).
func NewCapabilities() *Capabilities {
	def, max := 100, 100
	return &Capabilities{
		LimitsMax:         &max,
		LimitsDefault:     &def,
		SearchAvailable:   true,
		Categories:        NewCapabilitiesCategories(),
		TVSearchParams:    []string{},
		MovieSearchParams: []string{},
		MusicSearchParams: []string{},
		BookSearchParams:  []string{},
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func (c *Capabilities) tvSearchAvailable() bool    { return len(c.TVSearchParams) > 0 }
func (c *Capabilities) movieSearchAvailable() bool { return len(c.MovieSearchParams) > 0 }
func (c *Capabilities) musicSearchAvailable() bool { return len(c.MusicSearchParams) > 0 }
func (c *Capabilities) bookSearchAvailable() bool  { return len(c.BookSearchParams) > 0 }

// SupportedTvSearchParams mirrors the upstream private method (q always
// first; imdbid gated by the dedicated property).
func (c *Capabilities) SupportedTvSearchParams() string {
	params := []string{"q"}
	if contains(c.TVSearchParams, TVParamSeason) {
		params = append(params, "season")
	}
	if contains(c.TVSearchParams, TVParamEp) {
		params = append(params, "ep")
	}
	if c.TVSearchImdbAvailable {
		params = append(params, "imdbid")
	}
	if contains(c.TVSearchParams, TVParamTvdbID) {
		params = append(params, "tvdbid")
	}
	if contains(c.TVSearchParams, TVParamRID) {
		params = append(params, "rid")
	}
	if contains(c.TVSearchParams, TVParamTmdbID) {
		params = append(params, "tmdbid")
	}
	if contains(c.TVSearchParams, TVParamTvmazeID) {
		params = append(params, "tvmazeid")
	}
	if contains(c.TVSearchParams, TVParamTraktID) {
		params = append(params, "traktid")
	}
	if contains(c.TVSearchParams, TVParamDoubanID) {
		params = append(params, "doubanid")
	}
	if contains(c.TVSearchParams, TVParamYear) {
		params = append(params, "year")
	}
	if contains(c.TVSearchParams, TVParamGenre) {
		params = append(params, "genre")
	}
	return strings.Join(params, ",")
}

func (c *Capabilities) supportedMovieSearchParams() string {
	params := []string{"q"}
	for _, p := range []string{MovieParamImdbID, MovieParamTmdbID, MovieParamTraktID, MovieParamDoubanID, MovieParamYear, MovieParamGenre} {
		if contains(c.MovieSearchParams, p) {
			params = append(params, p)
		}
	}
	return strings.Join(params, ",")
}

func (c *Capabilities) supportedMusicSearchParams() string {
	params := []string{"q"}
	for _, p := range []string{MusicParamAlbum, MusicParamArtist, MusicParamLabel, MusicParamTrack, MusicParamYear, MusicParamGenre} {
		if contains(c.MusicSearchParams, p) {
			params = append(params, p)
		}
	}
	return strings.Join(params, ",")
}

func (c *Capabilities) supportedBookSearchParams() string {
	params := []string{"q"}
	for _, p := range []string{BookParamTitle, BookParamAuthor, BookParamPublisher, BookParamYear, BookParamGenre} {
		if contains(c.BookSearchParams, p) {
			params = append(params, p)
		}
	}
	return strings.Join(params, ",")
}

// yesNo renders upstream bool → "yes"/"no".
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// XML renders the <caps> document. Structure and attribute order follow
// upstream GetXDocument: server → limits → searching → categories.
func (c *Capabilities) XML() string {
	var b strings.Builder
	b.WriteString(xmlHeader)
	b.WriteString("<caps>")
	b.WriteString(`<server title="Vestt" />`)
	if c.LimitsMax != nil || c.LimitsDefault != nil {
		b.WriteString("<limits")
		if c.LimitsDefault != nil {
			b.WriteString(` default="`)
			b.WriteString(itoa(*c.LimitsDefault))
			b.WriteString(`"`)
		}
		if c.LimitsMax != nil {
			b.WriteString(` max="`)
			b.WriteString(itoa(*c.LimitsMax))
			b.WriteString(`"`)
		}
		b.WriteString(" />")
	}
	b.WriteString("<searching>")
	c.writeSearchMode(&b, "search", c.SearchAvailable, "q")
	c.writeSearchMode(&b, "tv-search", c.tvSearchAvailable(), c.SupportedTvSearchParams())
	c.writeSearchMode(&b, "movie-search", c.movieSearchAvailable(), c.supportedMovieSearchParams())
	c.writeSearchMode(&b, "music-search", c.musicSearchAvailable(), c.supportedMusicSearchParams())
	c.writeSearchMode(&b, "audio-search", c.musicSearchAvailable(), c.supportedMusicSearchParams())
	c.writeSearchMode(&b, "book-search", c.bookSearchAvailable(), c.supportedBookSearchParams())
	b.WriteString("</searching>")
	sortedTree := c.Categories.SortedTree()
	if len(sortedTree) == 0 {
		// upstream XElement renders childless elements self-closed
		b.WriteString("<categories />")
	} else {
		b.WriteString("<categories>")
	}
	for _, cat := range sortedTree {
		b.WriteString(`<category id="`)
		b.WriteString(itoa(cat.ID))
		b.WriteString(`" name="`)
		writeAttr(&b, cat.Name)
		b.WriteString(`">`)
		for _, sc := range cat.SubCategories {
			b.WriteString(`<subcat id="`)
			b.WriteString(itoa(sc.ID))
			b.WriteString(`" name="`)
			writeAttr(&b, sc.Name)
			b.WriteString(`" />`)
		}
		b.WriteString("</category>")
	}
	if len(sortedTree) > 0 {
		b.WriteString("</categories>")
	}
	b.WriteString("</caps>")
	return b.String()
}

func (c *Capabilities) writeSearchMode(b *strings.Builder, mode string, available bool, params string) {
	b.WriteString("<")
	b.WriteString(mode)
	b.WriteString(` available="`)
	b.WriteString(yesNo(available))
	b.WriteString(`" supportedParams="`)
	b.WriteString(params)
	b.WriteString(`"`)
	if c.SupportsRawSearch {
		b.WriteString(` searchEngine="raw"`)
	}
	b.WriteString(" />")
}

// SupportsCategory reports whether id exists in the category tree.
func (c *Capabilities) SupportsCategory(id int) bool {
	for _, cat := range c.Categories.FlatList(false) {
		if cat.ID == id {
			return true
		}
	}
	return false
}

// SupportsParam checks a search-mode parameter (queryType: search|tvsearch|
// movie|music|book; param lower-case wire name).
func (c *Capabilities) SupportsParam(queryType, param string) bool {
	switch queryType {
	case "search":
		return param == "q"
	case "tvsearch":
		if param == "imdbid" {
			return c.TVSearchImdbAvailable
		}
		return contains(c.TVSearchParams, param)
	case "movie":
		return contains(c.MovieSearchParams, param)
	case "music":
		return contains(c.MusicSearchParams, param)
	case "book":
		return contains(c.BookSearchParams, param)
	}
	return false
}

// CanHandleQuery mirrors upstream BaseIndexer.CanHandleQuery: the
// query-type capability checks only (no category gate).
func (c *Capabilities) CanHandleQuery(q *TorznabQuery) (bool, string) {
	if q == nil {
		return false, "Query is null"
	}
	if q.QueryType == "caps" {
		return true, ""
	}

	if c.TVSearchImdbAvailable && q.IsImdbQuery() && q.IsTVSearch() {
		return true, ""
	}
	if contains(c.MovieSearchParams, MovieParamImdbID) && q.IsImdbQuery() && q.IsMovieSearch() {
		return true, ""
	}
	if q.IsMovieSearch() && !contains(c.MovieSearchParams, MovieParamImdbID) && q.IsImdbQuery() && q.QueryType != "TorrentPotato" {
		return false, "Imdb search is not available"
	}
	if c.SearchAvailable && q.IsSearch() {
		return true, ""
	}
	if c.tvSearchAvailable() && q.IsTVSearch() {
		return true, ""
	}
	if c.movieSearchAvailable() && q.IsMovieSearch() {
		return true, ""
	}
	if c.musicSearchAvailable() && q.IsMusicSearch() {
		return true, ""
	}
	if c.bookSearchAvailable() && q.IsBookSearch() {
		return true, ""
	}
	if contains(c.TVSearchParams, TVParamRID) && q.IsTVRageQuery() {
		return true, ""
	}
	if contains(c.TVSearchParams, TVParamTvdbID) && q.IsTvdbQuery() {
		return true, ""
	}
	if contains(c.MovieSearchParams, MovieParamImdbID) && q.IsImdbQuery() {
		return true, ""
	}
	if contains(c.MovieSearchParams, MovieParamTmdbID) && q.IsTmdbQuery() && q.IsMovieSearch() {
		return true, ""
	}
	if contains(c.TVSearchParams, TVParamTmdbID) && q.IsTmdbQuery() && q.IsTVSearch() {
		return true, ""
	}

	return false, "Query is not supported"
}

// SupportsQuery = CanHandleQuery plus the category gate from upstream
// CanHandleCategories (only the all-unsupported case rejects).
func (c *Capabilities) SupportsQuery(q *TorznabQuery, isMetaIndexer bool) (bool, string) {
	ok, reason := c.CanHandleQuery(q)
	if !ok {
		return ok, reason
	}
	if q.HasSpecifiedCategories() {
		supported := c.Categories.SupportedCategories(q.Categories)
		if len(supported) == 0 {
			return false, "All categories provided are unsupported: " + joinInts(q.Categories)
		}
	}
	return true, ""
}

func joinInts(ints []int) string {
	var b strings.Builder
	for i, n := range ints {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(itoa(n))
	}
	return b.String()
}

// Concat merges two capabilities (upstream Concat).
func Concat(lhs, rhs *Capabilities) *Capabilities {
	lhs.SearchAvailable = lhs.SearchAvailable || rhs.SearchAvailable
	lhs.TVSearchParams = unionStrings(lhs.TVSearchParams, rhs.TVSearchParams)
	lhs.MovieSearchParams = unionStrings(lhs.MovieSearchParams, rhs.MovieSearchParams)
	lhs.MusicSearchParams = unionStrings(lhs.MusicSearchParams, rhs.MusicSearchParams)
	lhs.BookSearchParams = unionStrings(lhs.BookSearchParams, rhs.BookSearchParams)
	lhs.SupportsRawSearch = lhs.SupportsRawSearch || rhs.SupportsRawSearch
	// Categories.Concat: tree entries of rhs appended when not present by ID.
	for _, rc := range rhs.Categories.Tree() {
		found := false
		for _, lc := range lhs.Categories.Tree() {
			if lc.ID == rc.ID {
				found = true
				break
			}
		}
		if !found {
			lhs.Categories.AddTreeCategory(rc)
		}
	}
	return lhs
}

func unionStrings(a, b []string) []string {
	out := make([]string, len(a))
	copy(out, a)
	for _, s := range b {
		if !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
