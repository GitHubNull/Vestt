// Package integration contains black-box API integration tests: a real
// server is started with httptest and endpoints are exercised over HTTP.
package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vestt/common/httpclient"
	"vestt/common/services"
	"vestt/common/store"
	"vestt/common/utils/logging"
	"vestt/server"
	"vestt/test/fixtures"
)

type testEnv struct {
	srv      *httptest.Server
	config   *services.ConfigService
	security *services.SecurityService
	manager  *services.IndexerManagerService
	cache    *services.CacheService
	site     *fixtures.Site
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfgService := services.NewConfigService(st, "0.1.0-test")
	if err := cfgService.Load(t.Context()); err != nil {
		t.Fatalf("load config: %v", err)
	}
	security := services.NewSecurityService(st)
	ring, err := logging.Setup("", "info")
	if err != nil {
		t.Fatalf("logging: %v", err)
	}

	// SPEC-07: fake tracker site + fake indexers + full manager/cache wiring.
	site := fixtures.NewSite(t)
	fixtures.Setup(t, site)
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	webClient, err := httpclient.New(httpclient.Config{}, nil, discard)
	if err != nil {
		t.Fatalf("http client: %v", err)
	}
	manager := services.NewIndexerManager(st, webClient, discard)
	cache := services.NewCacheService(st, cfgService, slog.Default())
	manager.SetCache(cache)
	if err := manager.Init(t.Context()); err != nil {
		t.Fatalf("manager init: %v", err)
	}
	t.Cleanup(cache.Close)

	app := &server.App{
		Config:   cfgService,
		Security: security,
		Ring:     ring,
		Manager:  manager,
		Cache:    cache,
		Version:  "0.1.0-test",
		Start:    time.Now(),
	}
	srv := httptest.NewServer(server.New(app))
	t.Cleanup(srv.Close)

	return &testEnv{srv: srv, config: cfgService, security: security, manager: manager, cache: cache, site: site}
}

// apiKey returns the configured API key.
func (e *testEnv) apiKey() string { return e.config.Config().APIKey }

// configureIndexer posts a minimal configuration for one fake indexer.
func (e *testEnv) configureIndexer(t *testing.T, id string, items ...map[string]any) {
	t.Helper()
	if items == nil {
		items = []map[string]any{{"id": "apikey", "value": "test-key"}}
	}
	raw, _ := json.Marshal(items)
	resp := e.post(t, "/api/v2.0/indexers/"+id+"/Config", "application/json", raw)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("configure %s: status %d: %s", id, resp.StatusCode, body)
	}
}

