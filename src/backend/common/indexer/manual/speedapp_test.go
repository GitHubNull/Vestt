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
	"net/url"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type speedAppSite struct {
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

func newSpeedAppSite(t *testing.T) *speedAppSite {
	t.Helper()
	s := &speedAppSite{}
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
			if creds["username"] == "user@example.com" && creds["password"] == "pass" {
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
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, speedAppFixture)
		case "/api/torrent/101/download":
			s.mu.Lock()
			auth := r.Header.Get("Authorization")
			valid := s.validTokens[auth]
			s.mu.Unlock()
			if !valid {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = fmt.Fprint(w, "d4:infod4:name3:fooee")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *speedAppSite) addToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.validTokens == nil {
		s.validTokens = map[string]bool{}
	}
	s.validTokens["Bearer "+token] = true
}

func (s *speedAppSite) expire(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.validTokens, "Bearer "+token)
}

func (s *speedAppSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *speedAppSite) stats() (logins, searchGets int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logins, s.searchGets
}

func (s *speedAppSite) lastSearchAuth() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAuth
}

func newTestSpeedApp(t *testing.T, site *speedAppSite) *SpeedApp {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewSpeedApp(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewSpeedApp: %v", err)
	}
	s := ix.(*SpeedApp)
	s.SiteLinkValue = site.srv.URL + "/"
	// tests must not pay the upstream 2.1s API-limit delay
	client.SetRequestDelay(s.ID(), 0)
	return s
}

const speedAppFixture = `[
  {
    "id": 101,
    "url": "https://speedapp.faketest/torrent/101-ro-movie",
    "name": "Movie.Name.2024.1080p.[REQUEST].BluRay",
    "created_at": "2024-04-01 10:00:00",
    "short_description": "Actiune, Aventura",
    "poster": "https://speedapp.faketest/posters/101.jpg",
    "size": 3221225472,
    "times_completed": 21,
    "seeders": 14,
    "leechers": 2,
    "download_volume_factor": 0,
    "upload_volume_factor": 1,
    "category": {"id": "8", "name": "Movies: HD"}
  },
  {
    "id": 102,
    "url": "https://speedapp.faketest/torrent/102-ro-show",
    "name": "Show.Name.S02E04.720p",
    "created_at": "2024-04-02 11:00:00",
    "short_description": "",
    "poster": "",
    "size": 524288000,
    "times_completed": 4,
    "seeders": 6,
    "leechers": 0,
    "download_volume_factor": 1,
    "upload_volume_factor": 1,
    "category": {"id": "43", "name": "TV Episodes HD"}
  }
]`

func TestSpeedAppCaps(t *testing.T) {
	ix, err := NewSpeedApp(services.Deps{})
	if err != nil {
		t.Fatalf("NewSpeedApp: %v", err)
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
		{"8", 2040}, {"43", 5040}, {"61", 2045}, {"3", 5070},
		{"5", 3000}, {"68", 3040}, {"11", 4050}, {"15", 6000},
		{"49", 6060}, {"6", 7020}, {"9", 5080},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		if len(got) == 0 || got[0] != tc.want {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want %d", tc.tracker, got, tc.want)
		}
	}
	if legacy := ix.(*SpeedApp).LegacySiteLinks; len(legacy) != 8 || legacy[0] != "https://www.icetorrent.org/" {
		t.Fatalf("legacy links: %v", legacy)
	}
}

func TestSpeedAppConfigForm(t *testing.T) {
	ix, err := NewSpeedApp(services.Deps{})
	if err != nil {
		t.Fatalf("NewSpeedApp: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{"sitelink", "email", "password", "", "showfreeleechonly", "accountinactivity", "tags"}
	if fmt.Sprintf("%v", ids) != fmt.Sprintf("%v", want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

// TestSpeedAppTokenAcquisition: (a) token is acquired during
// ApplyConfiguration and used as Bearer on the search.
func TestSpeedAppTokenAcquisition(t *testing.T) {
	site := newSpeedAppSite(t)
	s := newTestSpeedApp(t, site)

	status, err := s.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "email", Value: "user@example.com"},
		{ID: "password", Value: "pass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !s.IsConfigured() {
		t.Fatalf("status/configured: %v/%v", status, s.IsConfigured())
	}
	if s.token != "tok1" {
		t.Fatalf("token: %q", s.token)
	}
	if auth := site.lastSearchAuth(); auth != "Bearer tok1" {
		t.Fatalf("search Authorization: %q", auth)
	}
	if logins, gets := site.stats(); logins != 1 || gets != 1 {
		t.Fatalf("logins/searchGets: %d/%d", logins, gets)
	}
}

// TestSpeedAppTokenRequestBody: (c) the token request carries the exact
// JSON body and headers (upstream rawbody + Content-Type: application/json).
func TestSpeedAppTokenRequestBody(t *testing.T) {
	site := newSpeedAppSite(t)
	s := newTestSpeedApp(t, site)

	if err := s.ApplyItems([]indexer.ConfigItem{
		{ID: "email", Value: " user@example.com "},
		{ID: "password", Value: " pass "},
	}); err != nil {
		t.Fatalf("ApplyItems: %v", err)
	}
	if err := s.renewalToken(context.Background()); err != nil {
		t.Fatalf("renewalToken: %v", err)
	}

	site.mu.Lock()
	body, ct := site.lastBody, site.contentType
	site.mu.Unlock()
	if ct != "application/json" {
		t.Fatalf("Content-Type: %q", ct)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("login body is not JSON: %q", body)
	}
	// upstream trims the credentials (SpeedAppTracker.cs:72-73)
	if decoded["username"] != "user@example.com" || decoded["password"] != "pass" {
		t.Fatalf("login body: %q", body)
	}
	if strings.Contains(body, " ") {
		t.Fatalf("login body must not contain spaces: %q", body)
	}
}

// TestSpeedAppExpiredTokenRenewal: (b) a 401 from the search API triggers a
// silent re-login and the request is retried with the new token.
func TestSpeedAppExpiredTokenRenewal(t *testing.T) {
	site := newSpeedAppSite(t)
	s := newTestSpeedApp(t, site)

	// acquire the first token through the normal configuration flow
	if _, err := s.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "email", Value: "user@example.com"},
		{ID: "password", Value: "pass"},
	}); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if s.token != "tok1" {
		t.Fatalf("token: %q", s.token)
	}

	// simulate expiry: the site no longer accepts tok1
	site.expire("tok1")

	res, err := s.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
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
	// ApplyConfiguration runs a self-test PerformQuery (SpeedAppTracker.cs:56),
	// so the 401→renew→retry cycle (SpeedAppTracker.cs:126-130) adds two more
	// search GETs on top of it.
	if gets != 3 {
		t.Fatalf("search gets: %d (want 3 — config self-test + original 401 + retry)", gets)
	}
}

