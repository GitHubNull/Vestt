package models

import (
	"net/url"
	"testing"
)

func capsWithCats() *Capabilities {
	c := NewCapabilities()
	c.Categories.AddCategoryTree(CatMoviesHD)
	c.Categories.AddCategoryTree(CatMoviesSD)
	c.Categories.AddCategoryTree(CatTVHD)
	return c
}

func TestCapabilitiesDefaults(t *testing.T) {
	c := NewCapabilities()
	if !c.SearchAvailable || c.LimitsMax == nil || *c.LimitsMax != 100 || *c.LimitsDefault != 100 {
		t.Fatalf("defaults: %+v", c)
	}
	if c.TVSearchImdbAvailable {
		t.Fatal("TvSearchImdbAvailable defaults to false (upstream #8107)")
	}
	xml := c.XML()
	if xml == "" {
		t.Fatal("xml empty")
	}
}

func TestAddCategoryTree(t *testing.T) {
	c := NewCapabilities()
	// child first: creates the parent node
	c.Categories.AddCategoryTree(CatMoviesHD)
	tree := c.Categories.Tree()
	if len(tree) != 1 || tree[0].ID != 2000 {
		t.Fatalf("tree: %+v", tree)
	}
	if len(tree[0].SubCategories) != 1 || tree[0].SubCategories[0].ID != 2040 {
		t.Fatalf("subs: %+v", tree[0].SubCategories)
	}
	// duplicate child must not be re-added
	c.Categories.AddCategoryTree(CatMoviesHD)
	if len(c.Categories.Tree()[0].SubCategories) != 1 {
		t.Fatal("duplicate child added")
	}
	// parent added bare
	c.Categories.AddCategoryTree(CatAudio)
	found := false
	for _, n := range c.Categories.Tree() {
		if n.ID == 3000 {
			found = true
			if len(n.SubCategories) != 0 {
				t.Fatal("parent must be added without sub-categories")
			}
		}
	}
	if !found {
		t.Fatal("parent not in tree")
	}
	// custom cat (>=100000) added as-is
	custom := &Category{ID: 100001, Name: "Custom"}
	c.Categories.AddCategoryTree(custom)
	if c.Categories.Tree()[len(c.Categories.Tree())-1].ID != 100001 {
		t.Fatal("custom cat not added")
	}
}

func TestSupportedCategoriesAndExpand(t *testing.T) {
	c := capsWithCats()
	got := c.Categories.SupportedCategories([]int{2000, 2040, 9999})
	// 2000 is a parent in tree? No: children were added, so tree node 2000
	// exists with subs 2040, 2030. Both 2000 and 2040 are supported.
	if len(got) != 2 {
		t.Fatalf("supported: %v", got)
	}
	if got := c.Categories.SupportedCategories([]int{9999}); len(got) != 0 {
		t.Fatalf("unsupported should be empty: %v", got)
	}

	q := NewTorznabQuery()
	q.Categories = []int{2040, 100001}
	expanded := c.Categories.ExpandTorznabQueryCategories(q, true)
	// 2040 + parent 2000; custom cat stands alone
	seen := map[int]bool{}
	for _, id := range expanded {
		seen[id] = true
	}
	if !seen[2040] || !seen[2000] || !seen[100001] {
		t.Fatalf("expanded: %v", expanded)
	}
	if len(expanded) != 3 {
		t.Fatalf("expanded should have 3 distinct: %v", expanded)
	}
}

