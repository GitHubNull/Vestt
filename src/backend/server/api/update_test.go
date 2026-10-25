package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vestt/common/services"
	"vestt/common/store"
)

// newUpdateTestHandlers builds handlers over an in-memory store with the
// given injected build version.
func newUpdateTestHandlers(t *testing.T, version string) (*ServerConfigHandlers, context.Context) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	cfg := services.NewConfigService(st, version)
	if err := cfg.Load(ctx); err != nil {
		t.Fatalf("load config: %v", err)
	}
	return &ServerConfigHandlers{Config: cfg}, ctx
}

// captureLogs redirects the default slog logger into a buffer for the
// duration of the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// setUpdateURL swaps the injectable GitHub endpoint for the test.
func setUpdateURL(t *testing.T, url string) {
	t.Helper()
	prev := updateAPIURL
	updateAPIURL = url
	t.Cleanup(func() { updateAPIURL = prev })
}

// fakeGitHub serves a canned /releases/latest response and records the
// User-Agent it received.
func fakeGitHub(t *testing.T, status int, body string, seenUA *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seenUA != nil {
			*seenUA = r.Header.Get("User-Agent")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runUpdate(t *testing.T, h *ServerConfigHandlers) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v2.0/server/update", nil)
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	return rec
}

func getConfigBody(t *testing.T, h *ServerConfigHandlers) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2.0/server/config", nil)
	rec := httptest.NewRecorder()
	h.GetConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET config status = %d, want 200", rec.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode config response: %v", err)
	}
	return m
}

