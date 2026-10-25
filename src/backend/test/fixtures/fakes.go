// Package fixtures provides the SPEC-07 integration-test fakes: an httptest
// fake tracker site, three hand-written fake indexers and one Cardigann
// engine fixture (overlay definition fakecardigann.yml), all registered
// into the global indexer registry exactly once per test binary.
package fixtures

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vestt/common/engine"
	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
)

//go:embed fakecardigann.yml
var fakeCardigannYAML []byte

// Site is the mutable fake tracker site. Every test builds a fresh one and
// publishes it via Setup; the registered factories resolve it at Init time
// and the fake indexers read it on every perform call.
type Site struct {
	*httptest.Server

	mu       sync.Mutex
	hits     int
	searches int
	delay    time.Duration
}

// NewSite starts the fake tracker site with the default routes.
func NewSite(t *testing.T) *Site {
	t.Helper()
	s := &Site{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits++
		delay := s.delay
		s.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		switch r.URL.Path {
		case "/page":
			_, _ = w.Write([]byte("fake tracker page"))
		case "/api/search":
			s.mu.Lock()
			s.searches++
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":[{"type":"torrent","id":"1","attributes":{
				"category_id":1,"name":"Cardigann Release",
				"details_link":%q,"download_link":%q,
				"seeders":7,"leechers":3,"size":2097152,
				"created_at":"02/01/2026 03:04:05"}}]}`,
				s.URL+"/details/1", s.URL+"/torrent")
		case "/torrent":
			// intentionally UNSORTED bencode dictionary keys
			// unsorted keys on purpose: the /dl proxy must re-encode canonically
			_, _ = w.Write([]byte("d4:spam4:eggs7:privatei1e1:ai2ee"))
		case "/img":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("PNG-BYTES"))
		case "/notfound":
			http.NotFound(w, r)
		case "/ratelimited":
			w.Header().Set("Retry-After", "42")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("slow down"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// Hits returns the total request count against the fake site.
func (s *Site) Hits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

// Searches returns the /api/search request count (Cardigann fixture hits).
func (s *Site) Searches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.searches
}

// SetDelay adds an artificial latency to every fake-site response.
func (s *Site) SetDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

// Behavior carries the per-test mutable knobs of the fake indexers.
type Behavior struct {
	// GoodPath is the fake-site path the "fakegood" indexer requests
	// ("/page" normally, "/ratelimited" for the 429 matrix case).
	GoodPath string
	// GoodErr, when set, makes fakegood's perform fail with this error.
	GoodErr error
	// FlakyErr is fakeflaky's perform error (the indexer configures fine).
	FlakyErr error
}

var behavior = struct {
	sync.Mutex
	b Behavior
}{b: Behavior{
	GoodPath: "/page",
	FlakyErr: &indexer.IndexerError{IndexerID: "fakeflaky", Kind: indexer.KindInternal, Message: "flaky indexer exploded"},
}}

// SetBehavior swaps the fake-indexer behavior knobs.
func SetBehavior(fn func(*Behavior)) {
	behavior.Lock()
	defer behavior.Unlock()
	fn(&behavior.b)
}

func currentBehavior() Behavior {
	behavior.Lock()
	defer behavior.Unlock()
	return behavior.b
}

var currentSite atomic.Pointer[Site]

func site() *Site { return currentSite.Load() }

// --- fake indexers -------------------------------------------------------

type fakeCfg struct {
	configdata.ConfigData
	APIKey *configdata.StringItem
}

func newFakeCfg() *fakeCfg {
	return &fakeCfg{
		ConfigData: *configdata.NewConfigData(),
		APIKey:     configdata.NewStringItem("API Key"),
	}
}

// fakeIndexer is a minimal indexer built on indexer.Base whose perform call
// goes through the shared fake site; ApplyConfiguration mirrors upstream
// success semantics (mark configured).
type fakeIndexer struct {
	indexer.Base
	id string
}

func (f *fakeIndexer) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if err := f.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	if err := f.LoadFromSavedConfiguration(ctx, []byte(`[]`)); err != nil {
		return indexer.ConfigCompleted, err
	}
	return indexer.ConfigCompleted, nil
}

func (f *fakeIndexer) perform(ctx context.Context, q *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	beh := currentBehavior()
	switch f.id {
	case "fakegood":
		if beh.GoodErr != nil {
			return nil, beh.GoodErr
		}
		if s := site(); s != nil {
			if _, err := f.hit(ctx, s.URL+beh.GoodPath); err != nil {
				return nil, err
			}
		}
		return goodReleases(), nil
	case "faketv":
		if s := site(); s != nil {
			if _, err := f.hit(ctx, s.URL+"/page"); err != nil {
				return nil, err
			}
		}
		return tvReleases(), nil
	case "fakeflaky":
		return nil, beh.FlakyErr
	}
	return nil, nil
}

func (f *fakeIndexer) hit(ctx context.Context, target string) (*httpclient.Result, error) {
	res, err := f.Client.Do(ctx, f.ID(), &httpclient.Request{URL: target})
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, &httpclient.ErrSiteDown{URL: target, Status: res.StatusCode}
	}
	return res, nil
}

func (f *fakeIndexer) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return f.ResultsForQueryWith(ctx, q, isMeta, f.perform)
}

func (f *fakeIndexer) Test(ctx context.Context) error {
	return f.Base.Test(ctx, f.perform)
}

func goodReleases() []*models.ReleaseInfo {
	base := "https://fake.example"
	if s := site(); s != nil {
		base = s.URL
	}
	dvf := 0.0
	imdb := int64(133093)
	sizeA := int64(1048576)
	sizeB := int64(2 * 1048576)
	seedersA := int64(10)
	peersA := int64(15)
	seedersB := int64(3)
	peersB := int64(5)
	tor, _ := url.Parse(base + "/torrent")
	guidA, _ := url.Parse(base + "/details/a")
	guidB, _ := url.Parse(base + "/details/b")
	magnet, _ := url.Parse("magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Good+Release+B")
	return []*models.ReleaseInfo{
		{
			Title:                "Good Release A",
			Guid:                 guidA,
			Link:                 tor,
			Category:             []int{2040},
			Size:                 &sizeA,
			Seeders:              &seedersA,
			Peers:                &peersA,
			PublishDate:          time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			Imdb:                 &imdb,
			DownloadVolumeFactor: &dvf,
		},
		{
			Title:       "Good Release B",
			Guid:        guidB,
			MagnetUri:   magnet,
			Category:    []int{2040},
			Size:        &sizeB,
			Seeders:     &seedersB,
			Peers:       &peersB,
			PublishDate: time.Date(2026, 1, 1, 3, 4, 5, 0, time.UTC),
		},
	}
}

func tvReleases() []*models.ReleaseInfo {
	guid, _ := url.Parse("https://fake.example/details/tv1")
	size := int64(512)
	seeders := int64(1)
	peers := int64(2)
	return []*models.ReleaseInfo{{
		Title:       "TV Release",
		Guid:        guid,
		Category:    []int{5040},
		Size:        &size,
		Seeders:     &seeders,
		Peers:       &peers,
		PublishDate: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}}
}

func newFakeBase(id, typ string, caps *models.Capabilities) indexer.Base {
	return indexer.Base{
		IDValue:          id,
		NameValue:        "Fake " + id,
		DescriptionValue: "Fake indexer " + id + " for integration tests",
		SiteLinkValue:    "https://fake.example/",
		DefaultSiteLink:  "https://fake.example/",
		LangValue:        "en-US",
		TypeValue:        typ,
		Config:           newFakeCfg(),
		Caps:             caps,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func fullCaps() *models.Capabilities {
	caps := &models.Capabilities{
		SearchAvailable:   true,
		TVSearchParams:    []string{"q", "season", "ep", "imdbid"},
		MovieSearchParams: []string{"q", "imdbid", "tmdbid"},
		MusicSearchParams: []string{"q"},
		BookSearchParams:  []string{"q"},
		Categories:        models.NewCapabilitiesCategories(),
	}
	caps.Categories.AddCategoryTree(&models.Category{ID: 2040, Name: "Movies/HD"})
	caps.Categories.AddCategoryTree(&models.Category{ID: 5040, Name: "TV/HD"})
	return caps
}

func tvCaps() *models.Capabilities {
	caps := &models.Capabilities{
		SearchAvailable: true,
		TVSearchParams:  []string{"q", "season", "ep"},
		Categories:      models.NewCapabilitiesCategories(),
	}
	caps.Categories.AddCategoryTree(&models.Category{ID: 5040, Name: "TV/HD"})
	return caps
}

// --- registry ------------------------------------------------------------

var (
	registerOnce sync.Once
	registerErr  error
)

// Setup publishes the site to the factories and registers all fake
// indexers (three hand-written + the Cardigann overlay fixture) into the
// global registry on first use.
func Setup(t *testing.T, s *Site) {
	t.Helper()
	currentSite.Store(s)
	registerOnce.Do(func() {
		registerErr = registerAll()
	})
	if registerErr != nil {
		t.Fatalf("register fake indexers: %v", registerErr)
	}
}

func registerAll() error {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Cardigann fixture: overlay definition loaded once.
	dir, err := os.MkdirTemp("", "vestt-fixture-defs")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "fakecardigann.yml"), fakeCardigannYAML, 0o644); err != nil {
		return err
	}
	defs, _, err := engine.LoadAll(logger, dir)
	if err != nil {
		return err
	}
	def, ok := defs["fakecardigann"]
	if !ok {
		return fmt.Errorf("fixture definition fakecardigann not loaded")
	}
	services.RegisterCardigann(func(deps services.Deps) ([]indexer.Indexer, error) {
		x, err := engine.NewCardigann(def, logger)
		if err != nil {
			return nil, err
		}
		x.Client = deps.Client
		x.Saver = deps.Saver
		x.Cache = deps.Cache
		return []indexer.Indexer{x}, nil
	})

	for _, spec := range []struct {
		id   string
		typ  string
		caps *models.Capabilities
	}{
		{"fakegood", "public", fullCaps()},
		{"faketv", "public", tvCaps()},
		{"fakeflaky", "private", fullCaps()},
	} {
		spec := spec
		services.Register(spec.id, func(deps services.Deps) (indexer.Indexer, error) {
			f := &fakeIndexer{id: spec.id}
			f.Base = newFakeBase(spec.id, spec.typ, spec.caps)
			f.Client = deps.Client
			f.Saver = deps.Saver
			f.Cache = deps.Cache
			return f, nil
		})
	}
	return nil
}
