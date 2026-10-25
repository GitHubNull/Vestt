// Jackett file format parsing and field mapping for vestt-import.
//
// Upstream formats (behaviour specification):
//   - ServerConfig.json: Newtonsoft serialization of
//     Jackett.Common/Models/Config/ServerConfig.cs (camelCase property names,
//     computed getter-only properties such as ProxyIsAnonymous are serialized
//     too and must be ignored here).
//   - Indexers/<id>.json: JSON array of configuration items produced by
//     ConfigurationData.ToJson(protectionService, forDisplay: false)
//     (Jackett.Common/Models/IndexerConfig/ConfigurationData.cs); each item
//     is {"id","type","name","value"[,...]}. A non-array file is the pre-0.8
//     "old format" which upstream also refuses to load
//     (BaseIndexer.LoadFromSavedConfiguration logs a warning and skips).
//   - Cookies: stored as the "cookieheader" hiddendata item inside the same
//     array ("k=v; k2=v2" header form, WebResult.Cookies semantics). There is
//     no separate cookies file in Jackett 0.24.2900; the value
//     "__jackett_expired__" marks a cookie deleted by the server
//     (CookieUtil.ExpiredCookieValue).
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"vestt/common/httpclient"
	"vestt/common/indexer/configdata"
	"vestt/common/models/config"
)

// bcryptCost mirrors the Vestt SecurityService (upstream cost 11,
// services/security.go).
const bcryptCost = 11

// expiredCookieValue is upstream CookieUtil.ExpiredCookieValue: a sentinel
// meaning "the server deleted this cookie"; it must not be imported.
const expiredCookieValue = "__jackett_expired__"

// jackettServerConfig mirrors the upstream ServerConfig.json field set.
// Pointer fields distinguish "absent from file" (keep the Vestt default)
// from a present value; unknown upstream fields (e.g. the serialized
// ProxyIsAnonymous getter or future additions) are ignored by encoding/json.
type jackettServerConfig struct {
	Port                      *int    `json:"Port"`
	LocalBindAddress          *string `json:"LocalBindAddress"`
	AllowExternal             *bool   `json:"AllowExternal"`
	AllowCORS                 *bool   `json:"AllowCORS"`
	APIKey                    *string `json:"APIKey"`
	AdminPassword             *string `json:"AdminPassword"`
	InstanceId                *string `json:"InstanceId"`
	BlackholeDir              *string `json:"BlackholeDir"`
	UpdateDisabled            *bool   `json:"UpdateDisabled"`
	UpdatePrerelease          *bool   `json:"UpdatePrerelease"`
	BasePathOverride          *string `json:"BasePathOverride"`
	BaseUrlOverride           *string `json:"BaseUrlOverride"`
	CacheEnabled              *bool   `json:"CacheEnabled"`
	CacheTtl                  *int64  `json:"CacheTtl"`
	CacheMaxResultsPerIndexer *int64  `json:"CacheMaxResultsPerIndexer"`
	FlareSolverrUrl           *string `json:"FlareSolverrUrl"`
	FlareSolverrMaxTimeout    *int    `json:"FlareSolverrMaxTimeout"`
	OmdbApiKey                *string `json:"OmdbApiKey"`
	OmdbApiUrl                *string `json:"OmdbApiUrl"`
	ProxyType                 *int    `json:"ProxyType"`
	ProxyUrl                  *string `json:"ProxyUrl"`
	ProxyPort                 *int    `json:"ProxyPort"`
	ProxyUsername             *string `json:"ProxyUsername"`
	ProxyPassword             *string `json:"ProxyPassword"`
}

