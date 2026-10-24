package httpclient

import (
	"bytes"
	"net/http"
)

// cfChallengeMarkers are the body fragments upstream treats as a Cloudflare
// challenge page (BaseWebIndexer/WebClient CF detection).
var cfChallengeMarkers = [][]byte{
	[]byte("__cf_chl"),
	[]byte("cf-browser-verification"),
}

// isCloudflareChallenge mirrors SPEC-04 §3.2 cloudflare.go:
// (403|503) && (body has a challenge marker || cf-mitigated: challenge).
func isCloudflareChallenge(resp *http.Response, body []byte) bool {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	if resp.Header.Get("Cf-Mitigated") == "challenge" {
		return true
	}
	for _, marker := range cfChallengeMarkers {
		if bytes.Contains(body, marker) {
			return true
		}
	}
	return false
}
