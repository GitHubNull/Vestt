package models

import (
	"testing"
)

// mappingCaps builds capabilities pre-mapped like a cardigann definition:
// tracker id "7" → TV (desc "TV Shows"), "4" → Movies (desc "Movies").
func mappingCaps() *Capabilities {
	c := NewCapabilities()
	c.Categories.AddCategoryMapping("7", CatTV, "TV Shows")
	c.Categories.AddCategoryMapping("4", CatMovies, "Movies")
	return c
}

func TestGetTrackerCategories(t *testing.T) {
	c := mappingCaps()
	got := c.Categories.GetTrackerCategories()
	if len(got) != 2 || got[0] != "7" || got[1] != "4" {
		t.Fatalf("tracker cats = %v, want [7 4]", got)
	}

	// duplicates collapse; custom mapping categories (>=100000) and custom
	// desc entries are excluded
	c2 := NewCapabilities()
	c2.Categories.AddCategoryMapping("x", CatTV, "")
	c2.Categories.AddCategoryMapping("x", CatTV, "")                      // duplicate tracker cat
	c2.Categories.AddCategoryMapping("y", CatMovies, "Y")                 // adds custom 100004
	c2.Categories.AddCategoryMapping("100001", &Category{ID: 100001}, "") // custom only
	got2 := c2.Categories.GetTrackerCategories()
	if len(got2) != 2 || got2[0] != "x" || got2[1] != "y" {
		t.Fatalf("deduped tracker cats = %v, want [x y]", got2)
	}

	// empty container
	if got3 := NewCapabilitiesCategories().GetTrackerCategories(); len(got3) != 0 {
		t.Fatalf("empty = %v", got3)
	}
}

func TestAddCategoryMappingNilCategory(t *testing.T) {
	// nil torznab category without a desc is a no-op (upstream would NRE)
	c := NewCapabilitiesCategories()
	c.AddCategoryMapping("9", nil, "")
	if len(c.Tree()) != 0 {
		t.Fatalf("tree = %+v, want empty", c.Tree())
	}
	if got := c.MapTrackerCatToNewznab("9"); len(got) != 0 {
		t.Fatalf("mappings recorded: %v", got)
	}

	// nil category with a desc still records the mapping under the custom
	// 1:1 category (the base mapping lands on the zero cat)
	c.AddCategoryMapping("9", nil, "Some Custom")
	if len(c.Tree()) == 0 {
		t.Fatal("custom category must be added to the tree")
	}
	if got := c.MapTrackerCatToNewznab("9"); len(got) != 2 || got[1] != 100009 {
		t.Fatalf("custom mapping = %v, want [0 100009]", got)
	}
}

func TestAddCategoryMappingCustomDescIDs(t *testing.T) {
	// integer tracker id: custom cat = id + 100000; both the real mapping
	// and the desc-derived custom mapping are recorded
	c := NewCapabilities()
	c.Categories.AddCategoryMapping("7", CatTV, "TV Shows")
	if got := c.Categories.MapTrackerCatDescToNewznab("TV Shows"); len(got) != 2 || got[0] != 5000 || got[1] != 100007 {
		t.Fatalf("int desc id = %v, want [5000 100007]", got)
	}
	// both the real and the custom mapping exist for the tracker cat
	if got := c.Categories.MapTrackerCatToNewznab("7"); len(got) != 2 || got[0] != 5000 || got[1] != 100007 {
		t.Fatalf("tracker mappings = %v, want [5000 100007]", got)
	}
	// the custom category is in the tree
	custom := false
	for _, n := range c.Categories.Tree() {
		if n.ID == 100007 && n.Name == "TV Shows" {
			custom = true
		}
	}
	if !custom {
		t.Fatalf("custom category missing from tree: %+v", c.Categories.Tree())
	}

	// non-integer tracker id: custom cat derives from a SHA1 prefix
	c2 := NewCapabilities()
	c2.Categories.AddCategoryMapping("section-x", CatMovies, "Section X")
	got := c2.Categories.MapTrackerCatDescToNewznab("Section X")
	if len(got) != 2 || got[1] < 100000 || got[1] >= 165536 {
		t.Fatalf("sha1-derived custom id out of range: %v", got)
	}
	// deterministic: re-adding the same mapping reuses the same custom id
	c2.Categories.AddCategoryMapping("section-x", CatMovies, "Section X")
	if got2 := c2.Categories.MapTrackerCatDescToNewznab("Section X"); len(got2) != 4 || got2[1] != got2[3] {
		t.Fatalf("sha1 custom id not deterministic: %v vs %v", got, got2)
	}
	// custom category landed in the tree
	found := false
	for _, n := range c2.Categories.Tree() {
		if n.ID == got[1] && n.Name == "Section X" {
			found = true
		}
	}
	if !found {
		t.Fatalf("custom category missing from tree: %+v", c2.Categories.Tree())
	}
}

