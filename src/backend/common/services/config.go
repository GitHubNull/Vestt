package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"

	"vestt/common/models/config"
	"vestt/common/store"
)

// PasswordMask is returned for a configured admin password (backend-api.md
// §1.1); submitting it back means "do not change".
const PasswordMask = "********"

// ConfigDTO is the API shape of server config (backend-api.md §1.1/§1.2).
// ProxyType serialises as a string (upstream serialises the enum via
// ToString, e.g. "-1").
type ConfigDTO struct {
	Notices                   []string `json:"notices"`
	Port                      int      `json:"port"`
	External                  bool     `json:"external"`
	LocalBindAddress          string   `json:"local_bind_address"`
	CORS                      bool     `json:"cors"`
	APIKey                    string   `json:"api_key"`
	BlackholeDir              string   `json:"blackholedir"`
	UpdateDisabled            bool     `json:"updatedisabled"`
	Prerelease                bool     `json:"prerelease"`
	Password                  string   `json:"password"`
	Logging                   bool     `json:"logging"`
	BasePathOverride          string   `json:"basepathoverride"`
	BaseURLOverride           string   `json:"baseurloverride"`
	CacheEnabled              bool     `json:"cache_enabled"`
	CacheTTL                  int      `json:"cache_ttl"`
	CacheMaxResultsPerIndexer int      `json:"cache_max_results_per_indexer"`
	FlareSolverrURL           string   `json:"flaresolverrurl"`
	FlareSolverrMaxTimeout    int      `json:"flaresolverr_maxtimeout"`
	OMDBKey                   string   `json:"omdbkey"`
	OMDBURL                   string   `json:"omdburl"`
	AppVersion                string   `json:"app_version"`
	CanRunNetCore             bool     `json:"can_run_netcore"`
	ProxyType                 string   `json:"proxy_type"`
	ProxyURL                  string   `json:"proxy_url"`
	ProxyPort                 *int     `json:"proxy_port"`
	ProxyUsername             string   `json:"proxy_username"`
	ProxyPassword             string   `json:"proxy_password"`

	// RestartRequired is only present on POST responses (§1.2).
	RestartRequired bool `json:"restart_required"`
}

// ConfigUpdate is the POST body: every field optional (§1.2 "可仅包含变更字段").
type ConfigUpdate struct {
	Port                      *int    `json:"port"`
	External                  *bool   `json:"external"`
	LocalBindAddress          *string `json:"local_bind_address"`
	CORS                      *bool   `json:"cors"`
	BlackholeDir              *string `json:"blackholedir"`
	UpdateDisabled            *bool   `json:"updatedisabled"`
	Prerelease                *bool   `json:"prerelease"`
	Password                  *string `json:"password"`
	Logging                   *bool   `json:"logging"`
	BasePathOverride          *string `json:"basepathoverride"`
	BaseURLOverride           *string `json:"baseurloverride"`
	CacheEnabled              *bool   `json:"cache_enabled"`
	CacheTTL                  *int    `json:"cache_ttl"`
	CacheMaxResultsPerIndexer *int    `json:"cache_max_results_per_indexer"`
	FlareSolverrURL           *string `json:"flaresolverrurl"`
	FlareSolverrMaxTimeout    *int    `json:"flaresolverr_maxtimeout"`
	OMDBKey                   *string `json:"omdbkey"`
	OMDBURL                   *string `json:"omdburl"`
	ProxyType                 *int    `json:"proxy_type"`
	ProxyURL                  *string `json:"proxy_url"`
	ProxyPort                 *int    `json:"proxy_port"`
	ProxyUsername             *string `json:"proxy_username"`
	ProxyPassword             *string `json:"proxy_password"`
}

// RestrictedPorts mirrors ServerUtil.RestrictedPorts (upstream verbatim,
// including comments preserved in code below).
var RestrictedPorts = []int{
	1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43, 53, 77, 79,
	87, 95, 101, 102, 103, 104, 109, 110, 111, 113, 115, 117, 119, 123, 135,
	139, 143, 179, 389, 465, 512, 513, 514, 515, 526, 530, 531, 532, 540, 556,
	563, 587, 601, 636, 993, 995, 2049, 3659, 4045, 6000, 6665, 6666, 6667,
	6668, 6669,
}

