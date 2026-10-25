package manual

import (
	"context"
	"strings"
	"testing"

	"vestt/common/indexer"
	"vestt/common/models"
)

// SPEC-09 acceptance: three full-chain spot checks against httptest fake
// sites — Configure (ApplyConfiguration) → Test → search → fixture releases.
// One json-api (FileList), one gazelle-api (Orpheus), one cookie html
// (IPTorrents).

func TestChainFileList(t *testing.T) {
	site := newFileListSite(t, map[string]string{
		"/api.php": fileListLatestJSON,
	})
	f := newTestFileList(t, site)
	ctx := context.Background()

	status, err := f.ApplyConfiguration(ctx, []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "passkey", Value: "secretpasskey"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !f.IsConfigured() {
		t.Fatalf("status=%v configured=%v", status, f.IsConfigured())
	}
	if err := f.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	res, err := f.ResultsForQuery(ctx, q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if res.Releases[0].Title != "Some.Movie.2020.1080p.BluRay.x264-GRP" {
		t.Fatalf("title: %q", res.Releases[0].Title)
	}
}

func TestChainOrpheus(t *testing.T) {
	site := newOrpheusSite(t)
	o := newTestOrpheus(t, site)
	ctx := context.Background()
	key := strings.Repeat("a", 116)

	status, err := o.ApplyConfiguration(ctx, []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "apikey", Value: key},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !o.IsConfigured() {
		t.Fatalf("status=%v configured=%v", status, o.IsConfigured())
	}
	if err := o.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}

	res, err := o.ResultsForQuery(ctx, &models.TorznabQuery{
		QueryType: "search", SearchTerm: "test artist",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if res.Releases[0].Title != "Test Artist - Test Album [2020] [Album] MP3 / 320 / WEB" {
		t.Fatalf("title: %q", res.Releases[0].Title)
	}
}

func TestChainIPTorrents(t *testing.T) {
	site := newIPSite(t, ipTorrentsFixture)
	i := newTestIPT(t, site)
	ctx := context.Background()

	status, err := i.ApplyConfiguration(ctx, []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "cookie", Value: "uid=123; pass=abc"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !i.IsConfigured() {
		t.Fatalf("status=%v configured=%v", status, i.IsConfigured())
	}
	if err := i.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	res, err := i.ResultsForQuery(ctx, q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if res.Releases[0].Title != "Some.Movie.2024.1080p.BluRay.x264-GRP" {
		t.Fatalf("title: %q", res.Releases[0].Title)
	}
}
