package manual

import (
	"context"
	"errors"
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

type nblSite struct {
	srv *httptest.Server

	mu   sync.Mutex
	hits []string
	resp map[string]nblResponse
}

type nblResponse struct {
	status int
	body   string
}

func newNBLSite(t *testing.T, resp map[string]nblResponse) *nblSite {
	t.Helper()
	s := &nblSite{resp: resp}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		res, ok := s.resp[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if res.status != 0 {
			w.WriteHeader(res.status)
		}
		_, _ = w.Write([]byte(res.body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *nblSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

const nblAPIKey = "0123456789abcdef0123456789abcdef"

func newTestNBL(t *testing.T, site *nblSite) *NebulanceAPI {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewNebulanceAPI(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewNebulanceAPI: %v", err)
	}
	n := ix.(*NebulanceAPI)
	n.SiteLinkValue = site.srv.URL + "/"
	n.cfg.Key.Value = nblAPIKey
	client.SetRequestDelay(n.ID(), 0)
	return n
}

const nblSearchFixture = `{
  "total_results": 2,
  "items": [
    {
      "rls_name": "Some.Show.S02E05.1080p.WEB-DL-GRP",
      "cat": "episode",
      "size": 1500000000,
      "seed": 12,
      "leech": 3,
      "snatch": 40,
      "download": "/download.php/12345",
      "file_list": ["a.mkv", "b.nfo"],
      "group_name": "Some Show",
      "group_id": 12345,
      "imdb_id": "tt0133093",
      "tvmaze_id": 42,
      "rls_utc": "2024-03-01 12:34:56",
      "tags": ["HD", "Drama", "1080p", "nonsense-tag"]
    },
    {
      "rls_name": "",
      "cat": "SEASON",
      "size": 8000000000,
      "seed": 5,
      "leech": 1,
      "snatch": 10,
      "download": "/download.php/999",
      "file_list": [],
      "group_name": "Some Show S01",
      "group_id": 999,
      "imdb_id": "",
      "tvmaze_id": null,
      "rls_utc": "2024-02-01 00:00:00",
      "tags": ["sd"]
    }
  ]
}`

func TestNebulanceAPICaps(t *testing.T) {
	ix, err := NewNebulanceAPI(services.Deps{})
	if err != nil {
		t.Fatalf("NewNebulanceAPI: %v", err)
	}
	caps := ix.Capabilities()
	wantParams := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamTvmazeID}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantParams, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if caps.LimitsDefault == nil || *caps.LimitsDefault != 100 || caps.LimitsMax == nil || *caps.LimitsMax != 1000 {
		t.Fatalf("limits: %v/%v", caps.LimitsDefault, caps.LimitsMax)
	}
	if !caps.SupportsRawSearch {
		t.Fatal("SupportsRawSearch must be true")
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"tv", 5000}, {"sd", 5030}, {"hd", 5040}, {"uhd", 5045}, {"4k", 5045},
		{"480p", 5030}, {"720p", 5040}, {"1080p", 5040}, {"1080i", 5040}, {"2160p", 5045},
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
}

func TestNebulanceAPIConfigForm(t *testing.T) {
	ix, err := NewNebulanceAPI(services.Deps{})
	if err != nil {
		t.Fatalf("NewNebulanceAPI: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	var infos []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
		if it["type"].(string) == "displayinfo" {
			infos = append(infos, it["value"].(string))
		}
	}
	// sitelink, apikey, keyInfo(""), loginRequirements(""), tags
	want := []string{"sitelink", "apikey", "", "", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
	var keyInfo, loginReq bool
	for _, v := range infos {
		if strings.Contains(v, "API Keys") {
			keyInfo = true
		}
		if strings.Contains(v, "login requirements") {
			loginReq = true
		}
	}
	if !keyInfo || !loginReq {
		t.Fatalf("dynamic info items missing: %v", infos)
	}
}

func nblQuery() *models.TorznabQuery {
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Some Show"
	return q
}

func TestNebulanceAPIRequestGeneration(t *testing.T) {
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: nblSearchFixture},
	})
	n := newTestNBL(t, site)

	if _, err := n.ResultsForQuery(context.Background(), nblQuery(), false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("paths: %v", paths)
	}
	want := "/api.php?action=search&api_key=" + nblAPIKey + "&age=%3E0&per_page=100&release=Some+Show"
	if paths[0] != want {
		t.Fatalf("request: %q want %q", paths[0], want)
	}
}

func TestNebulanceAPIRequestPaginationQuirk(t *testing.T) {
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: nblSearchFixture},
	})
	n := newTestNBL(t, site)

	// upstream quirk: page = offset/limit (no +1)
	q := nblQuery()
	q.Limit = 100
	q.Offset = 200
	if _, err := n.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 1 || !strings.Contains(paths[0], "&page=2&") {
		t.Fatalf("paths: %v", paths)
	}
}

