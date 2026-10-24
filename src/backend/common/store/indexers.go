package store

import (
	"context"
	"errors"
)

// IndexerConfigRow mirrors the indexer_config table (data-model.md §2.4).
type IndexerConfigRow struct {
	IndexerID   string
	Enabled     bool
	ConfigJSON  string
	CookiesJSON string
	LastError   *string
	State       string
	TestedAt    *string
	UpdatedAt   string
}

// ListIndexerConfigs returns every stored indexer configuration.
func (s *Store) ListIndexerConfigs(ctx context.Context) ([]*IndexerConfigRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT indexer_id, enabled, config_json, COALESCE(cookies_json,''), last_error, state, tested_at, updated_at
		 FROM indexer_config ORDER BY indexer_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*IndexerConfigRow
	for rows.Next() {
		r := &IndexerConfigRow{}
		var enabled int
		if err := rows.Scan(&r.IndexerID, &enabled, &r.ConfigJSON, &r.CookiesJSON, &r.LastError, &r.State, &r.TestedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetIndexerConfig returns one row; sql.ErrNoRows when absent.
func (s *Store) GetIndexerConfig(ctx context.Context, indexerID string) (*IndexerConfigRow, error) {
	r := &IndexerConfigRow{}
	var enabled int
	err := s.db.QueryRowContext(ctx,
		`SELECT indexer_id, enabled, config_json, COALESCE(cookies_json,''), last_error, state, tested_at, updated_at
		 FROM indexer_config WHERE indexer_id = ?`, indexerID).
		Scan(&r.IndexerID, &enabled, &r.ConfigJSON, &r.CookiesJSON, &r.LastError, &r.State, &r.TestedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	r.Enabled = enabled != 0
	return r, nil
}

// SaveIndexerConfig upserts one row (updated_at = now).
func (s *Store) SaveIndexerConfig(ctx context.Context, row *IndexerConfigRow) error {
	enabled := 0
	if row.Enabled {
		enabled = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO indexer_config (indexer_id, enabled, config_json, cookies_json, last_error, state, tested_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT(indexer_id) DO UPDATE SET
		   enabled=excluded.enabled, config_json=excluded.config_json,
		   cookies_json=excluded.cookies_json, last_error=excluded.last_error,
		   state=excluded.state, tested_at=excluded.tested_at, updated_at=excluded.updated_at`,
		row.IndexerID, enabled, row.ConfigJSON, nullIfEmpty(row.CookiesJSON), row.LastError, row.State, row.TestedAt, nowUTC())
	return err
}

// DeleteIndexerConfig removes one row (cache cleanup is the caller's job).
func (s *Store) DeleteIndexerConfig(ctx context.Context, indexerID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM indexer_config WHERE indexer_id = ?`, indexerID)
	return err
}

// ErrNotFound is returned by manager lookups that wrap the store.
var ErrNotFound = errors.New("not found")

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
