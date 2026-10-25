package manual

import (
	"context"
	"errors"
	"fmt"
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

type privatehdSite struct {
	srv *httptest.Server

	mu        sync.Mutex
	hits      []string
	lastForm  map[string]string
	lastAuth  string
	always401 bool
}

func newPrivateHDSite(t *testing.T) *privatehdSite {
	t.Helper()
	s := &privatehdSite{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/jackett/auth":
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			s.mu.Lock()
			s.lastForm = map[string]string{
				"username": r.PostForm.Get("username"),
				"password": r.PostForm.Get("password"),
				"pid":      r.PostForm.Get("pid"),
			}
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if r.PostForm.Get("username") == "user" && r.PostForm.Get("password") == "pass" && r.PostForm.Get("pid") == "pid1" {
				_, _ = fmt.Fprint(w, `{"token":"tok123"}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"token":null,"message":"Invalid credentials"}`)
		case "/api/v1/jackett/torrents":
			s.mu.Lock()
			always401 := s.always401
			s.lastAuth = r.Header.Get("Authorization")
			s.mu.Unlock()
			if always401 || r.Header.Get("Authorization") != "Bearer tok123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, privatehdFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *privatehdSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *privatehdSite) setAlways401(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.always401 = v
}

func (s *privatehdSite) form() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastForm
}

func newTestPrivateHD(t *testing.T, site *privatehdSite) *PrivateHD {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewPrivateHD(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewPrivateHD: %v", err)
	}
	p := ix.(*PrivateHD)
	p.SiteLinkValue = site.srv.URL + "/"
	// tests must not pay the upstream 6s request delay
	client.SetRequestDelay(p.ID(), 0)
	return p
}

const privatehdFixture = `{
  "data": [
    {
      "url": "https://privatehd.faketest/torrent/10-hd-movie",
      "download": "https://privatehd.faketest/download/10",
      "category": {"1": "Movies"},
      "movie_tv": {"tmdb": "603", "tvdb": "", "imdb": "tt0133093"},
      "created_at_iso": "2024-05-01 10:00:00",
      "file_name": "Movie.Name.1999.1080p.BluRay.x264",
      "release_title": "Movie Name 1999",
      "info_hash": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "leech": 3,
      "completed": 9,
      "seed": 12,
      "file_size": 2147483648,
      "file_count": 4,
      "download_multiply": 0.5,
      "upload_multiply": 1.0,
      "video_quality": "1080p",
      "type": "MOVIE",
      "format": "x264",
      "audio": [{"id": 1, "language": "English"}],
      "subtitle": [{"id": 2, "language": "German"}]
    },
    {
      "url": "https://privatehd.faketest/torrent/20-uhd-show",
      "download": "https://privatehd.faketest/download/20",
      "category": {"2": "TV"},
      "movie_tv": null,
      "created_at_iso": "2024-05-02 11:00:00",
      "file_name": "Some.Show.S01E01.2160p",
      "release_title": "Some Show",
      "info_hash": "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
      "leech": 1,
      "completed": 2,
      "seed": 5,
      "file_size": 4294967296,
      "file_count": 2,
      "download_multiply": 1.0,
      "upload_multiply": 1.0,
      "video_quality": "2160p",
      "type": "TV-SHOW",
      "format": "x265",
      "audio": [],
      "subtitle": []
    }
  ]
}`

func TestPrivateHDCaps(t *testing.T) {
	ix, err := NewPrivateHD(services.Deps{})
	if err != nil {
		t.Fatalf("NewPrivateHD: %v", err)
	}
	caps := ix.Capabilities()
	if caps.LimitsDefault == nil || *caps.LimitsDefault != 50 || caps.LimitsMax == nil || *caps.LimitsMax != 50 {
		t.Fatalf("limits: %v/%v", caps.LimitsDefault, caps.LimitsMax)
	}
	if !caps.TVSearchImdbAvailable {
		t.Fatal("TVSearchImdbAvailable must be true")
	}
	wantTV := []string{"q", "season", "ep", "imdbid", "tvdbid", "genre"}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{"q", "imdbid", "tmdbid", "genre"}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    []int
	}{
		{"1", []int{2000, 2045, 2040, 2030}},
		{"2", []int{5000, 5045, 5040, 5030}},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", tc.want) {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want %v", tc.tracker, got, tc.want)
		}
	}
}

