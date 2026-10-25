package manual

import (
	"context"
	"strings"
	"testing"

	"vestt/common/indexer"
	"vestt/common/models"
)

// SPEC-08 acceptance #4: three full-chain spot checks against httptest fake
// sites — Configure (ApplyConfiguration) → Test → search → fixture releases.
// One json-api (Anilibria), one gazelle (AlphaRatio), one html (DivxTotal).

func TestChainAnilibria(t *testing.T) {
	site := newAnilibriaSite(t, map[string]string{
		"/api/v1/anime/torrents/rss":       anilibriaRSSXML,
		"/api/v1/anime/torrents/11":        anilibriaSingleJSON,
		"/api/v1/app/search/releases":      `[{"id": 2}]`,
		"/api/v1/anime/torrents/release/2": anilibriaArrayJSON,
	})
	a := newTestAnilibria(t, site)
	ctx := context.Background()

	status, err := a.ApplyConfiguration(ctx, []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status = %v, want ConfigCompleted", status)
	}
	if !a.IsConfigured() {
		t.Fatal("expected configured")
	}
	if a.SiteLink() != site.srv.URL+"/" {
		t.Fatalf("sitelink = %q", a.SiteLink())
	}
	if err := a.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "фильм"
	res, err := a.ResultsForQuery(ctx, q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	if !strings.Contains(r.Title, "Фильм") {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Size == nil || *r.Size != 1073741824 {
		t.Fatalf("size: %v", r.Size)
	}
}

func TestChainAlphaRatio(t *testing.T) {
	site := newAlphaRatioSite(t, true)
	a := newTestAlphaRatio(t, site)
	ctx := context.Background()

	status, err := a.ApplyConfiguration(ctx, []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status = %v, want ConfigRequiresTesting", status)
	}
	if !a.IsConfigured() {
		t.Fatal("expected configured after login")
	}
	if err := a.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}

	res, err := a.ResultsForQuery(ctx, &models.TorznabQuery{
		QueryType: "search", SearchTerm: "show",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	if r.Title != "Some Show S01 [2023] WEB-DL / 1080p / MKV / x264" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Link == nil || r.Link.String() != site.URL+"/torrents.php?action=download&id=2001" {
		t.Fatalf("link: %v", r.Link)
	}
}

func TestChainDivxTotal(t *testing.T) {
	site := newDivxSite(t, map[string]string{
		"/page/1/":                          divxSearchPage,
		"/series/777/american-horror-story": divxSeriesDetails,
	})
	d := newTestDivxTotal(t, site)
	ctx := context.Background()

	status, err := d.ApplyConfiguration(ctx, []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status = %v, want ConfigCompleted", status)
	}
	if !d.IsConfigured() {
		t.Fatal("expected configured")
	}
	if err := d.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "American.Horror.Story.2014"
	res, err := d.ResultsForQuery(ctx, q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// matchWords on by default: only the matching series' episodes survive
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	for _, r := range res.Releases {
		if !strings.Contains(r.Title, "American Horror Story") {
			t.Fatalf("title: %q", r.Title)
		}
	}
}
