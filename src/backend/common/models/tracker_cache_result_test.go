package models

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

// SPEC-13 澄清记录: the §2.6 wire shape — PascalCase keys, URL fields as
// strings (or null), Languages/Subs never null after NewTrackerCacheResult.

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return u
}

func TestTrackerCacheResultJSONShape(t *testing.T) {
	size := int64(14680064000)
	seeders := int64(88)
	peers := int64(7)
	dvf := 0.0
	ulf := 1.0
	firstSeen := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	r := &ReleaseInfo{
		Title:                "Example Movie 2020 1080p BluRay",
		Guid:                 mustURL(t, "https://example.com/t/123"),
		Link:                 mustURL(t, "http://127.0.0.1:9117/dl/exampletracker?path=abc&jackett_apikey=k"),
		Details:              mustURL(t, "https://example.com/t/123"),
		PublishDate:          time.Date(2026, 10, 20, 18, 30, 0, 0, time.UTC),
		Category:             []int{2000, 2040},
		Size:                 &size,
		Seeders:              &seeders,
		Peers:                &peers,
		DownloadVolumeFactor: &dvf,
		UploadVolumeFactor:   &ulf,
		Poster:               mustURL(t, "http://127.0.0.1:9117/img/exampletracker?path=p"),
	}
	tcr := NewTrackerCacheResult(r)
	tcr.FirstSeen = firstSeen
	tcr.Tracker = "Example Tracker"
	tcr.TrackerId = "exampletracker"
	tcr.TrackerType = "private"
	tcr.CategoryDesc = "Movies/HD"

	raw, err := json.Marshal(tcr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// URL fields are strings, not nested structs.
	if got, ok := m["Link"].(string); !ok || !strings.HasPrefix(got, "http://127.0.0.1:9117/dl/") {
		t.Fatalf("Link not a string: %v", m["Link"])
	}
	if got := m["Guid"].(string); got != "https://example.com/t/123" {
		t.Fatalf("Guid: %v", got)
	}
	if got := m["Poster"].(string); !strings.HasPrefix(got, "http://127.0.0.1:9117/img/") {
		t.Fatalf("Poster: %v", got)
	}
	// nil URLs stay null (not "", not {}).
	for _, k := range []string{"MagnetUri", "BlackholeLink", "MinimumRatio", "MinimumSeedTime", "Files", "Grabs", "Imdb", "TMDb", "TVDBId"} {
		if v, present := m[k]; !present || v != nil {
			t.Fatalf("%s: want null, got %v (present %v)", k, v, present)
		}
	}
	// scalar + meta fields
	if m["Title"] != "Example Movie 2020 1080p BluRay" || m["TrackerId"] != "exampletracker" ||
		m["TrackerType"] != "private" || m["CategoryDesc"] != "Movies/HD" || m["Tracker"] != "Example Tracker" {
		t.Fatalf("meta fields: %v", m)
	}
	if m["DownloadVolumeFactor"] != float64(0) || m["UploadVolumeFactor"] != float64(1) {
		t.Fatalf("volume factors: %v", m)
	}
	// Languages/Subs survive as empty lists (upstream copy constructor).
	if l, ok := m["Languages"].([]any); !ok || len(l) != 0 {
		t.Fatalf("Languages: %v", m["Languages"])
	}
	if s, ok := m["Subs"].([]any); !ok || len(s) != 0 {
		t.Fatalf("Subs: %v", m["Subs"])
	}
	if cat, ok := m["Category"].([]any); !ok || len(cat) != 2 {
		t.Fatalf("Category: %v", m["Category"])
	}
	// Origin must never leak into the wire shape.
	if _, present := m["Origin"]; present {
		t.Fatal("Origin leaked into JSON")
	}
}
