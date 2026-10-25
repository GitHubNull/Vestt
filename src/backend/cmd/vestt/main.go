// Command vestt is the Vestt server entrypoint: it assembles dependencies,
// starts the HTTP server and handles graceful shutdown.
//
// Ported from: Jackett.Server/Program.cs (startup flag semantics),
// Jackett.Server/Startup.cs (middleware pipeline ordering).
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"vestt/common/engine"
	"vestt/common/httpclient"
	"vestt/common/indexer"
	_ "vestt/common/indexer/manual" // SPEC-08 batch 1: registers 21 manual indexers
	"vestt/common/services"
	"vestt/common/store"
	"vestt/common/utils/logging"
	"vestt/server"
)

type options struct {
	Port             int
	DataDir          string
	BasePathOverride string
	LogLevel         string
	ListenPublic     bool
	ListenPrivate    bool
}

func parseFlags() options {
	var opts options
	flag.IntVar(&opts.Port, "port", 0, "HTTP listen port (overrides config for this run only)")
	flag.StringVar(&opts.DataDir, "data-dir", "", "data directory (default: platform user config dir)")
	flag.StringVar(&opts.BasePathOverride, "basepathoverride", "", "url base path override (overrides config for this run only)")
	flag.StringVar(&opts.LogLevel, "loglevel", "", "log level info|debug (overrides config for this run only)")
	flag.BoolVar(&opts.ListenPublic, "listenpublic", false, "listen publicly (bind 0.0.0.0, overrides config for this run only)")
	flag.BoolVar(&opts.ListenPrivate, "listenprivate", false, "only allow local access (overrides config for this run only)")
	flag.Parse()
	return opts
}

// defaultDataDir returns the platform user config dir based default
// (architecture.md §7: --data-dir default).
func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "vestt")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".config", "vestt")
}

func main() {
	opts := parseFlags()
	start := time.Now()

	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "create data dir: %v\n", err)
		os.Exit(1)
	}

	// Assembly order follows architecture.md §4.1.
	st, err := store.Open(filepath.Join(dataDir, "vestt.db"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = st.Close() }()

	cfgService := services.NewConfigService(st, vesttVersion)
	if err := cfgService.Load(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	// Flag overrides: flag > DB config > default. Flags never write back.
	var portOverride *int
	if opts.Port != 0 {
		portOverride = &opts.Port
	}
	var basePathOverride *string
	if opts.BasePathOverride != "" {
		basePathOverride = &opts.BasePathOverride
	}
	if err := cfgService.OverrideRuntime(portOverride, basePathOverride); err != nil {
		fmt.Fprintf(os.Stderr, "apply flags: %v\n", err)
		os.Exit(1)
	}

	logLevel := "info"
	if cfgService.LoggingDebug() {
		logLevel = "debug"
	}
	if opts.LogLevel != "" {
		logLevel = opts.LogLevel
	}
	ring, err := logging.Setup(dataDir, logLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup logging: %v\n", err)
		os.Exit(1)
	}

	security := services.NewSecurityService(st)

	cfg := cfgService.Config()
	// Console listen overrides (upstream Initialisation.ProcessConsoleOverrides,
	// Initialisation.cs:119-136): per-run only, never persisted; --listenpublic
	// wins if both are given.
	allowExternal := cfg.AllowExternal
	if opts.ListenPublic || opts.ListenPrivate {
		allowExternal = opts.ListenPublic
		slog.Info(fmt.Sprintf("Overriding external access to %v", allowExternal))
	}
	bind := cfg.LocalBindAddress
	if allowExternal {
		bind = "0.0.0.0"
	}

	// HTTP client stack (SPEC-04): proxy + rate limit + cookie jar +
	// Cloudflare/FlareSolverr, per-indexer isolated.
	cookieSaver := func(indexerID string, cookies []httpclient.PersistedCookie) {
		raw, err := json.Marshal(cookies)
		if err != nil {
			slog.Error("marshal cookies", "indexer", indexerID, "error", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		row, err := st.GetIndexerConfig(ctx, indexerID)
		if errors.Is(err, sql.ErrNoRows) {
			row = &store.IndexerConfigRow{IndexerID: indexerID, Enabled: true, State: services.StateUnconfigured}
		} else if err != nil {
			slog.Error("load indexer config for cookies", "indexer", indexerID, "error", err)
			return
		}
		row.CookiesJSON = string(raw)
		if err := st.SaveIndexerConfig(ctx, row); err != nil {
			slog.Error("persist cookies", "indexer", indexerID, "error", err)
		}
	}
	webClient, err := httpclient.New(httpclient.Config{
		Proxy: httpclient.ProxyConfig{
			Type:     httpclient.ProxyType(cfg.ProxyType),
			URL:      cfg.ProxyURL,
			Port:     cfg.ProxyPort,
			Username: cfg.ProxyUsername,
			Password: cfg.ProxyPassword,
		},
		FlareSolverrURL:        cfg.FlareSolverrURL,
		FlareSolverrMaxTimeout: cfg.FlareSolverrMaxTimeout,
	}, cookieSaver, slog.Default())
	if err != nil {
		fmt.Fprintf(os.Stderr, "http client: %v\n", err)
		os.Exit(1)
	}

	// YAML engine (SPEC-05): load all cardigann definitions, then register
	// the dynamic factory that instantiates one indexer per definition.
	defs, _, err := engine.LoadAll(slog.Default(), "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "load definitions: %v\n", err)
		os.Exit(1)
	}
	services.RegisterCardigann(func(deps services.Deps) ([]indexer.Indexer, error) {
		out := make([]indexer.Indexer, 0, len(defs))
		for _, def := range defs {
			x, err := engine.NewCardigann(def, slog.Default())
			if err != nil {
				slog.Error("cardigann factory", "definition", def.ID, "error", err)
				continue
			}
			x.Client = deps.Client
			x.Saver = deps.Saver
			x.Cache = deps.Cache
			out = append(out, x)
		}
		return out, nil
	})

	// Indexer lifecycle manager: instantiates registered indexers, restores
	// saved configs/cookies, owns the aggregate meta indexer.
	manager := services.NewIndexerManager(st, webClient, slog.Default())

	// Result cache (SPEC-07): wired before Init so indexer factories get
	// the cache seam. The IMDB title resolver is installed only when an
	// OMDB API key is configured (upstream GetStrategyProviders).
	cache := services.NewCacheService(st, cfgService, slog.Default())
	defer cache.Close()
	manager.SetCache(cache)
	if cfg.OMDBAPIKey != "" {
		manager.SetMetaResolver(services.NewOmdbResolver(webClient, cfg.OMDBAPIKey, cfg.OMDBAPIURL))
	}
	if err := manager.Init(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "init indexers: %v\n", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              net.JoinHostPort(bind, fmt.Sprintf("%d", cfg.Port)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	shutdownFn := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("graceful shutdown failed", "error", err)
		}
	}

	handler := server.New(&server.App{
		Config:   cfgService,
		Security: security,
		Ring:     ring,
		Manager:  manager,
		Cache:    cache,
		Version:  vesttVersion,
		Start:    start,
		Shutdown: shutdownFn,
	})
	srv.Handler = handler

	slog.Info("Vestt starting", "version", vesttVersion, "addr", srv.Addr,
		"base_path", cfg.BasePathOverride, "data_dir", dataDir, "log_level", logLevel)
	if cfg.AllowExternal {
		slog.Warn("external access enabled: server bound to all interfaces; consider setting an admin password")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case sig := <-sigCh:
		slog.Info("shutting down", "signal", sig.String())
		shutdownFn()
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}
}
