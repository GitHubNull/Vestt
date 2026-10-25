// Command vestt-import migrates a Jackett data directory into a Vestt data
// directory (SPEC-14 §3.3).
//
// Usage:
//
//	vestt-import --src <jackett-dir> --out <vestt-data-dir>
//
// It reads <src>/ServerConfig.json and <src>/Indexers/*.json (upstream file
// layout: ConfigurationService.GetConfig<T>/SaveConfig<T> store "<Type>.json"
// in the app-data folder; IndexerConfigurationService.GetIndexerConfigFilePath
// stores "<id>.json" in the "Indexers" sub-folder, cookies included as the
// "cookieheader" config item). The server fields are mapped onto the single
// server_config row and every indexer file whose id is part of the built-in
// set (hand-written factories + embedded cardigann definitions) onto one
// indexer_config row; the rest are reported as skipped (doc/tech/data-model.md
// §2.2/§2.4/§3).
//
// The tool is pure local file + SQLite processing and never touches the
// network (SPEC-14 §3.3). Writes are upserts, so re-running against an
// already-populated data directory is idempotent.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"vestt/common/engine"
	_ "vestt/common/indexer/manual" // registers the hand-written indexer factories
	"vestt/common/services"
	"vestt/common/store"
)

const usage = "usage: vestt-import --src <jackett-dir> --out <vestt-data-dir>"

type options struct {
	src string
	out string
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	fsset := flag.NewFlagSet("vestt-import", flag.ContinueOnError)
	fsset.SetOutput(stderr)
	var opts options
	fsset.StringVar(&opts.src, "src", "", "Jackett data directory (contains ServerConfig.json and Indexers/)")
	fsset.StringVar(&opts.out, "out", "", "Vestt data directory to create/update (holds vestt.db)")
	if err := fsset.Parse(args); err != nil {
		return options{}, err
	}
	if opts.src == "" || opts.out == "" {
		return options{}, errors.New("both --src and --out are required")
	}
	return opts, nil
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// skipEntry is one non-imported indexer file with the reason.
type skipEntry struct {
	id     string
	reason string
}

// importReport accumulates the end-of-run summary (SPEC-14 §3.3: 导入 n /
// 跳过 m + 跳过清单).
type importReport struct {
	srcPath        string
	outPath        string
	outCreated     bool
	manualCount    int
	cardigannCount int
	serverImported bool
	notes          []string
	imported       []string
	skipped        []skipEntry
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vestt-import: %v\n%s\n", err, usage)
		return 2
	}

	if sr, err := os.Stat(opts.src); err != nil || !sr.IsDir() {
		if err == nil {
			err = errors.New("not a directory")
		}
		_, _ = fmt.Fprintf(stderr, "vestt-import: source %s: %v\n", opts.src, err)
		return 1
	}

	report := &importReport{srcPath: opts.src, outPath: opts.out}
	if _, err := os.Stat(opts.out); errors.Is(err, fs.ErrNotExist) {
		report.outCreated = true
	}
	if err := os.MkdirAll(opts.out, 0o755); err != nil {
		_, _ = fmt.Fprintf(stderr, "vestt-import: create output directory: %v\n", err)
		return 1
	}

	// Built-in id set: the hand-written factories (registered by the blank
	// manual import above) plus every embedded cardigann definition id.
	// The engine load logs per-definition problems internally; a low-frequency
	// CLI only needs the resulting id set, so the logger writes nowhere.
	report.manualCount = len(services.RegisteredFactories())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	defs, _, err := engine.LoadAll(logger, "")
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vestt-import: load cardigann definitions: %v\n", err)
		return 1
	}
	report.cardigannCount = len(defs)
	builtin := make(map[string]bool, len(defs)+report.manualCount)
	for id := range defs {
		builtin[id] = true
	}
	for _, f := range services.RegisteredFactories() {
		builtin[f.ID] = true
	}

	// Opening the store runs the schema migrations, so a fresh --out is
	// initialized with the full schema (SPEC-14 §3.3 step 3).
	st, err := store.Open(filepath.Join(opts.out, "vestt.db"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "vestt-import: open store: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	if err := importServerConfig(ctx, st, opts.src, report); err != nil {
		_, _ = fmt.Fprintf(stderr, "vestt-import: %v\n", err)
		return 1
	}
	if err := importIndexers(ctx, st, opts.src, builtin, report); err != nil {
		_, _ = fmt.Fprintf(stderr, "vestt-import: %v\n", err)
		return 1
	}

	sort.Strings(report.imported)
	sort.Slice(report.skipped, func(i, j int) bool { return report.skipped[i].id < report.skipped[j].id })
	printReport(stdout, report)
	return 0
}

