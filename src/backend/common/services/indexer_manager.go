package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/meta"
	"vestt/common/models"
	"vestt/common/store"
)

// Indexer state values (data-model.md §2.4).
const (
	StateUnconfigured = "unconfigured"
	StateHealthy      = "healthy"
	StateFailing      = "failing"
)

// aggregateID is the upstream "all" indexer.
const aggregateID = "all"

// IndexerManagerService mirrors upstream IndexerManagerService
// (Jackett.Common/Services/IndexerManagerService.cs).
type IndexerManagerService struct {
	store  *store.Store
	client *httpclient.Client
	logger *slog.Logger

	mu          sync.RWMutex
	indexers    map[string]indexer.Indexer
	aggregate   *meta.Aggregate
	filters     map[string]*meta.Filter
	cache       *CacheService
	resolver    meta.ImdbResolver
	initialized bool
}

// NewIndexerManager builds the manager (Init must be called before use).
func NewIndexerManager(st *store.Store, client *httpclient.Client, logger *slog.Logger) *IndexerManagerService {
	if logger == nil {
		logger = slog.Default()
	}
	return &IndexerManagerService{
		store:    st,
		client:   client,
		logger:   logger,
		indexers: map[string]indexer.Indexer{},
		filters:  map[string]*meta.Filter{},
	}
}

// SetCache injects the result cache (SPEC-07): used for the indexer Deps
// and cleaned on configure/delete.
func (m *IndexerManagerService) SetCache(c *CacheService) { m.cache = c }

// SetMetaResolver injects the IMDb title resolver used by the meta indexers
// (upstream OMDB resolver; nil selects the NoFallback/NoFilter strategies).
func (m *IndexerManagerService) SetMetaResolver(r meta.ImdbResolver) { m.resolver = r }