func TestSupportsQuery(t *testing.T) {
	c := capsWithCats()
	c.MovieSearchParams = []string{"q", "imdbid"}
	c.TVSearchParams = []string{"q", "season", "ep", "tvdbid"}

	ok, _ := c.SupportsQuery(ParseTorznabQuery(mustParse2(t, "t=search&q=x")), false)
	if !ok {
		t.Fatal("plain search must be supported")
	}
	ok, _ = c.SupportsQuery(ParseTorznabQuery(mustParse2(t, "t=movie&q=x")), false)
	if !ok {
		t.Fatal("movie search must be supported")
	}
	// imdb movie search supported via MovieSearchParams
	ok, _ = c.SupportsQuery(ParseTorznabQuery(mustParse2(t, "t=movie&imdbid=tt1234567")), false)
	if !ok {
		t.Fatal("imdb movie must be supported")
	}
	// caps pseudo-type always supported
	ok, _ = c.SupportsQuery(ParseTorznabQuery(mustParse2(t, "t=caps")), false)
	if !ok {
		t.Fatal("caps must be supported")
	}
	// tvdb id query supported
	ok, _ = c.SupportsQuery(ParseTorznabQuery(mustParse2(t, "t=tvsearch&tvdbid=123")), false)
	if !ok {
		t.Fatal("tvdb query must be supported")
	}
	// unsupported category
	ok, reason := c.SupportsQuery(ParseTorznabQuery(mustParse2(t, "t=search&cat=9999")), false)
	if ok || reason == "" {
		t.Fatalf("unsupported cat must fail: %v %q", ok, reason)
	}
	// unsupported music
	ok, _ = c.SupportsQuery(ParseTorznabQuery(mustParse2(t, "t=music&album=x")), false)
	if ok {
		t.Fatal("music must not be supported")
	}
	// nil query
	if ok, _ = c.SupportsQuery(nil, false); ok {
		t.Fatal("nil query must fail")
	}
	// movie imdb without imdbid support must fail
	c2 := capsWithCats()
	ok, _ = c2.SupportsQuery(ParseTorznabQuery(mustParse2(t, "t=movie&imdbid=tt1234567")), false)
	if ok {
		t.Fatal("movie imdb without imdb support must fail")
	}
	// tv imdb via TvSearchImdbAvailable override
	c2.TVSearchImdbAvailable = true
	c2.TVSearchParams = []string{"q"}
	ok, _ = c2.SupportsQuery(ParseTorznabQuery(mustParse2(t, "t=tvsearch&imdbid=tt1234567")), false)
	if !ok {
		t.Fatal("TvSearchImdbAvailable override must allow tv imdb")
	}
}

func mustParse2(t *testing.T, query string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	return v
}

func TestConcatCaps(t *testing.T) {
	a := NewCapabilities()
	a.TVSearchParams = []string{"q", "season"}
	a.Categories.AddCategoryTree(CatMoviesHD)
	b := NewCapabilities()
	b.SearchAvailable = false
	b.TVSearchParams = []string{"ep"}
	b.MovieSearchParams = []string{"q"}
	b.Categories.AddCategoryTree(CatTVHD)

	merged := Concat(a, b)
	if !merged.SearchAvailable {
		t.Fatal("search available must union to true")
	}
	if len(merged.TVSearchParams) != 3 {
		t.Fatalf("tv params union: %v", merged.TVSearchParams)
	}
	if len(merged.MovieSearchParams) != 1 {
		t.Fatalf("movie params: %v", merged.MovieSearchParams)
	}
	// categories merged by parent id
	if len(merged.Categories.Tree()) != 2 {
		t.Fatalf("cats: %+v", merged.Categories.Tree())
	}
}

func TestSortedTreeAndFlatList(t *testing.T) {
	c := NewCapabilities()
	// add out of order
	c.Categories.AddCategoryTree(CatTVHD)
	c.Categories.AddCategoryTree(CatMoviesHD)
	c.Categories.AddCategoryTree(CatMoviesSD)
	sorted := c.Categories.SortedTree()
	if sorted[0].ID != 2000 || sorted[1].ID != 5000 {
		t.Fatalf("sorted parents: %d %d", sorted[0].ID, sorted[1].ID)
	}
	subs := sorted[0].SubCategories
	if subs[0].ID != 2030 || subs[1].ID != 2040 {
		t.Fatalf("sorted subs: %d %d", subs[0].ID, subs[1].ID)
	}
	flat := c.Categories.FlatList(true)
	if flat[0].ID != 2000 || flat[1].ID != 2030 {
		t.Fatalf("flat list: %v", flat)
	}
}