// importServerConfig reads <src>/ServerConfig.json and upserts the single
// server_config row. A missing file is reported, not fatal: the server
// generates API key/instance id on first start (services.ConfigService.Load).
func importServerConfig(ctx context.Context, st *store.Store, srcDir string, report *importReport) error {
	raw, err := os.ReadFile(filepath.Join(srcDir, "ServerConfig.json"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		report.notes = append(report.notes,
			"ServerConfig.json not found in source; server settings left at Vestt defaults")
		return nil
	case err != nil:
		return fmt.Errorf("read ServerConfig.json: %w", err)
	}
	cfg, notes, err := mapServerConfig(raw)
	if err != nil {
		return err
	}
	if err := st.SaveServerConfig(ctx, cfg); err != nil {
		return fmt.Errorf("save server_config: %w", err)
	}
	report.serverImported = true
	report.notes = append(report.notes, notes...)
	return nil
}

// importIndexers walks <src>/Indexers/*.json (backup/temp siblings such as
// "<id>.json.bak" are ignored, mirroring upstream's rotating write) and
// upserts one indexer_config row per file whose base name matches the
// built-in id set.
func importIndexers(ctx context.Context, st *store.Store, srcDir string, builtin map[string]bool, report *importReport) error {
	idxDir := filepath.Join(srcDir, "Indexers")
	entries, err := os.ReadDir(idxDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		report.notes = append(report.notes,
			"Indexers/ directory not found in source; no indexer configurations imported")
		return nil
	case err != nil:
		return fmt.Errorf("read Indexers directory: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !builtin[id] {
			report.skipped = append(report.skipped, skipEntry{id: id, reason: "no matching built-in indexer"})
			continue
		}
		raw, err := os.ReadFile(filepath.Join(idxDir, e.Name()))
		if err != nil {
			report.skipped = append(report.skipped, skipEntry{id: id, reason: err.Error()})
			continue
		}
		parsed, err := parseIndexerConfig(raw)
		if err != nil {
			report.skipped = append(report.skipped, skipEntry{id: id, reason: err.Error()})
			continue
		}
		row := &store.IndexerConfigRow{
			IndexerID:   id,
			Enabled:     true,
			ConfigJSON:  parsed.configJSON,
			CookiesJSON: parsed.cookiesJSON,
			State:       services.StateUnconfigured,
		}
		if err := st.SaveIndexerConfig(ctx, row); err != nil {
			report.skipped = append(report.skipped, skipEntry{id: id, reason: fmt.Sprintf("save: %v", err)})
			continue
		}
		report.imported = append(report.imported, id)
	}
	return nil
}

func printReport(w io.Writer, r *importReport) {
	_, _ = fmt.Fprintln(w, "Jackett -> Vestt import")
	_, _ = fmt.Fprintf(w, "source: %s\n", r.srcPath)
	outNote := ""
	if r.outCreated {
		outNote = " (created)"
	}
	_, _ = fmt.Fprintf(w, "output: %s%s\n", r.outPath, outNote)
	_, _ = fmt.Fprintf(w, "builtin indexers: %d (%d manual + %d cardigann)\n",
		r.manualCount+r.cardigannCount, r.manualCount, r.cardigannCount)
	if r.serverImported {
		_, _ = fmt.Fprintln(w, "server config: imported")
	} else {
		_, _ = fmt.Fprintln(w, "server config: not imported")
	}
	for _, n := range r.notes {
		_, _ = fmt.Fprintf(w, "note: %s\n", n)
	}
	_, _ = fmt.Fprintf(w, "indexers: imported %d, skipped %d\n", len(r.imported), len(r.skipped))
	if len(r.imported) > 0 {
		_, _ = fmt.Fprintln(w, "imported:")
		for _, id := range r.imported {
			_, _ = fmt.Fprintf(w, "  - %s\n", id)
		}
	}
	if len(r.skipped) > 0 {
		_, _ = fmt.Fprintln(w, "skipped:")
		for _, s := range r.skipped {
			_, _ = fmt.Fprintf(w, "  - %s: %s\n", s.id, s.reason)
		}
	}
}
