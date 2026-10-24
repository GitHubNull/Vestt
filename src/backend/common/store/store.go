// Package store is the SQLite access layer.
//
// Ported from: Jackett.Common/Services (ConfigurationService persistence
// semantics) — see doc/tech/data-model.md.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store holds the database handle and provides domain DAOs.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path and runs
// migrations. PRAGMAs follow doc/tech/data-model.md §1; the connection pool
// is capped at one connection because modernc.org/sqlite does not support
// concurrent writers.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// OpenMemory opens an in-memory database (tests).
func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open sqlite memory: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// nowUTC returns the canonical timestamp format (data-model.md §1).
func nowUTC() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }
