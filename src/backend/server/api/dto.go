package api

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"vestt/common/models"
)

// Manual search result statuses (upstream ManualSearchResultIndexer.Status —
// serialized as an integer; no StringEnumConverter).
const (
	manualStatusUnknown = 0
	manualStatusError   = 1
	manualStatusOK      = 2
)

// ManualSearchResultIndexer is the per-indexer slice of a manual search
// response (upstream DTO/ManualSearchResult.cs).
type ManualSearchResultIndexer struct {
	ID          string  `json:"ID"`
	Name        string  `json:"Name"`
	Status      int     `json:"Status"`
	Results     int     `json:"Results"`
	Error       *string `json:"Error"`
	ElapsedTime int64   `json:"ElapsedTime"`
}

// ManualSearchResult is the aggregate manual-search payload (upstream
// DTO/ManualSearchResult.cs). Envelope keys are lowercase per
// backend-api.md §3.1 ({results, indexers:[…]}); per-entry fields stay
// PascalCase.
type ManualSearchResult struct {
	Results  []models.TrackerCacheResult `json:"results"`
	Indexers []ManualSearchResultIndexer `json:"indexers"`
}

// TorrentPotatoResponse mirrors upstream DTO/TorrentPotatoResponse.cs
// (snake_case JSON via [JsonProperty]).
type TorrentPotatoResponse struct {
	Results      []TorrentPotatoResponseItem `json:"results"`
	TotalResults int                         `json:"total_results"`
}

// TorrentPotatoResponseItem mirrors upstream DTO/TorrentPotatoResponseItem.cs.
type TorrentPotatoResponseItem struct {
	ReleaseName string `json:"release_name"`
	TorrentID   string `json:"torrent_id"`
	DetailsURL  string `json:"details_url"`
	DownloadURL string `json:"download_url"`
	ImdbID      string `json:"imdb_id"`
	Freeleech   bool   `json:"freeleech"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	Leechers    int64  `json:"leechers"`
	Seeders     int64  `json:"seeders"`
	PublishDate string `json:"publish_date"`
}

// splitCSV splits a comma-separated query parameter, trimming spaces and
// dropping empties (upstream query string handling).
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// firstValue returns the first value of a query key, or "".
func firstValue(q url.Values, key string) string {
	return q.Get(key)
}

func intPtr(v int) *int { return &v }

func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func urlParse(s string) (*url.URL, error) { return url.Parse(s) }

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		return nil
	}
	return u
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ApiSearch episode/season splits (upstream ApiSearch.cs → ToTorznabQuery).
var (
	seasonEpisodeRe = regexp.MustCompile(`\bS(\d{2,4})E(\d{2,4}[A-Za-z]?)$`)
	episodeRe       = regexp.MustCompile(`\bE(\d{2,4}[A-Za-z]?)$`)
	seasonRe        = regexp.MustCompile(`\bS(\d{2,4})$`)
)
