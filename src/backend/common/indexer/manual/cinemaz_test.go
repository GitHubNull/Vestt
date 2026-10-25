package manual

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/models"
	"vestt/common/services"
)

type cinemazSite struct {
	srv *httptest.Server

	mu   sync.Mutex
	hits []string
}

func newCinemazSite(t *testing.T) *cinemazSite {
	t.Helper()
	s := &cinemazSite{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/jackett/torrents":
			if r.Header.Get("Authorization") != "Bearer tok123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("imdb") == "tt9999999" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, cinemazFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *cinemazSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestCinemaZ(t *testing.T, site *cinemazSite) *CinemaZ {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewCinemaZ(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewCinemaZ: %v", err)
	}
	c := ix.(*CinemaZ)
	c.SiteLinkValue = site.srv.URL + "/"
	c.AuthorizationToken = "tok123"
	// tests must not pay the upstream 6s request delay
	client.SetRequestDelay(c.ID(), 0)
	return c
}

const cinemazFixture = `{
  "data": [
    {
      "url": "https://cinemaz.faketest/torrent/10-foreign-movie",
      "download": "https://cinemaz.faketest/download/10",
      "category": {"1": "Movies"},
      "movie_tv": {"tmdb": "", "tvdb": "", "imdb": "tt0057012"},
      "created_at_iso": "2024-04-01 20:00:00",
      "file_name": "Foreign.Movie.1962.720p.BluRay.x264",
      "release_title": "Foreign Movie",
      "info_hash": "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC",
      "leech": 4,
      "completed": 11,
      "seed": 6,
      "file_size": 4294967296,
      "file_count": 8,
      "download_multiply": 1.0,
      "upload_multiply": 1.0,
      "video_quality": "720p",
      "type": "MOVIE",
      "format": "x264",
      "audio": [{"id": 52, "language": "French"}],
      "subtitle": [{"id": 45, "language": "English"}]
    },
    {
      "url": "https://cinemaz.faketest/torrent/20-foreign-show",
      "download": "https://cinemaz.faketest/download/20",
      "category": {"2": "TV"},
      "movie_tv": null,
      "created_at_iso": "2024-04-02 21:00:00",
      "file_name": "Foreign.Show.S02E05.SD",
      "release_title": "Foreign Show",
      "info_hash": "",
      "leech": 0,
      "completed": 1,
      "seed": 2,
      "file_size": 262144000,
      "file_count": 3,
      "download_multiply": 1.0,
      "upload_multiply": 1.0,
      "video_quality": "480p",
      "type": "TV-SHOW",
      "format": "x264",
      "audio": [],
      "subtitle": []
    }
  ]
}`

func TestCinemaZCaps(t *testing.T) {
	ix, err := NewCinemaZ(services.Deps{})
	if err != nil {
		t.Fatalf("NewCinemaZ: %v", err)
	}
	caps := ix.Capabilities()
	if caps.LimitsDefault == nil || *caps.LimitsDefault != 50 || caps.LimitsMax == nil || *caps.LimitsMax != 50 {
		t.Fatalf("limits: %v/%v", caps.LimitsDefault, caps.LimitsMax)
	}
	if caps.SupportsRawSearch {
		t.Fatal("CinemaZ must not support raw search")
	}
	if !caps.TVSearchImdbAvailable {
		t.Fatal("CinemaZ must have TvSearchImdbAvailable")
	}
	wantTV := []string{"q", "season", "ep", "imdbid", "tvdbid", "genre"}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{"q", "imdbid", "tmdbid", "genre"}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	// no audio mapping (2 = TV is the last one)
	if got := caps.Categories.MapTrackerCatToNewznab("3"); len(got) != 0 {
		t.Fatalf("MapTrackerCatToNewznab(3) = %v", got)
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

func TestCinemaZConfigForm(t *testing.T) {
	ix, err := NewCinemaZ(services.Deps{})
	if err != nil {
		t.Fatalf("NewCinemaZ: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, fmt.Sprintf("%s:%s", it["id"].(string), it["type"].(string)))
	}
	want := []string{
		"sitelink:inputstring", "username:inputstring", "password:inputstring",
		"pid:inputstring", ":displayinfo", "searchfreeleechonly:inputbool",
		"accountinactivity:displayinfo", "tags:inputtags",
	}
	if fmt.Sprintf("%v", ids) != fmt.Sprintf("%v", want) {
		t.Fatalf("items = %v, want %v", ids, want)
	}
}

func TestCinemaZSearchRequest(t *testing.T) {
	site := newCinemazSite(t)
	c := newTestCinemaZ(t, site)

	// plain movie search with the movie category
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	q.Categories = []int{models.CatMovies.ID}
	if _, err := c.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	// imdb movie search with season/episode (base class behaviour)
	q2 := models.NewTorznabQuery()
	q2.QueryType = "movie"
	q2.ImdbID = "tt0133093"
	season := 1
	q2.Season = &season
	q2.Episode = "2"
	if _, err := c.ResultsForQuery(context.Background(), q2, false); err != nil {
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
}

func TestCinemaZParseFixture(t *testing.T) {
	site := newCinemazSite(t)
	c := newTestCinemaZ(t, site)

	releases, err := c.PerformQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	if err != nil {
		t.Fatalf("PerformQuery: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases: %d", len(releases))
	}

	r0 := releases[0]
	if r0.Title != "Foreign.Movie.1962.720p.BluRay.x264" {
		t.Fatalf("title: %q", r0.Title)
	}
	if r0.Category == nil || len(r0.Category) != 1 || r0.Category[0] != models.CatMoviesHD.ID {
		t.Fatalf("category: %v", r0.Category)
	}
	if r0.Size == nil || *r0.Size != 4294967296 {
		t.Fatalf("size: %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 6 || r0.Peers == nil || *r0.Peers != 10 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.Grabs == nil || *r0.Grabs != 11 {
		t.Fatalf("grabs: %v", r0.Grabs)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-04-01 20:00:00" {
		t.Fatalf("date: %v", r0.PublishDate)
	}
	if r0.Link == nil || r0.Link.String() != "https://cinemaz.faketest/download/10" {
		t.Fatalf("link: %v", r0.Link)
	}
	if r0.Imdb == nil || *r0.Imdb != 57012 {
		t.Fatalf("imdb: %v", r0.Imdb)
	}
	if r0.Description != "Audio: French<br/>Subtitles: English" {
		t.Fatalf("description: %q", r0.Description)
	}

	r1 := releases[1]
	if r1.Category[0] != models.CatTVSD.ID {
		t.Fatalf("category[1]: %v", r1.Category)
	}
}

func TestCinemaZErrorPath(t *testing.T) {
	site := newCinemazSite(t)
	c := newTestCinemaZ(t, site)

	// 404 on an imdb search = empty result set, no error (upstream comment:
	// search without results, eg CinemaZ: tt0075998)
	res, err := c.PerformQuery(context.Background(), &models.TorznabQuery{
		QueryType: "movie", ImdbID: "tt9999999",
	})
	if err != nil {
		t.Fatalf("PerformQuery: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("releases: %d", len(res))
	}
}
