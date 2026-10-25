package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"vestt/common/httpclient"
	"vestt/common/store"
)

// writeFixture builds a Jackett data directory in src:
//   - ServerConfig.json with every mappable field plus unknown/computed
//     fields that must be skipped;
//   - Indexers/filelist.json (hand-written indexer form, incl. a masked
//     password sentinel and a cookieheader);
//   - Indexers/0magnet.json (cardigann definition form, plaintext password,
//     cookieheader with an expired sentinel and a malformed pair);
//   - Indexers/unknowntracker.json (unknown id, must be skipped);
//   - Indexers/filelist.json.bak (backup sibling, must be ignored).
func writeFixture(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	files := map[string]string{
		"ServerConfig.json": `{
  "Port": 9118,
  "LocalBindAddress": "127.0.0.1",
  "AllowExternal": true,
  "AllowCORS": true,
  "APIKey": "jackett-api-key-0123456789abcdef",
  "AdminPassword": "hunter2",
  "InstanceId": "inst-0123456789abcdef0123456789abcdef",
  "BlackholeDir": "/tmp/blackhole",
  "UpdateDisabled": true,
  "UpdatePrerelease": true,
  "BasePathOverride": "/jackett",
  "BaseUrlOverride": "http://example.com/jackett",
  "CacheEnabled": false,
  "CacheTtl": 1234,
  "CacheMaxResultsPerIndexer": 500,
  "FlareSolverrUrl": "http://127.0.0.1:8191/v1",
  "FlareSolverrMaxTimeout": 55000,
  "OmdbApiKey": "omdb-key",
  "OmdbApiUrl": "https://omdb.example.com/",
  "ProxyType": 2,
  "ProxyUrl": "proxy.example.com",
  "ProxyPort": 1080,
  "ProxyUsername": "puser",
  "ProxyPassword": "ppass",
  "ProxyIsAnonymous": false,
  "SomeFutureField": {"nested": true}
}`,
		"Indexers/filelist.json": `[
{"id":"sitelink","type":"inputstring","name":"Site Link","value":"https://filelist.io/"},
{"id":"username","type":"inputstring","name":"Username","value":"user1"},
{"id":"passkey","type":"inputstring","name":"Passkey","value":"pk123"},
{"id":"password","type":"inputstring","name":"Password","value":"|||%%PREVJACKPASSWD%%|||"},
{"id":"cookieheader","type":"hiddendata","name":"CookieHeader","value":"uid=42; pass=deadbeef"}
]`,
		"Indexers/0magnet.json": `[
{"id":"sitelink","type":"inputstring","name":"Site Link","value":"https://16mag.net/"},
{"id":"password","type":"password","name":"Password","value":"realpw"},
{"id":"cookieheader","type":"hiddendata","name":"CookieHeader","value":"cf_clearance=abc; sid=__jackett_expired__; malformed"}
]`,
		"Indexers/unknowntracker.json": `[
{"id":"sitelink","type":"inputstring","name":"Site Link","value":"https://unknown.example/"},
{"id":"username","type":"inputstring","name":"Username","value":"u"}
]`,
		"Indexers/filelist.json.bak": `[]`,
	}
	for name, content := range files {
		path := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return src
}

// runImport executes run() and fails the test on a non-zero exit code.
func runImport(t *testing.T, args []string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := run(context.Background(), args, &out, io.Discard)
	return out.String(), code
}

// configItems parses a migrated configuration array into an id-keyed map.
func configItems(t *testing.T, configJSON string) map[string]map[string]any {
	t.Helper()
	var items []map[string]any
	if err := json.Unmarshal([]byte(configJSON), &items); err != nil {
		t.Fatalf("parse config_json: %v\n%s", err, configJSON)
	}
	out := make(map[string]map[string]any, len(items))
	for _, item := range items {
		id, _ := item["id"].(string)
		out[id] = item
	}
	return out
}

func TestImportEndToEnd(t *testing.T) {
	src := writeFixture(t)
	outDir := filepath.Join(t.TempDir(), "vestt-data") // --out must be created

	report, code := runImport(t, []string{"--src", src, "--out", outDir})
	if code != 0 {
		t.Fatalf("run exit code = %d, want 0\nreport:\n%s", code, report)
	}

	// report: counts, imported list, skip list line by line
	for _, want := range []string{
		"output: " + outDir + " (created)",
		"builtin indexers: ",
		"server config: imported",
		"note: AdminPassword imported as a bcrypt hash into admin_password_hash",
		"indexers: imported 2, skipped 1",
		"imported:\n  - 0magnet\n  - filelist",
		"skipped:\n  - unknowntracker: no matching built-in indexer",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q\ngot:\n%s", want, report)
		}
	}

	st, err := store.Open(filepath.Join(outDir, "vestt.db"))
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer func() { _ = st.Close() }()

	// server_config row, field by field
	ctx := context.Background()
	cfg, err := st.GetServerConfig(ctx)
	if err != nil {
		t.Fatalf("GetServerConfig: %v", err)
	}
	if cfg.Port != 9118 {
		t.Errorf("Port = %d, want 9118", cfg.Port)
	}
	if cfg.LocalBindAddress != "127.0.0.1" || !cfg.AllowExternal || !cfg.AllowCORS {
		t.Errorf("bind/external/cors = %q/%v/%v", cfg.LocalBindAddress, cfg.AllowExternal, cfg.AllowCORS)
	}
	if cfg.APIKey != "jackett-api-key-0123456789abcdef" {
		t.Errorf("APIKey = %q, want the imported key preserved", cfg.APIKey)
	}
	if cfg.AdminPasswordHash == "" || cfg.AdminPasswordHash == "hunter2" {
		t.Errorf("AdminPasswordHash must be a bcrypt hash, got %q", cfg.AdminPasswordHash)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(cfg.AdminPasswordHash), []byte("hunter2")); err != nil {
		t.Errorf("AdminPasswordHash does not match the source password: %v", err)
	}
	if cfg.InstanceID != "inst-0123456789abcdef0123456789abcdef" {
		t.Errorf("InstanceID = %q", cfg.InstanceID)
	}
	if cfg.BlackholeDir != "/tmp/blackhole" {
		t.Errorf("BlackholeDir = %q", cfg.BlackholeDir)
	}
	if !cfg.UpdateDisabled || !cfg.UpdatePrerelease {
		t.Errorf("UpdateDisabled/UpdatePrerelease = %v/%v, want true/true", cfg.UpdateDisabled, cfg.UpdatePrerelease)
	}
	if cfg.BasePathOverride != "/jackett" || cfg.BaseURLOverride != "http://example.com/jackett" {
		t.Errorf("BasePathOverride/BaseURLOverride = %q/%q", cfg.BasePathOverride, cfg.BaseURLOverride)
	}
	if cfg.CacheEnabled {
		t.Error("CacheEnabled = true, want false (imported value)")
	}
	if cfg.CacheTTL != 1234 || cfg.CacheMaxResultsPerIndexer != 500 {
		t.Errorf("CacheTTL/CacheMaxResultsPerIndexer = %d/%d, want 1234/500", cfg.CacheTTL, cfg.CacheMaxResultsPerIndexer)
	}
	if cfg.FlareSolverrURL != "http://127.0.0.1:8191/v1" || cfg.FlareSolverrMaxTimeout != 55000 {
		t.Errorf("FlareSolverr = %q/%d", cfg.FlareSolverrURL, cfg.FlareSolverrMaxTimeout)
	}
	if cfg.OMDBAPIKey != "omdb-key" || cfg.OMDBAPIURL != "https://omdb.example.com/" {
		t.Errorf("OMDB = %q/%q", cfg.OMDBAPIKey, cfg.OMDBAPIURL)
	}
	if cfg.ProxyType != 2 || cfg.ProxyURL != "proxy.example.com" || cfg.ProxyUsername != "puser" || cfg.ProxyPassword != "ppass" {
		t.Errorf("proxy = %d/%q/%q/%q", cfg.ProxyType, cfg.ProxyURL, cfg.ProxyUsername, cfg.ProxyPassword)
	}
	if cfg.ProxyPort == nil || *cfg.ProxyPort != 1080 {
		t.Errorf("ProxyPort = %v, want 1080", cfg.ProxyPort)
	}

	// filelist: verbatim config values + mask normalization + cookies
	row, err := st.GetIndexerConfig(ctx, "filelist")
	if err != nil {
		t.Fatalf("GetIndexerConfig(filelist): %v", err)
	}
	if !row.Enabled || row.State != "unconfigured" {
		t.Errorf("filelist Enabled/State = %v/%q, want true/unconfigured", row.Enabled, row.State)
	}
	items := configItems(t, row.ConfigJSON)
	if len(items) != 5 {
		t.Errorf("filelist config items = %d, want 5 (all items preserved)\n%s", len(items), row.ConfigJSON)
	}
	if got := items["username"]["value"]; got != "user1" {
		t.Errorf("filelist username = %v, want user1", got)
	}
	if got := items["passkey"]["value"]; got != "pk123" {
		t.Errorf("filelist passkey = %v, want pk123", got)
	}
	if got := items["sitelink"]["value"]; got != "https://filelist.io/" {
		t.Errorf("filelist sitelink = %v", got)
	}
	if got := items["password"]["value"]; got != "" {
		t.Errorf("filelist password = %v, want \"\" (display sentinel must not be imported)", got)
	}
	if got := items["cookieheader"]["value"]; got != "uid=42; pass=deadbeef" {
		t.Errorf("filelist cookieheader item = %v", got)
	}
	var jar []httpclient.PersistedCookie
	if err := json.Unmarshal([]byte(row.CookiesJSON), &jar); err != nil {
		t.Fatalf("parse filelist cookies_json: %v\n%s", err, row.CookiesJSON)
	}
	if len(jar) != 2 {
		t.Fatalf("filelist cookies = %d entries, want 2\n%s", len(jar), row.CookiesJSON)
	}
	wantCookies := map[string]string{"uid": "42", "pass": "deadbeef"}
	for _, c := range jar {
		if c.Domain != "filelist.io" || c.Path != "/" || wantCookies[c.Name] != c.Value {
			t.Errorf("filelist cookie %+v, want name/value from %v with domain filelist.io", c, wantCookies)
		}
	}

	// 0magnet: cardigann form, plaintext password preserved, expired
	// sentinel + malformed pair dropped from the cookie jar
	row, err = st.GetIndexerConfig(ctx, "0magnet")
	if err != nil {
		t.Fatalf("GetIndexerConfig(0magnet): %v", err)
	}
	items = configItems(t, row.ConfigJSON)
	if len(items) != 3 {
		t.Errorf("0magnet config items = %d, want 3\n%s", len(items), row.ConfigJSON)
	}
	if got := items["password"]["value"]; got != "realpw" {
		t.Errorf("0magnet password = %v, want realpw (plaintext must be kept)", got)
	}
	jar = nil
	if err := json.Unmarshal([]byte(row.CookiesJSON), &jar); err != nil {
		t.Fatalf("parse 0magnet cookies_json: %v\n%s", err, row.CookiesJSON)
	}
	if len(jar) != 1 || jar[0].Name != "cf_clearance" || jar[0].Value != "abc" || jar[0].Domain != "16mag.net" {
		t.Errorf("0magnet cookies = %+v, want [cf_clearance=abc @ 16mag.net]", jar)
	}

	// unknowntracker skipped, backup sibling ignored
	if _, err := st.GetIndexerConfig(ctx, "unknowntracker"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetIndexerConfig(unknowntracker) = %v, want sql.ErrNoRows", err)
	}
	rows, err := st.ListIndexerConfigs(ctx)
	if err != nil {
		t.Fatalf("ListIndexerConfigs: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("indexer_config rows = %d, want 2 (filelist, 0magnet)", len(rows))
	}
}

func TestImportOverwriteIsIdempotent(t *testing.T) {
	src := writeFixture(t)
	outDir := t.TempDir()

	if _, code := runImport(t, []string{"--src", src, "--out", outDir}); code != 0 {
		t.Fatalf("first run exit code = %d", code)
	}

	// mutate the source, re-run, and expect the changed values to overwrite
	fileListPath := filepath.Join(src, "Indexers", "filelist.json")
	mutated := strings.ReplaceAll(
		readFile(t, fileListPath), `"user1"`, `"user2"`)
	writeFile(t, fileListPath, mutated)
	serverCfgPath := filepath.Join(src, "ServerConfig.json")
	writeFile(t, serverCfgPath,
		strings.ReplaceAll(readFile(t, serverCfgPath), `"Port": 9118`, `"Port": 9999`))

	report, code := runImport(t, []string{"--src", src, "--out", outDir})
	if code != 0 {
		t.Fatalf("second run exit code = %d\nreport:\n%s", code, report)
	}
	if !strings.Contains(report, "indexers: imported 2, skipped 1") {
		t.Errorf("second run counts changed, want imported 2, skipped 1\ngot:\n%s", report)
	}
	if strings.Contains(report, "(created)") {
		t.Errorf("second run must not claim to create the output dir\ngot:\n%s", report)
	}

	st, err := store.Open(filepath.Join(outDir, "vestt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	cfg, err := st.GetServerConfig(ctx)
	if err != nil {
		t.Fatalf("GetServerConfig: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want 9999 (overwritten)", cfg.Port)
	}
	row, err := st.GetIndexerConfig(ctx, "filelist")
	if err != nil {
		t.Fatalf("GetIndexerConfig(filelist): %v", err)
	}
	if got := configItems(t, row.ConfigJSON)["username"]["value"]; got != "user2" {
		t.Errorf("filelist username = %v, want user2 (overwritten)", got)
	}
	rows, err := st.ListIndexerConfigs(ctx)
	if err != nil {
		t.Fatalf("ListIndexerConfigs: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("indexer_config rows = %d, want 2 (no duplicates)", len(rows))
	}
}

func TestImportMissingServerConfig(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "Indexers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "Indexers", "unknowntracker.json"),
		[]byte(`[{"id":"username","type":"inputstring","name":"Username","value":"u"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "nested", "vestt-data")

	report, code := runImport(t, []string{"--src", src, "--out", outDir})
	if code != 0 {
		t.Fatalf("run exit code = %d\nreport:\n%s", code, report)
	}
	if !strings.Contains(report, "note: ServerConfig.json not found in source") {
		t.Errorf("report must note the missing ServerConfig.json\ngot:\n%s", report)
	}
	if !strings.Contains(report, "indexers: imported 0, skipped 1") {
		t.Errorf("report counts wrong\ngot:\n%s", report)
	}

	st, err := store.Open(filepath.Join(outDir, "vestt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()
	// no server_config row written; the server generates one on first start
	if _, err := st.GetServerConfig(context.Background()); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetServerConfig = %v, want sql.ErrNoRows", err)
	}
}

func TestMapServerConfigDefaultsAndSkips(t *testing.T) {
	// unknown/computed fields are ignored; a present-but-empty ProxyUrl
	// forces the proxy type back to disabled (upstream OnDeserialized)
	raw := []byte(`{"ProxyIsAnonymous": true, "SomeFutureField": 1, "ProxyType": 2, "ProxyUrl": "", "AdminPassword": ""}`)
	cfg, notes, err := mapServerConfig(raw)
	if err != nil {
		t.Fatalf("mapServerConfig: %v", err)
	}
	if cfg.ProxyType != -1 {
		t.Errorf("ProxyType = %d, want -1 (empty proxy url disables)", cfg.ProxyType)
	}
	if cfg.Port != 9117 || cfg.CacheTTL != 2100 || cfg.CacheMaxResultsPerIndexer != 1000 || !cfg.CacheEnabled {
		t.Errorf("defaults not preserved: %+v", cfg)
	}
	if cfg.APIKey != "" || cfg.AdminPasswordHash != "" {
		t.Errorf("empty APIKey/AdminPassword must stay empty: %q/%q", cfg.APIKey, cfg.AdminPasswordHash)
	}
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none for an empty AdminPassword", notes)
	}
}

func TestCookiesFromHeader(t *testing.T) {
	testCases := []struct {
		name   string
		header string
		domain string
		want   []httpclient.PersistedCookie
	}{
		{
			name:   "pairs with = inside values",
			header: "a=1; b=2=3",
			domain: "x.io",
			want: []httpclient.PersistedCookie{
				{Name: "a", Value: "1", Domain: "x.io", Path: "/"},
				{Name: "b", Value: "2=3", Domain: "x.io", Path: "/"},
			},
		},
		{
			name:   "expired sentinel and malformed pairs dropped",
			header: "sid=__jackett_expired__; ok=1; malformed",
			domain: "x.io",
			want:   []httpclient.PersistedCookie{{Name: "ok", Value: "1", Domain: "x.io", Path: "/"}},
		},
		{name: "empty header", header: "", domain: "x.io", want: nil},
		{name: "empty domain", header: "a=1", domain: "", want: nil},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := cookiesFromHeader(tc.header, tc.domain)
			if len(got) != len(tc.want) {
				t.Fatalf("cookiesFromHeader(%q) = %+v, want %+v", tc.header, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("cookie[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestParseIndexerConfigOldFormatRejected(t *testing.T) {
	_, err := parseIndexerConfig([]byte(`{"cookies": "a=1"}`))
	if err == nil || !strings.Contains(err.Error(), "old format") {
		t.Errorf("parseIndexerConfig(old format) error = %v, want old-format rejection", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
