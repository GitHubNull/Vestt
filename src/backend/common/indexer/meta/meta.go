// Package meta holds the meta indexers: the "all" aggregate and the filter
// indexers (type/tag filters), porting upstream BaseMetaIndexer /
// AggregateIndexer / FilterIndexer (Jackett.Common/Indexers/Meta/).
//
// Fan-out semantics (SPEC-07 §3.5):
//   - members = every indexer (the manager injects the live set);
//   - ValidIndexers = members where IsConfigured && filter;
//   - CanHandleQuery: t=indexers always; otherwise per-member capabilities;
//   - ResultsForQuery fans out to members with a 40s budget (upstream
//     tasks.Until), applies the IMDb fallback + title result filters when
//     an OMDB resolver is configured, orders by Gain and takes query.Limit.
package meta

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"log/slog"

	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
)

// metaTimeout is the upstream metaindexer fan-out budget
// (BaseMetaIndexer.PerformQuery, tasks.Until 40s).
const metaTimeout = 40 * time.Second

// Members provides the live indexer set (upstream Indexers field).
type Members func() []indexer.Indexer

// FilterFunc decides whether a member belongs to this meta indexer
// (upstream Func<IIndexer, bool> filterFunc).
type FilterFunc func(indexer.Indexer) bool

// ImdbResolver resolves an IMDb id to a movie title (upstream IImdbResolver
// / OmdbResolver). Nil selects the NoFallback/NoFilter strategies.
type ImdbResolver interface {
	MovieTitle(ctx context.Context, imdbID string) (string, error)
}

// baseMeta carries the shared BaseMetaIndexer behaviour.
type baseMeta struct {
	indexer.Base
	idValue string
	members Members
	filter  FilterFunc
	resolver ImdbResolver
}

func newBaseMeta(id, name, description string, members Members, filter FilterFunc, resolver ImdbResolver) baseMeta {
	bm := baseMeta{
		idValue:  id,
		members:  members,
		filter:   filter,
		resolver: resolver,
	}
	bm.Base = indexer.Base{
		IDValue:          id,
		NameValue:        name,
		DescriptionValue: description,
		SiteLinkValue:    "http://127.0.0.1/",
		TypeValue:        "meta",
		LangValue:        "en-US",
		Caps:             models.NewCapabilities(),
		Config:           &struct{ configdata.ConfigData }{ConfigData: *configdata.NewConfigData()},
	}
	return bm
}

// ID overrides the base id.
func (m *baseMeta) ID() string { return m.idValue }

// IsConfigured mirrors upstream BaseMetaIndexer: configured once the member
// set has been injected.
func (m *baseMeta) IsConfigured() bool { return m.members != nil }

// Tags: meta indexers report no tags (upstream override).
func (m *baseMeta) Tags() []string { return nil }

// CanHandleQuery mirrors upstream: t=indexers always passes; otherwise the
// aggregate capabilities decide.
func (m *baseMeta) CanHandleQuery(q *models.TorznabQuery) bool {
	if q == nil {
		return false
	}
	if q.QueryType == "indexers" {
		return true
	}
	ok, _ := m.Caps.CanHandleQuery(q)
	return ok
}

// Members returns the current member set (t=indexers enumeration).
func (m *baseMeta) Members() []indexer.Indexer {
	if m.members == nil {
		return nil
	}
	return m.members()
}

// ValidIndexers mirrors upstream: members that are configured and pass the
// filter.
func (m *baseMeta) ValidIndexers() []indexer.Indexer {
	var out []indexer.Indexer
	for _, idx := range m.Members() {
		if idx.IsConfigured() && m.filter(idx) {
			out = append(out, idx)
		}
	}
	return out
}

// Capabilities mirrors upstream BaseMetaIndexer.TorznabCaps: the concat of
// member caps with limits forced to 1000 (AggregateIndexer/FilterIndexer
// override).
func (m *baseMeta) Capabilities() *models.Capabilities {
	caps := models.NewCapabilities()
	first := true
	for _, idx := range m.ValidIndexers() {
		c := idx.Capabilities()
		if c == nil {
			continue
		}
		if first {
			caps = models.Concat(models.NewCapabilities(), c)
			first = false
		} else {
			caps = models.Concat(caps, c)
		}
	}
	lim := 1000
	caps.LimitsMax = &lim
	caps.LimitsDefault = &lim
	return caps
}

