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
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type subsPleaseSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]string
}

func newSubsPleaseSite(t *testing.T, serve map[string]string) *subsPleaseSite {
	t.Helper()
	s := &subsPleaseSite{serve: serve}
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

func (s *subsPleaseSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestSubsPlease(t *testing.T, site *subsPleaseSite) *SubsPlease {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewSubsPlease(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewSubsPlease: %v", err)
	}
	sp := ix.(*SubsPlease)
	sp.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(sp.ID(), 0)
	return sp
}

const subsPleaseLatestJSON = `{
  "show-a": {
    "time": "1714560000",
    "release_date": "2024-05-01T15:30:00+00:00",
    "show": "Show A",
    "episode": "12",
    "downloads": [
      {"res": "1080", "magnet": "magnet:?xt=urn:btih:aaa&xl=2147483648"},
      {"res": "720", "magnet": "magnet:?xt=urn:btih:bbb"}
    ],
    "xdcc": "",
    "image_url": "/img/show-a.jpg",
    "page": "show-a"
  },
  "movie-b": {
    "time": "1714646400",
    "release_date": "2024-05-02T10:00:00Z",
    "show": "Movie B",
    "episode": "Movie",
    "downloads": [
      {"res": "1080", "magnet": "magnet:?xt=urn:btih:ccc&xl=0"}
    ],
    "xdcc": "",
    "image_url": "",
    "page": "movie-b"
  }
}`

func TestSubsPleaseCaps(t *testing.T) {
	ix, err := NewSubsPlease(services.Deps{})
	if err != nil {
		t.Fatalf("NewSubsPlease: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if len(caps.MovieSearchParams) != 1 || caps.MovieSearchParams[0] != models.MovieParamQ {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if got := caps.Categories.MapTrackerCatToNewznab("1"); !containsInt(got, models.CatTVAnime.ID) {
		t.Fatalf("cat 1: %v", got)
	}
	if got := caps.Categories.MapTrackerCatToNewznab("2"); !containsInt(got, models.CatMoviesOther.ID) {
		t.Fatalf("cat 2: %v", got)
	}
	// no descriptions upstream → no custom categories ride along
	if got := caps.Categories.MapTrackerCatToNewznab("1"); len(got) != 1 {
		t.Fatalf("cat 1 must map 1:1: %v", got)
	}
}

func TestSubsPleaseConfigForm(t *testing.T) {
	ix, err := NewSubsPlease(services.Deps{})
	if err != nil {
		t.Fatalf("NewSubsPlease: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// public site: sitelink + tags only
	if strings.Join(ids, "|") != "sitelink|tags" {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestSubsPleaseLatestRequestAndParse(t *testing.T) {
	site := newSubsPleaseSite(t, map[string]string{
		"/api/": subsPleaseLatestJSON,
	})
	sp := newTestSubsPlease(t, site)

	// a real query arrives as t=search; an untyped query is rejected by the
	// CanHandleQuery gate like upstream (BaseIndexer.cs:275-311)
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := sp.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// 2 downloads for show-a + 1 for movie-b
	if len(res.Releases) != 3 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	byTitle := map[string]*models.ReleaseInfo{}
	for _, r := range res.Releases {
		byTitle[r.Title] = r
	}

	a := byTitle["[SubsPlease] Show A - 12 (1080p)"]
	if a == nil {
		t.Fatalf("releases: %+v", byTitle)
	}
	if a.Size == nil || *a.Size != 2147483648 {
		t.Fatalf("size (xl=): %v", a.Size)
	}
	if a.MagnetUri == nil || !strings.Contains(a.MagnetUri.String(), "urn:btih:aaa") {
		t.Fatalf("magnet: %v", a.MagnetUri)
	}
	if a.Guid == nil || a.Guid.String() != a.MagnetUri.String() {
		t.Fatalf("guid: %v", a.Guid)
	}
	if a.Link != nil {
		t.Fatalf("link must be nil: %v", a.Link)
	}
	if a.Details == nil || !strings.HasSuffix(a.Details.Path, "/shows/show-a/") {
		t.Fatalf("details: %v", a.Details)
	}
	if a.Poster == nil || !strings.HasSuffix(a.Poster.Path, "/img/show-a.jpg") {
		t.Fatalf("poster: %v", a.Poster)
	}
	if len(a.Category) != 1 || a.Category[0] != models.CatTVAnime.ID {
		t.Fatalf("category: %v", a.Category)
	}
	if a.Seeders == nil || *a.Seeders != 1 || a.Peers == nil || *a.Peers != 2 {
		t.Fatalf("seeders/peers: %v %v", a.Seeders, a.Peers)
	}
	if a.Files == nil || *a.Files != 1 {
		t.Fatalf("files: %v", a.Files)
	}
	if a.DownloadVolumeFactor == nil || *a.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", a.DownloadVolumeFactor)
	}
	if a.UploadVolumeFactor == nil || *a.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", a.UploadVolumeFactor)
	}
	if a.MinimumSeedTime == nil || *a.MinimumSeedTime != 172800 {
		t.Fatalf("minimum seed time: %v", a.MinimumSeedTime)
	}
	wantDate := time.Date(2024, 5, 1, 15, 30, 0, 0, time.FixedZone("", 0)).Unix()
	if a.PublishDate.Unix() != wantDate {
		t.Fatalf("publish date: %v", a.PublishDate)
	}

	// no xl= in the magnet → 700 MB estimate for 720p
	b := byTitle["[SubsPlease] Show A - 12 (720p)"]
	if b == nil || b.Size == nil || *b.Size != 700*(1<<20) {
		t.Fatalf("720p estimate: %+v", b)
	}

	// movie episode: MoviesOther rides along; xl=0 falls back to the 1.3 GB estimate
	m := byTitle["[SubsPlease] Movie B - Movie (1080p)"]
	if m == nil {
		t.Fatalf("releases: %+v", byTitle)
	}
	if !containsInt(m.Category, models.CatTVAnime.ID) || !containsInt(m.Category, models.CatMoviesOther.ID) {
		t.Fatalf("movie categories: %v", m.Category)
	}
	if m.Size == nil || *m.Size != 1395864371 {
		t.Fatalf("movie size (1.3GB estimate): %v", m.Size)
	}
	if m.Poster != nil {
		t.Fatalf("poster must be nil for empty image_url: %v", m.Poster)
	}

	paths := site.paths()
	if len(paths) != 1 || paths[0] != "/api/?tz=UTC&f=latest" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestSubsPleaseSearchRequest(t *testing.T) {
	site := newSubsPleaseSite(t, map[string]string{
		"/api/": subsPleaseLatestJSON,
	})
	sp := newTestSubsPlease(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "[SubsPlease] Show A 1080p"
	season := 2
	q.Season = &season
	res, err := sp.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	// "[SubsPlease] " stripped, resolution stripped, season > 1 appended as " S2";
	// upstream never re-trims after the resolution removal → double space.
	paths := site.paths()
	if len(paths) != 1 || paths[0] != "/api/?tz=UTC&f=search&s=Show+A++S2" {
		t.Fatalf("paths: %v", paths)
	}

	// post-filter: resolution 1080p detected in the original search term →
	// only 1080p titles survive
	if len(res.Releases) != 1 || res.Releases[0].Title != "[SubsPlease] Show A - 12 (1080p)" {
		t.Fatalf("releases: %+v", res.Releases)
	}
	// upstream ResultsForQuery clones the query precisely so indexer-side
	// mutations (SubsPlease.cs:151 resets Season to 0) never reach the caller
	// (BaseIndexer.cs:355-357); the reset itself is still observable through
	// the releases above — without it GetQueryString() would grow an "S02"
	// and the AND filter would drop every release
	if season != 2 {
		t.Fatalf("caller query must not be mutated: got %d", season)
	}
}

func TestSubsPleaseEmptyResults(t *testing.T) {
	for _, body := range []string{"", "[]"} {
		site := newSubsPleaseSite(t, map[string]string{
			"/api/": body,
		})
		sp := newTestSubsPlease(t, site)
		q := models.NewTorznabQuery()
		// t=search so the request reaches the parser instead of being
		// rejected by the CanHandleQuery gate (BaseIndexer.cs:275-311)
		q.QueryType = "search"
		res, err := sp.ResultsForQuery(context.Background(), q, false)
		if err != nil {
			t.Fatalf("ResultsForQuery: %v", err)
		}
		if len(res.Releases) != 0 {
			t.Fatalf("releases: %d", len(res.Releases))
		}
	}
}

func TestSubsPleaseApplyConfiguration(t *testing.T) {
	site := newSubsPleaseSite(t, map[string]string{
		"/api/": subsPleaseLatestJSON,
	})
	sp := newTestSubsPlease(t, site)

	status, err := sp.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v", status)
	}
	if !sp.IsConfigured() {
		t.Fatal("must be configured")
	}
}

func TestSubsPleaseApplyConfigurationEmptyIsError(t *testing.T) {
	site := newSubsPleaseSite(t, map[string]string{
		"/api/": `[]`,
	})
	sp := newTestSubsPlease(t, site)

	_, err := sp.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
	})
	if err == nil || err.Error() != "Could not find releases from this URL" {
		t.Fatalf("err: %v", err)
	}
	if sp.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}
