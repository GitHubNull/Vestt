package engine

import (
	"strings"
	"testing"

	"vestt/common/models"
)

// loadDefinition loads one real definition by id (SPEC-05 loader).
func loadDefinition(t *testing.T, id string) *IndexerDefinition {
	t.Helper()
	defs, _, err := LoadAll(discardLogger(), "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	def, ok := defs[id]
	if !ok {
		t.Fatalf("definition %s not loaded", id)
	}
	return def
}

// TestCapsConstruction对拍 verifies acceptance #6 on 10 representative real
// definitions: categories tree, searching support bits and the
// tracker⇄torznab mappings (constructed caps must agree with the definition
// content, mirroring upstream TorznabCapabilities output).
func TestCapsConstruction(t *testing.T) {
	ids := []string{
		"1337x",              // public, categorymappings with desc → custom cats
		"thepiratebay",       // public, categories + categorymappings
		"torrentdownloads",   // tv/movie/search modes
		"eztv",               // tv-search only
		"yts",                // movie-search, categories with multi-mapping
		"limetorrents",       // categorymappings + defaults
		"kickasstorrents-ws", // public, categorymappings + defaults
		"nyaasi",             // anime, categories
		"acgrip",             // categories with multi-mapping
		"bjshare",            // semi-private, categories + categorymappings
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			def := loadDefinition(t, id)
			x, err := NewCardigann(def, discardLogger())
			if err != nil {
				t.Fatalf("NewCardigann: %v", err)
			}
			caps := x.Capabilities()

			// searching support bits derived from caps.modes
			if !caps.SearchAvailable {
				t.Errorf("search must be available")
			}
			wantTV := false
			wantMovie := false
			wantMusic := false
			wantBook := false
			for _, kv := range def.Caps.Modes {
				switch kv.Key {
				case "tv-search":
					wantTV = true
				case "movie-search":
					wantMovie = true
				case "music-search":
					wantMusic = true
				case "book-search":
					wantBook = true
				}
			}
			if (len(caps.TVSearchParams) > 0) != wantTV {
				t.Errorf("tv-search availability mismatch")
			}
			if (len(caps.MovieSearchParams) > 0) != wantMovie {
				t.Errorf("movie-search availability mismatch")
			}
			if (len(caps.MusicSearchParams) > 0) != wantMusic {
				t.Errorf("music-search availability mismatch")
			}
			if (len(caps.BookSearchParams) > 0) != wantBook {
				t.Errorf("book-search availability mismatch")
			}
			if caps.SupportsRawSearch != def.Caps.Allowrawsearch {
				t.Errorf("SupportsRawSearch = %v", caps.SupportsRawSearch)
			}

			// every caps.categories entry must exist in the tree by name
			flat := caps.Categories.FlatList(false)
			byID := map[int]*models.Category{}
			for _, c := range flat {
				byID[c.ID] = c
			}
			for _, kv := range def.Caps.Categories {
				cat := models.GetCatByName(kv.Value)
				if cat == nil {
					continue // loader logged it already
				}
				if _, ok := byID[cat.ID]; !ok {
					t.Errorf("caps category %s (%d) missing from tree", kv.Value, cat.ID)
				}
			}

			// every categorymapping creates a tracker mapping in both
			// directions (desc-bearing mappings also create a custom cat)
			for _, cm := range def.Caps.Categorymappings {
				mapped := caps.Categories.MapTrackerCatToNewznab(cm.ID)
				if len(mapped) == 0 {
					t.Errorf("categorymapping %s has no torznab mapping", cm.ID)
				}
				if cm.Cat != "" {
					cat := models.GetCatByName(cm.Cat)
					if cat != nil && !containsCatID(mapped, cat.ID) {
						t.Errorf("categorymapping %s → %v, want to contain %d", cm.ID, mapped, cat.ID)
					}
				}
				if cm.Default {
					found := false
					for _, d := range x.DefaultCategories {
						if d == cm.ID {
							found = true
						}
					}
					if !found {
						t.Errorf("default categorymapping %s not recorded", cm.ID)
					}
				}
			}

			// caps XML renders and includes a categories section
			xml := caps.XML()
			if !strings.Contains(xml, "<caps>") || !strings.Contains(xml, "<searching>") {
				t.Errorf("caps XML malformed")
			}
			if len(flat) > 0 && !strings.Contains(xml, "<categories>") {
				t.Errorf("caps XML missing categories despite non-empty tree")
			}
		})
	}
}