func TestNebulanceAPIRequestSeasonEpisode(t *testing.T) {
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: nblSearchFixture},
	})
	n := newTestNBL(t, site)

	season := 2
	q := nblQuery()
	q.Season = &season
	q.Episode = "5"
	if _, err := n.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("paths: %v", paths)
	}
	if !strings.Contains(paths[0], "season=2") || !strings.Contains(paths[0], "episode=5") {
		t.Fatalf("season/episode params missing: %q", paths[0])
	}
}

func TestNebulanceAPIRequestDailyDate(t *testing.T) {
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: nblSearchFixture},
	})
	n := newTestNBL(t, site)

	// daily show: season=yyyy, episode=MM/dd → release=yyyy.MM.dd (+name)
	season := 2024
	q := nblQuery()
	q.Season = &season
	q.Episode = "03/15"
	if _, err := n.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("paths: %v", paths)
	}
	if !strings.Contains(paths[0], "name=Some+Show") || !strings.Contains(paths[0], "release=2024.03.15") {
		t.Fatalf("daily params missing: %q", paths[0])
	}
}

func TestNebulanceAPIRequestTvmaze(t *testing.T) {
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: nblSearchFixture},
	})
	n := newTestNBL(t, site)

	tvmaze := 42
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.TvmazeID = &tvmaze
	if _, err := n.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 1 || !strings.Contains(paths[0], "tvmaze=42") {
		t.Fatalf("paths: %v", paths)
	}
}

func TestNebulanceAPIRequestGuards(t *testing.T) {
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: nblSearchFixture},
	})
	n := newTestNBL(t, site)

	// season-only query (no name/release/tvmaze) → upstream returns an empty
	// request chain: no HTTP request at all.
	season := 2
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.Season = &season
	res, err := n.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if paths := site.paths(); len(paths) != 0 {
		t.Fatalf("no request expected, got %v", paths)
	}

	// release/name shorter than 3 chars → empty chain as well
	q2 := models.NewTorznabQuery()
	q2.QueryType = "tvsearch"
	q2.SearchTerm = "ab"
	res, err = n.ResultsForQuery(context.Background(), q2, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if paths := site.paths(); len(paths) != 0 {
		t.Fatalf("no request expected, got %v", paths)
	}
}

func TestNebulanceAPIParseFixture(t *testing.T) {
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: nblSearchFixture},
	})
	n := newTestNBL(t, site)

	res, err := n.ResultsForQuery(context.Background(), nblQuery(), false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r := res.Releases[0]
	if r.Title != "Some.Show.S02E05.1080p.WEB-DL-GRP" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Size == nil || *r.Size != 1500000000 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Files == nil || *r.Files != 2 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.Grabs == nil || *r.Grabs != 40 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 12 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 15 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.PublishDate.UTC().Format("2006-01-02 15:04:05") != "2024-03-01 12:34:56" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	// category: first valid category tag in upstream HashSet order ("hd" < "1080p")
	found := false
	for _, c := range r.Category {
		if c == 5040 {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v", r.Category)
	}
	if strings.Join(r.Genres, ",") != "drama" {
		t.Fatalf("genres: %v", r.Genres)
	}
	if !strings.Contains(r.Description, "Group Name: Some Show") || !strings.Contains(r.Description, "Genre: drama") {
		t.Fatalf("description: %q", r.Description)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf (ratioless): %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 0 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}
	// cat "episode" → 24h seed time
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 86400 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}
	if r.Imdb == nil || *r.Imdb != 133093 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	if r.TVMazeId == nil || *r.TVMazeId != 42 {
		t.Fatalf("tvmaze: %v", r.TVMazeId)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.Path, "/torrents.php") || r.Details.Query().Get("id") != "12345" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Link == nil || r.Link.Path != "/download.php/12345" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Guid == nil || r.Guid.String() != r.Details.String() {
		t.Fatalf("guid: %v", r.Guid)
	}

	// second item: empty rls_name → group name; SEASON → 120h seed time; sd category
	r2 := res.Releases[1]
	if r2.Title != "Some Show S01" {
		t.Fatalf("title2: %q", r2.Title)
	}
	if r2.MinimumSeedTime == nil || *r2.MinimumSeedTime != 432000 {
		t.Fatalf("minimum seed time2: %v", r2.MinimumSeedTime)
	}
	if r2.Imdb != nil || r2.TVMazeId != nil {
		t.Fatalf("ids2 must be nil: %v %v", r2.Imdb, r2.TVMazeId)
	}
	found = false
	for _, c := range r2.Category {
		if c == 5030 {
			found = true
		}
	}
	if !found {
		t.Fatalf("category2: %v", r2.Category)
	}
}

