package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migration is one schema version bump. Migrations must never be modified
// once released; schema changes require a new migration (data-model.md §4).
type migration struct {
	version int
	name    string
	sql     string
}

// Migrations 1-3 land in SPEC-02; all tables are created up front so later
// SPECs never need retro-active schema changes (SPEC-02 §3.1.2).
var migrations = []migration{
	{
		// Version 1 is the schema_migrations bootstrap itself, created by
		// migrate() before running this list; recorded here for bookkeeping.
		version: 1,
		name:    "schema_migrations",
		sql:     `SELECT 1;`,
	},
	{
		version: 2,
		name:    "server_config_and_sessions",
		sql: `CREATE TABLE server_config (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    config_json  TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE TABLE sessions (
    token       TEXT PRIMARY KEY,
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL
);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);`,
	},
	{
		version: 3,
		name:    "indexer_config_cache_results_log_cache_saved_state",
		sql: `CREATE TABLE indexer_config (
    indexer_id   TEXT PRIMARY KEY,
    enabled      INTEGER NOT NULL DEFAULT 1,
    config_json  TEXT NOT NULL,
    cookies_json TEXT,
    last_error   TEXT,
    state        TEXT NOT NULL DEFAULT 'unconfigured',
    tested_at    TEXT,
    updated_at   TEXT NOT NULL
);
CREATE TABLE cache_results (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    query_key    TEXT NOT NULL,
    indexer_id   TEXT NOT NULL,
    release_json TEXT NOT NULL,
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_cache_query ON cache_results(query_key);
CREATE INDEX idx_cache_created ON cache_results(created_at);
CREATE TABLE log_cache (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    level     TEXT NOT NULL,
    message   TEXT NOT NULL,
    when_ts   TEXT NOT NULL
);
CREATE INDEX idx_log_when ON log_cache(when_ts DESC);
CREATE TABLE saved_state (
    key        TEXT PRIMARY KEY,
    value_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);`,
	},
}

// migrate applies pending migrations in order, each in a single transaction.
func (s *Store) migrate() error {
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
    version     INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    applied_at  TEXT NOT NULL
);`); err != nil {
		return err
	}

	var maxVersion int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&maxVersion)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.version <= maxVersion {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			m.version, m.name, nowUTC()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d (%s) bookkeeping: %w", m.version, m.name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// tableInfo is a test helper: column names of a table via PRAGMA table_info.
func (s *Store) tableInfo(ctx context.Context, table string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

// guard against accidental removal of the sql import in future edits.
var _ = sql.ErrNoRows
