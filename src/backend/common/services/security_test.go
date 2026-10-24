package services

import (
	"context"
	"testing"

	"vestt/common/store"
)

func newTestSecurity(t *testing.T) *SecurityService {
	t.Helper()
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewSecurityService(s)
}

func TestPasswordRoundTrip(t *testing.T) {
	sec := newTestSecurity(t)
	hash, err := sec.HashPassword("s3cret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !sec.CheckPassword(hash, "s3cret") {
		t.Fatal("correct password rejected")
	}
	if sec.CheckPassword(hash, "wrong") {
		t.Fatal("wrong password accepted")
	}
	if sec.CheckPassword("", "anything") {
		t.Fatal("empty hash must never match")
	}
}

func TestCheckAuthorised(t *testing.T) {
	sec := newTestSecurity(t)
	// no admin password configured → everything authorised
	if !sec.CheckAuthorised("", "") {
		t.Fatal("empty hash should authorise")
	}
	if !sec.CheckAuthorised("", "whatever") {
		t.Fatal("empty hash should authorise any password")
	}
	hash, _ := sec.HashPassword("pw")
	if !sec.CheckAuthorised(hash, "pw") {
		t.Fatal("matching password should authorise")
	}
	if sec.CheckAuthorised(hash, "nope") {
		t.Fatal("mismatch should not authorise")
	}
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	sec := newTestSecurity(t)

	if sec.ValidateSession(ctx, "") {
		t.Fatal("empty token must be invalid")
	}
	if sec.ValidateSession(ctx, "nonexistent") {
		t.Fatal("unknown token must be invalid")
	}

	token, err := sec.CreateSession(ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !sec.ValidateSession(ctx, token) {
		t.Fatal("fresh session must be valid")
	}

	if err := sec.DeleteSession(ctx, token); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if sec.ValidateSession(ctx, token) {
		t.Fatal("deleted session must be invalid")
	}
}

func TestDeleteAllSessions(t *testing.T) {
	ctx := context.Background()
	sec := newTestSecurity(t)
	t1, _ := sec.CreateSession(ctx)
	t2, _ := sec.CreateSession(ctx)
	if err := sec.DeleteAllSessions(ctx); err != nil {
		t.Fatalf("delete all: %v", err)
	}
	if sec.ValidateSession(ctx, t1) || sec.ValidateSession(ctx, t2) {
		t.Fatal("all sessions should be gone")
	}
}

func TestNewTokenUnique(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	b, err := NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if a == b {
		t.Fatal("tokens must be unique")
	}
	if len(a) != 64 {
		t.Fatalf("token must be 32B hex (64 chars), got %d", len(a))
	}
}
