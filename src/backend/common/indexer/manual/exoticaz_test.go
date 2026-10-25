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

type exoticazSite struct {
	srv *httptest.Server

	mu   sync.Mutex
	hits []string
}

func newExoticazSite(t *testing.T) *exoticazSite {
	t.Helper()
	s := &exoticazSite{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.mu.Unlock()
		if r.URL.Path != "/api/v1/jackett/torrents" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Query().Get("search") {
		case "servererror":
			w.WriteHeader(http.StatusInternalServerError)
		case "notjson":
			_, _ = fmt.Fprint(w, `<html>oops</html>`)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, exoticazFixture)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *exoticazSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestExoticaZ(t *testing.T, site *exoticazSite) *ExoticaZ {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewExoticaZ(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewExoticaZ: %v", err)
	}
	e := ix.(*ExoticaZ)
	e.SiteLinkValue = site.srv.URL + "/"
	e.AuthorizationToken = "tok123"
	// tests must not pay the upstream 6s request delay
	client.SetRequestDelay(e.ID(), 0)
	return e
}

const exoticazFixture = `{
  "data": [
    {
      "url": "https://exoticaz.faketest/torrent/10-clip",
      "download": "https://exoticaz.faketest/download/10",
      "category": {"1": "Video Clip"},
      "movie_tv": null,
      "created_at_iso": "2024-06-01 15:00:00",
      "file_name": "Some.Clip.1080p.x264",
      "release_title": "Some Clip",
      "info_hash": "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD",
      "leech": 5,
      "completed": 20,
      "seed": 8,
      "file_size": 734003200,
      "file_count": 2,
      "download_multiply": 0.0,
      "upload_multiply": 1.0,
      "video_quality": "1080p",
      "type": "XXX",
      "format": "x264",
      "audio": [{"id": 45, "language": "English"}],
      "subtitle": []
    },
    {
      "url": "https://exoticaz.faketest/torrent/20-photos",
      "download": "https://exoticaz.faketest/download/20",
      "category": {"7": "Photo Pack", "2": "Video Pack"},
      "movie_tv": null,
      "created_at_iso": "2024-06-02 16:00:00",
      "file_name": "Photo.Pack.2024",
      "release_title": "",
      "info_hash": "",
      "leech": 0,
      "completed": 1,
      "seed": 3,
      "file_size": 104857600,
      "file_count": 10,
      "download_multiply": 1.0,
      "upload_multiply": 1.0,
      "video_quality": "",
      "type": "XXX",
      "format": "",
      "audio": [],
      "subtitle": []
    }
  ]
}`

func TestExoticaZCaps(t *testing.T) {
	ix, err := NewExoticaZ(services.Deps{})
	if err != nil {
		t.Fatalf("NewExoticaZ: %v", err)
	}
	caps := ix.Capabilities()
	if caps.LimitsDefault == nil || *caps.LimitsDefault != 50 || caps.LimitsMax == nil || *caps.LimitsMax != 50 {
		t.Fatalf("limits: %v/%v", caps.LimitsDefault, caps.LimitsMax)
	}
	if len(caps.TVSearchParams) != 0 || len(caps.MovieSearchParams) != 0 {
		t.Fatalf("search params must be empty: tv=%v movie=%v", caps.TVSearchParams, caps.MovieSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 6040}, {"2", 6050}, {"3", 6050}, {"4", 6050},
		{"5", 6010}, {"6", 6040}, {"7", 6060}, {"8", 6060},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		// desc-based mappings create a custom 1:1 category riding along —
		// upstream parity; assert the real category is present.
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

func TestExoticaZConfigForm(t *testing.T) {
	ix, err := NewExoticaZ(services.Deps{})
	if err != nil {
		t.Fatalf("NewExoticaZ: %v", err)
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

func TestExoticaZSearchRequest(t *testing.T) {
	site := newExoticazSite(t)
	e := newTestExoticaZ(t, site)
	e.Config.Freeleech.Value = true

	// single mapped tracker category → category=<id>
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "A DVD"
	q.Categories = []int{models.CatXXXDVD.ID}
	if _, err := e.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	// multiple mapped tracker categories → category=0 (FirstIfSingleOrDefault)
	q2 := models.NewTorznabQuery()
	q2.QueryType = "search"
	q2.SearchTerm = "A Clip"
	q2.Categories = []int{models.CatXXXx264.ID}
	if _, err := e.ResultsForQuery(context.Background(), q2, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	// pagination + freeleech discount
	q3 := models.NewTorznabQuery()
	q3.QueryType = "search"
	q3.SearchTerm = "Paged"
	q3.Categories = []int{models.CatXXXDVD.ID}
	q3.Limit = 50
	q3.Offset = 100
	if _, err := e.ResultsForQuery(context.Background(), q3, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	paths := site.paths()
	want := []string{
		"/api/v1/jackett/torrents?in=1&category=5&limit=50&search=A+DVD&discount[]=1",
		"/api/v1/jackett/torrents?in=1&category=0&limit=50&search=A+Clip&discount[]=1",
		"/api/v1/jackett/torrents?in=1&category=5&limit=50&search=Paged&page=3&discount[]=1",
	}
	if fmt.Sprintf("%v", paths) != fmt.Sprintf("%v", want) {
		t.Fatalf("uris = %v, want %v", paths, want)
	}
}

func TestExoticaZParseFixture(t *testing.T) {
	site := newExoticazSite(t)
	e := newTestExoticaZ(t, site)

	releases, err := e.PerformQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	if err != nil {
		t.Fatalf("PerformQuery: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases: %d", len(releases))
	}

	r0 := releases[0]
	// no ParseTitle override: FileName is used verbatim
	if r0.Title != "Some.Clip.1080p.x264" {
		t.Fatalf("title: %q", r0.Title)
	}
	// ParseCategories: every row.Category key maps back to newznab
	found := false
	for _, id := range r0.Category {
		if id == models.CatXXXx264.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v", r0.Category)
	}
	if r0.Size == nil || *r0.Size != 734003200 {
		t.Fatalf("size: %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 8 || r0.Peers == nil || *r0.Peers != 13 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-06-01 15:00:00" {
		t.Fatalf("date: %v", r0.PublishDate)
	}
	if r0.Link == nil || r0.Link.String() != "https://exoticaz.faketest/download/10" {
		t.Fatalf("link: %v", r0.Link)
	}
	if r0.InfoHash != "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD" {
		t.Fatalf("infohash: %q", r0.InfoHash)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}

	// multiple category keys are flattened (SelectMany), keys sorted
	r1 := releases[1]
	if r1.Title != "Photo.Pack.2024" {
		t.Fatalf("title[1]: %q", r1.Title)
	}
	var got []int
	for _, id := range r1.Category {
		if id == models.CatXXXPack.ID || id == models.CatXXXImageSet.ID {
			got = append(got, id)
		}
	}
	if len(got) != 2 { // "2" → XXXPack, "7" → XXXImageSet
		t.Fatalf("category[1]: %v", r1.Category)
	}
}

func TestExoticaZErrorPath(t *testing.T) {
	site := newExoticazSite(t)
	e := newTestExoticaZ(t, site)

	_, err := e.PerformQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "servererror",
	})
	if err == nil || err.Error() != "Invalid status code 500 (InternalServerError) received from indexer" {
		t.Fatalf("err: %v", err)
	}

	// broken JSON surfaces as an indexer parse error
	_, err = e.PerformQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "notjson",
	})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindParse || ie.Message != "Parse error" {
		t.Fatalf("err: %v", err)
	}
}

func TestAvzHelpers(t *testing.T) {
	if got := avzFirstIfSingleOrDefault([]string{"7"}, "0"); got != "7" {
		t.Fatalf("single: %q", got)
	}
	if got := avzFirstIfSingleOrDefault([]string{"7", "8"}, "0"); got != "0" {
		t.Fatalf("multi: %q", got)
	}
	if got := avzFirstIfSingleOrDefault(nil, "0"); got != "0" {
		t.Fatalf("empty: %q", got)
	}
	if got := avzDistinctStrings([]string{"a", "b", "a", "c", "b"}); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("distinct: %v", got)
	}
}