// ApplyConfiguration: meta indexers accept but ignore configuration
// (upstream returns Completed).
func (m *baseMeta) ApplyConfiguration(_ context.Context, _ []indexer.ConfigItem) (indexer.ConfigStatus, error) {
	return indexer.ConfigCompleted, nil
}

// ResultsForQuery mirrors upstream BaseMetaIndexer.ResultsForQuery.
func (m *baseMeta) ResultsForQuery(ctx context.Context, q *models.TorznabQuery, _ bool) (*indexer.IndexerResult, error) {
	queryCopy := q.Clone()
	empty := &indexer.IndexerResult{Indexer: m, Releases: []*models.ReleaseInfo{}}

	if queryCopy.Offset > 0 || !m.CanHandleQuery(queryCopy) || !m.canHandleCategories(queryCopy) {
		return empty, nil
	}

	start := time.Now()
	results, err := m.performQuery(ctx, queryCopy)
	if err != nil {
		return nil, &indexer.IndexerError{IndexerID: m.idValue, Kind: indexer.KindInternal, Message: err.Error()}
	}
	return &indexer.IndexerResult{Indexer: m, Releases: results, ElapsedTime: time.Since(start).Milliseconds()}, nil
}

// logError writes a member fan-out error with the package fallback logger
// (the manager injects Base.Logger during assembly).
func (m *baseMeta) logError(msg string) {
	if m.Logger != nil {
		m.Logger.Error(msg)
		return
	}
	slog.Error(msg)
}

// canHandleCategories mirrors upstream CanHandleCategories(query, true): the
// meta gate only rejects when no requested category is supported at all.
func (m *baseMeta) canHandleCategories(q *models.TorznabQuery) bool {
	if !q.HasSpecifiedCategories() {
		return true
	}
	supported := m.Capabilities().Categories.SupportedCategories(q.Categories)
	return len(supported) > 0
}

// fanResult pairs a member's result with its identity (nil result on error).
type fanResult struct {
	idx    indexer.Indexer
	result *indexer.IndexerResult
	err    error
}

// performQuery ports BaseMetaIndexer.PerformQuery: member fan-out with
// fallback queries, result filters, Gain ordering and query.Limit.
func (m *baseMeta) performQuery(ctx context.Context, q *models.TorznabQuery) ([]*models.ReleaseInfo, error) {
	indexers := m.ValidIndexers()

	type task struct {
		idx indexer.Indexer
		q   *models.TorznabQuery
	}
	var tasks []task
	for _, idx := range indexers {
		if idx.CanHandleQuery(q) {
			tasks = append(tasks, task{idx, q})
		}
	}

	// IMDb fallback: resolve the title and re-query members that could not
	// handle the id-based query (upstream ImdbFallbackStrategy; NoFallback
	// when no resolver is configured).
	if m.resolver != nil && q.IsImdbQuery() {
		if title, err := m.resolver.MovieTitle(ctx, q.ImdbID); err == nil && title != "" {
			fallback := q.CreateFallback(title)
			for _, idx := range indexers {
				if !idx.CanHandleQuery(q) && idx.CanHandleQuery(fallback) {
					tasks = append(tasks, task{idx, fallback})
				}
			}
		}
	}

	fanCtx, cancel := context.WithTimeout(ctx, metaTimeout)
	defer cancel()

	results := make([]fanResult, len(tasks))
	var wg sync.WaitGroup
	for i, t := range tasks {
		wg.Add(1)
		go func(i int, t task) {
			defer wg.Done()
			res, err := t.idx.ResultsForQuery(fanCtx, t.q, true)
			results[i] = fanResult{idx: t.idx, result: res, err: err}
		}(i, t)
	}
	wg.Wait()

	var unordered []*models.ReleaseInfo
	for _, r := range results {
		if r.err != nil {
			m.logError(fmt.Sprintf("Error during request in metaindexer %s: %v", m.idValue, r.err))
			continue
		}
		if r.result == nil {
			continue
		}
		unordered = append(unordered, r.result.Releases...)
	}

	// result filters (upstream ImdbTitleResultFilter when a resolver is set;
	// NoFilter otherwise).
	filtered := unordered
	if m.resolver != nil && q.IsImdbQuery() {
		filtered = m.imdbTitleFilter(ctx, q, unordered)
	}

	// upstream .Distinct() uses reference equality (ReleaseInfo does not
	// override Equals), which is a no-op for distinct objects — see the
	// SPEC-07 澄清记录.

	sort.SliceStable(filtered, func(i, j int) bool {
		gi, gj := filtered[i].Gain(), filtered[j].Gain()
		if gi == nil {
			return gj != nil // nil sorts last, mirroring OrderByDescending semantics loosely
		}
		if gj == nil {
			return true
		}
		return *gi > *gj
	})

	if q.Limit > 0 && len(filtered) > q.Limit {
		filtered = filtered[:q.Limit]
	}
	return filtered, nil
}

