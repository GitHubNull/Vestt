package manual

import (
	"context"
	"encoding/base64"
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

type fileListSite struct {
	srv     *httptest.Server
	mu      sync.Mutex
	hits    []string
	headers []string
	serve   map[string]string
	status  int
}

func newFileListSite(t *testing.T, serve map[string]string) *fileListSite {
	t.Helper()
	s := &fileListSite{serve: serve, status: http.StatusOK}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		s.headers = append(s.headers, r.Header.Get("Authorization"))
		page, ok := s.serve[r.URL.Path]
		status := s.status
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *fileListSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *fileListSite) authHeader() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.headers) == 0 {
		return ""
	}
	return s.headers[0]
}

func newTestFileList(t *testing.T, site *fileListSite) *FileList {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewFileList(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewFileList: %v", err)
	}
	f := ix.(*FileList)
	f.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(f.ID(), 0)
	return f
}

const fileListLatestJSON = `[
  {
    "id": 123456,
    "name": "Some.Movie.2020.1080p.BluRay.x264-GRP",
    "download_link": "https://filelist.io/download.php?id=123456&passkey=abc",
    "size": 1610612736,
    "leechers": 3,
    "seeders": 12,
    "times_completed": 42,
    "files": 3,
    "imdb": "tt1234567",
    "internal": false,
    "freeleech": true,
    "doubleup": true,
    "upload_date": "2024-03-15 21:08:37",
    "category": "Movies HD",
    "small_description": "1080p BluRay"
  },
  {
    "id": 123457,
    "name": "Other.Show.S01E01.720p",
    "download_link": "https://filelist.io/download.php?id=123457&passkey=abc",
    "size": 524288000,
    "leechers": 0,
    "seeders": 5,
    "times_completed": 7,
    "files": 1,
    "imdb": "",
    "internal": true,
    "freeleech": false,
    "doubleup": false,
    "upload_date": "2024-03-16 08:00:00",
    "category": "TV-Series HD",
    "small_description": ""
  }
]`

