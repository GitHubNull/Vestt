package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/store"
)

// CacheService mirrors upstream CacheService (Jackett.Common/Services/
// CacheService.cs): per-indexer query-keyed result pages with TTL pruning,
// a per-indexer result budget, and the Cache view.
//
// Cache key (upstream GetQueryHash): SHA-256 of the JSON-serialised
// TorznabQuery (indexer id is the outer map key), with the
// "SearchTerm":null → "SearchTerm":"" normalisation to improve hits.
//
// Double layer per SPEC-07 §3.1: memory is authoritative; SQLite is a
// write-through layer flushed asynchronously (50ms batch window) and read
// when an indexer has no in-memory entry (e.g. after a restart).
type CacheService struct {
	store  *store.Store
	config *ConfigService
	logger *slog.Logger

	mu     sync.Mutex
	byID   map[string]*trackerCache // indexer id -> cache
	dirty  map[string]bool          // indexer ids with unflushed changes
	flush  chan struct{}
	stop   chan struct{}
	done   chan struct{}
	closed bool
}

// trackerCache mirrors upstream TrackerCache (queries keyed by hash).
type trackerCache struct {
	mu          sync.Mutex
	trackerID   string
	trackerName string
	trackerType string
	queries     map[string]*trackerCacheQuery
	createdSort []string // cached hash list, newest first (lazy)
}

// trackerCacheQuery mirrors upstream TrackerCacheQuery.
type trackerCacheQuery struct {
	created time.Time
	results []*models.ReleaseInfo
}

// compile-time seam check (common/indexer/base.go).
var _ indexer.ResultCache = (*CacheService)(nil)

// NewCacheService builds the service and starts the background flush loop.
func NewCacheService(st *store.Store, cfg *ConfigService, logger *slog.Logger) *CacheService {
	if logger == nil {
		logger = slog.Default()
	}
	c := &CacheService{
		store:  st,
		config: cfg,
		logger: logger,
		byID:   map[string]*trackerCache{},
		dirty:  map[string]bool{},
		flush:  make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go c.flushLoop()
	return c
}

// Close stops the flush loop after a final flush.
func (c *CacheService) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		<-c.done
		return
	}
	c.closed = true
	c.mu.Unlock()
	close(c.stop)
	<-c.done
}

// flushLoop batches SQLite writes: a dirty signal waits 50ms to collect
// further stores, then flushes every dirty indexer's full query map
// (replace-per-key, so evictions disappear from the DB too).
func (c *CacheService) flushLoop() {
	defer close(c.done)
	for {
		select {
		case <-c.stop:
			c.flushPending(context.Background())
			return
		case <-c.flush:
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-c.stop:
				timer.Stop()
				c.flushPending(context.Background())
				return
			case <-timer.C:
			}
			c.flushPending(context.Background())
		}
	}
}

func (c *CacheService) flushPending(ctx context.Context) {
	c.mu.Lock()
	dirty := make([]string, 0, len(c.dirty))
	for id := range c.dirty {
		dirty = append(dirty, id)
	}
	c.dirty = map[string]bool{}
	caches := make(map[string]*trackerCache, len(dirty))
	for _, id := range dirty {
		caches[id] = c.byID[id]
	}
	c.mu.Unlock()

	for _, id := range dirty {
		tc := caches[id]
		if tc == nil {
			// indexer evicted entirely: drop its DB rows
			if err := c.store.CacheDeleteByIndexer(ctx, id); err != nil {
				c.logger.Error(fmt.Sprintf("Cache flush: delete %s: %v", id, err))
			}
			continue
		}
		// rewrite the indexer's whole page set
		if err := c.store.CacheDeleteByIndexer(ctx, id); err != nil {
			c.logger.Error(fmt.Sprintf("Cache flush: clear %s: %v", id, err))
			continue
		}
		tc.mu.Lock()
		for hash, q := range tc.queries {
			raw, err := json.Marshal(q.results)
			if err != nil {
				c.logger.Error(fmt.Sprintf("Cache flush: serialize %s/%s: %v", id, hash, err))
				continue
			}
			if err := c.store.CachePut(ctx, id, hash, string(raw)); err != nil {
				c.logger.Error(fmt.Sprintf("Cache flush: store %s/%s: %v", id, hash, err))
			}
		}
		tc.mu.Unlock()
	}
}

// serializedQuery mirrors CacheService.GetSerializedQuery: Newtonsoft-style
// JSON of the query with the SearchTerm null→"" normalisation. The Go
// struct field order matches the upstream property declaration order.
func serializedQuery(q *models.TorznabQuery) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(q) // struct marshalling cannot fail
	json := string(bytes.TrimRight(buf.Bytes(), "\n"))
	return strings.ReplaceAll(json, `"SearchTerm":null,`, `"SearchTerm":"",`)
}

