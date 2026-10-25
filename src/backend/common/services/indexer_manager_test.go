package services

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/store"
)

// fakeCfg mirrors a minimal handwritten-indexer configuration with mixed
// item types (SPEC-04 acceptance #5).
type fakeCfg struct {
	configdata.ConfigData
	APIKey    *configdata.StringItem
	Password  *configdata.PasswordItem
	Freeleech *configdata.BoolItem
	Sort      *configdata.SelectItem
}

func newFakeCfg() *fakeCfg {
	return &fakeCfg{
		ConfigData: *configdata.NewConfigData(),
		APIKey:     configdata.NewStringItem("API Key"),
		Password:   configdata.NewPasswordItem("Password"),
		Freeleech:  configdata.NewBoolItem("Freeleech Only"),
		Sort: configdata.NewSelectItem("Sort By", []configdata.Option{
			{Value: "seeders", Label: "Seeders"},
			{Value: "leechers", Label: "Leechers"},
		}),
	}
}

// fakeIndexer is a minimal indexer.Indexer built on indexer.Base: perform
// hits an httptest fake site, ApplyConfiguration mirrors the upstream
// success semantics (mark configured).
type fakeIndexer struct {
	indexer.Base
	applyStatus indexer.ConfigStatus
	applyErr    error
	siteURL     string // httptest fake site; empty = no HTTP
	releases    []*models.ReleaseInfo
}

func (f *fakeIndexer) ApplyConfiguration(ctx context.Context, items []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	if f.applyErr != nil {
		return indexer.ConfigCompleted, f.applyErr
	}
	if err := f.ApplyItems(items); err != nil {
		return indexer.ConfigCompleted, err
	}
	// mirror upstream: a successful apply marks the indexer configured
	if err := f.LoadFromSavedConfiguration(ctx, []byte(`[]`)); err != nil {
		return indexer.ConfigCompleted, err
	}
	return f.applyStatus, nil
}

func (f *fakeIndexer) perform(ctx context.Context, _ *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	if f.siteURL != "" {
		res, err := f.Client.Do(ctx, f.ID(), &httpclient.Request{URL: f.siteURL})
		if err != nil {
			return nil, err
		}
		if res.StatusCode != http.StatusOK {
			return nil, &httpclient.ErrSiteDown{URL: f.siteURL, Status: res.StatusCode}
		}
	}
	return f.releases, nil
}

func (f *fakeIndexer) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, isMeta bool) (*indexer.IndexerResult, error) {
	return f.ResultsForQueryWith(ctx, q, isMeta, f.perform)
}

func (f *fakeIndexer) Test(ctx context.Context) error {
	return f.Base.Test(ctx, f.perform)
}

func fakeCaps() *models.Capabilities {
	caps := &models.Capabilities{
		SearchAvailable: true,
		Categories:      models.NewCapabilitiesCategories(),
	}
	caps.Categories.AddCategoryTree(&models.Category{ID: 2000, Name: "Movies"})
	caps.Categories.AddCategoryTree(&models.Category{ID: 2040, Name: "Movies/HD"})
	return caps
}

