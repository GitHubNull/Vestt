package manual

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type anilibriaSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]string
}

func newAnilibriaSite(t *testing.T, serve map[string]string) *anilibriaSite {
	t.Helper()
	s := &anilibriaSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		body, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *anilibriaSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestAnilibria(t *testing.T, site *anilibriaSite) *Anilibria {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewAnilibria(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewAnilibria: %v", err)
	}
	a := ix.(*Anilibria)
	a.SiteLinkValue = site.srv.URL + "/"
	// tests must not pay the upstream 2.1s DDoS-Guard delay
	client.SetRequestDelay(a.ID(), 0)
	return a
}

const anilibriaRSSXML = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0"><channel><item>
<title>Release</title><torrentId>11</torrentId>
</item></channel></rss>`

const anilibriaSingleJSON = `{
  "release": {"id": 1, "type": {"value": "TV"},
    "name": {"main": "Главное", "english": "Main Title"},
    "alias": "main-title", "year": 2024, "poster": {"src": "/uploads/poster.jpg"}},
  "label": "Сезон 1 [1-12] 1080p",
  "hash": "abcdef0123456789abcdef0123456789abcdef01",
  "size": 2147483648, "seeders": 5, "leechers": 2,
  "completed_times": 42, "created_at": "2024-01-01T00:00:00Z", "updated_at": "2024-06-01T12:00:00Z"
}`

const anilibriaArrayJSON = `[
  {
    "release": {"id": 2, "type": {"value": "MOVIE"},
      "name": {"main": "Фильм", "english": "The Movie"},
      "alias": "the-movie", "year": null, "poster": {"src": "/uploads/movie.jpg"}},
    "label": "Фильм 1080p",
    "hash": "1111111111111111111111111111111111111111",
    "size": 1073741824, "seeders": 3, "leechers": 1,
    "completed_times": 7, "created_at": "2024-02-01T00:00:00Z", "updated_at": "2024-02-02T00:00:00Z"
  }
]`

func TestAnilibriaCaps(t *testing.T) {
	a, err := NewAnilibria(services.Deps{})
	if err != nil {
		t.Fatalf("NewAnilibria: %v", err)
	}
	caps := a.Capabilities()
	wantParams := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantParams, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"TV", 5070}, {"MOVIE", 5070}, {"OVA", 5070}, {"ONA", 5070},
		{"SPECIAL", 5070}, {"WEB", 5070}, {"OAD", 5070}, {"DORAMA", 5000},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		// AddCategoryMapping with a desc also creates a custom 1:1 category
		// (id ≥ 100000) that rides along in the mapping — upstream parity.
		found := false
		for _, id := range got {
			if id == tc.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", tc.tracker, got, tc.want)
		}
	}
}

func TestAnilibriaConfigForm(t *testing.T) {
	a, err := NewAnilibria(services.Deps{})
	if err != nil {
		t.Fatalf("NewAnilibria: %v", err)
	}
	items, err := a.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// Hidden items (cookieheader/lasterror) are excluded from the setup form
	// (upstream ToJSON(forDisplay: true) semantics).
	if len(ids) != 5 || ids[0] != "sitelink" || ids[4] != "tags" {
		t.Fatalf("config ids: %v", ids)
	}
	for i, prefix := range []string{"addrus", "improvesonarrcompatibilitybytrying", "improvesonarrcompatibilitybyreturning"} {
		if !strings.HasPrefix(ids[i+1], prefix) {
			t.Fatalf("config id %d: %q want prefix %q", i+1, ids[i+1], prefix)
		}
	}
}

func TestAnilibriaLastReleasesRequestAndParse(t *testing.T) {
	site := newAnilibriaSite(t, map[string]string{
		"/api/v1/anime/torrents/rss": anilibriaRSSXML,
		"/api/v1/anime/torrents/11":  anilibriaSingleJSON,
	})
	a := newTestAnilibria(t, site)

	res, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	if r.Title != "Главное / E01-E12 Сезон 1 [1-12] 1080p " {
		t.Fatalf("title: %q", r.Title)
	}
	if r.InfoHash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("infohash: %q", r.InfoHash)
	}
	if r.Size == nil || *r.Size != 2147483648 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 5 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 7 {
		t.Fatalf("peers (seeders+leechers): %v", r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 42 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Year == nil || *r.Year != 2024 {
		t.Fatalf("year: %v", r.Year)
	}
	if len(r.Category) == 0 || r.Category[0] != 5070 {
		t.Fatalf("category: %v", r.Category)
	}
	if r.PublishDate.Format("2006-01-02") != "2024-06-01" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.Path, "/api/v1/anime/torrents/abcdef0123456789abcdef0123456789abcdef01/file") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Guid == nil || !strings.HasSuffix(r.Guid.Path, "/anime/releases/release/main-title/abcdef0123456789abcdef0123456789abcdef01") {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.Path, "/anime/releases/release/main-title") {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Poster == nil || !strings.HasSuffix(r.Poster.Path, "/uploads/poster.jpg") {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}

	paths := site.paths()
	if len(paths) != 2 || paths[0] != "/api/v1/anime/torrents/rss?limit=1" || paths[1] != "/api/v1/anime/torrents/11" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestAnilibriaSearchRequestAndParse(t *testing.T) {
	site := newAnilibriaSite(t, map[string]string{
		"/api/v1/app/search/releases":      `[{"id": 7}, {"id": 7}, {"id": null}]`,
		"/api/v1/anime/torrents/release/7": anilibriaArrayJSON,
	})
	a := newTestAnilibria(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "fake query"
	res, err := a.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	if len(r.Category) == 0 || r.Category[0] != 5070 {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Year != nil {
		t.Fatalf("year must be nil for null JSON: %v", r.Year)
	}
	if r.Peers == nil || *r.Peers != 4 {
		t.Fatalf("peers: %v", r.Peers)
	}
	paths := site.paths()
	// duplicate + null ids are de-duplicated/filtered upstream
	if len(paths) != 2 || paths[0] != "/api/v1/app/search/releases?query=fake%20query" || paths[1] != "/api/v1/anime/torrents/release/7" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestAnilibriaApplyConfigurationEmptyIsError(t *testing.T) {
	site := newAnilibriaSite(t, map[string]string{
		"/api/v1/anime/torrents/rss": `<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`,
	})
	a := newTestAnilibria(t, site)
	_, err := a.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err == nil || err.Error() != "API unavailable or unknown error" {
		t.Fatalf("err: %v", err)
	}
	if a.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
	if a.SiteLink() != site.srv.URL+"/" {
		t.Fatalf("site link rewritten to default: %q", a.SiteLink())
	}
}

func TestAnilibriaPerReleaseFetchFailureSkips(t *testing.T) {
	site := newAnilibriaSite(t, map[string]string{
		"/api/v1/anime/torrents/rss": anilibriaRSSXML,
		// /api/v1/anime/torrents/11 missing → 404 → skipped
	})
	a := newTestAnilibria(t, site)
	res, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
}

func TestAnilibriaTitleVariants(t *testing.T) {
	site := newAnilibriaSite(t, map[string]string{
		"/api/v1/anime/torrents/rss": anilibriaRSSXML,
		"/api/v1/anime/torrents/11":  anilibriaSingleJSON,
	})
	a := newTestAnilibria(t, site)
	a.cfg.EnglishTitleOnly.Value = true
	a.cfg.AddRussianToTitle.Value = true
	a.cfg.AddSeasonToTitle.Value = true

	res, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	want := "Main Title S01E01-E12 Сезон 1 [1-12] 1080p  RUS"
	if res.Releases[0].Title != want {
		t.Fatalf("title: %q want %q", res.Releases[0].Title, want)
	}
}

func TestParseSeasonEpisodes(t *testing.T) {
	// Faithful to upstream quirks: the roman-numeral alternative can match an
	// empty string at the first word boundary, so the season stays "S01"
	// unless the label starts with an English "Season N" / "N Season" token.
	cases := []struct {
		label      string
		wantSeason string
		wantEp     string
	}{
		{"ТВ (1-12) 1080p", "S01", ""},
		{"ТВ-2 (13-24) 720p", "S01", ""},
		{"Season 3 [01] 1080p", "S03", "E01"},
		{"Сезон 4 (2020) [5] 1080p", "S01", "E05"},
		{"Part 2 [01-02] 1080p", "S01", "E01-E02"}, // Part number blocks season detection
		{"Season II [1] 1080p", "S01", "E01"},      // empty-match quirk: roman alt matches "" at pos 0
		{"Movie (2021) 1080p", "S01", ""},
		{"2 Season [3] 720p", "S02", "E03"},
	}
	for _, tc := range cases {
		season, ep := parseSeasonEpisodes(tc.label)
		if season != tc.wantSeason || ep != tc.wantEp {
			t.Fatalf("parseSeasonEpisodes(%q) = %q,%q want %q,%q", tc.label, season, ep, tc.wantSeason, tc.wantEp)
		}
	}
}

func TestRomanToArabic(t *testing.T) {
	for tc, want := range map[string]int{"I": 1, "IV": 4, "IX": 9, "XII": 12, "MCMXCIV": 1994, "II": 2} {
		if got := romanToArabic(tc); got != want {
			t.Fatalf("romanToArabic(%q) = %d want %d", tc, got, want)
		}
	}
}