func TestSpeedAppSearchRequestAndParse(t *testing.T) {
	site := newSpeedAppSite(t)
	site.addToken("tok1")
	s := newTestSpeedApp(t, site)
	s.token = "tok1"

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Movie Name"
	q.Categories = []int{models.CatMoviesHD.ID}
	if _, err := s.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	paths := site.paths()
	// upstream GetQueryString UrlEncodes values only, never keys
	// (StringUtil.cs:155-158) — the `categories[]` key stays raw; MoviesHD
	// maps to both tracker cats 8 and 29 and MapTorznabCapsToTrackers
	// returns every match (TorznabCapabilitiesCategories.cs:76-83)
	want := []string{
		"/api/torrent?itemsPerPage=100&includingDead=1&sort=torrent.createdAt&direction=desc&categories[]=8&categories[]=29&search=Movie+Name",
	}
	if fmt.Sprintf("%v", paths) != fmt.Sprintf("%v", want) {
		t.Fatalf("uris = %v, want %v", paths, want)
	}

	releases, err := s.PerformQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	if err != nil {
		t.Fatalf("PerformQuery: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases: %d", len(releases))
	}

	r0 := releases[0]
	// [REQUEST] removed by CleanTitle; the dots around it remain and only the
	// ends are trimmed (SpeedAppTracker.cs:234-236)
	if r0.Title != "Movie.Name.2024.1080p..BluRay" {
		t.Fatalf("title: %q", r0.Title)
	}
	if len(r0.Category) == 0 || r0.Category[0] != models.CatMoviesHD.ID {
		t.Fatalf("category: %v", r0.Category)
	}
	if r0.Size == nil || *r0.Size != 3221225472 {
		t.Fatalf("size: %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 14 || r0.Peers == nil || *r0.Peers != 16 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.Grabs == nil || *r0.Grabs != 21 {
		t.Fatalf("grabs: %v", r0.Grabs)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-04-01 10:00:00" {
		t.Fatalf("date: %v", r0.PublishDate)
	}
	if r0.Link == nil || r0.Link.String() != site.srv.URL+"/api/torrent/101/download" {
		t.Fatalf("link: %v", r0.Link)
	}
	if r0.Poster == nil || r0.Poster.String() != "https://speedapp.faketest/posters/101.jpg" {
		t.Fatalf("poster: %v", r0.Poster)
	}
	if r0.Description != "Actiune, Aventura" {
		t.Fatalf("description: %q", r0.Description)
	}
	if fmt.Sprintf("%v", r0.Genres) != "[Actiune  Aventura]" {
		t.Fatalf("genres: %v", r0.Genres)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}
	// upstream default MinimumSeedTime (48h)
	if r0.MinimumSeedTime == nil || *r0.MinimumSeedTime != 172800 {
		t.Fatalf("minimum seed time: %v", r0.MinimumSeedTime)
	}

	r1 := releases[1]
	if r1.Category[0] != models.CatTVHD.ID {
		t.Fatalf("category[1]: %v", r1.Category)
	}
	if r1.Poster != nil {
		t.Fatalf("poster[1]: %v", r1.Poster)
	}
}

func TestSpeedAppAuthError(t *testing.T) {
	site := newSpeedAppSite(t)
	s := newTestSpeedApp(t, site)

	_, err := s.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "email", Value: "user@example.com"},
		{ID: "password", Value: "wrong"},
	})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindAuth {
		t.Fatalf("err: %v", err)
	}
	if ie.Message != "Invalid credentials" {
		t.Fatalf("message: %q", ie.Message)
	}
	if s.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestSpeedAppDownloadRenewal(t *testing.T) {
	site := newSpeedAppSite(t)
	s := newTestSpeedApp(t, site)

	if _, err := s.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "email", Value: "user@example.com"},
		{ID: "password", Value: "pass"},
	}); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	site.expire("tok1")

	link, err := url.Parse(site.srv.URL + "/api/torrent/101/download")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	content, err := s.Download(context.Background(), link)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(content) != "d4:infod4:name3:fooee" {
		t.Fatalf("content: %q", content)
	}
	if s.token != "tok2" {
		t.Fatalf("token after download renewal: %q", s.token)
	}
}