// mapServerConfig converts a ServerConfig.json payload into the Vestt
// server_config row (doc/tech/data-model.md §2.2). Upstream fields without a
// Vestt counterpart are skipped. The upstream AdminPassword is plaintext;
// Vestt only stores a bcrypt hash, so the password is hashed on import and a
// note is returned for the report.
func mapServerConfig(raw []byte) (*config.ServerConfig, []string, error) {
	var j jackettServerConfig
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, nil, fmt.Errorf("parse ServerConfig.json: %w", err)
	}

	cfg := config.DefaultServerConfig()
	var notes []string
	if j.Port != nil {
		cfg.Port = *j.Port
	}
	if j.LocalBindAddress != nil {
		cfg.LocalBindAddress = *j.LocalBindAddress
	}
	if j.AllowExternal != nil {
		cfg.AllowExternal = *j.AllowExternal
	}
	if j.AllowCORS != nil {
		cfg.AllowCORS = *j.AllowCORS
	}
	if j.APIKey != nil {
		cfg.APIKey = *j.APIKey
	}
	if j.InstanceId != nil {
		cfg.InstanceID = *j.InstanceId
	}
	if j.BlackholeDir != nil {
		cfg.BlackholeDir = *j.BlackholeDir
	}
	if j.UpdateDisabled != nil {
		cfg.UpdateDisabled = *j.UpdateDisabled
	}
	if j.UpdatePrerelease != nil {
		cfg.UpdatePrerelease = *j.UpdatePrerelease
	}
	if j.BasePathOverride != nil {
		cfg.BasePathOverride = *j.BasePathOverride
	}
	if j.BaseUrlOverride != nil {
		cfg.BaseURLOverride = *j.BaseUrlOverride
	}
	if j.CacheEnabled != nil {
		cfg.CacheEnabled = *j.CacheEnabled
	}
	if j.CacheTtl != nil {
		cfg.CacheTTL = int(*j.CacheTtl)
	}
	if j.CacheMaxResultsPerIndexer != nil {
		cfg.CacheMaxResultsPerIndexer = int(*j.CacheMaxResultsPerIndexer)
	}
	if j.FlareSolverrUrl != nil {
		cfg.FlareSolverrURL = *j.FlareSolverrUrl
	}
	if j.FlareSolverrMaxTimeout != nil {
		cfg.FlareSolverrMaxTimeout = *j.FlareSolverrMaxTimeout
	}
	if j.OmdbApiKey != nil {
		cfg.OMDBAPIKey = *j.OmdbApiKey
	}
	if j.OmdbApiUrl != nil {
		cfg.OMDBAPIURL = *j.OmdbApiUrl
	}
	if j.ProxyType != nil {
		cfg.ProxyType = *j.ProxyType
	}
	if j.ProxyUrl != nil {
		cfg.ProxyURL = *j.ProxyUrl
	}
	if j.ProxyPort != nil {
		p := *j.ProxyPort
		cfg.ProxyPort = &p
	}
	if j.ProxyUsername != nil {
		cfg.ProxyUsername = *j.ProxyUsername
	}
	if j.ProxyPassword != nil {
		cfg.ProxyPassword = *j.ProxyPassword
	}
	// Upstream ServerConfig.OnDeserialized: an empty proxy url always means
	// the proxy is disabled, whatever ProxyType says.
	if cfg.ProxyURL == "" {
		cfg.ProxyType = config.ProxyTypeDisabled
	}
	if j.AdminPassword != nil && *j.AdminPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*j.AdminPassword), bcryptCost)
		if err != nil {
			return nil, nil, fmt.Errorf("hash admin password: %w", err)
		}
		cfg.AdminPasswordHash = string(hash)
		notes = append(notes,
			"AdminPassword imported as a bcrypt hash into admin_password_hash (Vestt never stores plaintext passwords)")
	}
	return cfg, notes, nil
}

// parsedIndexerConfig is the migrated form of one Indexers/<id>.json file.
type parsedIndexerConfig struct {
	configJSON  string // configuration item array, values preserved verbatim
	cookiesJSON string // persisted cookie-jar shape, "" when no cookies
}