// imdbTitleFilter ports ImdbTitleResultFilter.FilterResults.
func (m *baseMeta) imdbTitleFilter(ctx context.Context, q *models.TorznabQuery, results []*models.ReleaseInfo) []*models.ReleaseInfo {
	var imdbID int64
	normalized := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, q.ImdbID)
	if normalized != "" {
		if _, err := fmt.Sscan(normalized, &imdbID); err != nil {
			imdbID = 0
		}
	}

	var perfect, remaining []*models.ReleaseInfo
	for _, r := range results {
		if r.Imdb != nil {
			if *r.Imdb == imdbID {
				perfect = append(perfect, r)
			}
			// wrong-id results are dropped
			continue
		}
		remaining = append(remaining, r)
	}
	if q.ImdbID == "" {
		return perfect
	}
	title, err := m.resolver.MovieTitle(ctx, q.ImdbID)
	if err != nil || title == "" {
		return perfect
	}

	variants := titleVariants(title)
	var out []*models.ReleaseInfo
	out = append(out, perfect...)
	for _, r := range remaining {
		lt := strings.ToLower(r.Title)
		for _, v := range variants {
			if strings.Contains(lt, strings.ToLower(v)) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// titleVariants ports GenerateTitleVariants + RemoveSpecialChars: the title
// without ':', plus variants with '.'/'_' replacing spaces.
func titleVariants(title string) []string {
	clean := strings.ReplaceAll(title, ":", "")
	variants := []string{clean}
	variants = append(variants, strings.ReplaceAll(clean, " ", "."))
	variants = append(variants, strings.ReplaceAll(clean, " ", "_"))
	return variants
}

// Test mirrors upstream: the manager's TestIndexer runs an empty query
// through ResultsForQuery.
func (m *baseMeta) Test(ctx context.Context) error {
	_, err := m.ResultsForQuery(ctx, indexer.TestQuery(), false)
	return err
}

// Download is not meaningful for meta indexers.
func (m *baseMeta) Download(_ context.Context, _ *url.URL) ([]byte, error) {
	return nil, fmt.Errorf("meta indexer %s cannot download", m.idValue)
}

// --- concrete meta indexers ----------------------------------------------

// Aggregate is the upstream AggregateIndexer ("all").
type Aggregate struct {
	baseMeta
}

// NewAggregate builds the "all" meta indexer over the live member set.
func NewAggregate(members Members, resolver ImdbResolver) *Aggregate {
	a := &Aggregate{}
	a.baseMeta = newBaseMeta("all", "AggregateSearch", "This feed includes all configured trackers", members, func(indexer.Indexer) bool { return true }, resolver)
	return a
}

// Filter is the upstream FilterIndexer (type/tag filters, id = the filter
// expression).
type Filter struct {
	baseMeta
}

// NewFilter builds a filter meta indexer.
func NewFilter(id string, members Members, filter FilterFunc, resolver ImdbResolver) *Filter {
	f := &Filter{}
	f.baseMeta = newBaseMeta(id, id, "This feed includes all configured trackers filter by "+id, members, filter, resolver)
	return f
}

// IsConfigured mirrors upstream FilterIndexer: configured only when at least
// one member matches.
func (f *Filter) IsConfigured() bool {
	return f.baseMeta.IsConfigured() && len(f.ValidIndexers()) > 0
}
