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

// scenehdPasskey is a valid-length (32 chars) fake passkey.
const scenehdPasskey = "0123456789abcdef0123456789abcdef"

type scenehdSite struct {
	srv *httptest.Server

	mu   sync.Mutex
	hits []string
}

func newSceneHDSite(t *testing.T) *scenehdSite {
	t.Helper()
	s := &scenehdSite{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.mu.Unlock()
		switch r.URL.Path {
		case "/browse.php":
			if r.URL.Query().Get("passkey") != scenehdPasskey {
				_, _ = fmt.Fprint(w, "User not found or passkey not set")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, sceneHDFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *scenehdSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestSceneHD(t *testing.T, site *scenehdSite) *SceneHD {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewSceneHD(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewSceneHD: %v", err)
	}
	s := ix.(*SceneHD)
	s.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(s.ID(), 0)
	return s
}

const sceneHDFixture = `[
  {
    "id": 1001,
    "name": "Some.Movie.2024.1080p.BluRay.x264-GRP",
    "added": "2024-02-01 10:20:30",
    "is_freeleech": 1,
    "size": 5368709120,
    "times_completed": 33,
    "numfiles": 12,
    "seeders": 20,
    "leechers": 5,
    "imdbid": "tt0133093",
    "category": "1"
  },
  {
    "id": 1002,
    "name": "Some.Show.S03E07.720p.HDTV.x264-GRP",
    "added": "2024-02-02 11:22:33",
    "is_freeleech": 0,
    "size": 1073741824,
    "times_completed": 7,
    "numfiles": 3,
    "seeders": 9,
    "leechers": 1,
    "imdbid": "",
    "category": "5"
  }
]`

func TestSceneHDCaps(t *testing.T) {
	ix, err := NewSceneHD(services.Deps{})
	if err != nil {
		t.Fatalf("NewSceneHD: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{"q", "season", "ep"}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{"q", "imdbid"}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if strings.Join(caps.MusicSearchParams, ",") != "q" {
		t.Fatalf("MusicSearchParams: %v", caps.MusicSearchParams)
	}
	// desc mappings create custom 1:1 categories (id ≥ 100000) that ride
	// along in MapTrackerCatToNewznab (TorznabCapabilitiesCategories.cs:58-73)
	// — assert contains, never exact equality
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"2", 2045}, {"1", 2040}, {"4", 2040},
		{"8", 2050}, {"6", 5045}, {"5", 5040},
		{"7", 5040}, {"22", 2050}, {"10", 6000},
		{"16", 2020}, {"13", 3020}, {"9", 8000},
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

func TestSceneHDConfigForm(t *testing.T) {
	ix, err := NewSceneHD(services.Deps{})
	if err != nil {
		t.Fatalf("NewSceneHD: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, fmt.Sprintf("%s:%s", it["id"].(string), it["type"].(string)))
	}
	want := []string{"sitelink:inputstring", "passkey:inputstring", ":displayinfo", "tags:inputtags"}
	if fmt.Sprintf("%v", ids) != fmt.Sprintf("%v", want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestSceneHDApplyConfiguration(t *testing.T) {
	site := newSceneHDSite(t)
	s := newTestSceneHD(t, site)

	status, err := s.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "passkey", Value: scenehdPasskey},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted || !s.IsConfigured() {
		t.Fatalf("status/configured: %v/%v", status, s.IsConfigured())
	}
	paths := site.paths()
	if len(paths) != 1 || paths[0] != "/browse.php?api=&passkey="+scenehdPasskey+"&search=" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestSceneHDBadPasskeyLength(t *testing.T) {
	site := newSceneHDSite(t)
	s := newTestSceneHD(t, site)

	_, err := s.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "passkey", Value: "tooshort"},
	})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindConfig {
		t.Fatalf("err: %v", err)
	}
	if ie.Message != "Invalid Passkey configured. Expected length: 32" {
		t.Fatalf("message: %q", ie.Message)
	}
	if s.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
	if len(site.paths()) != 0 {
		t.Fatalf("no request must be made with a bad passkey: %v", site.paths())
	}
}

func TestSceneHDSearchRequestAndParse(t *testing.T) {
	site := newSceneHDSite(t)
	s := newTestSceneHD(t, site)
	if err := s.ApplyItems([]indexer.ConfigItem{
		{ID: "passkey", Value: scenehdPasskey},
	}); err != nil {
		t.Fatalf("ApplyItems: %v", err)
	}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	q.Categories = []int{models.CatMoviesHD.ID, models.CatTVHD.ID}
	if _, err := s.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	// imdb search: the imdb id replaces the search term
	if _, err := s.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "movie", ImdbID: "tt0133093",
	}, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	paths := site.paths()
	// upstream maps 2040 → trackers {1,4} and 5040 → {5,7} (SceneHD.cs:66-72
	// caps; TorznabCapabilitiesCategories.cs:76-83), joined with Distinct
	want := []string{
		"/browse.php?api=&passkey=" + scenehdPasskey + "&search=Some+Movie&cat=1%2C4%2C5%2C7",
		"/browse.php?api=&passkey=" + scenehdPasskey + "&search=tt0133093",
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
	if r0.Title != "Some.Movie.2024.1080p.BluRay.x264-GRP" {
		t.Fatalf("title: %q", r0.Title)
	}
	if !hasInt(r0.Category, models.CatMoviesHD.ID) {
		t.Fatalf("category: %v", r0.Category)
	}
	if r0.Size == nil || *r0.Size != 5368709120 {
		t.Fatalf("size: %v", r0.Size)
	}
	if r0.Seeders == nil || *r0.Seeders != 20 || r0.Peers == nil || *r0.Peers != 25 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.Grabs == nil || *r0.Grabs != 33 || r0.Files == nil || *r0.Files != 12 {
		t.Fatalf("grabs/files: %v/%v", r0.Grabs, r0.Files)
	}
	if r0.PublishDate.Format("2006-01-02 15:04:05") != "2024-02-01 10:20:30" {
		t.Fatalf("date: %v", r0.PublishDate)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}
	if r0.UploadVolumeFactor == nil || *r0.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r0.UploadVolumeFactor)
	}
	if r0.Imdb == nil || *r0.Imdb != 133093 {
		t.Fatalf("imdb: %v", r0.Imdb)
	}
	if r0.Link == nil || r0.Link.String() != site.srv.URL+"/download.php?id=1001&passkey="+scenehdPasskey {
		t.Fatalf("link: %v", r0.Link)
	}
	if r0.Details == nil || r0.Details.String() != site.srv.URL+"/details.php?id=1001" {
		t.Fatalf("details: %v", r0.Details)
	}
	if r0.MinimumSeedTime == nil || *r0.MinimumSeedTime != 0 {
		t.Fatalf("minimum seed time: %v", r0.MinimumSeedTime)
	}

	r1 := releases[1]
	if !hasInt(r1.Category, models.CatTVHD.ID) {
		t.Fatalf("category[1]: %v", r1.Category)
	}
	if r1.DownloadVolumeFactor == nil || *r1.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf[1]: %v", r1.DownloadVolumeFactor)
	}
	if r1.Imdb != nil {
		t.Fatalf("imdb[1] must be nil: %v", r1.Imdb)
	}
}

func TestSceneHDInvalidPasskeyIsAuth(t *testing.T) {
	site := newSceneHDSite(t)
	s := newTestSceneHD(t, site)
	if err := s.ApplyItems([]indexer.ConfigItem{
		{ID: "passkey", Value: "fedcba9876543210fedcba9876543210"},
	}); err != nil {
		t.Fatalf("ApplyItems: %v", err)
	}

	_, err := s.PerformQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindAuth {
		t.Fatalf("err: %v", err)
	}
	if ie.Message != "The passkey is invalid. Check the indexer configuration." {
		t.Fatalf("message: %q", ie.Message)
	}
}
