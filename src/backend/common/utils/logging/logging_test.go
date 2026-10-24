package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ringLevelEnabled(level string) bool {
	v, ok := defaultLevelVar()
	if !ok {
		return false
	}
	var lv slog.Level
	if level == "debug" {
		lv = slog.LevelDebug
	} else {
		lv = slog.LevelInfo
	}
	return v.Level() <= lv
}

func TestRingEviction(t *testing.T) {
	r := NewRing(10)
	for i := 0; i < 15; i++ {
		r.Append(CachedLog{Level: "INFO", Message: fmt.Sprintf("msg-%d", i), When: time.Now()})
	}
	got := r.List()
	if len(got) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(got))
	}
	// newest first: last appended is msg-14
	if got[0].Message != "msg-14" {
		t.Fatalf("expected newest first, got %s", got[0].Message)
	}
	if got[9].Message != "msg-5" {
		t.Fatalf("expected oldest survivor msg-5, got %s", got[9].Message)
	}
}

func TestRingUnderCapacity(t *testing.T) {
	r := NewRing(1000)
	r.Append(CachedLog{Level: "INFO", Message: "a", When: time.Now()})
	if got := r.List(); len(got) != 1 || got[0].Message != "a" {
		t.Fatalf("unexpected list: %+v", got)
	}
}

func TestSetupAndSetLevel(t *testing.T) {
	dir := t.TempDir()
	ring, err := Setup(dir, "info")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	// runtime switch reaches the ring level gate
	SetLevel("debug")
	if !ringLevelEnabled("debug") {
		t.Fatal("debug should be enabled after SetLevel(\"debug\")")
	}
	SetLevel("info")
	if ringLevelEnabled("debug") {
		t.Fatal("debug should be disabled after SetLevel(\"info\")")
	}

	// the file is created lazily on first write
	slog.Default().Info("test log line")
	if _, err := os.Stat(filepath.Join(dir, "Logs", "vestt.log")); err != nil {
		t.Fatalf("log file missing: %v", err)
	}
	if ring == nil {
		t.Fatal("ring is nil")
	}
}

func TestRollingFileRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vestt.log")
	rf, err := newRollingFile(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	chunk := []byte(strings.Repeat("y", 1024))
	if _, err := rf.Write(chunk); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	// write past the cap with a small cap via direct size manipulation
	rf.size = FileMaxBytes - 100
	if n, err := rf.Write(chunk); err != nil || n != len(chunk) {
		t.Fatalf("write: %d %v", n, err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated backup missing: %v", err)
	}
	// second rotation shifts .1 -> .2, keeping FileKeep backups
	rf.size = FileMaxBytes - 100
	if _, err := rf.Write(chunk); err != nil {
		t.Fatalf("write2: %v", err)
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Fatalf("second rotated backup missing: %v", err)
	}
}