func TestMapTorznabCapsToTrackers(t *testing.T) {
	c := mappingCaps()
	// TV query expands to the tracker cat "7" (via mapping 5000)
	if got := c.Categories.MapTorznabCapsToTrackers(&TorznabQuery{Categories: []int{5000}}, false); len(got) != 1 || got[0] != "7" {
		t.Fatalf("tv = %v, want [7]", got)
	}
	// movie query → "4"; both mappings for a parent query stay distinct
	got := c.Categories.MapTorznabCapsToTrackers(&TorznabQuery{Categories: []int{2000, 5000}}, false)
	if len(got) != 2 || got[0] != "7" || got[1] != "4" {
		t.Fatalf("movies = %v, want [7 4]", got)
	}
	// unmapped category → no tracker cats
	if got := c.Categories.MapTorznabCapsToTrackers(&TorznabQuery{Categories: []int{7000}}, false); len(got) != 0 {
		t.Fatalf("book = %v, want empty", got)
	}
}

func TestMapTrackerCatToNewznab(t *testing.T) {
	c := mappingCaps()
	// exact (both the mapped and the desc-custom entry)
	if got := c.Categories.MapTrackerCatToNewznab("7"); len(got) != 2 || got[0] != 5000 || got[1] != 100007 {
		t.Fatalf("7 → %v", got)
	}
	// case-insensitive: the mapping id itself is compared case-insensitively
	// ("X" vs "x"), but the query is not trimmed before the comparison
	c2 := NewCapabilities()
	c2.Categories.AddCategoryMapping("X", CatMovies, "")
	if got := c2.Categories.MapTrackerCatToNewznab("x"); len(got) != 1 || got[0] != 2000 {
		t.Fatalf("case-insensitive → %v", got)
	}
	if got := c2.Categories.MapTrackerCatToNewznab(" 4 "); len(got) != 0 {
		t.Fatalf("untrimmed query must not match: %v", got)
	}
	// blank query and unknown tracker cat
	if got := c.Categories.MapTrackerCatToNewznab("   "); len(got) != 0 {
		t.Fatalf("blank → %v", got)
	}
	if got := c.Categories.MapTrackerCatToNewznab("999"); len(got) != 0 {
		t.Fatalf("unknown → %v", got)
	}
	// further duplicate mappings all returned in order
	c.Categories.AddCategoryMapping("7", CatMoviesHD, "")
	if got := c.Categories.MapTrackerCatToNewznab("7"); len(got) != 3 || got[2] != 2040 {
		t.Fatalf("dup → %v", got)
	}
}

func TestMapTrackerCatDescToNewznab(t *testing.T) {
	c := mappingCaps()
	if got := c.Categories.MapTrackerCatDescToNewznab("movies"); len(got) != 2 || got[0] != 2000 || got[1] != 100004 {
		t.Fatalf("desc case-insensitive → %v", got)
	}
	if got := c.Categories.MapTrackerCatDescToNewznab(""); len(got) != 0 {
		t.Fatalf("blank desc → %v", got)
	}
	if got := c.Categories.MapTrackerCatDescToNewznab("Nope"); len(got) != 0 {
		t.Fatalf("unknown desc → %v", got)
	}
}

