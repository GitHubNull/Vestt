package api

import (
	"embed"
	"io/fs"
	"net/http"
	"time"

	"vestt/common/services"
	"vestt/server/middleware"
)

// UIHandlers implements the /UI/* routes (backend-api.md §5), ported from
// Jackett.Server/Controllers/UIController.cs.
type UIHandlers struct {
	Config   *services.ConfigService
	Security *services.SecurityService
	Static   embed.FS // frontend build output (dist)
}

const sessionCookieName = middleware.SessionCookieName

// redirect writes a redirect with a relative Location header, matching
// upstream MVC Redirect("...") semantics (no path absolutisation).
func redirect(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusFound)
}

// setSessionCookie writes the session cookie (14 days, upstream semantics).
func (h *UIHandlers) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int((14 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
}

func (h *UIHandlers) hasSession(r *http.Request) bool {
	return h.Security.ValidateSession(r.Context(), middlewareSessionToken(r))
}

func middlewareSessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// serveIndex serves the embedded SPA index.html (SPEC-11 replaces the
// placeholder page inside the frontend; the serving path stays identical).
func (h *UIHandlers) serveIndex(w http.ResponseWriter) {
	data, err := h.Static.ReadFile("dist/index.html")
	if err != nil {
		http.Error(w, "UI not built", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// loginHTML is the placeholder login page served until SPEC-11 ships the SPA.
const loginHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Vestt Login</title></head>
<body><h1>Vestt</h1>
<form method="POST" action="Dashboard">
  <label>Password <input type="password" name="password" autofocus></label>
  <button type="submit">Login</button>
</form></body></html>`

// Login handles GET /UI/Login (upstream Login action).
func (h *UIHandlers) Login(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("cookiesChecked") == "" {
		http.SetCookie(w, &http.Cookie{
			Name:     "TestCookie",
			Value:    "1",
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteLaxMode,
		})
		redirect(w, "TestCookie")
		return
	}
	// no admin password configured → straight in
	if h.Config.Config().AdminPasswordHash == "" {
		token, err := h.Security.CreateSession(r.Context())
		if err == nil {
			h.setSessionCookie(w, r, token)
		}
		redirect(w, "Dashboard")
		return
	}
	if h.hasSession(r) {
		redirect(w, "Dashboard")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(loginHTML))
}

// TestCookie handles GET /UI/TestCookie (upstream TestCookie action).
func (h *UIHandlers) TestCookie(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie("TestCookie"); err == nil {
		redirect(w, "Login?cookiesChecked=1")
		return
	}
	http.Error(w, "Cookies required", http.StatusBadRequest)
}

// DashboardGet handles GET /UI/Dashboard.
func (h *UIHandlers) DashboardGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("logout") == "true" {
		redirect(w, "Logout")
		return
	}
	// The SPA is served regardless of the management session: the Vue router
	// probes GET server/config and routes to #/login on 401 (SPEC-11 §3.4),
	// which keeps the upstream "unauthenticated lands on a login page"
	// behaviour while making LoginView.vue reachable.
	h.serveIndex(w)
}

// DashboardPost handles POST /UI/Dashboard (form password).
func (h *UIHandlers) DashboardPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	password := r.PostForm.Get("password")
	if h.Security.CheckAuthorised(h.Config.Config().AdminPasswordHash, password) {
		if h.Config.Config().AdminPasswordHash != "" {
			token, err := h.Security.CreateSession(r.Context())
			if err == nil {
				h.setSessionCookie(w, r, token)
			}
		}
	}
	redirect(w, "Dashboard")
}

// Logout handles GET /UI/Logout.
func (h *UIHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	if token := middlewareSessionToken(r); token != "" {
		_ = h.Security.DeleteSession(r.Context(), token)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	redirect(w, "Login")
}

// StaticFiles returns an http handler serving the embedded dist assets
// (js/css/images) for the dashboard page.
func (h *UIHandlers) StaticFiles() http.Handler {
	sub, err := fs.Sub(h.Static, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServerFS(sub)
}
