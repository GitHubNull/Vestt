// Package api contains the HTTP handlers (aligned with Jackett.Server/Controllers).
package api

import (
	"encoding/json"
	"net/http"
	"runtime/debug"
	"time"
)

// HealthResponse matches doc/tech/backend-api.md §6.
type HealthResponse struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	GoVersion     string `json:"go_version"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

// Health returns GET /health (anonymous).
func Health(version string, start time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, _ := debug.ReadBuildInfo()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(HealthResponse{
			Status:        "ok",
			Version:       version,
			GoVersion:     info.GoVersion,
			UptimeSeconds: int64(time.Since(start).Seconds()),
		})
	}
}