func (e *testEnv) get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := http.Get(e.srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (e *testEnv) post(t *testing.T, path, contentType string, body []byte) *http.Response {
	t.Helper()
	resp, err := http.Post(e.srv.URL+path, contentType, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// TestGetConfigKeySet verifies the response contains every §1.1 key.
func TestGetConfigKeySet(t *testing.T) {
	e := newTestEnv(t)
	resp := e.get(t, "/api/v2.0/server/config")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	wantKeys := []string{
		"notices", "port", "external", "local_bind_address", "cors", "api_key",
		"blackholedir", "updatedisabled", "prerelease", "password", "logging",
		"basepathoverride", "baseurloverride", "cache_enabled", "cache_ttl",
		"cache_max_results_per_indexer", "flaresolverrurl",
		"flaresolverr_maxtimeout", "omdbkey", "omdburl", "app_version",
		"can_run_netcore", "proxy_type", "proxy_url", "proxy_port",
		"proxy_username", "proxy_password",
	}
	for _, k := range wantKeys {
		if _, ok := body[k]; !ok {
			t.Fatalf("missing key %q in config response", k)
		}
	}
	if body["app_version"] != "0.1.0-test" {
		t.Fatalf("app_version mismatch: %v", body["app_version"])
	}
	if body["proxy_type"] != "-1" {
		t.Fatalf("proxy_type should be \"-1\": %v", body["proxy_type"])
	}
	if notices, ok := body["notices"].([]any); !ok || notices == nil {
		t.Fatalf("notices must be an array: %v", body["notices"])
	}
	if key, ok := body["api_key"].(string); !ok || len(key) != 64 {
		t.Fatalf("api_key must be 32B hex: %v", body["api_key"])
	}
}

// TestUpdateConfigRestartRequired: port change → restart_required.
func TestUpdateConfigRestartRequired(t *testing.T) {
	e := newTestEnv(t)
	resp := e.post(t, "/api/v2.0/server/config", "application/json", []byte(`{"port": 9118}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["restart_required"] != true {
		t.Fatalf("restart_required should be true: %v", body)
	}
	if got := e.config.Config().Port; got != 9118 {
		t.Fatalf("port not persisted: %d", got)
	}
}

// TestUpdateConfigValidationError: bad port → 400 with upstream message.
func TestUpdateConfigValidationError(t *testing.T) {
	e := newTestEnv(t)
	resp := e.post(t, "/api/v2.0/server/config", "application/json", []byte(`{"port": 22}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	msg, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(msg), "restricted") {
		t.Fatalf("unexpected error body: %s", msg)
	}
}

// TestAdminPasswordFlow: set password → 401 without session → login via
// /UI/Dashboard → cookie → 200.
func TestAdminPasswordFlow(t *testing.T) {
	e := newTestEnv(t)

	// set admin password
	resp := e.post(t, "/api/v2.0/server/adminpassword", "application/json", []byte(`"s3cret"`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("set password status: %d", resp.StatusCode)
	}

	// admin API now requires a session
	resp2 := e.get(t, "/api/v2.0/server/config")
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}

	// login with wrong password → no session cookie; dashboard redirects to login
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	loginResp, err := client.PostForm(e.srv.URL+"/UI/Dashboard", map[string][]string{"password": {"wrong"}})
	if err != nil {
		t.Fatalf("login post: %v", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("login status: %d", loginResp.StatusCode)
	}
	for _, c := range loginResp.Cookies() {
		if c.Name == "vestt_session" && c.Value != "" {
			t.Fatal("wrong password must not create a session")
		}
	}

	// login with correct password → Set-Cookie
	loginResp2, err := client.PostForm(e.srv.URL+"/UI/Dashboard", map[string][]string{"password": {"s3cret"}})
	if err != nil {
		t.Fatalf("login post: %v", err)
	}
	defer loginResp2.Body.Close()
	var cookie *http.Cookie
	for _, c := range loginResp2.Cookies() {
		if c.Name == "vestt_session" {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("expected session cookie, cookies=%v", loginResp2.Cookies())
	}
	if cookie.HttpOnly != true || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie flags: %+v", cookie)
	}

	// session cookie now authorises admin API
	authReq, _ := http.NewRequest("GET", e.srv.URL+"/api/v2.0/server/config", nil)
	authReq.AddCookie(cookie)
	authResp, err := client.Do(authReq)
	if err != nil {
		t.Fatalf("authed get: %v", err)
	}
	defer authResp.Body.Close()
	if authResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with session, got %d", authResp.StatusCode)
	}
}

// TestShutdownApiKey: correct key → 200 ShuttingDown; wrong key → 401.
func TestShutdownApiKey(t *testing.T) {
	e := newTestEnv(t)
	apiKey := e.config.Config().APIKey

	resp := e.post(t, "/api/v2.0/server/shutdown?apikey=wrong", "application/json", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong apikey: expected 401, got %d", resp.StatusCode)
	}

	// passkey alias must also work
	resp2 := e.post(t, "/api/v2.0/server/shutdown?passkey="+apiKey, "application/json", nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("correct passkey: expected 200, got %d", resp2.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ShuttingDown"] != true {
		t.Fatalf("unexpected body: %v", body)
	}
}

// TestHealthAndUIRedirects covers /health, / → /UI/Dashboard, TestCookie chain.
func TestHealthAndUIRedirects(t *testing.T) {
	e := newTestEnv(t)

	resp := e.get(t, "/health")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status: %d", resp.StatusCode)
	}
	var h map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if h["status"] != "ok" || h["version"] != "0.1.0-test" {
		t.Fatalf("health body: %v", h)
	}

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	rootResp, err := client.Get(e.srv.URL + "/")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	defer rootResp.Body.Close()
	if loc := rootResp.Header.Get("Location"); loc != "/UI/Dashboard" {
		t.Fatalf("root redirect: %q", loc)
	}

	// TestCookie without the probe cookie → 400
	tcResp, err := client.Get(e.srv.URL + "/UI/TestCookie")
	if err != nil {
		t.Fatalf("testcookie: %v", err)
	}
	defer tcResp.Body.Close()
	if tcResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("testcookie status: %d", tcResp.StatusCode)
	}

	// Login redirect chain: /UI/Login → TestCookie → 400 (no cookie support in this client)
	loginResp, err := client.Get(e.srv.URL + "/UI/Login")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer loginResp.Body.Close()
	if loc := loginResp.Header.Get("Location"); loc != "TestCookie" {
		t.Fatalf("login redirect: %q", loc)
	}
}

// TestLogsEndpoint verifies the ring buffer surfaces via /server/logs.
func TestLogsEndpoint(t *testing.T) {
	e := newTestEnv(t)
	resp := e.get(t, "/api/v2.0/server/logs")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logs status: %d", resp.StatusCode)
	}
	var logs []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&logs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if logs == nil {
		t.Fatal("logs must be [] not null")
	}
}