func TestSemverCompare(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		{"patch bump", "1.2.3", "1.2.4", -1},
		{"minor bump", "1.2.3", "1.3.0", -1},
		{"major bump", "1.2.3", "2.0.0", -1},
		{"equal", "1.2.3", "1.2.3", 0},
		{"v prefix equal", "v1.2.3", "1.2.3", 0},
		{"v prefix less", "v0.1.0", "v0.2.0", -1},
		{"reverse direction", "0.2.0", "0.1.0", 1},
		{"prerelease below release", "1.0.0-rc.1", "1.0.0", -1},
		{"release above prerelease", "1.0.0", "1.0.0-rc.1", 1},
		{"dev release below its release", "0.1.0-dev", "0.1.0", -1},
		{"prerelease numeric growth", "1.0.0-alpha.1", "1.0.0-alpha.2", -1},
		{"prerelease numeric not lexical", "1.0.0-alpha.2", "1.0.0-alpha.11", -1},
		{"prerelease longer set wins", "1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"numeric below alphanumeric", "1.0.0-1", "1.0.0-alpha", -1},
		{"ascii order", "1.0.0-alpha", "1.0.0-beta", -1},
		{"dev below dirty", "1.0.0-dev", "1.0.0-dirty", -1},
		{"build metadata ignored", "1.0.0+build.42", "1.0.0", 0},
		{"build metadata ignored both", "1.0.0+a.b", "1.0.0+c.d", 0},
		{"dev to next minor", "0.1.0-dev", "0.2.0", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := semverCompare(tc.a, tc.b)
			if err != nil {
				t.Fatalf("semverCompare(%q, %q): %v", tc.a, tc.b, err)
			}
			if got != tc.want {
				t.Fatalf("semverCompare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
	for _, s := range []string{"", "abc", "1.2", "1.2.3.4", "x.y.z", "1.2.x"} {
		if _, err := semverCompare(s, "1.0.0"); err == nil {
			t.Errorf("semverCompare(%q, \"1.0.0\") expected an error", s)
		}
	}
}

func TestUpdateNewRelease(t *testing.T) {
	h, ctx := newUpdateTestHandlers(t, "0.1.0-dev")
	logs := captureLogs(t)
	var gotUA string
	srv := fakeGitHub(t, http.StatusOK,
		`{"tag_name":"v0.2.0","html_url":"https://github.com/GitHubNull/Vestt/releases/tag/v0.2.0"}`,
		&gotUA)
	setUpdateURL(t, srv.URL)

	rec := runUpdate(t, h)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST update status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("POST update body = %q, want empty", rec.Body.String())
	}
	if gotUA != "vestt/0.1.0-dev" {
		t.Fatalf("GitHub request User-Agent = %q, want vestt/0.1.0-dev", gotUA)
	}

	// saved_state.update.last_check holds an RFC3339 UTC timestamp.
	raw, ok, err := h.Config.State(ctx, stateKeyLastCheck)
	if err != nil || !ok {
		t.Fatalf("last_check state: ok=%v err=%v", ok, err)
	}
	var ts string
	if err := json.Unmarshal([]byte(raw), &ts); err != nil {
		t.Fatalf("last_check is not a JSON string: %v (%s)", err, raw)
	}
	when, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("last_check is not RFC3339: %v", err)
	}
	if d := time.Since(when); d < -time.Minute || d > time.Minute {
		t.Fatalf("last_check timestamp off by %v", d)
	}

	// saved_state.update.latest_version holds the raw tag.
	tagRaw, ok, err := h.Config.State(ctx, stateKeyLatestVersion)
	if err != nil || !ok {
		t.Fatalf("latest_version state: ok=%v err=%v", ok, err)
	}
	var tag string
	if err := json.Unmarshal([]byte(tagRaw), &tag); err != nil {
		t.Fatalf("latest_version is not a JSON string: %v (%s)", err, tagRaw)
	}
	if tag != "v0.2.0" {
		t.Fatalf("latest_version = %q, want v0.2.0", tag)
	}

	if !strings.Contains(logs.String(), "New release found") {
		t.Fatalf("log missing new-release notice:\n%s", logs.String())
	}

	// GET /server/config now carries latest_version.
	m := getConfigBody(t, h)
	if m["latest_version"] != "v0.2.0" {
		t.Fatalf("config latest_version = %v, want v0.2.0", m["latest_version"])
	}
	if m["app_version"] != "0.1.0-dev" {
		t.Fatalf("config app_version = %v, want 0.1.0-dev", m["app_version"])
	}
}

func TestUpdateNoNewRelease(t *testing.T) {
	h, _ := newUpdateTestHandlers(t, "0.1.0-dev")
	logs := captureLogs(t)
	setUpdateURL(t, fakeGitHub(t, http.StatusOK, `{"tag_name":"v0.0.1"}`, nil).URL)

	rec := runUpdate(t, h)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST update status = %d, want 204", rec.Code)
	}

	// The tag is recorded even without an update; no new-release log.
	m := getConfigBody(t, h)
	if m["latest_version"] != "v0.0.1" {
		t.Fatalf("config latest_version = %v, want v0.0.1", m["latest_version"])
	}
	if strings.Contains(logs.String(), "New release found") {
		t.Fatalf("unexpected new-release log:\n%s", logs.String())
	}
}

func TestUpdateNetworkFailureSilent(t *testing.T) {
	cases := []struct {
		name   string
		status int // 0 = already-closed port (connection refused)
		body   string
	}{
		{"connection refused", 0, ""},
		{"http 500", http.StatusInternalServerError, `{"tag_name":"v9.9.9"}`},
		{"malformed body", http.StatusOK, `<html>not json</html>`},
		{"missing tag_name", http.StatusOK, `{"name":"nope"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, ctx := newUpdateTestHandlers(t, "0.1.0-dev")
			logs := captureLogs(t)
			if tc.status == 0 {
				dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
				dead.Close()
				setUpdateURL(t, dead.URL)
			} else {
				setUpdateURL(t, fakeGitHub(t, tc.status, tc.body, nil).URL)
			}

			// Silent failure: still the §1.5 success semantics (204, no body).
			rec := runUpdate(t, h)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("POST update status = %d, want 204 (silent failure)", rec.Code)
			}
			if rec.Body.Len() != 0 {
				t.Fatalf("POST update body = %q, want empty", rec.Body.String())
			}

			// saved_state stays untouched on failure.
			for _, key := range []string{stateKeyLastCheck, stateKeyLatestVersion} {
				if _, ok, err := h.Config.State(ctx, key); ok || err != nil {
					t.Fatalf("state %q should be untouched (ok=%v err=%v)", key, ok, err)
				}
			}

			if !strings.Contains(logs.String(), "update check failed") {
				t.Fatalf("log missing failure warn:\n%s", logs.String())
			}
			if strings.Contains(logs.String(), "New release found") {
				t.Fatalf("unexpected new-release log:\n%s", logs.String())
			}

			// GET /server/config keeps omitting latest_version.
			m := getConfigBody(t, h)
			if _, has := m["latest_version"]; has {
				t.Fatalf("config should not carry latest_version after a failed check: %v", m["latest_version"])
			}
		})
	}
}

func TestConfigLatestVersionOmitted(t *testing.T) {
	h, _ := newUpdateTestHandlers(t, "0.1.0-dev")
	m := getConfigBody(t, h)
	if _, has := m["latest_version"]; has {
		t.Fatalf("config should omit latest_version before the first check: %v", m["latest_version"])
	}
}