func TestNebulanceAPIEmptyResultIsNotAnError(t *testing.T) {
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: `{"total_results":0,"items":[]}`},
	})
	n := newTestNBL(t, site)
	res, err := n.ResultsForQuery(context.Background(), nblQuery(), false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
}

func TestNebulanceAPIErrorMapping(t *testing.T) {
	var ie *indexer.IndexerError

	// "Invalid params" body → Kind Config
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: `Invalid params`},
	})
	n := newTestNBL(t, site)
	_, err := n.ResultsForQuery(context.Background(), nblQuery(), false)
	if !errors.As(err, &ie) {
		t.Fatalf("expected IndexerError, got %T: %v", err, err)
	}
	if ie.Kind != indexer.KindConfig || ie.Message != "Invalid API Key configured" {
		t.Fatalf("kind/message: %v %q", ie.Kind, ie.Message)
	}

	// API error payload → Kind Auth
	site2 := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: `{"total_results":0,"items":null,"error":{"message":"bad key"}}`},
	})
	n2 := newTestNBL(t, site2)
	_, err = n2.ResultsForQuery(context.Background(), nblQuery(), false)
	if !errors.As(err, &ie) {
		t.Fatalf("expected IndexerError, got %T: %v", err, err)
	}
	if ie.Kind != indexer.KindAuth || !strings.Contains(ie.Message, "bad key") {
		t.Fatalf("kind/message: %v %q", ie.Kind, ie.Message)
	}

	// "API is down" marker
	site3 := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: `API is down for maintenance`},
	})
	n3 := newTestNBL(t, site3)
	_, err = n3.ResultsForQuery(context.Background(), nblQuery(), false)
	if err == nil || !strings.Contains(err.Error(), "NBL API is down at the moment") {
		t.Fatalf("err: %v", err)
	}

	// non-OK status → Kind Auth with upstream status message
	site4 := newNBLSite(t, map[string]nblResponse{
		"/api.php": {status: http.StatusInternalServerError, body: `oops`},
	})
	n4 := newTestNBL(t, site4)
	_, err = n4.ResultsForQuery(context.Background(), nblQuery(), false)
	if !errors.As(err, &ie) {
		t.Fatalf("expected IndexerError, got %T: %v", err, err)
	}
	if ie.Kind != indexer.KindAuth || !strings.Contains(ie.Message, "500") {
		t.Fatalf("kind/message: %v %q", ie.Kind, ie.Message)
	}
}

func TestNebulanceAPIApplyConfiguration(t *testing.T) {
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: nblSearchFixture},
	})
	n := newTestNBL(t, site)
	n.cfg.Key.Value = ""

	status, err := n.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: nblAPIKey},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v", status)
	}
	if !n.IsConfigured() {
		t.Fatal("must be configured after success")
	}
	if n.SiteLink() != site.srv.URL+"/" {
		t.Fatalf("site link: %q", n.SiteLink())
	}
}

func TestNebulanceAPIApplyConfigurationBadKeyLength(t *testing.T) {
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: nblSearchFixture},
	})
	n := newTestNBL(t, site)
	n.cfg.Key.Value = ""

	_, err := n.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: "short"},
	})
	if err == nil || err.Error() != "Invalid API Key configured: expected length: 32, got 5" {
		t.Fatalf("err: %v", err)
	}
	if n.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
	if paths := site.paths(); len(paths) != 0 {
		t.Fatalf("no request expected with a bad key, got %v", paths)
	}
}

func TestNebulanceAPIApplyConfigurationEmptyResults(t *testing.T) {
	site := newNBLSite(t, map[string]nblResponse{
		"/api.php": {body: `{"total_results":0,"items":[]}`},
	})
	n := newTestNBL(t, site)

	_, err := n.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apikey", Value: nblAPIKey},
	})
	if err == nil || err.Error() != "Testing returned no results!" {
		t.Fatalf("err: %v", err)
	}
}
