// Package services holds application services: IndexerManager, Cache,
// Config, LogCache, Security, FlareSolverr.
//
// Ported from: Jackett.Common/Services/SecurityService.cs (password hashing
// and session validation).
package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"golang.org/x/crypto/bcrypt"

	"vestt/common/store"
)

// bcryptCost mirrors upstream SecurityService (cost 11).
const bcryptCost = 11

// SessionTTL is how long an admin session stays valid (14 days, upstream
// cookie ExpiresUtc semantics).
const SessionTTL = 14 * 24 * time.Hour

// SecurityService hashes admin passwords and validates session tokens.
type SecurityService struct {
	store *store.Store
}

// NewSecurityService constructs the service.
func NewSecurityService(s *store.Store) *SecurityService {
	return &SecurityService{store: s}
}

// HashPassword returns a bcrypt hash of the password.
func (s *SecurityService) HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(h), err
}

// CheckPassword compares a plaintext password with a stored bcrypt hash.
// An empty hash never matches (admin password not set).
func (s *SecurityService) CheckPassword(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// CheckAuthorised mirrors upstream SecurityService.CheckAuthorised: when no
// admin password is configured everything is authorised; otherwise the
// provided password must match.
func (s *SecurityService) CheckAuthorised(passwordHash, password string) bool {
	if passwordHash == "" {
		return true
	}
	return s.CheckPassword(passwordHash, password)
}

// NewToken returns 32 random bytes as hex (API keys, session tokens).
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// CreateSession persists a new session token and returns it.
func (s *SecurityService) CreateSession(ctx context.Context) (string, error) {
	token, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := s.store.CreateSession(ctx, token, time.Now().UTC().Add(SessionTTL)); err != nil {
		return "", err
	}
	return token, nil
}

// ValidateSession reports whether the token identifies a live session.
func (s *SecurityService) ValidateSession(ctx context.Context, token string) bool {
	if token == "" {
		return false
	}
	sess, err := s.store.GetSession(ctx, token)
	return err == nil && sess != nil
}

// DeleteSession removes a session (logout).
func (s *SecurityService) DeleteSession(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, token)
}

// DeleteAllSessions removes every session (admin password change forces
// re-login, upstream equivalent).
func (s *SecurityService) DeleteAllSessions(ctx context.Context) error {
	_, err := s.store.DeleteAllSessions(ctx)
	return err
}