func TestPrivateHDConfigForm(t *testing.T) {
	ix, err := NewPrivateHD(services.Deps{})
	if err != nil {
		t.Fatalf("NewPrivateHD: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// the upstream base item AND the PrivateHD dynamic share the
	// "accountinactivity" id (upstream shows both).
	want := []string{
		"sitelink", "username", "password", "pid", "",
		"searchfreeleechonly", "accountinactivity", "accountinactivity", "tags",
	}
	if fmt.Sprintf("%v", ids) != fmt.Sprintf("%v", want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestPrivateHDApplyConfiguration(t *testing.T) {
	site := newPrivateHDSite(t)
	p := newTestPrivateHD(t, site)

	status, err := p.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
		{ID: "pid", Value: "pid1"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v", status)
	}
	if !p.IsConfigured() {
		t.Fatal("must be configured")
	}
	if form := site.form(); form["username"] != "user" || form["password"] != "pass" || form["pid"] != "pid1" {
		t.Fatalf("auth form: %v", form)
	}
	paths := site.paths()
	if len(paths) != 2 || paths[0] != "/api/v1/jackett/auth" || paths[1] != "/api/v1/jackett/torrents?in=1&type=0&limit=50&search=" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestPrivateHDSearchRequestAndParse(t *testing.T) {
	site := newPrivateHDSite(t)
	p := newTestPrivateHD(t, site)
	p.AuthorizationToken = "tok123"

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	q.Categories = []int{models.CatMovies.ID}
	if _, err := p.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	// imdb movie search with season/episode (base class behaviour)
	q2 := models.NewTorznabQuery()
	q2.QueryType = "movie"
	q2.ImdbID = "tt0133093"
	season := 1
	q2.Season = &season
	q2.Episode = "2"
	if _, err := p.ResultsForQuery(context.Background(), q2, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	paths := site.paths()
	want := []string{
		"/api/v1/jackett/torrents?in=1&type=1&limit=50&search=Some+Movie",
		"/api/v1/jackett/torrents?in=1&type=0&limit=50&imdb=tt0133093&search=S01E02",
	}
	if fmt.Sprintf("%v", paths) != fmt.Sprintf("%v", want) {
		t.Fatalf("uris = %v, want %v", paths, want)
	}

	releases, err := p.PerformQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	if err != nil {
		t.Fatalf("PerformQuery: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases: %d", len(releases))
	}

	r0 := releases[0]
	if r0.Title != "Movie.Name.1999.1080p.BluRay.x264" {
		t.Fatalf("title: %q", r0.Title)
	}
	if r0.Category == nil || len(r0.Category) != 1 || r0.Category[0] != models.CatMoviesHD.ID {
		t.Fatalf("category: %v", r0.Category)
	}
	if r0.Size == nil || *r0.Size != 2147483648 {
		t.Fatalf("size: %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 12 || r0.Peers == nil || *r0.Peers != 15 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.Grabs == nil || *r0.Grabs != 9 {
		t.Fatalf("grabs: %v", r0.Grabs)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-05-01 10:00:00" {
		t.Fatalf("date: %v", r0.PublishDate)
	}
	if r0.Link == nil || r0.Link.String() != "https://privatehd.faketest/download/10" {
		t.Fatalf("link: %v", r0.Link)
	}
	if r0.Imdb == nil || *r0.Imdb != 133093 {
		t.Fatalf("imdb: %v", r0.Imdb)
	}
	if r0.TMDb == nil || *r0.TMDb != 603 {
		t.Fatalf("tmdb: %v", r0.TMDb)
	}
	if r0.InfoHash != "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("infohash: %q", r0.InfoHash)
	}
	if r0.Description != "Audio: English<br/>Subtitles: German" {
		t.Fatalf("description: %q", r0.Description)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0.5 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}
	if r0.MinimumRatio == nil || *r0.MinimumRatio != 1 {
		t.Fatalf("minimum ratio: %v", r0.MinimumRatio)
	}
	if r0.MinimumSeedTime == nil || *r0.MinimumSeedTime != 259200+(int64(2147483648.0/(1024*1024*1024))*7200) {
		t.Fatalf("minimum seed time: %v", r0.MinimumSeedTime)
	}

	r1 := releases[1]
	if r1.Category[0] != models.CatTVUHD.ID {
		t.Fatalf("category[1]: %v", r1.Category)
	}
}

func TestPrivateHDAuthError(t *testing.T) {
	site := newPrivateHDSite(t)
	p := newTestPrivateHD(t, site)

	_, err := p.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "wrong"},
		{ID: "pid", Value: "pid1"},
	})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindAuth {
		t.Fatalf("err: %v", err)
	}
	if ie.Message != "Invalid credentials" {
		t.Fatalf("message: %q", ie.Message)
	}
	if p.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestPrivateHDPersistent401IsAuth(t *testing.T) {
	site := newPrivateHDSite(t)
	site.setAlways401(true)
	p := newTestPrivateHD(t, site)

	// valid credentials renew the token, but the search keeps answering
	// 401 → "Invalid status code 401" → KindAuth.
	if err := p.ApplyItems([]indexer.ConfigItem{
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
		{ID: "pid", Value: "pid1"},
	}); err != nil {
		t.Fatalf("ApplyItems: %v", err)
	}
	p.AuthorizationToken = "stale"

	_, err := p.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	var ie *indexer.IndexerError
	// Upstream AvistazTracker surfaces persistent 401 as a plain "Invalid
	// status code" exception (Abstract/AvistazTracker.cs) — no auth-specific
	// mapping there, so the base wraps it as KindInternal.
	if !errors.As(err, &ie) || ie.Kind != indexer.KindInternal {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasPrefix(ie.Message, "Invalid status code 401") {
		t.Fatalf("message: %q", ie.Message)
	}
}
