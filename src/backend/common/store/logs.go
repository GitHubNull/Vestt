package store

import (
	"context"
	"time"
)

// CachedLog is one ring-buffer log entry (backend-api.md §1.4).
type CachedLog struct {
	Level   string    `json:"level"`
	Message string    `json:"message"`
	When    time.Time `json:"when"`
}

// AppendLog inserts one log row into log_cache (used when log persistence
// is enabled; data-model.md §2.6).
func (s *Store) AppendLog(ctx context.Context, level, message string, when time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO log_cache (level, message, when_ts) VALUES (?, ?, ?)`,
		level, message, when.UTC().Format(sessionTimeFormat))
	return err
}

// ListLogs returns the most recent limit rows, newest first.
func (s *Store) ListLogs(ctx context.Context, limit int) ([]CachedLog, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT level, message, when_ts FROM log_cache ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CachedLog
	for rows.Next() {
		var l CachedLog
		var when string
		if err := rows.Scan(&l.Level, &l.Message, &when); err != nil {
			return nil, err
		}
		l.When, err = time.Parse(sessionTimeFormat, when)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// TrimLogs keeps only the newest max rows; returns affected count.
func (s *Store) TrimLogs(ctx context.Context, max int) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM log_cache WHERE id NOT IN (SELECT id FROM log_cache ORDER BY id DESC LIMIT ?)`, max)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
