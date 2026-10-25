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
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type avzSite struct {
	srv *httptest.Server

	mu         sync.Mutex
	hits       []string
	lastAuth   string
	lastForm   map[string]string
	authPosts  int
	searchGets int
}

func newAvzSite(t *testing.T) *avzSite {
	t.Helper()
	s := &avzSite{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/jackett/auth":
			s.mu.Lock()
			s.authPosts++
			s.mu.Unlock()
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
			if r.PostForm.Get("username") == "user" && r.PostForm.Get("password") == "pass" && r.PostForm.Get("pid") == "pid" {
				_, _ = fmt.Fprint(w, `{"token":"tok123"}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"token":null,"message":"Invalid credentials"}`)
		case "/api/v1/jackett/torrents":
			s.mu.Lock()
			s.searchGets++
			s.lastAuth = r.Header.Get("Authorization")
			s.mu.Unlock()
			if r.Header.Get("Authorization") != "Bearer tok123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("search") == "servererror" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, avistazManualFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *avzSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestAvistaZ(t *testing.T, site *avzSite) *AvistaZ {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewAvistaZ(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewAvistaZ: %v", err)
	}
	a := ix.(*AvistaZ)
	a.SiteLinkValue = site.srv.URL + "/"
	a.AuthorizationToken = "tok123"
	// tests must not pay the upstream 6s request delay
	client.SetRequestDelay(a.ID(), 0)
	return a
}

const avistazManualFixture = `{
  "data": [
    {
      "url": "https://avistaz.faketest/torrent/10-movie-1080p",
      "download": "https://avistaz.faketest/download/10",
      "category": {"1": "Movies"},
      "movie_tv": {"tmdb": "1234", "tvdb": "", "imdb": "tt0133093"},
      "created_at_iso": "2024-03-01 12:34:56",
      "file_name": "Movie.Name.1999.1080p.BluRay.x264",
      "release_title": "Movie Name 1999",
      "info_hash": "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
      "leech": 2,
      "completed": 5,
      "seed": 7,
      "file_size": 2147483648,
      "file_count": 12,
      "download_multiply": 0.5,
      "upload_multiply": 1.0,
      "video_quality": "1080p",
      "type": "MOVIE",
      "format": "x264",
      "audio": [{"id": 45, "language": "English"}],
      "subtitle": [{"id": 137, "language": "Russian"}]
    },
    {
      "url": "https://avistaz.faketest/torrent/20-show-2160p",
      "download": "https://avistaz.faketest/download/20",
      "category": {"2": "TV"},
      "movie_tv": null,
      "created_at_iso": "2024-03-02 08:00:00",
      "file_name": "Show.Name.S01E02.2160p",
      "release_title": "Show Name",
      "info_hash": "",
      "leech": 1,
      "completed": 3,
      "seed": 9,
      "file_size": 524288000,
      "file_count": 5,
      "download_multiply": 1.0,
      "upload_multiply": 1.0,
      "video_quality": "2160p",
      "type": "TV-SHOW",
      "format": "x264",
      "audio": [],
      "subtitle": []
    }
  ]
}`

func TestAvistaZCaps(t *testing.T) {
	ix, err := NewAvistaZ(services.Deps{})
	if err != nil {
		t.Fatalf("NewAvistaZ: %v", err)
	}
	caps := ix.Capabilities()
	if caps.LimitsDefault == nil || *caps.LimitsDefault != 50 || caps.LimitsMax == nil || *caps.LimitsMax != 50 {
		t.Fatalf("limits: %v/%v", caps.LimitsDefault, caps.LimitsMax)
	}
	if !caps.SupportsRawSearch || !caps.TVSearchImdbAvailable {
		t.Fatalf("raw search / tv imdb: %v/%v", caps.SupportsRawSearch, caps.TVSearchImdbAvailable)
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
		{"3", []int{3000}},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", tc.want) {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want %v", tc.tracker, got, tc.want)
		}
	}
}

