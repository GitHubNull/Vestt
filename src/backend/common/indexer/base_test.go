package indexer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
)

type testConfig struct {
	configdata.ConfigData
	Username *configdata.StringItem
	Password *configdata.PasswordItem
}

func newTestConfig() *testConfig {
	return &testConfig{
		ConfigData: *configdata.NewConfigData(),
		Username:   configdata.NewStringItem("Username"),
		Password:   configdata.NewPasswordItem("Password"),
	}
}

func newTestBase(cfg any) *Base {
	if cfg == nil {
		cfg = newTestConfig()
	}
	return &Base{
		IDValue:          "testidx",
		NameValue:        "Test Indexer",
		DescriptionValue: "a test indexer",
		SiteLinkValue:    "https://example.com/",
		DefaultSiteLink:  "https://example.com/",
		LangValue:        "en-US",
		TypeValue:        "public",
		Config:           cfg,
		Caps: &models.Capabilities{
			SearchAvailable: true,
			Categories:      models.NewCapabilitiesCategories(),
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func int64p(v int64) *int64 { return &v }

// --- configuration lifecycle ----------------------------------------------

// TestLoadFromSavedConfiguration mirrors upstream: a JSON array configures
// the indexer (with site-link normalization); anything else is ignored.
func TestLoadFromSavedConfiguration(t *testing.T) {
	cfg := newTestConfig()
	b := newTestBase(cfg)

	raw := `[{"id":"sitelink","value":"https://tracker.example.org"},{"id":"username","value":"alice"},{"id":"password","value":"secret"}]`
	if err := b.LoadFromSavedConfiguration(context.Background(), []byte(raw)); err != nil {
		t.Fatal(err)
	}
	if !b.IsConfigured() {
		t.Fatal("expected configured")
	}
	if got := b.ConfigData().SiteLink.Value; got != "https://tracker.example.org/" {
		t.Fatalf("site link: %q", got)
	}
	if b.SiteLink() != "https://tracker.example.org/" {
		t.Fatalf("SiteLink(): %q", b.SiteLink())
	}
	if cfg.Username.Value != "alice" || cfg.Password.Value != "secret" {
		t.Fatalf("items: %q / %q", cfg.Username.Value, cfg.Password.Value)
	}

	// old (non-array) format: warn, ignore, stay unconfigured
	b2 := newTestBase(nil)
	if err := b2.LoadFromSavedConfiguration(context.Background(), []byte(`{"legacy":true}`)); err != nil {
		t.Fatal(err)
	}
	if b2.IsConfigured() {
		t.Fatal("old format must not configure the indexer")
	}

	// empty site link falls back to the default
	b3 := newTestBase(nil)
	raw3 := `[{"id":"username","value":"bob"}]`
	if err := b3.LoadFromSavedConfiguration(context.Background(), []byte(raw3)); err != nil {
		t.Fatal(err)
	}
	if got := b3.ConfigData().SiteLink.Value; got != "https://example.com/" {
		t.Fatalf("default site link: %q", got)
	}
}

// TestLoadFromSavedConfigurationLegacyLink: a legacy site link is reset to
// the default (upstream LoadValuesFromJson).
func TestLoadFromSavedConfigurationLegacyLink(t *testing.T) {
	b := newTestBase(nil)
	b.LegacySiteLinks = []string{"https://old.example.com/"}
	raw := `[{"id":"sitelink","value":"https://old.example.com/"}]`
	if err := b.LoadFromSavedConfiguration(context.Background(), []byte(raw)); err != nil {
		t.Fatal(err)
	}
	if got := b.ConfigData().SiteLink.Value; got != "https://example.com/" {
		t.Fatalf("legacy site link not reset: %q", got)
	}
}

// TestSetLastErrorPersists mirrors upstream: persist only when configured
// and the value changed.
func TestSetLastErrorPersists(t *testing.T) {
	var saved []byte
	b := newTestBase(nil)
	b.Saver = func(_ context.Context, id string, configJSON []byte) error {
		if id != "testidx" {
			t.Fatalf("saver id: %q", id)
		}
		saved = append([]byte(nil), configJSON...)
		return nil
	}

	b.SetLastError("boom") // not configured: no save
	if saved != nil {
		t.Fatal("save without configured flag")
	}

	if err := b.LoadFromSavedConfiguration(context.Background(), []byte(`[]`)); err != nil {
		t.Fatal(err)
	}
	b.SetLastError("boom again") // changed → save
	if saved == nil {
		t.Fatal("expected save on change")
	}
	if !strings.Contains(string(saved), `"boom again"`) {
		t.Fatalf("saved config: %s", saved)
	}
	saved = nil
	b.SetLastError("boom again") // unchanged: no save
	if saved != nil {
		t.Fatal("save on unchanged value")
	}
}

// TestApplyItems: posted ConfigItems load into matching items; the Cookie
// field aliases the value (upstream cookie channel).
func TestApplyItems(t *testing.T) {
	cfg := newTestConfig()
	b := newTestBase(cfg)
	items := []ConfigItem{
		{ID: "username", Value: "carol"},
		{ID: "password", Value: "hunter2"},
		{ID: "cookieheader", Cookie: "uid=42; pass=xyz"},
	}
	if err := b.ApplyItems(items); err != nil {
		t.Fatal(err)
	}
	if cfg.Username.Value != "carol" || cfg.Password.Value != "hunter2" {
		t.Fatalf("items: %q / %q", cfg.Username.Value, cfg.Password.Value)
	}
	if got := b.CookieHeader(); got != "uid=42; pass=xyz" {
		t.Fatalf("cookie header: %q", got)
	}
}

func TestUnconfigureAndReset(t *testing.T) {
	b := newTestBase(nil)
	raw := `[{"id":"sitelink","value":"https://tracker.example.org/"},{"id":"cookieheader","value":"uid=1"}]`
	if err := b.LoadFromSavedConfiguration(context.Background(), []byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := b.Unconfigure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.IsConfigured() {
		t.Fatal("still configured")
	}
	if got := b.SiteLink(); got != "https://example.com/" {
		t.Fatalf("site link not restored: %q", got)
	}
	if got := b.CookieHeader(); got != "" {
		t.Fatalf("cookies not cleared: %q", got)
	}

	// ResetBaseConfig mirrors upstream (clear cookies + configured flag)
	if err := b.LoadFromSavedConfiguration(context.Background(), []byte(raw)); err != nil {
		t.Fatal(err)
	}
	b.ResetBaseConfig()
	if b.IsConfigured() || b.CookieHeader() != "" {
		t.Fatal("ResetBaseConfig incomplete")
	}
}

// --- FilterResults / FixResults --------------------------------------------

func testCapsWithMovies() *models.Capabilities {
	caps := &models.Capabilities{
		SearchAvailable: true,
		Categories:      models.NewCapabilitiesCategories(),
	}
	// AddCategoryTree adds parents bare and attaches children under them
	// (upstream AddTorznabCategoryTree).
	caps.Categories.AddCategoryTree(&models.Category{ID: 2000, Name: "Movies"})
	caps.Categories.AddCategoryTree(&models.Category{ID: 2040, Name: "Movies/HD"})
	return caps
}

func TestFilterResultsValidity(t *testing.T) {
	b := newTestBase(nil)
	b.Caps = testCapsWithMovies()

	good := &models.ReleaseInfo{
		Title:    "Some Movie 2020 1080p",
		Size:     int64p(1000),
		Category: []int{2040},
		Details:  mustURL(t, "https://example.com/t/1"),
	}
	missingSize := &models.ReleaseInfo{Title: "x", Category: []int{2040}}
	missingCat := &models.ReleaseInfo{Title: "y", Size: int64p(1)}
	noTitle := &models.ReleaseInfo{Size: int64p(1), Category: []int{2040}}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	got := b.FilterResults(q, []*models.ReleaseInfo{good, missingSize, missingCat, noTitle})
	if len(got) != 1 || got[0] != good {
		t.Fatalf("validity filter: %d results", len(got))
	}

	// interactive search keeps size/category-less releases
	q.InteractiveSearch = true
	got = b.FilterResults(q, []*models.ReleaseInfo{good, missingSize, noTitle})
	if len(got) != 2 {
		t.Fatalf("interactive filter: %d results", len(got))
	}
}

func TestFilterResultsCategoriesAndLimit(t *testing.T) {
	b := newTestBase(nil)
	b.Caps = testCapsWithMovies()

	mk := func(cat int, title string) *models.ReleaseInfo {
		return &models.ReleaseInfo{Title: title, Size: int64p(1), Category: []int{cat}}
	}
	// a release without any category at all is kept (upstream Any() check);
	// interactive search bypasses the validity rules so the category
	// intersection branch can be observed on its own
	uncat := &models.ReleaseInfo{Title: "uncategorized kept", Size: int64p(1)}
	// parent category 2000 expands to include child 2040
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.InteractiveSearch = true
	q.Categories = []int{2000}
	got := b.FilterResults(q, []*models.ReleaseInfo{
		mk(2040, "child match"),
		mk(3000, "no match"),
		uncat,
	})
	if len(got) != 2 || got[0].Title != "child match" || got[1].Title != "uncategorized kept" {
		t.Fatalf("category filter: %+v", got)
	}

	// limit
	q.Limit = 1
	got = b.FilterResults(q, []*models.ReleaseInfo{mk(2040, "a"), mk(2040, "b")})
	if len(got) != 1 || got[0].Title != "a" {
		t.Fatalf("limit filter: %+v", got)
	}
}

func TestFixResults(t *testing.T) {
	b := newTestBase(nil)
	future := time.Now().Add(2 * time.Hour)

	r := &models.ReleaseInfo{
		Title:       "Movie",
		InfoHash:    "0123456789ABCDEF0123456789ABCDEF01234567",
		PublishDate: future,
		Details:     mustURL(t, "https://example.com/t/9"),
	}
	b.FixResults([]*models.ReleaseInfo{r})

	if r.Origin.ID != "testidx" || r.Origin.Name != "Test Indexer" || r.Origin.Type != "public" {
		t.Fatalf("origin: %+v", r.Origin)
	}
	if r.PublishDate.After(time.Now().Add(time.Minute)) {
		t.Fatal("publish date not clamped")
	}
	if r.MagnetUri == nil {
		t.Fatal("magnet not derived from infohash")
	}
	if got := r.MagnetUri.Query().Get("xt"); got != "urn:btih:0123456789ABCDEF0123456789ABCDEF01234567" {
		t.Fatalf("magnet xt: %q", got)
	}
	// the derived magnet takes guid precedence over Details (upstream
	// FixResults order: magnet derivation runs before the guid fallback)
	if r.Guid == nil || r.Guid.String() != r.MagnetUri.String() {
		t.Fatalf("guid fallback: %v", r.Guid)
	}

	// guid falls back to Details when there is no link or magnet
	rd := &models.ReleaseInfo{Title: "d", Details: mustURL(t, "https://example.com/t/9")}
	b.FixResults([]*models.ReleaseInfo{rd})
	if rd.Guid == nil || rd.Guid.String() != "https://example.com/t/9" {
		t.Fatalf("details guid fallback: %v", rd.Guid)
	}

	// private indexers must not get a public magnet
	priv := newTestBase(nil)
	priv.TypeValue = "private"
	rp := &models.ReleaseInfo{Title: "x", InfoHash: "0123456789ABCDEF0123456789ABCDEF01234567"}
	priv.FixResults([]*models.ReleaseInfo{rp})
	if rp.MagnetUri != nil {
		t.Fatal("private indexer derived a magnet")
	}

	// magnet → infohash derivation
	rm := &models.ReleaseInfo{
		Title:     "y",
		MagnetUri: mustURL(t, "magnet:?xt=urn:btih:AAAAAAAABBBBBBBBCCCCCCCCDDDDDDDDEEEEEEEE"),
	}
	b.FixResults([]*models.ReleaseInfo{rm})
	if rm.InfoHash != "AAAAAAAABBBBBBBBCCCCCCCCDDDDDDDDEEEEEEEE" {
		t.Fatalf("infohash: %q", rm.InfoHash)
	}
	if rm.Guid == nil || rm.Guid.Scheme != "magnet" {
		t.Fatalf("guid from magnet: %v", rm.Guid)
	}

	// guid prefers Link over Details
	rl := &models.ReleaseInfo{
		Title:   "z",
		Link:    mustURL(t, "https://example.com/download/1.torrent"),
		Details: mustURL(t, "https://example.com/t/1"),
	}
	b.FixResults([]*models.ReleaseInfo{rl})
	if rl.Guid.String() != "https://example.com/download/1.torrent" {
		t.Fatalf("guid precedence: %v", rl.Guid)
	}
}

// TestInfoHashToPublicMagnet: empty inputs yield nil; the tracker list is
// appended (MagnetUtil._Trackers).
func TestInfoHashToPublicMagnet(t *testing.T) {
	if InfoHashToPublicMagnet("", "title") != nil {
		t.Fatal("empty hash must yield nil")
	}
	if InfoHashToPublicMagnet("abc", " ") != nil {
		t.Fatal("empty title must yield nil")
	}
	m := InfoHashToPublicMagnet("0123456789ABCDEF0123456789ABCDEF01234567", "My Movie 2020")
	if m == nil {
		t.Fatal("nil magnet")
	}
	q := m.Query()
	if q.Get("dn") != "My Movie 2020" {
		t.Fatalf("dn: %q", q.Get("dn"))
	}
	if len(q["tr"]) != 10 {
		t.Fatalf("trackers: %d", len(q["tr"]))
	}
	if q["tr"][0] != "udp://tracker.opentrackr.org:1337/announce" {
		t.Fatalf("first tracker: %q", q["tr"][0])
	}
}

// --- ResultsForQueryWith ----------------------------------------------------

type fakeCache struct {
	searchHits int
	cached     []*models.ReleaseInfo
	stored     [][]*models.ReleaseInfo
	ttl        time.Duration
}

func (f *fakeCache) Search(_ string, _ *models.TorznabQuery) []*models.ReleaseInfo {
	f.searchHits++
	return f.cached
}
func (f *fakeCache) CacheResults(_, _, _ string, _ *models.TorznabQuery, releases []*models.ReleaseInfo) {
	f.stored = append(f.stored, releases)
}
func (f *fakeCache) TTL() time.Duration { return f.ttl }

func TestResultsForQueryDedupesAndCaches(t *testing.T) {
	b := newTestBase(nil)
	b.Caps = testCapsWithMovies()
	fc := &fakeCache{ttl: 2100 * time.Second}
	b.Cache = fc

	link := mustURL(t, "https://example.com/t/1")
	r1 := &models.ReleaseInfo{Title: "a", Size: int64p(1), Category: []int{2040}, Link: link}
	r2 := &models.ReleaseInfo{Title: "a dup", Size: int64p(1), Category: []int{2040}, Link: link}
	r3 := &models.ReleaseInfo{Title: "b", Size: int64p(1), Category: []int{2040}, Details: mustURL(t, "https://example.com/t/2")}

	perform := func(_ context.Context, q *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
		if !q.IsTest {
			// the template must operate on a clone
			q.SearchTerm = "mutated"
		}
		return []*models.ReleaseInfo{r1, r2, r3}, nil
	}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.IsTest = true
	res, err := b.ResultsForQueryWith(context.Background(), q, false, perform)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("dedupe: %d releases", len(res.Releases))
	}
	if len(fc.stored) != 1 || len(fc.stored[0]) != 2 {
		t.Fatalf("cache store: %+v", fc.stored)
	}
	if !b.IsHealthy() {
		t.Fatal("expected healthy after success")
	}
	if q.SearchTerm != "" {
		t.Fatal("original query mutated")
	}
}

func TestResultsForQueryCacheHit(t *testing.T) {
	b := newTestBase(nil)
	b.Caps = testCapsWithMovies()
	cached := []*models.ReleaseInfo{{Title: "cached"}}
	fc := &fakeCache{cached: cached}
	b.Cache = fc

	performCalled := false
	perform := func(context.Context, *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
		performCalled = true
		return nil, nil
	}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := b.ResultsForQueryWith(context.Background(), q, false, perform)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsFromCache || len(res.Releases) != 1 || res.Releases[0] != cached[0] {
		t.Fatalf("cache hit: %+v", res)
	}
	if performCalled {
		t.Fatal("perform must not run on cache hit")
	}
}

func TestResultsForQueryGates(t *testing.T) {
	b := newTestBase(nil)
	b.Caps = testCapsWithMovies() // search only

	perform := func(context.Context, *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
		t.Fatal("perform must not run")
		return nil, nil
	}

	// unsupported query type (tvsearch without tv params)
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	res, err := b.ResultsForQueryWith(context.Background(), q, false, perform)
	if err != nil || len(res.Releases) != 0 {
		t.Fatalf("tvsearch gate: %v %+v", err, res)
	}

	// all categories unsupported
	q = models.NewTorznabQuery()
	q.QueryType = "search"
	q.Categories = []int{8000} // Books root; not in our caps
	res, err = b.ResultsForQueryWith(context.Background(), q, false, perform)
	if err != nil || len(res.Releases) != 0 {
		t.Fatalf("category gate: %v %+v", err, res)
	}
}

func TestResultsForQueryErrorWrapping(t *testing.T) {
	b := newTestBase(nil)
	b.Caps = testCapsWithMovies()

	// TooManyRequests → KindRateLimit with RetryAfter, no backoff counter
	perform429 := func(context.Context, *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
		return nil, &httpclient.TooManyRequests{Message: "Remote server returned 429", RetryAfter: 2 * time.Minute}
	}
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := b.ResultsForQueryWith(context.Background(), q, false, perform429)
	var ie *IndexerError
	if !errors.As(err, &ie) {
		t.Fatalf("expected IndexerError, got %v", err)
	}
	if ie.Kind != KindRateLimit || ie.RetryAfter != 2*time.Minute {
		t.Fatalf("rate limit wrap: %+v", ie)
	}
	if ie.Error() != "Exception (testidx): Remote server returned 429" {
		t.Fatalf("error format: %q", ie.Error())
	}
	if b.IsFailing() {
		t.Fatal("rate limit must not use the error backoff")
	}

	// generic failure → KindInternal + exponential backoff health window
	boom := errors.New("parse exploded")
	performErr := func(context.Context, *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
		return nil, fmt.Errorf("outer: %w", boom)
	}
	for i := 0; i < 3; i++ {
		_, err = b.ResultsForQueryWith(context.Background(), q, false, performErr)
	}
	if !errors.As(err, &ie) || ie.Kind != KindInternal {
		t.Fatalf("internal wrap: %v", err)
	}
	if ie.Message != "parse exploded" {
		t.Fatalf("root message: %q", ie.Message)
	}
	if !b.IsFailing() {
		t.Fatal("expected failing state after errors")
	}

	// IndexerError from a perform implementation passes through unchanged
	sentinel := &IndexerError{IndexerID: "testidx", Kind: KindParse, Message: "bad row"}
	performIE := func(context.Context, *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
		return nil, sentinel
	}
	_, err = b.ResultsForQueryWith(context.Background(), q, false, performIE)
	if !errors.Is(err, sentinel) {
		t.Fatalf("passthrough: %v", err)
	}
}

// TestResultsForQueryBackoffDoubling mirrors the upstream error validity:
// 10 min × 2^n capped at 24 h.
func TestResultsForQueryBackoffDoubling(t *testing.T) {
	b := newTestBase(nil)
	b.Caps = testCapsWithMovies()
	performErr := func(context.Context, *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
		return nil, errors.New("nope")
	}
	q := models.NewTorznabQuery()
	q.QueryType = "search"

	_, _ = b.ResultsForQueryWith(context.Background(), q, false, performErr)
	first := time.Until(b.expireAt)
	if first < 9*time.Minute || first > 11*time.Minute {
		t.Fatalf("first backoff: %v", first)
	}
	_, _ = b.ResultsForQueryWith(context.Background(), q, false, performErr)
	second := time.Until(b.expireAt)
	if second < 19*time.Minute || second > 21*time.Minute {
		t.Fatalf("second backoff: %v", second)
	}
}

// --- Test + Download --------------------------------------------------------

func TestTestMethod(t *testing.T) {
	b := newTestBase(nil)
	b.Caps = testCapsWithMovies()

	// empty results → upstream error message
	err := b.Test(context.Background(), func(context.Context, *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "Found no results while trying to browse this tracker") {
		t.Fatalf("empty test: %v", err)
	}

	// perform error propagates
	performErr := func(context.Context, *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
		return nil, errors.New("down")
	}
	if err := b.Test(context.Background(), performErr); err == nil {
		t.Fatal("expected error")
	}

	// success
	ok := func(context.Context, *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
		return []*models.ReleaseInfo{{Title: "x", Size: int64p(1), Category: []int{2040}}}, nil
	}
	if err := b.Test(context.Background(), ok); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadMagnetPassthrough(t *testing.T) {
	b := newTestBase(nil)
	m := mustURL(t, "magnet:?xt=urn:btih:ABC")
	body, err := b.Download(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != m.String() {
		t.Fatalf("magnet passthrough: %q", body)
	}
}

func newHTTPTestClient(t *testing.T) *httpclient.Client {
	t.Helper()
	c, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDownloadHTTPEscapingAndStatus(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		_, _ = w.Write([]byte("torrent-bytes"))
	}))
	defer srv.Close()

	b := newTestBase(nil)
	b.Client = newHTTPTestClient(t)
	link := mustURL(t, srv.URL+"/dl/Movie(2020)'s.torrent")
	body, err := b.Download(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "torrent-bytes" {
		t.Fatalf("body: %q", body)
	}
	if !strings.Contains(gotURL, "Movie%282020%29%27s.torrent") {
		t.Fatalf("escaped url: %q", gotURL)
	}

	// non-OK status → error
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srvErr.Close()
	_, err = b.Download(context.Background(), mustURL(t, srvErr.URL+"/x"))
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("status error: %v", err)
	}
}

func TestDownloadResolveHook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/details/1" {
			_, _ = w.Write([]byte("<html></html>"))
			return
		}
		_, _ = w.Write([]byte("real-torrent"))
	}))
	defer srv.Close()

	b := newTestBase(nil)
	b.Client = newHTTPTestClient(t)
	b.ResolveDownload = func(_ context.Context, link *url.URL) (string, error) {
		return srv.URL + "/real.torrent", nil
	}
	body, err := b.Download(context.Background(), mustURL(t, srv.URL+"/details/1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "real-torrent" {
		t.Fatalf("body: %q", body)
	}
}

// TestConfigurationForSetupPrefillsSiteLink: the setup form shows the
// default site link for an unconfigured indexer (upstream behavior;
// the stored value only exists after the first LoadValuesFromJSON).
func TestConfigurationForSetupPrefillsSiteLink(t *testing.T) {
	b := newTestBase(nil)
	form, err := b.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(form) == 0 {
		t.Fatal("empty setup form")
	}
	if form[0]["id"] != "sitelink" {
		t.Fatalf("first item = %v, want sitelink", form[0]["id"])
	}
	if got := form[0]["value"]; got != "https://example.com/" {
		t.Fatalf("sitelink value = %v, want default link", got)
	}
}
