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

type bhdSite struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []string
	bodies []string
	serve  map[string]string
}

func newBHDSite(t *testing.T, serve map[string]string) *bhdSite {
	t.Helper()
	s := &bhdSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		s.bodies = append(s.bodies, string(body))
		payload, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

const (
	bhdTestAPIKey = "0123456789abcdef0123456789abcdef"
	bhdTestRSSKey = "fedcba9876543210fedcba9876543210"
)

func newTestBeyondHDAPI(t *testing.T, site *bhdSite) *BeyondHDAPI {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewBeyondHDAPI(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewBeyondHDAPI: %v", err)
	}
	b := ix.(*BeyondHDAPI)
	b.apiBase = site.srv.URL + "/api/torrents/"
	b.cfg.ApiKey.Value = bhdTestAPIKey
	b.cfg.RSSKey.Value = bhdTestRSSKey
	client.SetRequestDelay(b.ID(), 0)
	return b
}

func TestBeyondHDAPICaps(t *testing.T) {
	ix, err := NewBeyondHDAPI(services.Deps{})
	if err != nil {
		t.Fatalf("NewBeyondHDAPI: %v", err)
	}
	caps := ix.Capabilities()
	if caps.LimitsDefault == nil || *caps.LimitsDefault != 100 || caps.LimitsMax == nil || *caps.LimitsMax != 100 {
		t.Fatalf("limits: %v/%v", caps.LimitsDefault, caps.LimitsMax)
	}
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID, models.TVParamTmdbID}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{models.MovieParamQ, models.MovieParamImdbID, models.MovieParamTmdbID}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 2000}, {"2", 5000},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
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
	// desc mapping used by the parser
	if got := caps.Categories.MapTrackerCatDescToNewznab("Movies"); !containsInt(got, 2000) {
		t.Fatalf("MapTrackerCatDescToNewznab(Movies) = %v", got)
	}
	if got := caps.Categories.MapTrackerCatDescToNewznab("TV"); !containsInt(got, 5000) {
		t.Fatalf("MapTrackerCatDescToNewznab(TV) = %v", got)
	}
}

func containsInt(list []int, v int) bool {
	for _, n := range list {
		if n == v {
			return true
		}
	}
	return false
}