func TestAvistaZConfigForm(t *testing.T) {
	ix, err := NewAvistaZ(services.Deps{})
	if err != nil {
		t.Fatalf("NewAvistaZ: %v", err)
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
		"accountinactivity:displayinfo", "searchaudiolanguages:inputcheckbox",
		"searchsubtitlelanguages:inputcheckbox", "infosearchlanguages:displayinfo",
		"tags:inputtags",
	}
	if fmt.Sprintf("%v", ids) != fmt.Sprintf("%v", want) {
		t.Fatalf("items = %v, want %v", ids, want)
	}
	// upstream ConfigurationDataAvistaZ option dictionaries (audio: 72,
	// subtitle: 95 entries) with spot checks
	audioOpts := items[7]["options"].(map[string]any)
	subtitleOpts := items[8]["options"].(map[string]any)
	if len(audioOpts) != 72 || len(subtitleOpts) != 95 {
		t.Fatalf("options: audio=%d subtitle=%d", len(audioOpts), len(subtitleOpts))
	}
	if audioOpts["79"] != "Japanese" || audioOpts["45"] != "English" || subtitleOpts["22"] != "Norwegian Bokmål" {
		t.Fatalf("option spot check failed: %v %v %v", audioOpts["79"], audioOpts["45"], subtitleOpts["22"])
	}
}

func TestAvistaZSearchRequest(t *testing.T) {
	site := newAvzSite(t)
	a := newTestAvistaZ(t, site)
	a.cfg.SearchAudioLanguages.Values = []string{"45", "45", "79"}
	a.cfg.SearchSubtitleLanguages.Values = []string{"137"}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	q.Categories = []int{models.CatMovies.ID}

	res, err := a.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// ResultsForQuery keeps only releases matching the query categories:
	// the TV-SHOW row is filtered out for a movie query.
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if site.lastAuth != "Bearer tok123" {
		t.Fatalf("Authorization = %q", site.lastAuth)
	}

	paths := site.paths()
	wantURI := "/api/v1/jackett/torrents?in=1&type=1&limit=50&search=Some+Movie" +
		"&language[]=45&language[]=79&subtitle[]=137"
	if len(paths) != 1 || paths[0] != wantURI {
		t.Fatalf("uri = %v, want %q", paths, wantURI)
	}
}

func TestAvistaZEpisodeSearchTerm(t *testing.T) {
	site := newAvzSite(t)
	a := newTestAvistaZ(t, site)

	// season-less episode: "Running Man E323"
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Running Man"
	q.Episode = "323"
	if u := a.SearchURL(q); !strings.Contains(u, "search=Running+Man+E323") {
		t.Fatalf("url = %s", u)
	}

	// with a season the base SxxEyy form is used
	q2 := models.NewTorznabQuery()
	q2.QueryType = "tvsearch"
	q2.SearchTerm = "Running Man"
	season := 1
	q2.Season = &season
	q2.Episode = "2"
	if u := a.SearchURL(q2); !strings.Contains(u, "search=Running+Man+S01E02") {
		t.Fatalf("url = %s", u)
	}
}

func TestAvistaZParseFixture(t *testing.T) {
	site := newAvzSite(t)
	a := newTestAvistaZ(t, site)

	releases, err := a.PerformQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
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
	if r0.Seeders == nil || *r0.Seeders != 7 || r0.Peers == nil || *r0.Peers != 9 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-03-01 12:34:56" {
		t.Fatalf("date: %v", r0.PublishDate)
	}
	if r0.Link == nil || r0.Link.String() != "https://avistaz.faketest/download/10" {
		t.Fatalf("link: %v", r0.Link)
	}
	if r0.Imdb == nil || *r0.Imdb != 133093 {
		t.Fatalf("imdb: %v", r0.Imdb)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0.5 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}

	r1 := releases[1]
	if r1.Category[0] != models.CatTVUHD.ID {
		t.Fatalf("category[1]: %v", r1.Category)
	}
}

func TestAvistaZAuthError(t *testing.T) {
	site := newAvzSite(t)
	a := newTestAvistaZ(t, site)
	a.AuthorizationToken = ""

	_, err := a.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "user"},
		{ID: "password", Value: "wrong"},
		{ID: "pid", Value: "pid"},
	})
	if err == nil || err.Error() != "Invalid credentials" {
		t.Fatalf("err: %v", err)
	}
	if a.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestAvistaZErrorPath(t *testing.T) {
	site := newAvzSite(t)
	a := newTestAvistaZ(t, site)

	_, err := a.PerformQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "servererror",
	})
	if err == nil || err.Error() != "Invalid status code 500 (InternalServerError) received from indexer" {
		t.Fatalf("err: %v", err)
	}
}
