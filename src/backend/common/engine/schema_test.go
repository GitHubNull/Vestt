package engine

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mutate loads the 1337x definition text and applies a string replacement.
func mutate(t *testing.T, repl [2]string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("definitions", "1337x.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if repl[0] == "" {
		return s
	}
	if !strings.Contains(s, repl[0]) {
		t.Fatalf("mutation anchor %q not found", repl[0])
	}
	return strings.ReplaceAll(s, repl[0], repl[1])
}

// TestSchemaRejectsMutations is acceptance #2: 6 mutated definitions are
// all rejected by the JSON schema validation.
func TestSchemaRejectsMutations(t *testing.T) {
	mutations := []struct {
		name string
		repl [2]string
	}{
		{"missing id", [2]string{"id: 1337x", "# id: 1337x"}},
		{"unknown top-level block", [2]string{"caps:\n", "caps:\n  bogusblock: true\n"}},
		{"bad type enum", [2]string{"type: public", "type: not-a-type"}},
		{"bad language", [2]string{"language: en-US", "language: xx-INVALID"}},
		{"search path not string", [2]string{"    - path: \"{{ if and (.Keywords)", "    - path: 42\n      # {{ if and (.Keywords)"}},
		{"rows selector missing", [2]string{"  rows:\n    selector:", "  rows:\n    nosuchkey:"}},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			defs := map[string]*IndexerDefinition{}
			stats := &LoadStats{}
			loadOne(defs, stats, discardLogger(), func() ([]byte, error) {
				return []byte(mutate(t, m.repl)), nil
			}, "mutated.yml")
			if len(defs) != 0 {
				t.Fatal("mutated definition was accepted")
			}
			if len(stats.Skipped) != 1 {
				t.Fatalf("expected 1 skip, got %v", stats.Skipped)
			}
		})
	}
}

// TestSchemaAcceptsPristine double-checks the harness: the unmutated
// definition loads.
func TestSchemaAcceptsPristine(t *testing.T) {
	defs := map[string]*IndexerDefinition{}
	stats := &LoadStats{}
	loadOne(defs, stats, discardLogger(), func() ([]byte, error) {
		return []byte(mutate(t, [2]string{})), nil
	}, "pristine.yml")
	if len(defs) != 1 || len(stats.Skipped) != 0 {
		t.Fatalf("pristine rejected: %v", stats.Skipped)
	}
}