func TestParseCardigannSearchModes(t *testing.T) {
	base := func() []SearchMode {
		return []SearchMode{{Key: "search", Params: []string{"q"}}}
	}

	// empty modes
	if err := NewCapabilities().ParseCardigannSearchModes(nil); err == nil || err.Error() != "At least one search mode is required" {
		t.Fatalf("empty modes err = %v", err)
	}
	// missing mandatory "search"
	if err := NewCapabilities().ParseCardigannSearchModes([]SearchMode{{Key: "tv-search", Params: []string{"q"}}}); err == nil || err.Error() != "The search mode 'search' is mandatory" {
		t.Fatalf("missing search err = %v", err)
	}
	// search mode must carry exactly q
	if err := NewCapabilities().ParseCardigannSearchModes([]SearchMode{{Key: "search", Params: []string{"q", "season"}}}); err == nil || err.Error() != "In search mode 'search' only 'q' parameter is supported and it's mandatory" {
		t.Fatalf("search params err = %v", err)
	}
	// unsupported mode key
	if err := NewCapabilities().ParseCardigannSearchModes([]SearchMode{{Key: "search", Params: []string{"q"}}, {Key: "xxx-search"}}); err == nil || err.Error() != "Unsupported search mode: xxx-search" {
		t.Fatalf("unknown mode err = %v", err)
	}

	// tv-search: unsupported / duplicate params
	c := NewCapabilities()
	if err := c.ParseCardigannSearchModes([]SearchMode{
		{Key: "search", Params: []string{"q"}},
		{Key: "tv-search", Params: []string{"nope"}},
	}); err == nil || err.Error() != "Not supported tv-search param: nope" {
		t.Fatalf("tv unsupported err = %v", err)
	}
	c = NewCapabilities()
	if err := c.ParseCardigannSearchModes([]SearchMode{
		{Key: "search", Params: []string{"q"}},
		{Key: "tv-search", Params: []string{"season", "SEASON"}},
	}); err == nil || err.Error() != "Duplicate tv-search param: SEASON" {
		t.Fatalf("tv duplicate err = %v", err)
	}
	// movie-search: unsupported / duplicate
	c = NewCapabilities()
	if err := c.ParseCardigannSearchModes([]SearchMode{
		{Key: "search", Params: []string{"q"}},
		{Key: "movie-search", Params: []string{"ep"}},
	}); err == nil || err.Error() != "Not supported movie-search param: ep" {
		t.Fatalf("movie unsupported err = %v", err)
	}
	c = NewCapabilities()
	if err := c.ParseCardigannSearchModes([]SearchMode{
		{Key: "search", Params: []string{"q"}},
		{Key: "movie-search", Params: []string{"imdbid", "imdbid"}},
	}); err == nil || err.Error() != "Duplicate movie-search param: imdbid" {
		t.Fatalf("movie duplicate err = %v", err)
	}
	// music-search: unsupported / duplicate
	c = NewCapabilities()
	if err := c.ParseCardigannSearchModes([]SearchMode{
		{Key: "search", Params: []string{"q"}},
		{Key: "music-search", Params: []string{"tvdbid"}},
	}); err == nil || err.Error() != "Not supported music-search param: tvdbid" {
		t.Fatalf("music unsupported err = %v", err)
	}
	c = NewCapabilities()
	if err := c.ParseCardigannSearchModes([]SearchMode{
		{Key: "search", Params: []string{"q"}},
		{Key: "music-search", Params: []string{"album", "Album"}},
	}); err == nil || err.Error() != "Duplicate music-search param: Album" {
		t.Fatalf("music duplicate err = %v", err)
	}
	// book-search: unsupported / duplicate
	c = NewCapabilities()
	if err := c.ParseCardigannSearchModes([]SearchMode{
		{Key: "search", Params: []string{"q"}},
		{Key: "book-search", Params: []string{"season"}},
	}); err == nil || err.Error() != "Not supported book-search param: season" {
		t.Fatalf("book unsupported err = %v", err)
	}
	c = NewCapabilities()
	if err := c.ParseCardigannSearchModes([]SearchMode{
		{Key: "search", Params: []string{"q"}},
		{Key: "book-search", Params: []string{"author", "AUTHOR"}},
	}); err == nil || err.Error() != "Duplicate book-search param: AUTHOR" {
		t.Fatalf("book duplicate err = %v", err)
	}

	// success: every mode populates its param list (wire names, upper-cased
	// inputs normalized)
	c = NewCapabilities()
	err := c.ParseCardigannSearchModes([]SearchMode{
		{Key: "search", Params: []string{"q"}},
		{Key: "tv-search", Params: []string{"q", "season", "ep", "imdbid", "tvdbid", "rid", "tmdbid", "tvmazeid", "traktid", "doubanid", "year", "genre"}},
		{Key: "movie-search", Params: []string{"q", "imdbid", "tmdbid", "traktid", "doubanid", "year", "genre"}},
		{Key: "music-search", Params: []string{"q", "album", "artist", "label", "track", "year", "genre"}},
		{Key: "book-search", Params: []string{"q", "title", "author", "publisher", "year", "genre"}},
	})
	if err != nil {
		t.Fatalf("valid modes: %v", err)
	}
	if len(c.TVSearchParams) != 12 || len(c.MovieSearchParams) != 7 ||
		len(c.MusicSearchParams) != 7 || len(c.BookSearchParams) != 6 {
		t.Fatalf("params = tv:%v movie:%v music:%v book:%v",
			c.TVSearchParams, c.MovieSearchParams, c.MusicSearchParams, c.BookSearchParams)
	}
	if c.TVSearchParams[1] != TVParamSeason || c.MovieSearchParams[1] != MovieParamImdbID {
		t.Fatalf("wire names not mapped: %v", c.TVSearchParams)
	}

	// re-running over the same caps still detects duplicates (contains sees
	// the earlier entries)
	c2 := NewCapabilities()
	modes := base()
	modes = append(modes, SearchMode{Key: "tv-search", Params: []string{"season"}})
	if err := c2.ParseCardigannSearchModes(modes); err != nil {
		t.Fatal(err)
	}
	if err := c2.ParseCardigannSearchModes(modes); err == nil || err.Error() != "Duplicate tv-search param: season" {
		t.Fatalf("second run err = %v", err)
	}

	// empty params on the search mode is invalid too
	if err := NewCapabilities().ParseCardigannSearchModes([]SearchMode{{Key: "search"}}); err == nil {
		t.Fatal("search without q must fail")
	}
}