func newFakeBase(id string) indexer.Base {
	return indexer.Base{
		IDValue:         id,
		NameValue:       "Fake " + id,
		SiteLinkValue:   "https://fake.example/",
		DefaultSiteLink: "https://fake.example/",
		LangValue:       "en-US",
		TypeValue:       "public",
		Config:          newFakeCfg(),
		Caps:            fakeCaps(),
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

var registerFakesOnce sync.Once

// registerFakeIndexers wires three fakes into the global registry exactly
// once: "fakegood" (immediate config, serves one release over httptest),
// "faketest" (RequiresTesting, serves one release), "fakefail" (apply error).
func registerFakeIndexers(siteURL string) {
	registerFakesOnce.Do(func() {
		Register("fakegood", func(deps Deps) (indexer.Indexer, error) {
			f := &fakeIndexer{siteURL: siteURL, releases: []*models.ReleaseInfo{{
				Title:    "Fake Release",
				Size:     int64Ptr(1234),
				Category: []int{2040},
			}}}
			f.Base = newFakeBase("fakegood")
			f.Client = deps.Client
			f.Saver = deps.Saver
			return f, nil
		})
		Register("faketest", func(deps Deps) (indexer.Indexer, error) {
			f := &fakeIndexer{
				applyStatus: indexer.ConfigRequiresTesting,
				siteURL:     siteURL,
				releases: []*models.ReleaseInfo{{
					Title:    "Fake Release 2",
					Size:     int64Ptr(4321),
					Category: []int{2040},
				}},
			}
			f.Base = newFakeBase("faketest")
			f.Client = deps.Client
			f.Saver = deps.Saver
			return f, nil
		})
		Register("fakefail", func(deps Deps) (indexer.Indexer, error) {
			f := &fakeIndexer{applyErr: errors.New("invalid credentials")}
			f.Base = newFakeBase("fakefail")
			f.Client = deps.Client
			f.Saver = deps.Saver
			return f, nil
		})
	})
}

func int64Ptr(v int64) *int64 { return &v }

func newTestManager(t *testing.T) (*IndexerManagerService, *store.Store) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	m := NewIndexerManager(st, client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return m, st
}

// TestIndexerManagerLifecycle covers SPEC-04 acceptance #5: Init restores
// saved configs, Configure/Test/Delete persist state per indexer.
func TestIndexerManagerLifecycle(t *testing.T) {
	// httptest fake site standing in for a tracker
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fake tracker page"))
	}))
	defer srv.Close()
	registerFakeIndexers(srv.URL)

	m, st := newTestManager(t)
	ctx := context.Background()

	// pre-seed a saved configuration for fakegood (as a previous run would)
	if err := st.SaveIndexerConfig(ctx, &store.IndexerConfigRow{
		IndexerID:  "fakegood",
		Enabled:    true,
		ConfigJSON: `[{"id":"apikey","value":"restored-key"}]`,
		State:      StateHealthy,
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Init: 3 fakes loaded, fakegood restored as configured.
	// GetAllIndexers mirrors upstream: non-meta only, ordered by name.
	all := m.GetAllIndexers()
	if len(all) != 3 {
		t.Fatalf("GetAllIndexers: %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Name() > all[i].Name() {
			t.Fatalf("GetAllIndexers not name-ordered: %s > %s", all[i-1].Name(), all[i].Name())
		}
	}
	good, err := m.GetIndexer("fakegood")
	if err != nil {
		t.Fatal(err)
	}
	if !good.IsConfigured() {
		t.Fatal("fakegood config not restored at Init")
	}
	agg, err := m.GetIndexer("all")
	if err != nil {
		t.Fatal(err)
	}
	if agg.ID() != "all" {
		t.Fatalf("aggregate id: %q", agg.ID())
	}
	if _, err := m.GetIndexer("nosuch"); err == nil {
		t.Fatal("expected error for unknown indexer")
	}

	// aggregate members are ALL indexers (upstream Indexers field);
	// ValidIndexers narrows to configured members
	if members := agg.(interface{ Members() []indexer.Indexer }).Members(); len(members) != 3 {
		t.Fatalf("aggregate members: %+v", members)
	}
	valid := agg.(interface{ ValidIndexers() []indexer.Indexer }).ValidIndexers()
	if len(valid) != 1 || valid[0].ID() != "fakegood" {
		t.Fatalf("aggregate valid indexers: %+v", valid)
	}

	// Configure fakegood: ConfigCompleted → no auto-test, config persisted
	if err := m.ConfigureIndexer(ctx, "fakegood", []indexer.ConfigItem{{ID: "apikey", Value: "new-key"}}); err != nil {
		t.Fatal(err)
	}
	row, err := st.GetIndexerConfig(ctx, "fakegood")
	if err != nil {
		t.Fatal(err)
	}
	if row.ConfigJSON == "" || !strings.Contains(row.ConfigJSON, "new-key") {
		t.Fatalf("persisted config: %q", row.ConfigJSON)
	}

	// Configure faketest: RequiresTesting → auto-test against the fake site;
	// mixed item types round-trip through ApplyItems + the setup form.
	testItems := []indexer.ConfigItem{
		{ID: "apikey", Value: "k"},
		{ID: "password", Value: "secret"},
		{ID: "freeleechonly", Value: "true"},
		{ID: "sortby", Value: "leechers"},
	}
	if err := m.ConfigureIndexer(ctx, "faketest", testItems); err != nil {
		t.Fatal(err)
	}
	tested, err := m.GetIndexer("faketest")
	if err != nil {
		t.Fatal(err)
	}
	if !tested.IsConfigured() {
		t.Fatal("faketest not configured after auto-test")
	}
	fc := tested.(*fakeIndexer)
	if fcCfg := fc.Config.(*fakeCfg); fcCfg.APIKey.Value != "k" ||
		fcCfg.Password.Value != "secret" ||
		!fcCfg.Freeleech.Value ||
		fcCfg.Sort.Value != "leechers" {
		t.Fatalf("mixed items not applied: %+v", fcCfg)
	}
	// the setup form echoes the applied values (GET Config shape)
	form, err := tested.ConfigurationForSetup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{}
	for _, item := range form {
		id, _ := item["id"].(string)
		values[id] = item["value"]
	}
	if values["apikey"] != "k" || values["freeleechonly"] != true || values["sortby"] != "leechers" {
		t.Fatalf("setup echo: %+v", values)
	}
	// password is masked in the display form (upstream PASSWORD_REPLACEMENT)
	if values["password"] != configdata.PasswordReplacement {
		t.Fatalf("password mask: %q", values["password"])
	}
	row, err = st.GetIndexerConfig(ctx, "faketest")
	if err != nil {
		t.Fatal(err)
	}
	if row.State != StateHealthy || row.TestedAt == nil {
		t.Fatalf("faketest state: %+v", row)
	}

	// Configure fakefail: apply error → reset, stays unconfigured
	if err := m.ConfigureIndexer(ctx, "fakefail", []indexer.ConfigItem{{ID: "apikey", Value: "k"}}); err == nil {
		t.Fatal("expected configure error")
	}
	failIdx, _ := m.GetIndexer("fakefail")
	if failIdx.IsConfigured() {
		t.Fatal("fakefail configured despite error")
	}

	// TestIndexer against the fake site → healthy state row
	if err := m.TestIndexer(ctx, "fakegood"); err != nil {
		t.Fatal(err)
	}
	row, err = st.GetIndexerConfig(ctx, "fakegood")
	if err != nil {
		t.Fatal(err)
	}
	if row.State != StateHealthy {
		t.Fatalf("fakegood state: %q", row.State)
	}

	// failing test on a never-configured indexer: no state row is created
	// (the row's lifecycle follows the config), only the last error is set
	if err := m.TestIndexer(ctx, "fakefail"); err == nil {
		t.Fatal("expected test error for fakefail (no releases)")
	}
	if _, err := st.GetIndexerConfig(ctx, "fakefail"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no state row for fakefail, got %v", err)
	}
	if failIdx.LastError() == "" {
		t.Fatal("expected last error on fakefail")
	}

	// ValidIndexers: only configured indexers that support the query
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	valid = m.ValidIndexers(q)
	if len(valid) != 2 { // fakegood + faketest (fakefail never configured)
		t.Fatalf("ValidIndexers: %d", len(valid))
	}

	// Delete fakegood: row gone, indexer unconfigured
	if err := m.DeleteIndexer(ctx, "fakegood"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetIndexerConfig(ctx, "fakegood"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected row deleted, got %v", err)
	}
	if good.IsConfigured() {
		t.Fatal("fakegood still configured after delete")
	}
	// members still include every registered indexer; only the configured
	// subset narrows (upstream Indexers/ValidIndexers semantics)
	if members := agg.(interface{ Members() []indexer.Indexer }).Members(); len(members) != 3 {
		t.Fatalf("aggregate members after delete: %+v", members)
	}
	if valid := agg.(interface{ ValidIndexers() []indexer.Indexer }).ValidIndexers(); len(valid) != 1 || valid[0].ID() != "faketest" {
		t.Fatalf("aggregate valid indexers after delete: %+v", valid)
	}
}
