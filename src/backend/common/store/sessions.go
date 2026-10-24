package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Session is one admin login session (data-model.md §2.3).
type Session struct {
	Token     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

const sessionTimeFormat = "2006-01-02T15:04:05Z"

// CreateSession stores a new session token expiring at expiresAt.
func (s *Store) CreateSession(ctx context.Context, token string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token, created_at, expires_at) VALUES (?, ?, ?)`,
		token, nowUTC(), expiresAt.UTC().Format(sessionTimeFormat))
	return err
}

// GetSession returns the session if it exists and has not expired.
// Expired rows are deleted lazily (data-model.md §2.3).
func (s *Store) GetSession(ctx context.Context, token string) (*Session, error) {
	var sess Session
	var created, expires string
	err := s.db.QueryRowContext(ctx,
		`SELECT token, created_at, expires_at FROM sessions WHERE token = ?`, token).
		Scan(&sess.Token, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sess.CreatedAt, err = time.Parse(sessionTimeFormat, created)
	if err != nil {
		return nil, err
	}
	sess.ExpiresAt, err = time.Parse(sessionTimeFormat, expires)
	if err != nil {
		return nil, err
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		_ = s.DeleteSession(ctx, token)
		return nil, nil
	}
	return &sess, nil
}

// DeleteSession removes one session (logout / password change).
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// DeleteAllSessions removes every session (admin password change forces
// re-login); returns affected count.
func (s *Store) DeleteAllSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CleanupSessions removes all expired sessions; returns affected count.
func (s *Store) CleanupSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, nowUTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
