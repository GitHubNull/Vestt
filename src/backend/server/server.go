// Package server wires the HTTP layer (chi router assembly).
//
// Ported from: Jackett.Server/Startup.cs — middleware ordering:
// Exception → BasePath → Static → Authentication → MVC/CORS.
package server

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"vestt/common/services"
	"vestt/common/utils"
	"vestt/common/utils/logging"
	"vestt/server/api"
	"vestt/server/middleware"
	"vestt/server/static"
)

// App carries everything the HTTP layer needs.
type App struct {
	Config   *services.ConfigService
	Security *services.SecurityService
	Ring     *logging.Ring
	Manager  *services.IndexerManagerService
	Cache    *services.CacheService
	Version  string
	Start    time.Time
	Shutdown func()
}

// New builds the chi router with the Startup.cs-equivalent pipeline.
func New(app *App) http.Handler {
	deps := &middleware.Deps{Config: app.Config, Security: app.Security}
	basePath := strings.TrimSuffix(app.Config.Config().BasePathOverride, "/")

	r := chi.NewRouter()
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(basePathStrip(basePath))

	// CORS: upstream allows everything when allow_cors is set.
	if app.Config.Config().AllowCORS {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "*")
				if req.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, req)
			})
		})
	}

	// debug-level request logging (sanitised)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			slog.Debug("http request", "method", req.Method, "url", utils.Sanitize(req.URL.String()))
			next.ServeHTTP(w, req)
		})
	})

	ui := &api.UIHandlers{Config: app.Config, Security: app.Security, Static: static.Dist}
	cfg := &api.ServerConfigHandlers{
		Config:          app.Config,
		Security:        app.Security,
		Ring:            app.Ring,
		TriggerShutdown: app.Shutdown,
	}
	results := &api.ResultsHandlers{Config: app.Config, Manager: app.Manager, Logger: slog.Default()}
	indexers := &api.IndexerHandlers{
		Config:  app.Config,
		Manager: app.Manager,
		Cache:   app.Cache,
		Logger:  slog.Default(),
	}
	proxy := &api.ProxyHandlers{Config: app.Config, Manager: app.Manager, Logger: slog.Default()}

	// Anonymous routes.
	r.Get("/health", api.Health(app.Version, app.Start))
	r.Get("/UI/Login", ui.Login)
	r.Get("/UI/TestCookie", ui.TestCookie)
	r.Post("/UI/Dashboard", ui.DashboardPost)
	r.Get("/UI/Logout", ui.Logout)
	r.Handle("/assets/*", ui.StaticFiles())
	// The SPA is served at /UI/Dashboard with relative asset URLs (vite
	// base './'), which resolve to /UI/assets/*.
	r.Handle("/UI/assets/*", http.StripPrefix("/UI", ui.StaticFiles()))
	r.Handle("/UI/favicon.svg", http.StripPrefix("/UI", ui.StaticFiles()))
	r.Handle("/favicon.svg", ui.StaticFiles())

	// Redirect root to the dashboard (upstream behaviour).
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, basePath+"/UI/Dashboard", http.StatusFound)
	})

	// /api/v2.0: result endpoints are anonymous (API key in the query
	// string); the indexer admin API is session-guarded (upstream
	// [Authorize]).
	results.Routes(r)
	r.Route("/api/v2.0", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireSession(deps))
			r.Route("/indexers", indexers.Routes)
			r.Route("/server", func(r chi.Router) {
				r.Get("/config", cfg.GetConfig)
				r.Post("/config", cfg.UpdateConfig)
				r.Post("/adminpassword", cfg.SetAdminPassword)
				r.Get("/logs", cfg.Logs)
				r.Post("/update", cfg.Update)
				r.Post("/shutdown", middleware.RequireApiKey(deps)(http.HandlerFunc(cfg.Shutdown)).ServeHTTP)
			})
		})
	})

	// UI dashboard (session check inside the handler for redirect semantics).
	r.Get("/UI/Dashboard", ui.DashboardGet)

	// Anonymous download proxies (upstream Download/Image/Blackhole
	// controllers, API key in the query string).
	proxy.Routes(r)

	return r
}

// basePathStrip removes the configured base path prefix so internal routes
// stay unprefixed (AGENTS.md §7 basePath rule).
func basePathStrip(basePath string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if basePath != "" && strings.HasPrefix(r.URL.Path, basePath+"/") {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, basePath)
			} else if basePath != "" && r.URL.Path == basePath {
				r.URL.Path = "/"
			}
			next.ServeHTTP(w, r)
		})
	}
}
