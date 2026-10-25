package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"vestt/common/services"
	"vestt/common/utils"
	"vestt/common/utils/logging"
)

// ServerConfigHandlers exposes the server configuration API
// (backend-api.md §1).
type ServerConfigHandlers struct {
	Config          *services.ConfigService
	Security        *services.SecurityService
	Ring            *logging.Ring
	TriggerShutdown func() // triggers graceful server shutdown
}

// GetConfig handles GET /api/v2.0/server/config.
func (h *ServerConfigHandlers) GetConfig(w http.ResponseWriter, r *http.Request) {
	dto := h.Config.DTO()
	// latest_version (backend-api.md §1.1, SPEC-14): omitted entirely when
	// no successful update check was recorded yet.
	if latest, ok := h.lastKnownLatestVersion(r.Context()); ok {
		writeJSON(w, http.StatusOK, struct {
			*services.ConfigDTO
			LatestVersion string `json:"latest_version,omitempty"`
		}{ConfigDTO: dto, LatestVersion: latest})
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// lastKnownLatestVersion reads the tag recorded by the last successful
// update check (saved_state.update.latest_version); a corrupt row is
// reported as "never checked" instead of failing the config endpoint.
func (h *ServerConfigHandlers) lastKnownLatestVersion(ctx context.Context) (string, bool) {
	raw, ok, err := h.Config.State(ctx, stateKeyLatestVersion)
	if err != nil {
		slog.Warn("read update latest_version failed", "error", utils.Sanitize(err.Error()))
		return "", false
	}
	if !ok {
		return "", false
	}
	var tag string
	if err := json.Unmarshal([]byte(raw), &tag); err != nil || tag == "" {
		return "", false
	}
	return tag, true
}

// UpdateConfig handles POST /api/v2.0/server/config (§1.2).
func (h *ServerConfigHandlers) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var update services.ConfigUpdate
	if err := json.Unmarshal(body, &update); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	loggingSwitch := update.Logging != nil && *update.Logging != h.Config.LoggingDebug()

	dto, err := h.Config.Apply(r.Context(), &update)
	if err != nil {
		if ve, ok := err.(*services.ValidationError); ok {
			http.Error(w, ve.Msg, http.StatusBadRequest)
			return
		}
		slog.Error("apply config failed", "error", utils.Sanitize(err.Error()))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if loggingSwitch {
		level := "info"
		if dto.Logging {
			level = "debug"
		}
		logging.SetLevel(level)
		slog.Info("log level changed", "level", level)
	}

	writeJSON(w, http.StatusOK, dto)
}

// SetAdminPassword handles POST /api/v2.0/server/adminpassword (§1.3).
// Body is a raw JSON string; empty string or null clears the password.
func (h *ServerConfigHandlers) SetAdminPassword(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var password *string
	trimmed := strings.TrimSpace(string(body))
	if trimmed != "" && trimmed != "null" {
		var s string
		if err := json.Unmarshal(body, &s); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		password = &s
	}

	if password == nil || *password == "" {
		if err := h.Config.SetAdminPasswordHash(r.Context(), ""); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		hash, err := h.Security.HashPassword(*password)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := h.Config.SetAdminPasswordHash(r.Context(), hash); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// force re-login everywhere (upstream equivalent)
	if err := h.Security.DeleteAllSessions(r.Context()); err != nil {
		slog.Error("clear sessions after password change failed", "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// Logs handles GET /api/v2.0/server/logs (§1.4).
func (h *ServerConfigHandlers) Logs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.Ring.List())
}

// Update handles POST /api/v2.0/server/update (§1.5): checks the latest
// GitHub release, records it in saved_state and logs when a newer version
// exists. Self-update is not supported (backend-api.md §8); failures are
// logged, the response stays 204.
func (h *ServerConfigHandlers) Update(w http.ResponseWriter, r *http.Request) {
	h.checkForUpdate(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// Shutdown handles POST /api/v2.0/server/shutdown (§1.6).
func (h *ServerConfigHandlers) Shutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ShuttingDown": true})
	if h.TriggerShutdown != nil {
		go h.TriggerShutdown()
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
