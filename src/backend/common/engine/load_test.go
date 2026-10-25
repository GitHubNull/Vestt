package engine

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"vestt/common/models"
)

// TestLoadAll is SPEC-05 acceptance #1: all upstream definitions load.
func TestLoadAll(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	defs, stats, err := LoadAll(logger, "")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Loaded != 582 {
		t.Fatalf("loaded %d definitions, want 582; skipped: %v", stats.Loaded, stats.Skipped)
	}
	if len(stats.Skipped) != 0 {
		t.Fatalf("skipped definitions: %v", stats.Skipped)
	}
	d := defs["1337x"]
	if d == nil {
		t.Fatal("1337x missing")
	}
	if d.Name != "1337x" || len(d.Search.Paths) == 0 || len(d.Search.Fields) == 0 {
		t.Fatalf("1337x malformed: %+v", d)
	}
}

// TestLoadAllDecoding spot-checks tricky decode paths on real definitions.
func TestLoadAllDecoding(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	defs, _, err := LoadAll(logger, "")
	if err != nil {
		t.Fatal(err)
	}

	// ordered inputs preserved (form field order matters)
	var foundInputs bool
	for _, def := range defs {
		if def.Search != nil && len(def.Search.Inputs) > 1 {
			first := def.Search.Inputs[0]
			if first.Key == "" {
				t.Fatalf("%s: empty input key", def.ID)
			}
			foundInputs = true
			break
		}
	}
	if !foundInputs {
		t.Fatal("no definition with search inputs found")
	}

	// fields keep document order (1337x.yml starts with title_default)
	d := defs["1337x"]
	if len(d.Search.Fields) < 5 {
		t.Fatalf("1337x fields: %d", len(d.Search.Fields))
	}
	if d.Search.Fields[0].Name != "title_default" {
		t.Fatalf("first field: %q", d.Search.Fields[0].Name)
	}

	// categorymappings present on a known definition
	if len(defs["thepiratebay"].Caps.Categorymappings) == 0 {
		t.Fatal("thepiratebay categorymappings missing")
	}

	// login blocks decode (form logins may legitimately have no static
	// inputs, which LoadAll must accept)
	withLogin := 0
	for _, def := range defs {
		if def.Login != nil && def.Login.Method != "" {
			withLogin++
		}
	}
	if withLogin == 0 {
		t.Fatal("no login definitions found")
	}
	t.Logf("definitions with login: %d", withLogin)
}

// TestAllDefsRequestGeneration is SPEC-06 acceptance #4: every definition
// constructs caps and generates search requests for representative query
// shapes without panicking or producing empty URLs.
func TestAllDefsRequestGeneration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	defs, _, err := LoadAll(logger, "")
	if err != nil {
		t.Fatal(err)
	}

	queries := []*models.TorznabQuery{
		{QueryType: "search", SearchTerm: "test query", Categories: []int{5000}},
		{QueryType: "tv-search", SearchTerm: "some show", Season: intPtr(2), Episode: "03", Categories: []int{5000}},
		{QueryType: "movie-search", SearchTerm: "a movie", Categories: []int{2000}},
		{QueryType: "music-search", SearchTerm: "an album", Categories: []int{3000}},
		{QueryType: "book-search", SearchTerm: "a book", Categories: []int{7000}},
		{QueryType: "search", Categories: []int{8000}}, // rss-style empty query
	}

	instantiated := 0
	generated := 0
	for _, def := range defs {
		x, err := NewCardigann(def, logger)
		if err != nil {
			t.Errorf("%s: NewCardigann: %v", def.ID, err)
			continue
		}
		instantiated++
		for _, q := range queries {
			reqs, err := x.searchRequests(context.Background(), q, x.queryVariables(q))
			if err != nil {
				t.Errorf("%s: searchRequests(%s): %v", def.ID, q.QueryType, err)
				continue
			}
			for _, req := range reqs {
				generated++
				if req.URL == "" {
					t.Errorf("%s: empty URL for %s", def.ID, q.QueryType)
				}
				if req.Method != "GET" && req.Method != "POST" {
					t.Errorf("%s: bad method %q", def.ID, req.Method)
				}
			}
		}
	}
	if instantiated != 582 {
		t.Fatalf("instantiated %d indexers, want 582", instantiated)
	}
	if generated == 0 {
		t.Fatal("no requests generated at all")
	}
	t.Logf("instantiated %d indexers, generated %d requests", instantiated, generated)
}