// Init instantiates all registered indexers, restores saved configurations
// and cookies, and creates the aggregate meta indexer.
func (m *IndexerManagerService) Init(ctx context.Context) error {
	m.mu.Lock()
	if m.initialized {
		m.mu.Unlock()
		return nil
	}

	deps := Deps{Client: m.client}
	if m.cache != nil {
		deps.Cache = m.cache
	}
	// the saver persists config + cookie changes to the store
	deps.Saver = func(ctx context.Context, indexerID string, configJSON []byte) error {
		row, err := m.store.GetIndexerConfig(ctx, indexerID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if row == nil {
			row = &store.IndexerConfigRow{IndexerID: indexerID, Enabled: true, State: StateUnconfigured}
		}
		row.ConfigJSON = string(configJSON)
		return m.store.SaveIndexerConfig(ctx, row)
	}

	for _, entry := range RegisteredFactories() {
		idx, err := entry.Factory(deps)
		if err != nil {
			m.logger.Error(fmt.Sprintf("Failed to instantiate indexer %s: %v", entry.ID, err))
			continue
		}
		m.indexers[idx.ID()] = idx
	}
	if dyn := CardigannFactory(); dyn != nil {
		defs, err := dyn(deps)
		if err != nil {
			m.logger.Error(fmt.Sprintf("Failed to instantiate cardigann indexers: %v", err))
		} else {
			for _, idx := range defs {
				m.indexers[idx.ID()] = idx
			}
		}
	}

	rows, err := m.store.ListIndexerConfigs(ctx)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	for _, row := range rows {
		idx, ok := m.indexers[row.IndexerID]
		if !ok {
			m.logger.Warn(fmt.Sprintf("Saved configuration for unknown indexer %s, skipping", row.IndexerID))
			continue
		}
		if err := idx.LoadFromSavedConfiguration(ctx, []byte(row.ConfigJSON)); err != nil {
			m.logger.Error(fmt.Sprintf("Failed to load configuration for %s: %v", row.IndexerID, err))
			continue
		}
		var cookies []httpclient.PersistedCookie
		if row.CookiesJSON != "" {
			if err := json.Unmarshal([]byte(row.CookiesJSON), &cookies); err != nil {
				m.logger.Error(fmt.Sprintf("Failed to load cookies for %s: %v", row.IndexerID, err))
			}
		}
		m.client.LoadCookies(row.IndexerID, cookies)
	}

	m.initialized = true
	total := len(m.indexers)
	resolver := m.resolver
	m.mu.Unlock()

	// the aggregate reads the live member set (upstream Indexers field);
	// build it after releasing the init lock (RLock self-deadlock otherwise)
	agg := meta.NewAggregate(m.allIndexers, resolver)
	m.mu.Lock()
	m.aggregate = agg
	m.mu.Unlock()

	// predefined filter indexers: type filters + one per distinct tag
	// (upstream InitMetaIndexers)
	m.registerFilters()

	m.logger.Info(fmt.Sprintf("Loaded %d indexers (%d configured)", total, len(m.configuredMembers())))
	return nil
}

// registerFilters builds the predefined filter indexers (public/private/
// semi-public + one per distinct tag).
func (m *IndexerManagerService) registerFilters() {
	register := func(id string, filter meta.FilterFunc) {
		m.logger.Info(fmt.Sprintf("Adding filter indexer ('%s' indexer) ...", id))
		f := meta.NewFilter(id, m.allIndexers, filter, m.resolver)
		f.Logger = m.logger
		m.mu.Lock()
		m.filters[id] = f
		m.mu.Unlock()
	}
	for _, id := range []string{"public", "private", "semi-private"} {
		if filter, ok := meta.ParseFilter(id); ok {
			register(id, filter)
		}
	}
	tags := map[string]bool{}
	for _, idx := range m.GetAllIndexers() {
		for _, t := range idx.Tags() {
			tags[t] = true
		}
	}
	sorted := make([]string, 0, len(tags))
	for t := range tags {
		sorted = append(sorted, t)
	}
	sort.Strings(sorted)
	for _, t := range sorted {
		if filter, ok := meta.ParseFilter("tag:" + t); ok {
			register("tag:"+t, filter)
		}
	}
}

// allIndexers is the live member provider for the meta indexers (upstream
// Indexers field: every registered indexer, configured or not).
func (m *IndexerManagerService) allIndexers() []indexer.Indexer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]indexer.Indexer, 0, len(m.indexers))
	for _, idx := range m.indexers {
		out = append(out, idx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// configuredMembers is the live configured-indexer set.
func (m *IndexerManagerService) configuredMembers() []indexer.Indexer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]indexer.Indexer, 0, len(m.indexers))
	for _, idx := range m.indexers {
		if idx.IsConfigured() {
			out = append(out, idx)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// GetAllIndexers mirrors upstream GetAllIndexers: every non-meta indexer
// ordered by name.
func (m *IndexerManagerService) GetAllIndexers() []indexer.Indexer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]indexer.Indexer, 0, len(m.indexers))
	for _, idx := range m.indexers {
		out = append(out, idx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// GetIndexer mirrors upstream GetIndexer/GetWebIndexer: concrete indexers
// first, then "all", then the predefined/dynamic filter indexers; unknown
// names error ("Unknown indexer: <name>").
func (m *IndexerManagerService) GetIndexer(name string) (indexer.Indexer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if idx, ok := m.indexers[name]; ok {
		return idx, nil
	}
	if name == aggregateID && m.aggregate != nil {
		return m.aggregate, nil
	}
	if f, ok := m.filters[name]; ok {
		return f, nil
	}
	if filter, ok := meta.ParseFilter(name); ok {
		f := meta.NewFilter(name, m.allIndexers, filter, m.resolver)
		f.Logger = m.logger
		m.filters[name] = f
		return f, nil
	}
	return nil, fmt.Errorf("Unknown indexer: %s", name)
}

// GetConfiguredIndexers returns configured non-meta indexers.
func (m *IndexerManagerService) GetConfiguredIndexers() []indexer.Indexer {
	return m.configuredMembers()
}

// TestIndexer mirrors upstream TestIndexer: empty search, must return at
// least one release; updates state/last_error in the store.
func (m *IndexerManagerService) TestIndexer(ctx context.Context, name string) error {
	idx, err := m.GetIndexer(name)
	if err != nil {
		return err
	}

	start := time.Now()
	result, err := idx.ResultsForQuery(ctx, indexer.TestQuery(), false)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		m.setState(ctx, idx, StateFailing, err.Error())
		return err
	}

	m.logger.Info(fmt.Sprintf("Test search in %s => Found %d releases [%dms]", idx.Name(), len(result.Releases), elapsed))
	if len(result.Releases) == 0 {
		msg := fmt.Sprintf("Test search in %s => Found no results while trying to browse this tracker. This may be an issue with the indexer, or other indexer settings such as search freeleech only etc.", idx.Name())
		m.setState(ctx, idx, StateFailing, msg)
		return errors.New(msg)
	}
	m.setState(ctx, idx, StateHealthy, "")
	return nil
}

// setState updates the store row's state/last_error/tested_at. The state
// row's lifecycle follows the config row: an indexer without a stored
// config (never configured / deleted) gets no row — only the in-memory
// last error is updated.
func (m *IndexerManagerService) setState(ctx context.Context, idx indexer.Indexer, state, lastError string) {
	row, err := m.store.GetIndexerConfig(ctx, idx.ID())
	if err != nil {
		idx.SetLastError(lastError)
		return
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	row.State = state
	row.TestedAt = &now
	if lastError == "" {
		row.LastError = nil
	} else {
		row.LastError = &lastError
	}
	if err := m.store.SaveIndexerConfig(ctx, row); err != nil {
		m.logger.Error(fmt.Sprintf("Failed to persist state for %s: %v", idx.ID(), err))
	}
	idx.SetLastError(lastError)
}

// ConfigureIndexer mirrors upstream UpdateConfig: clean the result cache,
// apply items, auto-test when the indexer requires it, persist; on failure
// reset base config.
func (m *IndexerManagerService) ConfigureIndexer(ctx context.Context, name string, items []indexer.ConfigItem) error {
	idx, err := m.GetIndexer(name)
	if err != nil {
		return err
	}
	if m.cache != nil {
		if err := m.cache.CleanIndexer(ctx, idx.ID()); err != nil {
			m.logger.Error(fmt.Sprintf("Failed to clean cache for %s: %v", idx.ID(), err))
		}
	}

	status, err := idx.ApplyConfiguration(ctx, items)
	if err != nil {
		resetBaseConfig(idx)
		return err
	}

	// persist immediately: upstream saves the config during
	// ApplyConfiguration (ConfigureIfOK), before the optional test runs —
	// the test's state write must see a stored config row
	if saver, ok := idx.(interface{ SaveConfig(context.Context) }); ok {
		saver.SaveConfig(ctx)
	}

	if status == indexer.ConfigRequiresTesting {
		if err := m.TestIndexer(ctx, name); err != nil {
			resetBaseConfig(idx)
			return err
		}
	}
	m.logger.Info(fmt.Sprintf("Configured indexer: %s", name))
	return nil
}

// resetBaseConfig mirrors the upstream controller catch block.
func resetBaseConfig(idx indexer.Indexer) {
	if rb, ok := idx.(interface{ ResetBaseConfig() }); ok {
		rb.ResetBaseConfig()
	}
}

// DeleteIndexer mirrors upstream: delete config row, unconfigure, clear
// cookies and cache.
func (m *IndexerManagerService) DeleteIndexer(ctx context.Context, name string) error {
	idx, err := m.GetIndexer(name)
	if err != nil {
		return err
	}
	if err := m.store.DeleteIndexerConfig(ctx, name); err != nil {
		return err
	}
	if m.cache != nil {
		if err := m.cache.CleanIndexer(ctx, idx.ID()); err != nil {
			m.logger.Error(fmt.Sprintf("Failed to clean cache for %s: %v", idx.ID(), err))
		}
	}
	return idx.Unconfigure(ctx)
}

// ValidIndexers returns the configured indexers able to serve the query
// (meta-indexer fan-out input, upstream ValidIndexers).
func (m *IndexerManagerService) ValidIndexers(q *models.TorznabQuery) []indexer.Indexer {
	var out []indexer.Indexer
	for _, idx := range m.configuredMembers() {
		if ok, _ := idx.Capabilities().SupportsQuery(q, true); ok {
			out = append(out, idx)
		}
	}
	return out
}
