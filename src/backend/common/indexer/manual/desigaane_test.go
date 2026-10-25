package manual

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

const desigaaneBrowseFixture = `{
  "status": "success",
  "response": {
    "results": [
      {
        "groupId": 500,
        "groupName": "Desi Hits",
        "artist": "Desi Artist",
        "cover": "static/common/covers/500.jpg",
        "tags": ["bollywood"],
        "groupTime": "1690000000",
        "groupYear": "2022",
        "releaseType": "Album",
        "torrents": [
          {
            "torrentId": 6001,
            "time": "2023-09-01 10:00:00",
            "size": 52428800,
            "seeders": 8,
            "leechers": 1,
            "snatches": 20,
            "fileCount": 6,
            "format": "MP3",
            "encoding": "320",
            "media": "CD",
            "category": "Music",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      }
    ]
  }
}`

// desigaaneValidKey is a 116-char API key (upstream ApiKeyLength override).
const desigaaneValidKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123"

// desigaaneSite is a fake Gazelle API tracker for DesiGaane tests.
type desigaaneSite struct {
	*httptest.Server

	mu       sync.Mutex
	lastAuth string
	hits     []string
}

func newDesiGaaneSite(t *testing.T) *desigaaneSite {
	t.Helper()
	s := &desigaaneSite{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path != "/ajax.php" {
			http.NotFound(w, r)
			return
		}
		s.hits = append(s.hits, r.URL.RequestURI())
		s.lastAuth = r.Header.Get("Authorization")
		if s.lastAuth == desigaaneValidKey {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, desigaaneBrowseFixture)
			return
		}
		if s.lastAuth != "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"status":"failure","error":"bad key"}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body>login required</body></html>`)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *desigaaneSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestDesiGaane(t *testing.T, site *desigaaneSite) *DesiGaane {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewDesiGaane(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewDesiGaane: %v", err)
	}
	d := ix.(*DesiGaane)
	d.SiteLinkValue = site.URL + "/"
	d.DefaultSiteLink = site.URL + "/"
	return d
}

func TestDesiGaaneCaps(t *testing.T) {
	ix, err := NewDesiGaane(services.Deps{})
	if err != nil {
		t.Fatalf("NewDesiGaane: %v", err)
	}
	caps := ix.Capabilities()
	if len(caps.TVSearchParams) != 0 || len(caps.MovieSearchParams) != 0 || len(caps.MusicSearchParams) != 0 {
		t.Fatalf("search params must be empty: %v %v %v",
			caps.TVSearchParams, caps.MovieSearchParams, caps.MusicSearchParams)
	}
	got := caps.Categories.MapTrackerCatToNewznab("1")
	// AddCategoryMapping with a desc also creates a custom 1:1 category
	// (id ≥ 100000) that rides along in the mapping — upstream parity.
	found := false
	for _, id := range got {
		if id == models.CatAudio.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("MapTrackerCatToNewznab(1) = %v, want it to contain %d", got, models.CatAudio.ID)
	}
}

func TestDesiGaaneConfigForm(t *testing.T) {
	ix, err := NewDesiGaane(services.Deps{})
	if err != nil {
		t.Fatalf("NewDesiGaane: %v", err)
	}
	d := ix.(*DesiGaane)
	items, err := d.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// useApiKey replaces username/password; the freeleech-token toggle stays;
	// the dynamic "Account Inactivity" DisplayInfo rides after the
	// instructions, before tags
	want := []string{
		"sitelink", "apikey", "usefreeleechtokenswhenavailable", "", "accountinactivity", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
}

func TestDesiGaaneAPIKeyLengthValidation(t *testing.T) {
	site := newDesiGaaneSite(t)
	d := newTestDesiGaane(t, site)

	_, err := d.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "apikey", Value: strings.Repeat("a", 40)},
	})
	if err == nil || err.Error() != "Invalid API Key configured: expected length: 116, got 40" {
		t.Fatalf("err: %v", err)
	}
	if d.IsConfigured() {
		t.Fatal("must not be configured after invalid key length")
	}
	if len(site.paths()) != 0 {
		t.Fatalf("no request expected for length mismatch, got %v", site.paths())
	}
}

func TestDesiGaaneAPIKeyAndParse(t *testing.T) {
	site := newDesiGaaneSite(t)
	d := newTestDesiGaane(t, site)

	status, err := d.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "apikey", Value: desigaaneValidKey},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status = %v, want Completed", status)
	}
	if !d.IsConfigured() {
		t.Fatal("expected configured")
	}
	if site.lastAuth != desigaaneValidKey {
		t.Fatalf("Authorization = %q", site.lastAuth)
	}

	res, err := d.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "desi",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	if r.Title != "Desi Artist - Desi Hits [2022] [Album] MP3 / 320 / CD" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Size == nil || *r.Size != 52428800 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 8 || r.Peers == nil || *r.Peers != 9 {
		t.Fatalf("seeders/peers: %v/%v", r.Seeders, r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 20 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.PublishDate.Format("2006-01-02 15:04:05") != "2023-09-01 10:00:00" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	found := false
	for _, c := range r.Category {
		if c == models.CatAudio.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v, want 3000 included", r.Category)
	}
	if r.Details == nil || r.Details.String() != site.URL+"/torrents.php?torrentid=6001" {
		t.Fatalf("details: %v", r.Details)
	}
	// upstream GetDownloadUrl override: ajax.php download endpoint
	if r.Link == nil || r.Link.String() != site.URL+"/ajax.php?action=download&id=6001" {
		t.Fatalf("link: %v", r.Link)
	}
	// upstream PerformQuery post-parse stamps
	if r.MinimumRatio == nil || *r.MinimumRatio != 0.6 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 259200 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}

	// with freeleech tokens enabled the download link is token-flipped
	d.Config.UseToken.Value = true
	res2, err := d.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "desi",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery (tokens): %v", err)
	}
	if len(res2.Releases) != 1 {
		t.Fatalf("releases (tokens): %d", len(res2.Releases))
	}
	if link := res2.Releases[0].Link; link == nil ||
		link.String() != site.URL+"/ajax.php?action=download&usetoken=1&id=6001" {
		t.Fatalf("token link: %v", link)
	}
}

func TestDesiGaaneBadAPIKey(t *testing.T) {
	site := newDesiGaaneSite(t)
	d := newTestDesiGaane(t, site)

	_, err := d.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "apikey", Value: strings.Repeat("b", 116)},
	})
	if err == nil ||
		!strings.Contains(err.Error(), `Your API Key did not work: Unexpected response from indexer request: "bad key"`) {
		t.Fatalf("err: %v", err)
	}
	if d.IsConfigured() {
		t.Fatal("must not be configured after API key failure")
	}
}
