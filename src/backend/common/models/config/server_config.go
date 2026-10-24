// Package config holds ServerConfig and IndexerConfig base types.
//
// Ported from: Jackett.Common/Models/Config/ServerConfig.cs — field set and
// defaults follow doc/tech/data-model.md §2.2.
package config

// Proxy type enum values, mirroring upstream Jackett.Common.Models.Config.ProxyType.
const (
	ProxyTypeDisabled = -1
	ProxyTypeHTTP     = 0
	ProxyTypeSOCKS4   = 1
	ProxyTypeSOCKS5   = 2
)

// ServerConfig is the persisted server configuration (single JSON row in
// table server_config). JSON tags are the storage/transport field names
// (snake_case), matching backend-api.md §1.1.
type ServerConfig struct {
	Port                      int    `json:"port"`
	LocalBindAddress          string `json:"local_bind_address"`
	AllowExternal             bool   `json:"allow_external"`
	AllowCORS                 bool   `json:"allow_cors"`
	APIKey                    string `json:"api_key"`
	AdminPasswordHash         string `json:"admin_password_hash"`
	InstanceID                string `json:"instance_id"`
	BlackholeDir              string `json:"blackhole_dir"`
	UpdateDisabled            bool   `json:"update_disabled"`
	UpdatePrerelease          bool   `json:"update_prerelease"`
	BasePathOverride          string `json:"base_path_override"`
	BaseURLOverride           string `json:"base_url_override"`
	CacheEnabled              bool   `json:"cache_enabled"`
	CacheTTL                  int    `json:"cache_ttl"`
	CacheMaxResultsPerIndexer int    `json:"cache_max_results_per_indexer"`
	FlareSolverrURL           string `json:"flaresolverr_url"`
	FlareSolverrMaxTimeout    int    `json:"flaresolverr_max_timeout"`
	OMDBAPIKey                string `json:"omdb_api_key"`
	OMDBAPIURL                string `json:"omdb_api_url"`
	ProxyType                 int    `json:"proxy_type"`
	ProxyURL                  string `json:"proxy_url"`
	ProxyPort                 *int   `json:"proxy_port"`
	ProxyUsername             string `json:"proxy_username"`
	ProxyPassword             string `json:"proxy_password"`
}

// DefaultServerConfig returns the factory defaults (upstream ServerConfig
// constructor semantics, data-model.md §2.2).
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		Port:                      9117,
		LocalBindAddress:          "127.0.0.1",
		AllowExternal:             false,
		AllowCORS:                 false,
		APIKey:                    "",
		AdminPasswordHash:         "",
		InstanceID:                "",
		BlackholeDir:              "",
		UpdateDisabled:            false,
		UpdatePrerelease:          false,
		BasePathOverride:          "",
		BaseURLOverride:           "",
		CacheEnabled:              true,
		CacheTTL:                  2100,
		CacheMaxResultsPerIndexer: 1000,
		FlareSolverrURL:           "",
		FlareSolverrMaxTimeout:    60000,
		OMDBAPIKey:                "",
		OMDBAPIURL:                "",
		ProxyType:                 ProxyTypeDisabled,
		ProxyURL:                  "",
		ProxyPort:                 nil,
		ProxyUsername:             "",
		ProxyPassword:             "",
	}
}
