package manual

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"vestt/common/indexer"
	"vestt/common/models"
)

// SPEC-10 acceptance: three full-chain spot checks against httptest fake
// sites — ApplyConfiguration → Test → search → fixture releases (same shape
// as spotcheck_batch2_test.go). One html-login site exercising the upstream
// season/episode query branch (RuTracker), one bearer-token json-api with a
// mid-chain 401 token renewal (SpeedApp), one cookie json-api carrying the
// full category mapping on the request URL (TorrentDay).

func TestChainRuTracker(t *testing.T) {
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/forum/login.php":
			w.Header().Set("Set-Cookie", "bb_session=fresh; Path=/")
			rtWrite(t, w, rtLoggedInHeader+"</body></html>")
		case "/forum/tracker.php":
			rtWrite(t, w, rtResultsFixture)
		default:
			http.NotFound(w, r)
		}
	})
	rt := newTestRuTracker(t, site)
	ctx := context.Background()

	rtApplyOK(t, ctx, rt, site)
	if err := rt.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}

	season := 2
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Show Name"
	q.Season = &season
	q.Episode = "3"
	res, err := rt.ResultsForQuery(ctx, q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	base := site.srv.URL + "/forum/"
	if res.Releases[0].Link == nil || res.Releases[0].Link.String() != base+"dl.php?t=1002" {
		t.Fatalf("link[0]: %v", res.Releases[0].Link)
	}
	if res.Releases[1].Link == nil || res.Releases[1].Link.String() != base+"dl.php?t=1001" {
		t.Fatalf("link[1]: %v", res.Releases[1].Link)
	}

	// request order: setup login → Test browse → the tvsearch query, whose
	// URL carries the upstream season/episode branch (RuTracker.cs).
	hits := site.hitList()
	if len(hits) != 3 {
		t.Fatalf("hits: %v", hits)
	}
	if hits[0] != "POST /forum/login.php" {
		t.Fatalf("hit[0]: %q", hits[0])
	}
	if !strings.HasPrefix(hits[1], "GET /forum/tracker.php") {
		t.Fatalf("hit[1]: %q", hits[1])
	}
	want := "GET /forum/tracker.php?nm=Show%25Name+%D0%A2%D0%92+%7C+%D0%A1%D0%B5%D0%B7%D0%BE%D0%BD%3A+2+%D0%A1%D0%B5%D1%80%D0%B8%D0%B8%3A+3"
	if hits[2] != want {
		t.Fatalf("tvsearch hit:\n got %q\nwant %q", hits[2], want)
	}
}

func TestChainSpeedApp(t *testing.T) {
	site := newSpeedAppSite(t)
	s := newTestSpeedApp(t, site)
	ctx := context.Background()

	if _, err := s.ApplyConfiguration(ctx, []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "email", Value: "user@example.com"},
		{ID: "password", Value: "pass"},
	}); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if err := s.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if s.token != "tok1" {
		t.Fatalf("token: %q", s.token)
	}

	// expire the session token mid-chain: the next search must transparently
	// re-login and retry once (SpeedAppTracker.cs:126-130).
	site.expire("tok1")
	res, err := s.ResultsForQuery(ctx, &models.TorznabQuery{QueryType: "search"}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if s.token != "tok2" {
		t.Fatalf("token after renewal: %q", s.token)
	}
	if auth := site.lastSearchAuth(); auth != "Bearer tok2" {
		t.Fatalf("retry Authorization: %q", auth)
	}
	logins, gets := site.stats()
	if logins != 2 {
		t.Fatalf("logins: %d (want 2 — initial + re-login)", logins)
	}
	if gets != 4 { // config self-test + Test + original 401 + retry
		t.Fatalf("search gets: %d", gets)
	}
	r0 := res.Releases[0]
	if r0.Title != "Movie.Name.2024.1080p..BluRay" {
		t.Fatalf("title[0]: %q", r0.Title)
	}
	if r0.Link == nil || r0.Link.String() != site.srv.URL+"/api/torrent/101/download" {
		t.Fatalf("link[0]: %v", r0.Link)
	}
}

func TestChainTorrentDay(t *testing.T) {
	site := newTorrentDaySite(t, map[string]string{
		"/t.json": torrentDayResultsJSON,
	})
	td := newTestTorrentDay(t, site)
	ctx := context.Background()

	status, err := td.ApplyConfiguration(ctx, []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "cookie", Value: "uid=1; pass=abc"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !td.IsConfigured() {
		t.Fatalf("status=%v configured=%v", status, td.IsConfigured())
	}
	if err := td.Test(ctx); err != nil {
		t.Fatalf("Test: %v", err)
	}

	// Movies/Bluray (2040) and TV/HD (5040) each map to two tracker cats;
	// the mapping rides the request as a ;-joined list and both fixture rows
	// survive the upstream category-intersection filter (BaseIndexer.cs:
	// 174-183).
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.Categories = []int{models.CatMoviesBluRay.ID, models.CatTVHD.ID}
	res, err := td.ResultsForQuery(ctx, q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if res.Releases[0].Title != "Some Movie 2024 1080p" {
		t.Fatalf("title[0]: %q", res.Releases[0].Title)
	}

	hits := site.paths()
	if len(hits) != 3 { // apply browse + Test browse + category search
		t.Fatalf("hits: %v", hits)
	}
	// browse requests carry the full tracker table plus the empty q= value
	// (upstream builds t.json?<ids joined by ;>;q=...); the exact full table
	// is pinned by TestTorrentDayCategoryTable's GetTrackerCategories check.
	for _, h := range hits[:2] {
		if !strings.HasPrefix(h, "/t.json?") || !strings.HasSuffix(h, ";q=") {
			t.Fatalf("browse hit: %q", h)
		}
	}
	if hits[0] != hits[1] {
		t.Fatalf("browse hits differ: %q vs %q", hits[0], hits[1])
	}
	// 2040 → tracker 11 (Bluray) + 5 (Bluray-Full); 5040 → 32 (TV/Bluray) +
	// 7 (TV/x264): every matching tracker cat rides the request in table
	// registration order (TorznabCapabilitiesCategories.cs:76-83).
	if hits[2] != "/t.json?11;5;32;7;q=" {
		t.Fatalf("category hit: %q", hits[2])
	}
}
