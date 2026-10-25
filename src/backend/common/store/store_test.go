package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"vestt/common/models/config"
)

func TestMigrateIdempotent(t *testing.T) {
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Re-run migrations on an already-migrated store: must be a no-op.
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var version int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 3 {
		t.Fatalf("expected migration version 3, got %d", version)
	}
}

func TestTableStructure(t *testing.T) {
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	want := map[string][]string{
		"server_config":  {"id", "config_json", "updated_at"},
		"sessions":       {"token", "created_at", "expires_at"},
		"indexer_config": {"indexer_id", "enabled", "config_json", "cookies_json", "last_error", "state", "tested_at", "updated_at"},
		"cache_results":  {"id", "query_key", "indexer_id", "release_json", "created_at"},
		"log_cache":      {"id", "level", "message", "when_ts"},
		"saved_state":    {"key", "value_json", "updated_at"},
	}
	for table, cols := range want {
		got, err := s.tableInfo(ctx, table)
		if err != nil {
			t.Fatalf("table_info %s: %v", table, err)
		}
		if len(got) != len(cols) {
			t.Fatalf("table %s: expected columns %v, got %v", table, cols, got)
		}
		for i, c := range cols {
			if got[i] != c {
				t.Fatalf("table %s: expected columns %v, got %v", table, cols, got)
			}
		}
	}
}

func TestServerConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if _, err := s.GetServerConfig(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows for missing config, got %v", err)
	}

	port := 8080
	cfg := config.DefaultServerConfig()
	cfg.Port = 9222
	cfg.ProxyPort = &port
	cfg.APIKey = "abc123"
	if err := s.SaveServerConfig(ctx, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	// upsert again (idempotency)
	if err := s.SaveServerConfig(ctx, cfg); err != nil {
		t.Fatalf("save twice: %v", err)
	}

	loaded, err := s.GetServerConfig(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loaded.Port != 9222 || loaded.APIKey != "abc123" {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	if loaded.ProxyPort == nil || *loaded.ProxyPort != 8080 {
		t.Fatalf("proxy_port round trip mismatch: %+v", loaded.ProxyPort)
	}
}

func TestSessionsLifecycle(t *testing.T) {
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	sess, err := s.GetSession(ctx, "missing")
	if err != nil || sess != nil {
		t.Fatalf("missing session: got %v, %v", sess, err)
	}

	expires := time.Now().UTC().Add(14 * 24 * time.Hour)
	if err := s.CreateSession(ctx, "tok1", expires); err != nil {
		t.Fatalf("create: %v", err)
	}
	sess, err = s.GetSession(ctx, "tok1")
	if err != nil || sess == nil {
		t.Fatalf("get: %v %v", sess, err)
	}
	if !sess.ExpiresAt.Equal(expires.Truncate(0).Round(0)) && sess.ExpiresAt.Unix() != expires.Unix() {
		t.Fatalf("expiry mismatch: %v vs %v", sess.ExpiresAt, expires)
	}

	// expired session: GetSession must treat it as absent and delete it.
	past := time.Now().UTC().Add(-time.Hour)
	if err := s.CreateSession(ctx, "tok2", past); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	sess, err = s.GetSession(ctx, "tok2")
	if err != nil {
		t.Fatalf("get expired: %v", err)
	}
	if sess != nil {
		t.Fatalf("expired session should be nil: %+v", sess)
	}
	// tok2 was already lazily deleted by GetSession; create a fresh expired
	// row directly to exercise CleanupSessions.
	if err := s.CreateSession(ctx, "tok3", past); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	n, err := s.CleanupSessions(ctx)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n != 1 {
		t.Fatalf("cleanup should have removed exactly 1 row, got %d", n)
	}

	if err := s.DeleteSession(ctx, "tok1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if sess, _ := s.GetSession(ctx, "tok1"); sess != nil {
		t.Fatal("deleted session still present")
	}
}

func TestLogsAppendListTrim(t *testing.T) {
	ctx := context.Background()
	s, err := OpenMemory()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	when := time.Now().UTC().Truncate(time.Second)
	if err := s.AppendLog(ctx, "INFO", "first", when); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.AppendLog(ctx, "DEBUG", "second", when.Add(time.Second)); err != nil {
		t.Fatalf("append: %v", err)
	}

	logs, err := s.ListLogs(ctx, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(logs))
	}
	// newest first
	if logs[0].Message != "second" || logs[1].Message != "first" {
		t.Fatalf("unexpected order: %+v", logs)
	}
	if logs[0].Level != "DEBUG" {
		t.Fatalf("unexpected level: %+v", logs[0])
	}

	if _, err := s.TrimLogs(ctx, 1); err != nil {
		t.Fatalf("trim: %v", err)
	}
	logs, err = s.ListLogs(ctx, 100)
	if err != nil {
		t.Fatalf("list after trim: %v", err)
	}
	if len(logs) != 1 || logs[0].Message != "second" {
		t.Fatalf("unexpected logs after trim: %+v", logs)
	}
}