func TestBeyondHDAPIConfigForm(t *testing.T) {
	ix, err := NewBeyondHDAPI(services.Deps{})
	if err != nil {
		t.Fatalf("NewBeyondHDAPI: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	if len(ids) != 11 {
		t.Fatalf("config ids (%d): %v", len(ids), ids)
	}
	short := []struct {
		idx  int
		want string
	}{
		{0, "sitelink"},
		{1, "apikey"},
		{2, "rsskey"},
		{3, ""}, // instructions displayinfo (upstream Name "")
		{5, "filterfreeleech"},
		{6, "filterfreeleech(limitedul)"},
		{7, "filterrefund"},
		{8, "filterrewind"},
		{10, "tags"},
	}
	for _, tc := range short {
		if ids[tc.idx] != tc.want {
			t.Fatalf("config id %d: %q want %q (all: %v)", tc.idx, ids[tc.idx], tc.want, ids)
		}
	}
	if !strings.HasPrefix(ids[4], "includedv/hdr10") {
		t.Fatalf("config id 4: %q", ids[4])
	}
	if !strings.HasPrefix(ids[9], "selectthetypes") {
		t.Fatalf("config id 9: %q", ids[9])
	}
}

// TestBeyondHDAPIRequestGeneration asserts URL, method, headers and the exact
// ordered POST body for a fully-populated query.
func TestBeyondHDAPIRequestGeneration(t *testing.T) {
	site := newBHDSite(t, map[string]string{
		"/api/torrents/" + bhdTestAPIKey: `{"status_code":1,"results":[]}`,
	})
	b := newTestBeyondHDAPI(t, site)
	b.cfg.FilterFreeleech.Value = true
	b.cfg.FilterLimited.Value = true
	b.cfg.FilterRefund.Value = true
	b.cfg.FilterRewind.Value = true
	b.cfg.SearchTypes.Values = []string{"UHD 100", "BD 50"}
	b.cfg.AddHybridFeaturesToTitle.Value = true

	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.ImdbID = "tt1234567"
	q.Categories = []int{2000}
	q.Limit = 100
	q.Offset = 200

	res, err := b.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	site.mu.Lock()
	defer site.mu.Unlock()
	if len(site.hits) != 1 {
		t.Fatalf("hits: %v", site.hits)
	}
	if site.hits[0] != "POST /api/torrents/"+bhdTestAPIKey {
		t.Fatalf("hit: %v", site.hits[0])
	}
	wantBody := `{"action":"search","rsskey":"` + bhdTestRSSKey + `","search":"","freeleech":1,"limited":1,"refund":1,"rewind":1,"types":["UHD 100","BD 50"],"categories":[1],"imdb_id":"1234567","page":"3"}`
	if site.bodies[0] != wantBody {
		t.Fatalf("body:\n%s\nwant:\n%s", site.bodies[0], wantBody)
	}
}

func TestBeyondHDAPITMDBAndDailySearchBody(t *testing.T) {
	site := newBHDSite(t, map[string]string{
		"/api/torrents/" + bhdTestAPIKey: `{"status_code":1,"results":[]}`,
	})
	b := newTestBeyondHDAPI(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Some Show"
	tmdb := 55
	q.TmdbID = &tmdb

	if _, err := b.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	// daily-style episode: season carries the 4-digit year, episode MM/dd
	q2 := models.NewTorznabQuery()
	q2.QueryType = "tvsearch"
	q2.SearchTerm = "Some Show"
	season := 2024
	q2.Season = &season
	q2.Episode = "01/15"
	if _, err := b.ResultsForQuery(context.Background(), q2, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	site.mu.Lock()
	defer site.mu.Unlock()
	if len(site.bodies) != 2 {
		t.Fatalf("bodies: %d", len(site.bodies))
	}
	wantTmdb := `{"action":"search","rsskey":"` + bhdTestRSSKey + `","search":"Some Show","tmdb_id":"tv/55"}`
	if site.bodies[0] != wantTmdb {
		t.Fatalf("body[0]:\n%s\nwant:\n%s", site.bodies[0], wantTmdb)
	}
	wantDaily := `{"action":"search","rsskey":"` + bhdTestRSSKey + `","search":"Some Show 2024-01-15"}`
	if site.bodies[1] != wantDaily {
		t.Fatalf("body[1]:\n%s\nwant:\n%s", site.bodies[1], wantDaily)
	}
}

const bhdResultsJSON = `{
  "status_code": 1,
  "status_message": "Success",
  "results": [
    {
      "id": 123,
      "name": "Some Movie 2024 1080p",
      "info_hash": "abcdef0123456789abcdef0123456789abcdef01",
      "size": 2147483648,
      "category": "Movies",
      "times_completed": 42,
      "seeders": 5,
      "leechers": 2,
      "audios": "English, Japanese",
      "subtitles": "English",
      "imdb_id": "tt1234567",
      "tmdb_id": "movie/9988",
      "freeleech": true,
      "promo50": true,
      "created_at": "2024-01-15 10:30:00",
      "url": "https://beyond-hd.me/torrents/123",
      "download_url": "https://beyond-hd.me/download/123",
      "dv": true,
      "hdr10": true,
      "hdr10+": true,
      "hlg": false
    },
    {
      "id": 124,
      "name": "Some Show S01 2160p",
      "info_hash": "1111111111111111111111111111111111111111",
      "size": 1073741824,
      "category": "TV",
      "times_completed": 7,
      "seeders": 3,
      "leechers": 1,
      "promo25": true,
      "created_at": "2024-02-20 08:00:00",
      "url": "https://beyond-hd.me/torrents/124",
      "download_url": "https://beyond-hd.me/download/124"
    },
    {
      "id": 125,
      "name": "Skipped No Url",
      "category": "Movies",
      "created_at": "2024-02-20 08:00:00"
    }
  ]
}`

func TestBeyondHDAPIParseResponse(t *testing.T) {
	site := newBHDSite(t, map[string]string{
		"/api/torrents/" + bhdTestAPIKey: bhdResultsJSON,
	})
	b := newTestBeyondHDAPI(t, site)
	b.cfg.AddHybridFeaturesToTitle.Value = true

	res, err := b.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r := res.Releases[0]
	// hybrid features title: DV + HDR10 + HDR10+ (>1 → appended)
	if r.Title != "Some Movie 2024 1080p (Dolby Vision / HDR10 / HDR10+)" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Size == nil || *r.Size != 2147483648 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 5 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 7 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 42 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if !containsInt(r.Category, 2000) {
		t.Fatalf("category: %v", r.Category)
	}
	if r.PublishDate.UTC().Format("2006-01-02 15:04:05") != "2024-01-15 10:30:00" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Link == nil || r.Link.String() != "https://beyond-hd.me/download/123" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Guid == nil || r.Guid.String() != "https://beyond-hd.me/torrents/123" {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Details == nil || r.Details.String() != "https://beyond-hd.me/torrents/123" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.InfoHash != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("infohash: %q", r.InfoHash)
	}
	if strings.Join(r.Languages, ",") != "English,Japanese" {
		t.Fatalf("languages: %v", r.Languages)
	}
	if strings.Join(r.Subs, ",") != "English" {
		t.Fatalf("subs: %v", r.Subs)
	}
	if r.Imdb == nil || *r.Imdb != 1234567 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	if r.TMDb == nil || *r.TMDb != 9988 {
		t.Fatalf("tmdb: %v", r.TMDb)
	}
	// freeleech → 0, then promo50 overrides to .50
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0.50 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}

	r2 := res.Releases[1]
	if r2.Title != "Some Show S01 2160p" {
		t.Fatalf("title2: %q", r2.Title)
	}
	if !containsInt(r2.Category, 5000) {
		t.Fatalf("category2: %v", r2.Category)
	}
	// no freeleech/limited, promo25 → .75
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 0.75 {
		t.Fatalf("dvf2: %v", r2.DownloadVolumeFactor)
	}
	if len(r2.Languages) != 0 || len(r2.Subs) != 0 {
		t.Fatalf("languages2/subs2: %v/%v", r2.Languages, r2.Subs)
	}
}

func TestBeyondHDAPIErrorPaths(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"html", `<html><body>down</body></html>`, "The response was not JSON"},
		{"invalid key", `{"status_code":0,"status_message":"Invalid API Key"}`, "API Key invalid or not authorized"},
		{"status zero", `{"status_code":0,"status_message":"Something broke"}`, "Indexer Error: Something broke"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			site := newBHDSite(t, map[string]string{
				"/api/torrents/" + bhdTestAPIKey: tc.body,
			})
			b := newTestBeyondHDAPI(t, site)
			_, err := b.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
			if err == nil {
				t.Fatal("expected error")
			}
			// ResultsForQueryWith wraps into IndexerError; compare the message
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err: %v want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestBeyondHDAPIApplyConfiguration(t *testing.T) {
	site := newBHDSite(t, map[string]string{
		"/api/torrents/" + bhdTestAPIKey: bhdResultsJSON,
	})

	// invalid key lengths are rejected before any request
	b := newTestBeyondHDAPI(t, site)
	_, err := b.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: "short"},
		{ID: "rsskey", Value: bhdTestRSSKey},
	})
	if err == nil || err.Error() != "Invalid API Key configured: expected length: 32, got 5" {
		t.Fatalf("err: %v", err)
	}
	b.cfg.ApiKey.Value = bhdTestAPIKey
	b.cfg.RSSKey.Value = ""
	_, err = b.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err == nil || err.Error() != "Missing RSS Key." {
		t.Fatalf("err: %v", err)
	}

	// success path configures and saves
	b2 := newTestBeyondHDAPI(t, site)
	status, err := b2.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: bhdTestAPIKey},
		{ID: "rsskey", Value: bhdTestRSSKey},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !b2.IsConfigured() {
		t.Fatalf("status: %v configured: %v", status, b2.IsConfigured())
	}
	if b2.SiteLink() != site.srv.URL+"/" {
		t.Fatalf("sitelink: %q", b2.SiteLink())
	}

	// empty result set fails the configuration test
	siteEmpty := newBHDSite(t, map[string]string{
		"/api/torrents/" + bhdTestAPIKey: `{"status_code":1,"results":[]}`,
	})
	b3 := newTestBeyondHDAPI(t, siteEmpty)
	_, err = b3.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: siteEmpty.srv.URL + "/"},
		{ID: "apikey", Value: bhdTestAPIKey},
		{ID: "rsskey", Value: bhdTestRSSKey},
	})
	if err == nil || err.Error() != "Testing returned no results!" {
		t.Fatalf("err: %v", err)
	}
	if b3.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}