// TestCapsCustomCategoryID verifies the upstream custom-cat id rules:
// integer tracker ids get id+100000; string ids get a SHA1-derived id.
func TestCapsCustomCategoryID(t *testing.T) {
	def := loadDefinition(t, "1337x")
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann: %v", err)
	}
	caps := x.Capabilities()
	// 1337x uses categorymappings with desc; every desc must surface as a
	// custom category (id ≥ 100000) reachable by desc.
	def = loadDefinition(t, "1337x")
	for _, cm := range def.Caps.Categorymappings {
		if cm.Desc == "" {
			continue
		}
		mapped := caps.Categories.MapTrackerCatDescToNewznab(cm.Desc)
		if len(mapped) == 0 {
			t.Errorf("desc %q has no custom category", cm.Desc)
			continue
		}
		// upstream returns both the standard mapping and the custom cat:
		// at least one result must be a custom id ≥ 100000
		custom := false
		for _, id := range mapped {
			if id >= 100000 {
				custom = true
			}
		}
		if !custom {
			t.Errorf("desc %q has no custom category in %v", cm.Desc, mapped)
		}
	}
}

// TestCapsSearchModeValidation mirrors upstream ParseCardigannSearchModes
// error cases.
func TestCapsSearchModeValidation(t *testing.T) {
	mk := func(modes ...models.SearchMode) *models.Capabilities {
		c := models.NewCapabilities()
		if err := c.ParseCardigannSearchModes(modes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return c
	}
	c := mk(models.SearchMode{Key: "search", Params: []string{"q"}})
	if len(c.TVSearchParams) != 0 {
		t.Errorf("tv params must start empty")
	}
	c = mk(
		models.SearchMode{Key: "search", Params: []string{"q"}},
		models.SearchMode{Key: "tv-search", Params: []string{"q", "season", "ep", "imdbid"}},
	)
	if !contains(c.TVSearchParams, models.TVParamSeason) || !contains(c.TVSearchParams, models.TVParamImdbID) {
		t.Errorf("tv params = %v", c.TVSearchParams)
	}
	// imdbid parses but does NOT enable TvSearchImdbAvailable (#8107)
	if c.TVSearchImdbAvailable {
		t.Errorf("TVSearchImdbAvailable must stay false")
	}
	if got := c.SupportedTvSearchParams(); strings.Contains(got, "imdbid") {
		t.Errorf("SupportedTvSearchParams must not include imdbid: %q", got)
	}

	bad := []struct {
		name  string
		modes []models.SearchMode
	}{
		{"no modes", nil},
		{"missing search", []models.SearchMode{{Key: "tv-search", Params: []string{"q"}}}},
		{"search extra param", []models.SearchMode{{Key: "search", Params: []string{"q", "season"}}}},
		{"bad tv param", []models.SearchMode{{Key: "search", Params: []string{"q"}}, {Key: "tv-search", Params: []string{"qq"}}}},
		{"duplicate tv param", []models.SearchMode{{Key: "search", Params: []string{"q"}}, {Key: "tv-search", Params: []string{"q", "q"}}}},
		{"unknown mode", []models.SearchMode{{Key: "search", Params: []string{"q"}}, {Key: "x-search", Params: []string{"q"}}}},
	}
	for _, tt := range bad {
		c := models.NewCapabilities()
		if err := c.ParseCardigannSearchModes(tt.modes); err == nil {
			t.Errorf("%s: expected error", tt.name)
		}
	}
}

func containsCatID(list []int, v int) bool {
	for _, n := range list {
		if n == v {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
