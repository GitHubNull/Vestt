package api

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"vestt/common/services"
	"vestt/common/torznab"
)

// GetServerUrl ports upstream ServerService.GetServerUrl: the configured
// BaseURLOverride wins; otherwise scheme://host:port with reverse-proxy
// header handling (X-Forwarded-Proto, Front-End-Https) and the implicit
// 443 for https hosts without a port. The result ends with "/".
func GetServerUrl(cfg *services.ConfigService, r *http.Request) string {
	c := cfg.Config()
	basePath := normalizeBasePath(c.BasePathOverride)
	if c.BaseURLOverride != "" {
		return strings.TrimRight(c.BaseURLOverride, "/") + basePath + "/"
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.Header.Get("Front-End-Https") == "on" {
		scheme = "https"
	}
	if scheme == "https" && !strings.Contains(host, ":") {
		host += ":443"
	}
	return scheme + "://" + host + basePath + "/"
}

// normalizeBasePath mirrors upstream ServerService.BasePath: leading slash,
// no trailing slash, empty when unset.
func normalizeBasePath(p string) string {
	if p == "" {
		return ""
	}
	p = "/" + strings.Trim(p, "/")
	if p == "/" {
		return ""
	}
	return p
}

// ConvertToProxyLink ports upstream ServerService.ConvertToProxyLink. Upstream
// encrypts the URL with ASP.NET Data Protection before base64url-encoding;
// Vestt has no protection layer (backend-api.md §8), so the URL is encoded
// verbatim — the wire format (base64url in `path`, URL-encoded `file`) is
// identical. Magnet links pass through untouched except for blackhole links.
func ConvertToProxyLink(cfg *services.ConfigService, link *url.URL, serverURL, indexerID, action, file string) *url.URL {
	if link == nil || (link.IsAbs() && link.Scheme == "magnet" && action != "bh") {
		return link
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(link.String()))
	proxy := serverURL + action + "/" + indexerID + "/?jackett_apikey=" + url.QueryEscape(cfg.Config().APIKey) +
		"&path=" + encoded + "&file=" + url.QueryEscape(file)
	u, err := url.Parse(proxy)
	if err != nil {
		return link
	}
	return u
}

// decodeProxyPath reverses ConvertToProxyLink's path encoding (upstream
// Base64UrlDecode + UnProtect; Vestt has no protection layer).
func decodeProxyPath(encoded string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// errorAction mirrors upstream ResultsController.GetErrorActionResult: on
// the torznab action every error becomes an XML error document with HTTP
// 200; other actions map the status (401/404/400) with a plain description
// body.
func errorAction(w http.ResponseWriter, isTorznab bool, status, torznabCode int, description string) {
	if isTorznab {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(torznab.ErrorXMLString(torznabCode, description)))
		return
	}
	switch status {
	case http.StatusUnauthorized:
		w.WriteHeader(http.StatusUnauthorized)
	case http.StatusNotFound:
		http.Error(w, description, http.StatusNotFound)
	case http.StatusBadRequest:
		http.Error(w, description, http.StatusBadRequest)
	default:
		http.Error(w, description, status)
	}
}

// noStore mirrors upstream [ResponseCache(NoStore = true)].
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}