func TestFileListCaps(t *testing.T) {
	ix, err := NewFileList(services.Deps{})
	if err != nil {
		t.Fatalf("NewFileList: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != strings.Join([]string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join([]string{models.MovieParamQ, models.MovieParamImdbID}, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if !caps.TVSearchImdbAvailable {
		t.Fatal("TVSearchImdbAvailable must be true")
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"24", 5070}, {"8", 4000}, {"25", 2060}, {"6", 2045},
		{"26", 2050}, {"2", 2070}, {"4", 2040}, {"1", 2030},
		{"11", 3000}, {"13", 5060}, {"27", 5045}, {"21", 5040},
		{"23", 5030}, {"7", 6000}, {"28", 2010}, {"28", 5020},
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

func TestFileListConfigForm(t *testing.T) {
	ix, err := NewFileList(services.Deps{})
	if err != nil {
		t.Fatalf("NewFileList: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// sitelink, username, passkey, instructions(""), searchfreeleechonly,
	// categorywarning, tags
	want := []string{"sitelink", "username", "passkey", "", "searchfreeleechonly", "categorywarning", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestFileListApplyConfigurationOK(t *testing.T) {
	site := newFileListSite(t, map[string]string{
		"/api.php": fileListLatestJSON,
	})
	f := newTestFileList(t, site)

	status, err := f.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "passkey", Value: "secretpasskey"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v, want ConfigCompleted", status)
	}
	if !f.IsConfigured() {
		t.Fatal("must be configured after a successful test query")
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:secretpasskey"))
	if got := site.authHeader(); got != wantAuth {
		t.Fatalf("Authorization: %q want %q", got, wantAuth)
	}
}

func TestFileListApplyConfigurationNoReleases(t *testing.T) {
	site := newFileListSite(t, map[string]string{
		"/api.php": `[]`,
	})
	f := newTestFileList(t, site)

	_, err := f.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "passkey", Value: "secretpasskey"},
	})
	if err == nil || err.Error() != "Could not find releases." {
		t.Fatalf("err: %v", err)
	}
	if f.IsConfigured() {
		t.Fatal("must not be configured when no releases are found")
	}
}

func TestFileListLatestRequestAndParse(t *testing.T) {
	site := newFileListSite(t, map[string]string{
		"/api.php": fileListLatestJSON,
	})
	f := newTestFileList(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := f.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	req := site.paths()[0]
	if !strings.HasPrefix(req, "GET /api.php?") ||
		!strings.Contains(req, "category=") ||
		!strings.Contains(req, "action=latest-torrents") {
		t.Fatalf("request: %q", req)
	}

	r := res.Releases[0]
	if r.Title != "Some.Movie.2020.1080p.BluRay.x264-GRP" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Guid == nil || !strings.HasSuffix(r.Guid.Path, "details.php") || r.Guid.Query().Get("id") != "123456" {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Details == nil || r.Details.String() != r.Guid.String() {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Link == nil || !strings.Contains(r.Link.String(), "download.php?id=123456") {
		t.Fatalf("link: %v", r.Link)
	}
	// 2024-03-15 21:08:37 +0200 → 19:08:37 UTC
	if r.PublishDate.Format("2006-01-02 15:04:05") != "2024-03-15 19:08:37" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Size == nil || *r.Size != 1610612736 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Files == nil || *r.Files != 3 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.Grabs == nil || *r.Grabs != 42 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 12 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 15 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.Imdb == nil || *r.Imdb != 1234567 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	found := false
	for _, id := range r.Category {
		if id == 2040 { // Movies HD (desc-mapped)
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v, want it to contain 2040", r.Category)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf (freeleech): %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 2 {
		t.Fatalf("uvf (doubleup): %v", r.UploadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 172800 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}

	// second row: not freeleech, no doubleup, no imdb
	r2 := res.Releases[1]
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf 2: %v", r2.DownloadVolumeFactor)
	}
	if r2.UploadVolumeFactor == nil || *r2.UploadVolumeFactor != 1 {
		t.Fatalf("uvf 2: %v", r2.UploadVolumeFactor)
	}
	if r2.Imdb != nil {
		t.Fatalf("imdb 2 must be nil: %v", r2.Imdb)
	}
	found = false
	for _, id := range r2.Category {
		if id == 5040 { // TV-Series HD
			found = true
		}
	}
	if !found {
		t.Fatalf("category 2: %v, want it to contain 5040", r2.Category)
	}
}

func TestFileListSearchRequest(t *testing.T) {
	site := newFileListSite(t, map[string]string{
		"/api.php": fileListLatestJSON,
	})
	f := newTestFileList(t, site)

	season := 2
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Some Show"
	q.Season = &season
	q.Episode = "5"
	q.Categories = []int{5040} // TV HD → cat 21

	_, err := f.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	req := site.paths()[0]
	for _, fragment := range []string{
		"action=search-torrents", "type=name", "query=Some+Show",
		"season=2", "episode=5", "category=21",
	} {
		if !strings.Contains(req, fragment) {
			t.Fatalf("request %q missing %q", req, fragment)
		}
	}
}

func TestFileListImdbRequest(t *testing.T) {
	site := newFileListSite(t, map[string]string{
		"/api.php": fileListLatestJSON,
	})
	f := newTestFileList(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "movie"
	q.ImdbID = "tt7654321"

	_, err := f.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	req := site.paths()[0]
	for _, fragment := range []string{"action=search-torrents", "type=imdb", "query=tt7654321"} {
		if !strings.Contains(req, fragment) {
			t.Fatalf("request %q missing %q", req, fragment)
		}
	}
}

func TestFileListDailyEpisodeSkipsImdbSearch(t *testing.T) {
	site := newFileListSite(t, map[string]string{
		"/api.php": fileListLatestJSON,
	})
	f := newTestFileList(t, site)

	season := 2024
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.ImdbID = "tt7654321"
	q.Season = &season
	q.Episode = "03/15"

	// upstream: skip ID searches for daily episodes → no provider call, no results
	res, err := f.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if len(site.paths()) != 0 {
		t.Fatalf("hits: %v", site.paths())
	}
}

func TestFileListErrorPayload(t *testing.T) {
	site := newFileListSite(t, map[string]string{
		"/api.php": `{"error":"Invalid passkey"}`,
	})
	f := newTestFileList(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := f.ResultsForQuery(context.Background(), q, false)
	if err == nil || !strings.Contains(err.Error(), "Invalid passkey") {
		t.Fatalf("err: %v", err)
	}
}

func TestFileListFreeleechFilter(t *testing.T) {
	site := newFileListSite(t, map[string]string{
		"/api.php": fileListLatestJSON,
	})
	f := newTestFileList(t, site)
	f.cfg.Freeleech.Value = true

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := f.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 || *res.Releases[0].DownloadVolumeFactor != 0 {
		t.Fatalf("releases: %+v", res.Releases)
	}
	req := site.paths()[0]
	if !strings.Contains(req, "freeleech=1") {
		t.Fatalf("request %q missing freeleech=1", req)
	}
}
