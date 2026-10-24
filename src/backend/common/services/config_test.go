package services

import (
	"context"
	"testing"

	"vestt/common/store"
)

func newTestConfig(t *testing.T) *ConfigService {
	t.Helper()
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	cs := NewConfigService(s, "0.1.0-test")
	if err := cs.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	return cs
}

func TestLoadDefaultsAndFirstRun(t *testing.T) {
	cs := newTestConfig(t)
	cfg := cs.Config()
	if cfg.Port != 9117 {
		t.Fatalf("default port: %d", cfg.Port)
	}
	if cfg.CacheTTL != 2100 {
		t.Fatalf("default cache_ttl: %d", cfg.CacheTTL)
	}
	if cfg.CacheMaxResultsPerIndexer != 1000 {
		t.Fatalf("default cache_max: %d", cfg.CacheMaxResultsPerIndexer)
	}
	if cfg.FlareSolverrMaxTimeout != 60000 {
		t.Fatalf("default flaresolverr_maxtimeout: %d", cfg.FlareSolverrMaxTimeout)
	}
	if cfg.APIKey == "" || cfg.InstanceID == "" {
		t.Fatal("first run must generate api_key and instance_id")
	}
	apiKey := cfg.APIKey

	// reload from the same underlying store must not regenerate values.
	cs2 := NewConfigService(cs.store, "0.1.0-test")
	if err := cs2.Load(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := cs2.Config().APIKey; got != apiKey {
		t.Fatal("api_key must persist across reloads")
	}
}

func TestDTOMaskAndShape(t *testing.T) {
	cs := newTestConfig(t)
	dto := cs.DTO()
	if dto.Password != "" {
		t.Fatalf("unset password should be empty, got %q", dto.Password)
	}
	if dto.ProxyType != "-1" {
		t.Fatalf("default proxy_type should be \"-1\", got %q", dto.ProxyType)
	}
	if dto.AppVersion != "0.1.0-test" || !dto.CanRunNetCore {
		t.Fatalf("version/can_run_netcore mismatch: %+v", dto)
	}
	if dto.Notices == nil {
		t.Fatal("notices must be [] not null")
	}

	if err := cs.SetAdminPasswordHash(context.Background(), "hashvalue"); err != nil {
		t.Fatalf("set hash: %v", err)
	}
	if dto := cs.DTO(); dto.Password != PasswordMask {
		t.Fatalf("masked password expected, got %q", dto.Password)
	}
}

func boolPtr(b bool) *bool    { return &b }
func intPtr(i int) *int       { return &i }
func strPtr(s string) *string { return &s }

func TestApplyValidationMatrix(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		update  *ConfigUpdate
		wantErr string
	}{
		{"valid port change", &ConfigUpdate{Port: intPtr(9118)}, ""},
		{"port zero", &ConfigUpdate{Port: intPtr(0)}, "The port you have selected is invalid, it must be between 1 and 65535."},
		{"port too large", &ConfigUpdate{Port: intPtr(70000)}, "The port you have selected is invalid, it must be between 1 and 65535."},
		{"restricted port 22", &ConfigUpdate{Port: intPtr(22)}, "The port you have selected is restricted, try a different one."},
		{"restricted port 6667", &ConfigUpdate{Port: intPtr(6667)}, "The port you have selected is restricted, try a different one."},
		{"bind empty", &ConfigUpdate{LocalBindAddress: strPtr("")}, "You need to provide a local bind address."},
		{"bind not loopback", &ConfigUpdate{LocalBindAddress: strPtr("192.168.1.10")}, "The local address you selected is not a loopback one."},
		{"bind loopback ok", &ConfigUpdate{LocalBindAddress: strPtr("::1")}, ""},
		{"basepath no slash", &ConfigUpdate{BasePathOverride: strPtr("jackett")}, "The Base Path Override must start with a /"},
		{"basepath ok", &ConfigUpdate{BasePathOverride: strPtr("/jackett/")}, ""},
		{"baseurl invalid", &ConfigUpdate{BaseURLOverride: strPtr("notaurl")}, "Base URL Override is invalid. Example: http://jackett:9117"},
		{"baseurl ftp", &ConfigUpdate{BaseURLOverride: strPtr("ftp://x/")}, "Base URL Override is invalid. Example: http://jackett:9117"},
		{"baseurl ok", &ConfigUpdate{BaseURLOverride: strPtr("http://jackett:9117/")}, ""},
		{"flaresolverr invalid", &ConfigUpdate{FlareSolverrURL: strPtr("javascript:x")}, "FlareSolverr API URL is invalid. Example: http://127.0.0.1:8191"},
		{"flaresolverr timeout low", &ConfigUpdate{FlareSolverrMaxTimeout: intPtr(4999)}, "FlareSolverr Max Timeout must be greater than 5000 ms."},
		{"flaresolverr timeout ok", &ConfigUpdate{FlareSolverrMaxTimeout: intPtr(5000)}, ""},
		{"proxy port zero with url", &ConfigUpdate{ProxyURL: strPtr("http://proxy"), ProxyPort: intPtr(0)}, "The port you have selected is invalid, it must be between 1 and 65535."},
		{"proxy ok", &ConfigUpdate{ProxyURL: strPtr("http://proxy"), ProxyPort: intPtr(8080), ProxyType: intPtr(0)}, ""},
		{"blackhole missing", &ConfigUpdate{BlackholeDir: strPtr("/nonexistent-dir-xyz")}, "Blackhole directory does not exist"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := newTestConfig(t)
			dto, err := cs.Apply(ctx, tc.update)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if dto == nil {
					t.Fatal("expected dto")
				}
				return
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("expected ValidationError, got %T %v", err, err)
			}
			if ve.Msg != tc.wantErr {
				t.Fatalf("message = %q, want %q", ve.Msg, tc.wantErr)
			}
		})
	}
}

func TestApplyRestartRequired(t *testing.T) {
	ctx := context.Background()
	cs := newTestConfig(t)

	dto, err := cs.Apply(ctx, &ConfigUpdate{Port: intPtr(9118)})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !dto.RestartRequired || !cs.RestartRequired() {
		t.Fatal("port change must set restart_required")
	}
	if got := cs.Config().Port; got != 9118 {
		t.Fatalf("port not persisted: %d", got)
	}

	// immediate-effect change: baseurloverride must NOT set restart flag by itself
	cs2 := newTestConfig(t)
	dto, err = cs2.Apply(ctx, &ConfigUpdate{BaseURLOverride: strPtr("http://x:9117")})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if dto.RestartRequired {
		t.Fatal("baseurloverride should not require restart")
	}
}

func TestApplyProxyURLForcesDisabled(t *testing.T) {
	ctx := context.Background()
	cs := newTestConfig(t)
	dto, err := cs.Apply(ctx, &ConfigUpdate{
		ProxyType: intPtr(2),
		ProxyURL:  strPtr(""),
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if dto.ProxyType != "-1" {
		t.Fatalf("empty proxy url must force disabled (-1), got %q", dto.ProxyType)
	}
	if dto.ProxyPort != nil {
		t.Fatalf("proxy_port should stay null, got %v", *dto.ProxyPort)
	}
}
