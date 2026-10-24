package models

import (
	"strings"
	"testing"
)

func TestSupportsCategory(t *testing.T) {
	c := NewCapabilities()
	c.Categories.AddCategoryTree(CatMoviesHD)
	if !c.SupportsCategory(CatMoviesHD.ID) {
		t.Fatal("Movies/HD must be supported")
	}
	if !c.SupportsCategory(CatMovies.ID) {
		t.Fatal("parent Movies must be supported after adding child")
	}
	if c.SupportsCategory(CatTVSD.ID) {
		t.Fatal("TV/SD must not be supported")
	}
}

func TestSupportsParam(t *testing.T) {
	c := NewCapabilities()
	c.TVSearchParams = []string{TVParamSeason, TVParamEp, TVParamTvdbID}
	c.MovieSearchParams = []string{MovieParamImdbID, MovieParamTmdbID}
	c.MusicSearchParams = []string{MusicParamAlbum}
	c.BookSearchParams = []string{BookParamAuthor, BookParamPublisher}

	if !c.SupportsParam("search", "q") {
		t.Fatal("search must support q")
	}
	if c.SupportsParam("search", "imdbid") {
		t.Fatal("search must not support imdbid")
	}
	for _, p := range []string{TVParamSeason, TVParamEp, TVParamTvdbID} {
		if !c.SupportsParam("tvsearch", p) {
			t.Fatalf("tvsearch must support %s", p)
		}
	}
	if c.SupportsParam("tvsearch", TVParamImdbID) {
		t.Fatal("tv imdbid gated by TVSearchImdbAvailable (false)")
	}
	c.TVSearchImdbAvailable = true
	if !c.SupportsParam("tvsearch", TVParamImdbID) {
		t.Fatal("tv imdbid supported once TVSearchImdbAvailable=true")
	}
	if !c.SupportsParam("movie", MovieParamImdbID) || !c.SupportsParam("movie", MovieParamTmdbID) {
		t.Fatal("movie imdbid/tmdbid supported")
	}
	if c.SupportsParam("movie", MovieParamGenre) {
		t.Fatal("movie genre not declared")
	}
	if !c.SupportsParam("music", MusicParamAlbum) || c.SupportsParam("music", MusicParamTrack) {
		t.Fatal("music album yes, track no")
	}
	if !c.SupportsParam("book", BookParamAuthor) || !c.SupportsParam("book", BookParamPublisher) {
		t.Fatal("book author/publisher supported")
	}
	if c.SupportsParam("unknown", "q") {
		t.Fatal("unknown query type unsupported")
	}
}

func TestIsGenreQuery(t *testing.T) {
	q := &TorznabQuery{}
	if q.IsGenreQuery() {
		t.Fatal("empty genre is not a genre query")
	}
	q.Genre = "drama"
	if !q.IsGenreQuery() {
		t.Fatal("genre set")
	}
}

func TestSanitizedSearchTermUnicode(t *testing.T) {
	// CJK letters/digits and unicode spaces survive; symbols are dropped.
	q := &TorznabQuery{SearchTerm: "ハリーポッター\u00A02020\u3000电影＆"}
	got := q.SanitizedSearchTerm()
	if !strings.Contains(got, "ハリーポッター") || !strings.Contains(got, "2020") || !strings.Contains(got, "电影") {
		t.Fatalf("unicode letters/digits must survive: %q", got)
	}
	if strings.Contains(got, "＆") {
		t.Fatalf("fullwidth ampersand (symbol) must be dropped: %q", got)
	}
	// U+3000/U+00A0 are unicode whitespace (upstream char.IsWhiteSpace keeps them).
	if !strings.Contains(got, "\u3000") {
		t.Fatalf("ideographic space is whitespace and must survive: %q", got)
	}
}

func TestXMLVariants(t *testing.T) {
	// no limits element when both are nil
	c := NewCapabilities()
	c.LimitsMax = nil
	c.LimitsDefault = nil
	xml := c.XML()
	if strings.Contains(xml, "<limits") {
		t.Fatalf("limits must be omitted: %s", xml)
	}

	// only default limit
	c = NewCapabilities()
	def := 50
	c.LimitsMax = nil
	c.LimitsDefault = &def
	xml = c.XML()
	if !strings.Contains(xml, `<limits default="50" />`) {
		t.Fatalf("default-only limits: %s", xml)
	}

	// raw search flag
	c = NewCapabilities()
	c.SupportsRawSearch = true
	c.TVSearchParams = []string{TVParamSeason}
	xml = c.XML()
	if !strings.Contains(xml, `<search available="yes" supportedParams="q" searchEngine="raw" />`) {
		t.Fatalf("searchEngine=raw: %s", xml)
	}
	if !strings.Contains(xml, `<tv-search available="yes" supportedParams="q,season" searchEngine="raw" />`) {
		t.Fatalf("tv-search raw: %s", xml)
	}

	// full tv param set exercises every SupportedTvSearchParams branch
	c = NewCapabilities()
	c.TVSearchParams = []string{
		TVParamSeason, TVParamEp, TVParamTvdbID, TVParamRID,
		TVParamTmdbID, TVParamTvmazeID, TVParamTraktID, TVParamDoubanID,
		TVParamYear, TVParamGenre,
	}
	c.TVSearchImdbAvailable = true
	xml = c.XML()
	want := `supportedParams="q,season,ep,imdbid,tvdbid,rid,tmdbid,tvmazeid,traktid,doubanid,year,genre"`
	if !strings.Contains(xml, want) {
		t.Fatalf("full tv params:\n got: %s\nwant substring: %s", xml, want)
	}

	// custom categories (>=100000) sort last, category name escaped
	c = NewCapabilities()
	c.Categories.AddCategoryTree(CatTVHD)
	c.Categories.AddCategoryTree(&Category{ID: 100001, Name: "Custom & \"Cats\""})
	xml = c.XML()
	ci := strings.Index(xml, `<category id="5000"`)
	xj := strings.Index(xml, `<category id="100001"`)
	if ci < 0 || xj < 0 || ci > xj {
		t.Fatalf("custom cat must sort after regular parents: %s", xml)
	}
	if !strings.Contains(xml, `name="Custom &amp; &quot;Cats&quot;"`) {
		t.Fatalf("category name must be attr-escaped: %s", xml)
	}
}

func TestExpandTorznabQueryCategoriesMapChildren(t *testing.T) {
	c := NewCapabilities()
	c.Categories.AddCategoryTree(CatMoviesHD) // attaches under Movies parent
	q := &TorznabQuery{Categories: []int{CatMoviesHD.ID}}
	out := c.Categories.ExpandTorznabQueryCategories(q, true)
	if len(out) != 2 || out[0] != CatMoviesHD.ID || out[1] != CatMovies.ID {
		t.Fatalf("child must map back to parent: %v", out)
	}
}

func TestWriteAttrAndWriteText(t *testing.T) {
	var b strings.Builder
	writeAttr(&b, `a&b<c>d"e'f`)
	if b.String() != `a&amp;b&lt;c&gt;d&quot;e'f` {
		t.Fatalf("writeAttr: %q", b.String())
	}
	b.Reset()
	writeText(&b, `a&b<c>d"e'f`)
	if b.String() != `a&amp;b&lt;c&gt;d"e'f` {
		t.Fatalf("writeText: %q", b.String())
	}
}