func isRestrictedPort(port int) bool {
	for _, p := range RestrictedPorts {
		if p == port {
			return true
		}
	}
	return false
}

// ConfigService loads and persists ServerConfig (upstream
// ConfigurationService semantics).
type ConfigService struct {
	mu              sync.Mutex
	store           *store.Store
	cfg             *config.ServerConfig
	restartRequired bool
	loggingDebug    bool
	appVersion      string
	initialised     bool
}

// NewConfigService constructs the service; Load must be called before use.
func NewConfigService(s *store.Store, appVersion string) *ConfigService {
	return &ConfigService{store: s, appVersion: appVersion}
}

// Load reads the config from the store, generating API key and instance id
// on first run (upstream first-start behaviour).
func (c *ConfigService) Load(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cfg, err := c.store.GetServerConfig(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		cfg = config.DefaultServerConfig()
		cfg.ProxyType = config.ProxyTypeDisabled
		apiKey, err := NewToken()
		if err != nil {
			return err
		}
		instanceID, err := NewToken()
		if err != nil {
			return err
		}
		cfg.APIKey = apiKey
		cfg.InstanceID = instanceID
		if err := c.store.SaveServerConfig(ctx, cfg); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	c.cfg = cfg
	c.initialised = true
	return nil
}

// Config returns a copy of the current config (no lock held by caller).
func (c *ConfigService) Config() *config.ServerConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *c.cfg
	return &cp
}

// RestartRequired reports whether a setting needing process restart changed.
func (c *ConfigService) RestartRequired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.restartRequired
}

// SetLoggingDebug records the runtime logging-enhanced flag (POST logging).
func (c *ConfigService) SetLoggingDebug(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loggingDebug = v
}

// LoggingDebug reports whether enhanced (debug) logging is active.
func (c *ConfigService) LoggingDebug() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loggingDebug
}

// DTO maps the current config to the API shape. passwordMasked controls the
// password field value.
func (c *ConfigService) DTO() *ConfigDTO {
	cfg := c.Config()
	dto := &ConfigDTO{
		Notices:                   []string{},
		Port:                      cfg.Port,
		External:                  cfg.AllowExternal,
		LocalBindAddress:          cfg.LocalBindAddress,
		CORS:                      cfg.AllowCORS,
		APIKey:                    cfg.APIKey,
		BlackholeDir:              cfg.BlackholeDir,
		UpdateDisabled:            cfg.UpdateDisabled,
		Prerelease:                cfg.UpdatePrerelease,
		Logging:                   c.LoggingDebug(),
		BasePathOverride:          cfg.BasePathOverride,
		BaseURLOverride:           cfg.BaseURLOverride,
		CacheEnabled:              cfg.CacheEnabled,
		CacheTTL:                  cfg.CacheTTL,
		CacheMaxResultsPerIndexer: cfg.CacheMaxResultsPerIndexer,
		FlareSolverrURL:           cfg.FlareSolverrURL,
		FlareSolverrMaxTimeout:    cfg.FlareSolverrMaxTimeout,
		OMDBKey:                   cfg.OMDBAPIKey,
		OMDBURL:                   cfg.OMDBAPIURL,
		AppVersion:                c.appVersion,
		CanRunNetCore:             true,
		ProxyType:                 fmt.Sprintf("%d", cfg.ProxyType),
		ProxyURL:                  cfg.ProxyURL,
		ProxyPort:                 cfg.ProxyPort,
		ProxyUsername:             cfg.ProxyUsername,
		ProxyPassword:             cfg.ProxyPassword,
		RestartRequired:           c.RestartRequired(),
	}
	if cfg.AdminPasswordHash != "" {
		dto.Password = PasswordMask
	} else {
		dto.Password = ""
	}
	return dto
}

// ValidationError carries an upstream-equivalent 400 message.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalidPortErr() *ValidationError {
	return &ValidationError{"The port you have selected is invalid, it must be between 1 and 65535."}
}

func validAbsHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return u.IsAbs() && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// Apply validates and applies an update (upstream UpdateConfig ordering and
// error messages). On success the config is persisted and the DTO returned.
// changedRestart collects whether restart-requiring fields changed.
func (c *ConfigService) Apply(ctx context.Context, u *ConfigUpdate) (*ConfigDTO, error) {
	c.mu.Lock()
	cfg := *c.cfg
	loggingDebug := c.loggingDebug
	c.mu.Unlock()

	restart := false

	// basepathoverride (validated first, upstream order)
	if u.BasePathOverride != nil {
		bp := strings.TrimRight(*u.BasePathOverride, "/")
		if strings.TrimSpace(bp) != "" && !strings.HasPrefix(bp, "/") {
			return nil, &ValidationError{"The Base Path Override must start with a /"}
		}
		if bp != cfg.BasePathOverride {
			restart = true
		}
		cfg.BasePathOverride = bp
	}

	// baseurloverride (immediate)
	if u.BaseURLOverride != nil {
		bu := strings.TrimRight(*u.BaseURLOverride, "/")
		if strings.TrimSpace(bu) == "" {
			bu = ""
		} else if !validAbsHTTPURL(bu) {
			return nil, &ValidationError{"Base URL Override is invalid. Example: http://jackett:9117"}
		}
		cfg.BaseURLOverride = bu
	}

	// cache / cors / update flags (cors → restart)
	if u.CORS != nil {
		if *u.CORS != cfg.AllowCORS {
			restart = true
		}
		cfg.AllowCORS = *u.CORS
	}
	if u.UpdateDisabled != nil {
		cfg.UpdateDisabled = *u.UpdateDisabled
	}
	if u.Prerelease != nil {
		cfg.UpdatePrerelease = *u.Prerelease
	}
	if u.CacheEnabled != nil {
		cfg.CacheEnabled = *u.CacheEnabled
	}
	if u.CacheTTL != nil {
		cfg.CacheTTL = *u.CacheTTL
	}
	if u.CacheMaxResultsPerIndexer != nil {
		cfg.CacheMaxResultsPerIndexer = *u.CacheMaxResultsPerIndexer
	}
	if u.Logging != nil {
		loggingDebug = *u.Logging
	}

	// flaresolverr (restart when changed)
	fsChanged := u.FlareSolverrURL != nil || u.FlareSolverrMaxTimeout != nil
	if fsChanged {
		fsURL := cfg.FlareSolverrURL
		fsTimeout := cfg.FlareSolverrMaxTimeout
		if u.FlareSolverrURL != nil {
			fsURL = *u.FlareSolverrURL
		}
		if u.FlareSolverrMaxTimeout != nil {
			fsTimeout = *u.FlareSolverrMaxTimeout
		}
		if strings.TrimSpace(fsURL) == "" {
			fsURL = ""
		} else if !validAbsHTTPURL(fsURL) {
			return nil, &ValidationError{"FlareSolverr API URL is invalid. Example: http://127.0.0.1:8191"}
		}
		if fsTimeout < 5000 {
			return nil, &ValidationError{"FlareSolverr Max Timeout must be greater than 5000 ms."}
		}
		if fsURL != cfg.FlareSolverrURL || fsTimeout != cfg.FlareSolverrMaxTimeout {
			restart = true
		}
		cfg.FlareSolverrURL = fsURL
		cfg.FlareSolverrMaxTimeout = fsTimeout
	}

	// omdb (immediate)
	if u.OMDBKey != nil {
		cfg.OMDBAPIKey = *u.OMDBKey
	}
	if u.OMDBURL != nil {
		cfg.OMDBAPIURL = strings.TrimRight(*u.OMDBURL, "/")
	}

	// proxy (restart; url empty → forced disabled; clears result cache)
	proxyChanged := u.ProxyType != nil || u.ProxyURL != nil || u.ProxyPort != nil ||
		u.ProxyUsername != nil || u.ProxyPassword != nil
	if proxyChanged {
		pURL := cfg.ProxyURL
		pType := cfg.ProxyType
		pPort := cfg.ProxyPort
		pUser := cfg.ProxyUsername
		pPass := cfg.ProxyPassword
		if u.ProxyURL != nil {
			pURL = *u.ProxyURL
		}
		if u.ProxyType != nil {
			pType = *u.ProxyType
		}
		if u.ProxyPort != nil {
			pPort = u.ProxyPort
		}
		if u.ProxyUsername != nil {
			pUser = *u.ProxyUsername
		}
		if u.ProxyPassword != nil {
			pPass = *u.ProxyPassword
		}
		if strings.TrimSpace(pURL) == "" {
			pType = config.ProxyTypeDisabled
		} else {
			if pPort == nil || *pPort < 1 || *pPort > 65535 {
				return nil, invalidPortErr()
			}
		}
		cfg.ProxyType = pType
		cfg.ProxyURL = pURL
		cfg.ProxyPort = pPort
		cfg.ProxyUsername = pUser
		cfg.ProxyPassword = pPass
		restart = true
		// TODO(SPEC-07): cacheService.CleanCache() once the cache exists.
		slog.Info("proxy config changed; result cache must be cleared")
	}

	// port / external / bind (restart)
	netChanged := u.Port != nil || u.External != nil || u.LocalBindAddress != nil
	if netChanged {
		port := cfg.Port
		external := cfg.AllowExternal
		bind := cfg.LocalBindAddress
		if u.Port != nil {
			port = *u.Port
		}
		if u.External != nil {
			external = *u.External
		}
		if u.LocalBindAddress != nil {
			bind = *u.LocalBindAddress
		}
		if port != cfg.Port || external != cfg.AllowExternal || bind != cfg.LocalBindAddress {
			if isRestrictedPort(port) {
				return nil, &ValidationError{"The port you have selected is restricted, try a different one."}
			}
			if port < 1 || port > 65535 {
				return nil, invalidPortErr()
			}
			if strings.TrimSpace(bind) == "" {
				return nil, &ValidationError{"You need to provide a local bind address."}
			}
			ip := net.ParseIP(bind)
			if ip == nil || !ip.IsLoopback() {
				return nil, &ValidationError{"The local address you selected is not a loopback one."}
			}
			restart = true
		}
		cfg.Port = port
		cfg.AllowExternal = external
		cfg.LocalBindAddress = bind
	}

	// blackholedir (immediate)
	if u.BlackholeDir != nil {
		if *u.BlackholeDir != cfg.BlackholeDir {
			if *u.BlackholeDir != "" {
				if _, err := os.Stat(*u.BlackholeDir); err != nil {
					return nil, &ValidationError{"Blackhole directory does not exist"}
				}
			}
		}
		cfg.BlackholeDir = *u.BlackholeDir
	}

	if err := c.store.SaveServerConfig(ctx, &cfg); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cfg = &cfg
	c.loggingDebug = loggingDebug
	c.restartRequired = c.restartRequired || restart
	c.mu.Unlock()

	return c.DTO(), nil
}

