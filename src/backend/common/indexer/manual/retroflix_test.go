package manual

import (
	"context"
	"encoding/json"
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

type retroflixSite struct {
	srv *httptest.Server

	mu          sync.Mutex
	hits        []string
	logins      int
	lastBody    string
	contentType string
	lastAuth    string
	searchGets  int
	tokenSerial int
	validTokens map[string]bool
}

func (s *retroflixSite) addToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.validTokens == nil {
		s.validTokens = map[string]bool{}
	}
	s.validTokens["Bearer "+token] = true
}

func newRetroFlixSite(t *testing.T) *retroflixSite {
	t.Helper()
	s := &retroflixSite{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.mu.Unlock()
		switch r.URL.Path {
		case "/api/login":
			s.mu.Lock()
			s.logins++
			s.contentType = r.Header.Get("Content-Type")
			s.mu.Unlock()
			body, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			s.mu.Lock()
			s.lastBody = string(body)
			s.mu.Unlock()
			var creds map[string]string
			if err := json.Unmarshal(body, &creds); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if creds["username"] == "user" && creds["password"] == "pass" {
				s.mu.Lock()
				s.tokenSerial++
				token := fmt.Sprintf("tok%d", s.tokenSerial)
				s.mu.Unlock()
				s.addToken(token)
				_, _ = fmt.Fprintf(w, `{"token":%q}`, token)
				return
			}
			_, _ = fmt.Fprint(w, `{"message":"Invalid credentials"}`)
		case "/api/torrent":
			s.mu.Lock()
			s.searchGets++
			auth := r.Header.Get("Authorization")
			s.lastAuth = auth
			valid := s.validTokens[auth]
			s.mu.Unlock()
			if !valid {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("search") == "servererror" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, retroflixFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *retroflixSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *retroflixSite) stats() (logins, searchGets int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins, s.searchGets
}

func newTestRetroFlix(t *testing.T, site *retroflixSite) *RetroFlix {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewRetroFlix(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewRetroFlix: %v", err)
	}
	r := ix.(*RetroFlix)
	r.SiteLinkValue = site.srv.URL + "/"
	// tests must not pay the upstream 2.1s API-limit delay
	client.SetRequestDelay(r.ID(), 0)
	return r
}

const retroflixFixture = `[
  {
    "id": 101,
    "url": "https://retroflix.faketest/torrent/101-classic-movie",
    "name": "Classic.Movie.1955.[REQUESTED].720p",
    "created_at": "2024-03-01 12:00:00",
    "short_description": "Drama, Film-Noir",
    "poster": "https://retroflix.faketest/posters/101.jpg",
    "size": 1073741824,
    "times_completed": 7,
    "seeders": 4,
    "leechers": 1,
    "download_volume_factor": 0,
    "upload_volume_factor": 1,
    "category": {"id": "401", "name": "Movies"}
  },
  {
    "id": 102,
    "url": "https://retroflix.faketest/torrent/102-classic-show",
    "name": "Classic.Show.S01E03.480p",
    "created_at": "2024-03-02 13:00:00",
    "short_description": "",
    "poster": "/relative/poster.jpg",
    "size": 262144000,
    "times_completed": 2,
    "seeders": 3,
    "leechers": 0,
    "download_volume_factor": 1,
    "upload_volume_factor": 1,
    "category": {"id": "402", "name": "TV Series"}
  }
]`

func TestRetroFlixCaps(t *testing.T) {
	ix, err := NewRetroFlix(services.Deps{})
	if err != nil {
		t.Fatalf("NewRetroFlix: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{"q", "season", "ep", "imdbid"}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{"q", "imdbid"}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if strings.Join(caps.MusicSearchParams, ",") != "q" || strings.Join(caps.BookSearchParams, ",") != "q" {
		t.Fatalf("music/book params: %v/%v", caps.MusicSearchParams, caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"401", 2000}, {"402", 5000}, {"406", 3020},
		{"407", 5060}, {"409", 7000}, {"408", 3000},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		if len(got) == 0 || got[0] != tc.want {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want %d", tc.tracker, got, tc.want)
		}
	}
	if alts := ix.AlternativeSiteLinks(); len(alts) != 2 || alts[1] != "https://retroflix.club/" {
		t.Fatalf("alt links: %v", alts)
	}
}

func TestRetroFlixConfigForm(t *testing.T) {
	ix, err := NewRetroFlix(services.Deps{})
	if err != nil {
		t.Fatalf("NewRetroFlix: %v", err)
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
		"sitelink", "email", "password", "",
		"showfreeleechonly", "accountinactivity", "accountinactivity", "tags",
	}
	if fmt.Sprintf("%v", ids) != fmt.Sprintf("%v", want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestRetroFlixTokenAcquisition(t *testing.T) {
	site := newRetroFlixSite(t)
	r := newTestRetroFlix(t, site)

	status, err := r.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "email", Value: "user"},
		{ID: "password", Value: "pass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !r.IsConfigured() {
		t.Fatalf("status/configured: %v/%v", status, r.IsConfigured())
	}

	site.mu.Lock()
	body, ct := site.lastBody, site.contentType
	site.mu.Unlock()
	// the login body is a JSON string, not a form (upstream rawbody)
	var creds map[string]string
	if err := json.Unmarshal([]byte(body), &creds); err != nil {
		t.Fatalf("login body is not JSON: %q", body)
	}
	if creds["username"] != "user" || creds["password"] != "pass" {
		t.Fatalf("login creds: %v", creds)
	}
	if ct != "application/json" {
		t.Fatalf("login Content-Type: %q", ct)
	}
	if r.token != "tok1" {
		t.Fatalf("token: %q", r.token)
	}
	if _, gets := site.stats(); gets != 1 {
		t.Fatalf("search gets: %d", gets)
	}
}

func TestRetroFlixSearchRequestAndParse(t *testing.T) {
	site := newRetroFlixSite(t)
	site.addToken("tok1")
	r := newTestRetroFlix(t, site)
	r.token = "tok1"

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Classic"
	q.Categories = []int{models.CatMovies.ID}
	if _, err := r.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	q2 := models.NewTorznabQuery()
	q2.QueryType = "movie"
	q2.ImdbID = "tt0057012"
	if _, err := r.ResultsForQuery(context.Background(), q2, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	paths := site.paths()
	want := []string{
		"/api/torrent?itemsPerPage=100&includingDead=1&sort=torrent.createdAt&direction=desc&categories[]=401&search=Classic",
		"/api/torrent?itemsPerPage=100&includingDead=1&sort=torrent.createdAt&direction=desc&imdbId=tt0057012",
	}
	if fmt.Sprintf("%v", paths) != fmt.Sprintf("%v", want) {
		t.Fatalf("uris = %v, want %v", paths, want)
	}

	releases, err := r.PerformQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	if err != nil {
		t.Fatalf("PerformQuery: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases: %d", len(releases))
	}

	r0 := releases[0]
	// [REQUESTED] removed by CleanTitle; the dot before it remains — only the
	// ends are trimmed (SpeedAppTracker.cs:234-236)
	if r0.Title != "Classic.Movie.1955..720p" {
		t.Fatalf("title: %q", r0.Title)
	}
	if len(r0.Category) == 0 || r0.Category[0] != models.CatMovies.ID {
		t.Fatalf("category: %v", r0.Category)
	}
	if r0.Size == nil || *r0.Size != 1073741824 {
		t.Fatalf("size: %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 4 || r0.Peers == nil || *r0.Peers != 5 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.Grabs == nil || *r0.Grabs != 7 {
		t.Fatalf("grabs: %v", r0.Grabs)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-03-01 12:00:00" {
		t.Fatalf("date: %v", r0.PublishDate)
	}
	if r0.Link == nil || r0.Link.String() != site.srv.URL+"/api/torrent/101/download" {
		t.Fatalf("link: %v", r0.Link)
	}
	if r0.Poster == nil || r0.Poster.String() != "https://retroflix.faketest/posters/101.jpg" {
		t.Fatalf("poster: %v", r0.Poster)
	}
	if r0.Description != "Drama, Film-Noir" {
		t.Fatalf("description: %q", r0.Description)
	}
	// upstream quirk: genres re-split on "," after joining with ", "
	if fmt.Sprintf("%v", r0.Genres) != "[Drama  Film-Noir]" {
		t.Fatalf("genres: %v", r0.Genres)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}
	if r0.MinimumRatio == nil || *r0.MinimumRatio != 1 {
		t.Fatalf("minimum ratio: %v", r0.MinimumRatio)
	}
	// RetroFlix overrides MinimumSeedTime to 120h (upstream RetroFlix.cs:29)
	if r0.MinimumSeedTime == nil || *r0.MinimumSeedTime != 432000 {
		t.Fatalf("minimum seed time: %v", r0.MinimumSeedTime)
	}

	r1 := releases[1]
	if r1.Category[0] != models.CatTV.ID {
		t.Fatalf("category[1]: %v", r1.Category)
	}
	// relative poster → nil (upstream Uri.TryCreate Absolute)
	if r1.Poster != nil {
		t.Fatalf("poster[1]: %v", r1.Poster)
	}
	// empty short_description → genres [""] upstream quirk
	if fmt.Sprintf("%v", r1.Genres) != "[]" {
		t.Fatalf("genres[1]: %v", r1.Genres)
	}
}

func TestRetroFlixAuthError(t *testing.T) {
	site := newRetroFlixSite(t)
	r := newTestRetroFlix(t, site)

	_, err := r.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "email", Value: "user"},
		{ID: "password", Value: "wrong"},
	})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindAuth {
		t.Fatalf("err: %v", err)
	}
	if ie.Message != "Invalid credentials" {
		t.Fatalf("message: %q", ie.Message)
	}
	if r.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestRetroFlixSearchError(t *testing.T) {
	site := newRetroFlixSite(t)
	site.addToken("tok1")
	r := newTestRetroFlix(t, site)
	r.token = "tok1"

	_, err := r.PerformQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "servererror",
	})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindConfig {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasPrefix(ie.Message, "Unknown error in search: ") {
		t.Fatalf("message: %q", ie.Message)
	}
}
