// Package logging implements the Vestt slog pipeline: console, rolling file
// and an in-memory ring buffer for /server/logs.
//
// Ported from: Jackett.Common/Services/LogCacheService.cs (ring cache) and
// Jackett.Common/Utils/LoggingSetup.cs (file rolling).
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// RingCapacity is the ring buffer size (LogCacheService.CachedLogs cap).
	RingCapacity = 1000
	// FileMaxBytes rotates vestt.log at 10 MiB keeping one backup file.
	FileMaxBytes = 10 * 1024 * 1024
	// FileKeep is the number of rotated log files retained (upstream keeps 1).
	FileKeep = 1
)

// CachedLog is one ring buffer entry (backend-api.md §1.4: level/message/when).
type CachedLog struct {
	Level   string    `json:"level"`
	Message string    `json:"message"`
	When    time.Time `json:"when"`
}

// Ring is a thread-safe fixed-capacity ring buffer, newest-first on read.
type Ring struct {
	mu   sync.Mutex
	buf  []CachedLog
	next int
	full bool
}

// NewRing returns an empty ring with the given capacity.
func NewRing(capacity int) *Ring {
	return &Ring{buf: make([]CachedLog, capacity)}
}

// Append adds an entry, evicting the oldest when full.
func (r *Ring) Append(l CachedLog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = l
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// List returns entries newest-first.
func (r *Ring) List() []CachedLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.buf)
	count := n
	if !r.full {
		count = r.next
	}
	out := make([]CachedLog, 0, count)
	for i := 0; i < count; i++ {
		idx := (r.next - 1 - i + n) % n
		out = append(out, r.buf[idx])
	}
	return out
}

// fanoutHandler fans one record out to several handlers.
type fanoutHandler struct {
	handlers []slog.Handler
}

func (h *fanoutHandler) Enabled(_ context.Context, level slog.Level) bool {
	for _, sub := range h.handlers {
		if sub.Enabled(context.Background(), level) {
			return true
		}
	}
	return false
}

func (h *fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, sub := range h.handlers {
		if sub.Enabled(ctx, r.Level) {
			if err := sub.Handle(ctx, r.Clone()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	subs := make([]slog.Handler, len(h.handlers))
	for i, sub := range h.handlers {
		subs[i] = sub.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: subs}
}

func (h *fanoutHandler) WithGroup(name string) slog.Handler {
	subs := make([]slog.Handler, len(h.handlers))
	for i, sub := range h.handlers {
		subs[i] = sub.WithGroup(name)
	}
	return &fanoutHandler{handlers: subs}
}

// ringHandler writes records into the ring buffer as CachedLog entries.
type ringHandler struct {
	ring  *Ring
	level *slog.LevelVar
}

func (h *ringHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *ringHandler) Handle(_ context.Context, r slog.Record) error {
	h.ring.Append(CachedLog{
		Level:   r.Level.String(),
		Message: r.Message,
		When:    r.Time.UTC(),
	})
	return nil
}

func (h *ringHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *ringHandler) WithGroup(_ string) slog.Handler      { return h }

// rollingFile is an io.Writer that rotates the file at FileMaxBytes,
// keeping FileKeep backups (LoggingSetup semantics).
type rollingFile struct {
	mu   sync.Mutex
	path string
	size int64
}

func newRollingFile(path string) (*rollingFile, error) {
	f := &rollingFile{path: path}
	if info, err := os.Stat(path); err == nil {
		f.size = info.Size()
	}
	return f, nil
}

func (f *rollingFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.size+int64(len(p)) > FileMaxBytes {
		if err := f.rotate(); err != nil {
			return 0, err
		}
	}
	fd, err := os.OpenFile(f.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() { _ = fd.Close() }()
	n, err := fd.Write(p)
	f.size += int64(n)
	return n, err
}

func (f *rollingFile) rotate() error {
	// prune the oldest backup beyond the keep window, then shift .i -> .i+1
	_ = os.Remove(f.path + fmt.Sprintf(".%d", FileKeep+1))
	for i := FileKeep; i >= 1; i-- {
		_ = os.Rename(f.path+fmt.Sprintf(".%d", i), f.path+fmt.Sprintf(".%d", i+1))
	}
	if err := os.Rename(f.path, f.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	f.size = 0
	return nil
}

// Setup configures the default slog logger: console + rolling file under
// <dataDir>/Logs + ring buffer. level is "info" or "debug". The ring is
// returned for /server/logs reads; SetLevel switches levels at runtime.
func Setup(dataDir string, level string) (*Ring, error) {
	ring := NewRing(RingCapacity)
	levelVar := &slog.LevelVar{}
	setLevelVar(levelVar, level)

	console := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar})

	handlers := []slog.Handler{console, &ringHandler{ring: ring, level: levelVar}}

	if dataDir != "" {
		logDir := filepath.Join(dataDir, "Logs")
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return nil, fmt.Errorf("create log dir: %w", err)
		}
		rf, err := newRollingFile(filepath.Join(logDir, "vestt.log"))
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, slog.NewTextHandler(rf, &slog.HandlerOptions{Level: levelVar}))
	}

	slog.SetDefault(slog.New(&fanoutHandler{handlers: handlers}))
	return ring, nil
}

// SetLevel switches the default logger level at runtime ("info"|"debug"),
// matching the logging config item semantics.
func SetLevel(level string) {
	if v, ok := defaultLevelVar(); ok {
		setLevelVar(v, level)
	}
}

// defaultLevelVar tracks the LevelVar created by Setup for runtime switches.
var (
	defaultLevelMu sync.Mutex
	defaultLevel   *slog.LevelVar
)

func setLevelVar(v *slog.LevelVar, level string) {
	switch level {
	case "debug":
		v.Set(slog.LevelDebug)
	default:
		v.Set(slog.LevelInfo)
	}
	defaultLevelMu.Lock()
	defaultLevel = v
	defaultLevelMu.Unlock()
}

func defaultLevelVar() (*slog.LevelVar, bool) {
	defaultLevelMu.Lock()
	defer defaultLevelMu.Unlock()
	return defaultLevel, defaultLevel != nil
}