// OverrideRuntime applies command-line flag overrides in memory only; flags
// never write back to the DB (SPEC-02 §3.5). Validation matches Apply.
func (c *ConfigService) OverrideRuntime(port *int, basePath *string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if basePath != nil {
		bp := strings.TrimRight(*basePath, "/")
		if strings.TrimSpace(bp) != "" && !strings.HasPrefix(bp, "/") {
			return &ValidationError{"The Base Path Override must start with a /"}
		}
		cfg := *c.cfg
		cfg.BasePathOverride = bp
		c.cfg = &cfg
	}
	if port != nil {
		if isRestrictedPort(*port) {
			return &ValidationError{"The port you have selected is restricted, try a different one."}
		}
		if *port < 1 || *port > 65535 {
			return invalidPortErr()
		}
		cfg := *c.cfg
		cfg.Port = *port
		c.cfg = &cfg
	}
	return nil
}

// SetAdminPasswordHash stores a new admin password hash (empty clears).
func (c *ConfigService) SetAdminPasswordHash(ctx context.Context, hash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cfg := *c.cfg
	cfg.AdminPasswordHash = hash
	if err := c.store.SaveServerConfig(ctx, &cfg); err != nil {
		return err
	}
	c.cfg = &cfg
	return nil
}

// CheckAPIKey compares a query-string key against the configured one.
func (c *ConfigService) CheckAPIKey(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return key != "" && key == c.cfg.APIKey
}
