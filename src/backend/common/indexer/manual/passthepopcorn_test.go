package manual

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type ptpSite struct {
	srv *httptest.Server

	mu       sync.Mutex
	hits     []string
	lastUser string
	lastKey  string
	respond  map[string]ptpResponse
}

type ptpResponse struct {
	status int
	body   string
}

func newPTPSite(t *testing.T, respond map[string]ptpResponse) *ptpSite {
	t.Helper()
	s := &ptpSite{respond: respond}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.lastUser = r.Header.Get("ApiUser")
		s.lastKey = r.Header.Get("ApiKey")
		resp, ok := s.respond[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if resp.status != 0 {
			w.WriteHeader(resp.status)
		}
		_, _ = w.Write([]byte(resp.body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *ptpSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *ptpSite) authHeaders() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUser, s.lastKey
}

func newTestPTP(t *testing.T, site *ptpSite) *PassThePopcorn {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewPassThePopcorn(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewPassThePopcorn: %v", err)
	}
	p := ix.(*PassThePopcorn)
	p.searchBase = site.srv.URL + "/"
	// tests must not pay the upstream 4s request delay
	client.SetRequestDelay(p.ID(), 0)
	return p
}

const ptpSearchFixture = `{
  "TotalResults": "2",
  "Movies": [
    {
      "GroupId": "123456",
      "CategoryId": "1",
      "Title": "Some Movie",
      "Year": "2024",
      "ImdbId": "0133093",
      "Cover": "https://ptpimg.me/abc123.jpg",
      "Tags": ["drama", "sci-fi"],
      "Torrents": [
        {
          "Id": 1001,
          "Quality": "High Quality",
          "Source": "Blu-ray",
          "Container": "MKV",
          "Codec": "x264",
          "Resolution": "1080p",
          "Scene": false,
          "Size": "2000000000",
          "UploadTime": "2024-03-01 10:20:30",
          "RemasterTitle": "",
          "Snatched": "12",
          "Seeders": "5",
          "Leechers": "2",
          "ReleaseName": "Some.Movie.2024.1080p.BluRay.x264-GRP",
          "Checked": true,
          "GoldenPopcorn": true,
          "FreeleechType": "Freeleech"
        },
        {
          "Id": 1002,
          "Quality": "",
          "Source": "",
          "Container": "",
          "Codec": "",
          "Resolution": "",
          "Scene": false,
          "Size": "1000000000",
          "UploadTime": "2024-03-02 11:22:33",
          "RemasterTitle": "",
          "Snatched": "3",
          "Seeders": "1",
          "Leechers": "0",
          "ReleaseName": "Some.Movie.2024.DVDRip-OTHER",
          "Checked": false,
          "GoldenPopcorn": false,
          "FreeleechType": ""
        }
      ]
    }
  ]
}`

func TestPassThePopcornCaps(t *testing.T) {
	ix, err := NewPassThePopcorn(services.Deps{})
	if err != nil {
		t.Fatalf("NewPassThePopcorn: %v", err)
	}
	caps := ix.Capabilities()
	wantParams := []string{models.MovieParamQ, models.MovieParamImdbID}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantParams, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if caps.LimitsDefault == nil || *caps.LimitsDefault != 50 || caps.LimitsMax == nil || *caps.LimitsMax != 50 {
		t.Fatalf("limits: %v/%v", caps.LimitsDefault, caps.LimitsMax)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 2000}, {"2", 2000}, {"3", 2000}, {"4", 2000}, {"5", 2000}, {"6", 2000},
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

func TestPassThePopcornConfigForm(t *testing.T) {
	ix, err := NewPassThePopcorn(services.Deps{})
	if err != nil {
		t.Fatalf("NewPassThePopcorn: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{
		"sitelink", "apiauthentication", "apiuser", "apikey",
		"includereleaseattributesinthetitle", "", "filters(optional)", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
}

func ptpQuery() *models.TorznabQuery {
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	return q
}

func TestPassThePopcornRequestHeadersAndParse(t *testing.T) {
	site := newPTPSite(t, map[string]ptpResponse{
		"/torrents.php": {body: ptpSearchFixture},
	})
	p := newTestPTP(t, site)
	p.cfg.User.Value = "ptpuser"
	p.cfg.Key.Value = "ptpkey"
	p.cfg.AddAttributesToTitle.Value = true

	res, err := p.ResultsForQuery(context.Background(), ptpQuery(), false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// --- ApiUser/ApiKey header assertion (SPEC-09 §4 专项) ---
	user, key := site.authHeaders()
	if user != "ptpuser" || key != "ptpkey" {
		t.Fatalf("auth headers: %q/%q", user, key)
	}

	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("paths: %v", paths)
	}
	want := "/torrents.php?action=advanced&json=noredirect&grouping=0&order_by=time&order_way=desc&searchstr=Some+Movie"
	if paths[0] != want {
		t.Fatalf("request: %q want %q", paths[0], want)
	}

	r := res.Releases[0]
	// fixture parse assertions (>= 6 fields)
	if r.Title != "Some.Movie.2024.1080p.BluRay.x264-GRP [1080p / Blu-ray / x264 / MKV / Checked / Golden Popcorn]" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Year == nil || *r.Year != 2024 {
		t.Fatalf("year: %v", r.Year)
	}
	if r.Size == nil || *r.Size != 2000000000 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Grabs == nil || *r.Grabs != 12 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 5 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 7 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.PublishDate.Format("2006-01-02 15:04:05") != "2024-03-01 10:20:30" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Imdb == nil || *r.Imdb != 133093 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	found := false
	for _, c := range r.Category {
		if c == 2000 {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v", r.Category)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 || r.MinimumSeedTime == nil || *r.MinimumSeedTime != 345600 {
		t.Fatalf("minimums: %v %v", r.MinimumRatio, r.MinimumSeedTime)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.String(), "torrents.php?action=download&id=1001") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.String(), "torrents.php?id=123456&torrentid=1001") {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Guid == nil || r.Guid.String() != r.Details.String() {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Poster == nil || r.Poster.String() != "https://ptpimg.me/abc123.jpg" {
		t.Fatalf("poster: %v", r.Poster)
	}
	if strings.Join(r.Genres, ",") != "drama,sci-fi" {
		t.Fatalf("genres: %v", r.Genres)
	}
	if !strings.Contains(r.Description, "<br>\nYear: 2024") || !strings.Contains(r.Description, "<br>\nGolden Popcorn") {
		t.Fatalf("description: %q", r.Description)
	}

	// --- magnet/infohash mixed-return handling (SPEC-09 §4 专项) ---
	// PTP returns a download URL only; as a private tracker Base.FixResults
	// must NOT synthesize a magnet from an infohash (none present either).
	if r.InfoHash != "" || r.MagnetUri != nil {
		t.Fatalf("infohash/magnet must stay empty for PTP downloads: %q %v", r.InfoHash, r.MagnetUri)
	}

	// second torrent: plain attributes, no freeleech
	r2 := res.Releases[1]
	if r2.Title != "Some.Movie.2024.DVDRip-OTHER" {
		t.Fatalf("title2: %q", r2.Title)
	}
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf2: %v", r2.DownloadVolumeFactor)
	}
}

func TestPassThePopcornImdbQueryBranch(t *testing.T) {
	site := newPTPSite(t, map[string]ptpResponse{
		"/torrents.php": {body: ptpSearchFixture},
	})
	p := newTestPTP(t, site)

	// IMDb 查询分支：searchstr 直接使用 imdb id，忽略搜索词
	q := models.NewTorznabQuery()
	q.QueryType = "movie"
	q.SearchTerm = "Some Movie"
	q.ImdbID = "tt0133093"
	if _, err := p.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 1 || !strings.Contains(paths[0], "searchstr=tt0133093") {
		t.Fatalf("paths: %v", paths)
	}
}

func TestPassThePopcornFilterParamsAndGoldenOnly(t *testing.T) {
	site := newPTPSite(t, map[string]ptpResponse{
		"/torrents.php": {body: ptpSearchFixture},
	})
	p := newTestPTP(t, site)
	p.cfg.FilterString.Value = "GoldenPopcorn Free"

	res, err := p.ResultsForQuery(context.Background(), ptpQuery(), false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// golden-popcorn + free filter: only the first torrent survives
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("paths: %v", paths)
	}
	if !strings.Contains(paths[0], "freetorrent=1") || !strings.Contains(paths[0], "scene=2") {
		t.Fatalf("filter params missing: %q", paths[0])
	}
}

func TestPassThePopcornCategoryAndPaginationParams(t *testing.T) {
	site := newPTPSite(t, map[string]ptpResponse{
		"/torrents.php": {body: ptpSearchFixture},
	})
	p := newTestPTP(t, site)

	// no search string + categories → filter_cat[N]=1
	q := models.NewTorznabQuery()
	q.QueryType = "movie"
	q.Categories = []int{2000}
	q.Limit = 50
	q.Offset = 50 // page = 50/50 + 1 = 2
	res, err := p.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) == 0 {
		t.Fatal("expected releases")
	}
	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("paths: %v", paths)
	}
	if !strings.Contains(paths[0], "filter_cat[1]=1") || !strings.Contains(paths[0], "page=2") {
		t.Fatalf("cat/page params missing: %q", paths[0])
	}
}

func TestPassThePopcornEmptyResultIsNotAnError(t *testing.T) {
	site := newPTPSite(t, map[string]ptpResponse{
		"/torrents.php": {body: `{"TotalResults":"0","Movies":[]}`},
	})
	p := newTestPTP(t, site)
	res, err := p.ResultsForQuery(context.Background(), ptpQuery(), false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
}

func TestPassThePopcornAuthErrorMapping(t *testing.T) {
	// 401 with a JSON error body → Kind Auth
	site401 := newPTPSite(t, map[string]ptpResponse{
		"/torrents.php": {status: http.StatusUnauthorized, body: `{"error":"Invalid API Key"}`},
	})
	p := newTestPTP(t, site401)
	_, err := p.ResultsForQuery(context.Background(), ptpQuery(), false)
	if err == nil {
		t.Fatal("expected error")
	}
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) {
		t.Fatalf("expected IndexerError, got %T: %v", err, err)
	}
	if ie.Kind != indexer.KindAuth || ie.Message != "Invalid API Key" {
		t.Fatalf("kind/message: %v %q", ie.Kind, ie.Message)
	}

	// 400 → Kind Config
	site400 := newPTPSite(t, map[string]ptpResponse{
		"/torrents.php": {status: http.StatusBadRequest, body: `{"error":"bad request"}`},
	})
	p2 := newTestPTP(t, site400)
	_, err = p2.ResultsForQuery(context.Background(), ptpQuery(), false)
	if !errors.As(err, &ie) {
		t.Fatalf("expected IndexerError, got %T: %v", err, err)
	}
	if ie.Kind != indexer.KindConfig {
		t.Fatalf("kind: %v", ie.Kind)
	}
}

func TestPassThePopcornApplyConfiguration(t *testing.T) {
	site := newPTPSite(t, map[string]ptpResponse{
		"/torrents.php": {body: ptpSearchFixture},
	})
	p := newTestPTP(t, site)

	status, err := p.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "apiuser", Value: "ptpuser"},
		{ID: "apikey", Value: "ptpkey"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v", status)
	}
	if !p.IsConfigured() {
		t.Fatal("must be configured after success")
	}
	if p.SiteLink() != site.srv.URL+"/" {
		t.Fatalf("site link: %q", p.SiteLink())
	}
	user, key := site.authHeaders()
	if user != "ptpuser" || key != "ptpkey" {
		t.Fatalf("auth headers: %q/%q", user, key)
	}
}

func TestPassThePopcornApplyConfigurationEmptyIsError(t *testing.T) {
	site := newPTPSite(t, map[string]ptpResponse{
		"/torrents.php": {body: `{"TotalResults":"0","Movies":[]}`},
	})
	p := newTestPTP(t, site)
	_, err := p.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err == nil || err.Error() != "Testing returned no results!" {
		t.Fatalf("err: %v", err)
	}
	if p.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestPassThePopcornDownloadSendsAuthHeaders(t *testing.T) {
	site := newPTPSite(t, map[string]ptpResponse{
		"/torrents.php": {body: "torrent-bytes"},
	})
	p := newTestPTP(t, site)
	p.cfg.User.Value = "ptpuser"
	p.cfg.Key.Value = "ptpkey"

	link, err := url.Parse(site.srv.URL + "/torrents.php?action=download&id=1001")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	body, err := p.Download(context.Background(), link)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(body) != "torrent-bytes" {
		t.Fatalf("body: %q", body)
	}
	user, key := site.authHeaders()
	if user != "ptpuser" || key != "ptpkey" {
		t.Fatalf("download auth headers: %q/%q", user, key)
	}
}