// configItemHeader carries the fields needed to inspect one configuration
// item without disturbing the rest of its raw JSON.
type configItemHeader struct {
	ID    string          `json:"id"`
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

// parseIndexerConfig converts one upstream Indexers/<id>.json payload:
// the configuration item array is preserved verbatim (data-model.md §3 keeps
// upstream key names), the display sentinel on password items is normalized
// to an empty value, and the "cookieheader" item is converted into the
// persisted cookie-jar shape stored in cookies_json, with the cookie domain
// taken from the configured "sitelink" item.
func parseIndexerConfig(raw []byte) (parsedIndexerConfig, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return parsedIndexerConfig{}, fmt.Errorf("not a Jackett configuration array (old format?): %w", err)
	}

	var (
		siteLink     string
		cookieHeader string
		out          = make([]json.RawMessage, 0, len(items))
	)
	for _, item := range items {
		var h configItemHeader
		if err := json.Unmarshal(item, &h); err != nil {
			return parsedIndexerConfig{}, fmt.Errorf("malformed configuration item: %w", err)
		}
		switch strings.ToLower(h.ID) {
		case "sitelink":
			_ = json.Unmarshal(h.Value, &siteLink)
		case "cookieheader":
			_ = json.Unmarshal(h.Value, &cookieHeader)
		}
		if isMaskedPassword(h) {
			fixed, err := stripItemValue(item)
			if err != nil {
				return parsedIndexerConfig{}, fmt.Errorf("strip masked password: %w", err)
			}
			item = fixed
		}
		out = append(out, item)
	}

	cookies := ""
	if cookieHeader != "" && siteLink != "" {
		jar := cookiesFromHeader(cookieHeader, hostOf(siteLink))
		if len(jar) > 0 {
			encoded, err := json.Marshal(jar)
			if err != nil {
				return parsedIndexerConfig{}, fmt.Errorf("encode cookies: %w", err)
			}
			cookies = string(encoded)
		}
	}

	configJSON, err := json.Marshal(out)
	if err != nil {
		return parsedIndexerConfig{}, fmt.Errorf("re-encode configuration: %w", err)
	}
	return parsedIndexerConfig{configJSON: string(configJSON), cookiesJSON: cookies}, nil
}

// isMaskedPassword reports whether the item carries the upstream display
// sentinel for passwords (PASSWORD_REPLACEMENT). The sentinel only appears
// in forDisplay JSON; importing it verbatim would plant a literal
// "|||%%PREVJACKPASSWD%%|||" password. Upstream FromJson treats the sentinel
// as "no new value", so the equivalent migration is an empty value.
func isMaskedPassword(h configItemHeader) bool {
	var v string
	if err := json.Unmarshal(h.Value, &v); err != nil || v != configdata.PasswordReplacement {
		return false
	}
	return strings.EqualFold(h.ID, "password") || strings.EqualFold(h.Type, "password")
}

// stripItemValue rewrites one raw item with "value": "" (key order of the
// remaining fields is preserved).
func stripItemValue(item json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(item, &m); err != nil {
		return nil, err
	}
	m["value"] = json.RawMessage(`""`)
	return json.Marshal(m)
}

// cookiesFromHeader converts an upstream CookieHeader value ("k=v; k2=v2")
// into the persisted cookie-jar shape of indexer_config.cookies_json
// (doc/tech/data-model.md §2.4). Upstream sends the header unconditionally
// on every request, so no Secure flag is reconstructed; expired sentinels
// are dropped; the empty domain (unknown site link) yields no cookies
// because the Vestt cookie jar cannot scope them.
func cookiesFromHeader(header, domain string) []httpclient.PersistedCookie {
	if header == "" || domain == "" {
		return nil
	}
	var out []httpclient.PersistedCookie
	for _, part := range strings.Split(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == expiredCookieValue {
			continue
		}
		out = append(out, httpclient.PersistedCookie{
			Name:   k,
			Value:  v,
			Domain: domain,
			Path:   "/",
		})
	}
	return out
}

// hostOf returns the host name of a site link ("" when unparsable).
func hostOf(siteLink string) string {
	u, err := url.Parse(siteLink)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
