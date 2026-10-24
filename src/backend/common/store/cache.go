package store

import (
	"context"
	"time"
)

// CacheResultRow mirrors one cache_results row (data-model.md §2.4): one
// (indexer, query-key) page holding the JSON-serialised release array.
type CacheResultRow struct {
	QueryKey    string
	IndexerID   string
	ReleaseJSON string
	CreatedAt   time.Time
}

func parseCacheCreated(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// CacheGet returns the stored page for (indexer, query key);
// sql.ErrNoRows when absent.
func (s *Store) CacheGet(ctx context.Context, indexerID, queryKey string) (*CacheResultRow, error) {
	r := &CacheResultRow{}
	var created string
	err := s.db.QueryRowContext(ctx,
		`SELECT query_key, indexer_id, release_json, created_at FROM cache_results
		 WHERE indexer_id = ? AND query_key = ?`, indexerID, queryKey).
		Scan(&r.QueryKey, &r.IndexerID, &r.ReleaseJSON, &created)
	if err != nil {
		return nil, err
	}
	r.CreatedAt = parseCacheCreated(created)
	return r, nil
}

// CachePut replaces the (indexer, query-key) page. The schema has no unique
// constraint on the composite key, so replace = DELETE + INSERT in one
// transaction (SQLite single-writer discipline, AGENTS.md §7).
func (s *Store) CachePut(ctx context.Context, indexerID, queryKey, releaseJSON string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM cache_results WHERE indexer_id = ? AND query_key = ?`, indexerID, queryKey); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO cache_results (query_key, indexer_id, release_json, created_at) VALUES (?,?,?,?)`,
		queryKey, indexerID, releaseJSON, nowUTC()); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// CacheDeleteKey removes one page by (indexer, query key) (eviction path).
func (s *Store) CacheDeleteKey(ctx context.Context, indexerID, queryKey string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM cache_results WHERE indexer_id = ? AND query_key = ?`, indexerID, queryKey)
	return err
}

// CacheDeleteByIndexer removes every cached page of one indexer.
func (s *Store) CacheDeleteByIndexer(ctx context.Context, indexerID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cache_results WHERE indexer_id = ?`, indexerID)
	return err
}

// CacheDeleteAll empties the result cache.
func (s *Store) CacheDeleteAll(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cache_results`)
	return err
}

// CacheList returns every cached page (Cache view assembly).
func (s *Store) CacheList(ctx context.Context) ([]*CacheResultRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT query_key, indexer_id, release_json, created_at FROM cache_results ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CacheResultRow
	for rows.Next() {
		r := &CacheResultRow{}
		var created string
		if err := rows.Scan(&r.QueryKey, &r.IndexerID, &r.ReleaseJSON, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = parseCacheCreated(created)
		out = append(out, r)
	}
	return out, rows.Err()
}
