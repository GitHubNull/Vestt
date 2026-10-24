// Package middleware contains API key, CORS, base path, logging and recovery middleware.
package middleware

import (
	"net/http"

	"vestt/common/services"
)

// SessionCookieName is the admin session cookie (backend-api.md §0).
const SessionCookieName = "vestt_session"

// Deps are the service dependencies middleware needs.
type Deps struct {
	Config   *services.ConfigService
	Security *services.SecurityService
}

// sessionToken extracts the session token from the request cookie.
func sessionToken(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// RequireSession guards admin APIs: when no admin password is configured all
// requests pass; otherwise a valid vestt_session cookie is required (401).
func RequireSession(d *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d.Config.Config().AdminPasswordHash == "" {
				next.ServeHTTP(w, r)
				return
			}
			if d.Security.ValidateSession(r.Context(), sessionToken(r)) {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})
	}
}

// RequireApiKey guards endpoints by query-string apikey (alias passkey).
// Used by /server/shutdown now and the result/proxy APIs in SPEC-07.
func RequireApiKey(d *Deps) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.URL.Query().Get("apikey")
			if key == "" {
				key = r.URL.Query().Get("passkey")
			}
			if !d.Config.CheckAPIKey(key) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