// queryHash mirrors CacheService.GetQueryHash: SHA-256 of the serialised
// query.
func queryHash(q *models.TorznabQuery) string {
	sum := sha256.Sum256([]byte(serializedQuery(q)))
	return hex.EncodeToString(sum[:])
}

func (c *CacheService) enabled() bool { return c.config.Config().CacheEnabled }

// ttl returns the configured cache TTL in SECONDS (upstream CacheTtl).
func (c *CacheService) ttl() time.Duration {
	return time.Duration(c.config.Config().CacheTTL) * time.Second
}

// maxResultsPerIndexer is the configured per-indexer release budget.
func (c *CacheService) maxResultsPerIndexer() int {
	return c.config.Config().CacheMaxResultsPerIndexer
}

// trackerCacheFor returns (building if needed) the in-memory cache for one
// indexer, first back-filling it from SQLite when unknown.
func (c *CacheService) trackerCacheFor(indexerID string) *trackerCache {
	tc, ok := c.byID[indexerID]
	if ok {
		return tc
	}
	tc = &trackerCache{trackerID: indexerID, queries: map[string]*trackerCacheQuery{}}
	rows, err := c.store.CacheList(context.Background())
	if err != nil {
		c.logger.Error(fmt.Sprintf("Cache backfill: list: %v", err))
		c.byID[indexerID] = tc
		return tc
	}
	for _, row := range rows {
		if row.IndexerID != indexerID {
			continue
		}
		var releases []*models.ReleaseInfo
		if err := json.Unmarshal([]byte(row.ReleaseJSON), &releases); err != nil {
			c.logger.Error(fmt.Sprintf("Cache backfill: deserialize %s: %v", row.QueryKey, err))
			continue
		}
		tc.queries[row.QueryKey] = &trackerCacheQuery{created: row.CreatedAt, results: releases}
	}
	c.byID[indexerID] = tc
	return tc
}

// Search implements the indexer.ResultCache seam: returns the stored page
// for (indexer, query) or nil on a miss. Upstream hands out the stored list
// as-is (releases were cloned at CacheResults time).
func (c *CacheService) Search(indexerID string, q *models.TorznabQuery) []*models.ReleaseInfo {
	c.mu.Lock()
	if !c.enabled() {
		c.mu.Unlock()
		return nil
	}
	c.pruneByTTLLocked()
	tc := c.trackerCacheFor(indexerID)
	c.mu.Unlock()

	hash := queryHash(q)
	tc.mu.Lock()
	query, hit := tc.queries[hash]
	if hit {
		c.logger.Debug(fmt.Sprintf("CACHE Search / Indexer: %s / CacheHit: true / Query: %s", indexerID, serializedQuery(q)))
		c.logger.Debug(fmt.Sprintf("CACHE Search Hit / Indexer: %s / Found: %d releases", indexerID, len(query.results)))
	} else {
		c.logger.Debug(fmt.Sprintf("CACHE Search / Indexer: %s / CacheHit: false / Query: %s", indexerID, serializedQuery(q)))
	}
	tc.mu.Unlock()
	if !hit {
		return nil
	}
	return query.results
}

// CacheResults implements the indexer.ResultCache seam: stores a page,
// replacing any previous entry for the key and pruning the per-indexer
// budget. Test queries are never cached (upstream behaviour).
func (c *CacheService) CacheResults(indexerID, indexerName, indexerType string, q *models.TorznabQuery, releases []*models.ReleaseInfo) {
	if q.IsTest {
		return
	}
	c.mu.Lock()
	if !c.enabled() {
		c.mu.Unlock()
		return
	}
	tc := c.trackerCacheFor(indexerID)
	c.mu.Unlock()

	cloned := make([]*models.ReleaseInfo, len(releases))
	for i, r := range releases {
		cloned[i] = r.Clone()
	}
	hash := queryHash(q)

	tc.mu.Lock()
	tc.trackerName = indexerName
	tc.trackerType = indexerType
	tc.queries[hash] = &trackerCacheQuery{created: time.Now(), results: cloned}
	tc.createdSort = nil
	tc.mu.Unlock()
	c.logger.Debug(fmt.Sprintf("CACHE CacheResults / Indexer: %s / Added: %d releases", indexerID, len(releases)))

	c.pruneByMaxResults(tc)

	c.mu.Lock()
	c.dirty[indexerID] = true
	c.mu.Unlock()
	select {
	case c.flush <- struct{}{}:
	default:
	}
}

// pruneByTTLLocked drops expired queries (upstream PruneCacheByTtl, run on
// every Search). Caller holds c.mu.
func (c *CacheService) pruneByTTLLocked() {
	expiration := time.Now().Add(-c.ttl())
	pruned := 0
	for _, tc := range c.byID {
		tc.mu.Lock()
		for hash, q := range tc.queries {
			if q.created.Before(expiration) {
				delete(tc.queries, hash)
				tc.createdSort = nil
				pruned++
			}
		}
		tc.mu.Unlock()
	}
	if pruned > 0 {
		c.logger.Debug(fmt.Sprintf("CACHE PruneCacheByTtl / Pruned queries: %d", pruned))
		c.printStatusLocked()
	}
}

// pruneByMaxResults removes the oldest whole queries until the indexer is
// within its release budget (upstream PruneCacheByMaxResultsPerIndexer).
func (c *CacheService) pruneByMaxResults(tc *trackerCache) {
	max := int64(c.maxResultsPerIndexer())
	tc.mu.Lock()
	var total int64
	for _, q := range tc.queries {
		total += int64(len(q.results))
	}
	if total <= max {
		tc.mu.Unlock()
		return
	}
	pruned := 0
	for _, hash := range tc.sortedHashesLocked() {
		if total <= max {
			break
		}
		q := tc.queries[hash]
		delete(tc.queries, hash)
		total -= int64(len(q.results))
		pruned++
	}
	tc.createdSort = nil
	tc.mu.Unlock()
	c.logger.Debug(fmt.Sprintf("CACHE PruneCacheByMaxResultsPerIndexer / Indexer: %s / Pruned queries: %d", tc.trackerID, pruned))
	c.printStatus()
}

// sortedHashesLocked returns query hashes newest-first.
func (tc *trackerCache) sortedHashesLocked() []string {
	if tc.createdSort != nil {
		return tc.createdSort
	}
	hashes := make([]string, 0, len(tc.queries))
	for h := range tc.queries {
		hashes = append(hashes, h)
	}
	sort.Slice(hashes, func(i, j int) bool {
		return tc.queries[hashes[i]].created.After(tc.queries[hashes[j]].created)
	})
	tc.createdSort = hashes
	return hashes
}

func (c *CacheService) printStatus() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.printStatusLocked()
}

func (c *CacheService) printStatusLocked() {
	total := 0
	for _, tc := range c.byID {
		tc.mu.Lock()
		for _, q := range tc.queries {
			total += len(q.results)
		}
		tc.mu.Unlock()
	}
	c.logger.Debug(fmt.Sprintf("CACHE Status / Total cached results: %d", total))
}

// CleanIndexer drops every cached page of one indexer (memory + SQLite).
func (c *CacheService) CleanIndexer(ctx context.Context, indexerID string) error {
	c.mu.Lock()
	delete(c.byID, indexerID)
	delete(c.dirty, indexerID)
	c.mu.Unlock()
	return c.store.CacheDeleteByIndexer(ctx, indexerID)
}

// CleanAll drops the whole cache (memory + SQLite).
func (c *CacheService) CleanAll(ctx context.Context) error {
	c.mu.Lock()
	c.byID = map[string]*trackerCache{}
	c.dirty = map[string]bool{}
	c.mu.Unlock()
	return c.store.CacheDeleteAll(ctx)
}

// GetCachedResults mirrors upstream CacheService.GetCachedResults: per
// indexer, queries newest-first flattened into TrackerCacheResult
// (FirstSeen = query creation, Tracker fields from the cache entry),
// de-duplicated by Guid — .NET GroupBy folds null Guids into one group, so
// only the first null-Guid release survives — Take(300) per indexer, then
// ordered by PublishDate descending, capped at 3000.
func (c *CacheService) GetCachedResults(ctx context.Context) []*models.TrackerCacheResult {
	c.mu.Lock()
	if !c.enabled() {
		c.mu.Unlock()
		return nil
	}
	c.pruneByTTLLocked()
	ids := make([]string, 0, len(c.byID))
	for id := range c.byID {
		ids = append(ids, id)
	}
	caches := make(map[string]*trackerCache, len(ids))
	for _, id := range ids {
		caches[id] = c.trackerCacheFor(id)
	}
	c.mu.Unlock()

	var out []*models.TrackerCacheResult
	for _, id := range ids {
		tc := caches[id]
		tc.mu.Lock()
		seen := map[string]bool{}
		count := 0
		for _, hash := range tc.sortedHashesLocked() {
			if count >= 300 {
				break
			}
			q := tc.queries[hash]
			for _, r := range q.results {
				key := ""
				if r.Guid != nil {
					key = r.Guid.String()
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				t := models.NewTrackerCacheResult(r)
				t.FirstSeen = q.created
				t.Tracker = tc.trackerName
				t.TrackerId = tc.trackerID
				t.TrackerType = tc.trackerType
				out = append(out, t)
				count++
				if count >= 300 {
					break
				}
			}
		}
		tc.mu.Unlock()
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].PublishDate.After(out[j].PublishDate)
	})
	if len(out) > 3000 {
		out = out[:3000]
	}
	c.logger.Debug(fmt.Sprintf("CACHE GetCachedResults / Results: %d (cache may contain more results)", len(out)))
	c.printStatus()
	return out
}
