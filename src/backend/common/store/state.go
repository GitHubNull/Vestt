package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetSavedState returns the raw value_json stored under key
// (data-model.md §2.7). ok=false when the key was never written.
func (s *Store) GetSavedState(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value_json FROM saved_state WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get saved_state %q: %w", key, err)
	}
	return value, true, nil
}

// SaveSavedState upserts a saved_state row; valueJSON must be a JSON value.
func (s *Store) SaveSavedState(ctx context.Context, key, valueJSON string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO saved_state (key, value_json, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
		key, valueJSON, nowUTC())
	if err != nil {
		return fmt.Errorf("save saved_state %q: %w", key, err)
	}
	return nil
}
