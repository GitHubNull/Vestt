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

const simurgBrowseFixture = `{
  "status": "success",
  "response": {
    "results": [
      {
        "groupId": 800,
        "groupName": "Some Book",
        "artist": "",
        "cover": "",
        "tags": [],
        "groupTime": "1699900000",
        "groupYear": "2019",
        "releaseType": "",
        "torrents": [
          {
            "torrentId": 801,
            "time": "2023-11-10 09:00:00",
            "size": 1048576,
            "seeders": 4,
            "leechers": 1,
            "snatches": 12,
            "fileCount": 1,
            "format": "PDF",
            "media": "E-Book",
            "category": "E-Books",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      },
      {
        "groupId": 802,
        "groupName": "No Cat Release",
        "artist": "",
        "cover": "",
        "tags": [],
        "groupTime": "1699800000",
        "groupYear": "2020",
        "releaseType": "",
        "torrents": [
          {
            "torrentId": 802,
            "time": "2023-11-09 09:00:00",
            "size": 2097152,
            "seeders": 2,
            "leechers": 0,
            "snatches": 3,
            "fileCount": 1,
            "format": "CBZ",
            "media": "",
            "category": "",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      },
      {
        "groupId": 803,
        "groupName": "Old Grouped Audiobook",
        "artist": "",
        "cover": "",
        "tags": [],
        "groupTime": "1699700000",
        "groupYear": "2018",
        "releaseType": "",
        "torrents": [
          {
            "torrentId": 803,
            "time": "2023-11-08 09:00:00",
            "size": 4194304,
            "seeders": 7,
            "leechers": 2,
            "snatches": 21,
            "fileCount": 2,
            "format": "MP3",
            "media": "",
            "category": "",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      }
    ]
  }
}`

// simurgValidKey is a 116-char API key (upstream ApiKeyLength override).
var simurgValidKey = strings.Repeat("c", 116)

// simurgSite is a fake Gazelle API tracker for Simurg tests.
type simurgSite struct {
	*httptest.Server

	mu       sync.Mutex
	lastAuth string
	hits     []string
}

func newSimurgSite(t *testing.T) *simurgSite {
	t.Helper()
	s := &simurgSite{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.URL.Path != "/ajax.php" {
			http.NotFound(w, r)
			return
		}
		s.hits = append(s.hits, r.URL.RequestURI())
		s.lastAuth = r.Header.Get("Authorization")
		if s.lastAuth == "token "+simurgValidKey {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, simurgBrowseFixture)
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

func (s *simurgSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestSimurg(t *testing.T, site *simurgSite) *Simurg {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewSimurg(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewSimurg: %v", err)
	}
	si := ix.(*Simurg)
	si.SiteLinkValue = site.URL + "/"
	si.DefaultSiteLink = site.URL + "/"
	client.SetRequestDelay(si.ID(), 0)
	return si
}

func TestSimurgCaps(t *testing.T) {
	ix, err := NewSimurg(services.Deps{})
	if err != nil {
		t.Fatalf("NewSimurg: %v", err)
	}
	caps := ix.Capabilities()
	if len(caps.TVSearchParams) != 0 || len(caps.MovieSearchParams) != 0 ||
		len(caps.MusicSearchParams) != 0 || len(caps.BookSearchParams) != 0 {
		t.Fatalf("search params must be empty: %v %v %v %v",
			caps.TVSearchParams, caps.MovieSearchParams, caps.MusicSearchParams, caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"3", 7020}, {"4", 3030}, {"7", 7030}, {"8", 7010},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		found := false
		for _, id := range got {
			if id == tc.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", tc.tracker, got, tc.want)
		}
	}
}

func TestSimurgConfigForm(t *testing.T) {
	ix, err := NewSimurg(services.Deps{})
	if err != nil {
		t.Fatalf("NewSimurg: %v", err)
	}
	si := ix.(*Simurg)
	items, err := si.ConfigurationForSetup(context.Background())
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

func TestSimurgAPIKeyLengthValidation(t *testing.T) {
	site := newSimurgSite(t)
	si := newTestSimurg(t, site)

	_, err := si.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "apikey", Value: strings.Repeat("a", 40)},
	})
	if err == nil || err.Error() != "Invalid API Key configured: expected length: 116, got 40" {
		t.Fatalf("err: %v", err)
	}
	if si.IsConfigured() {
		t.Fatal("must not be configured after invalid key length")
	}
	if len(site.paths()) != 0 {
		t.Fatalf("no request expected for length mismatch, got %v", site.paths())
	}
}

func TestSimurgBadAPIKey(t *testing.T) {
	site := newSimurgSite(t)
	si := newTestSimurg(t, site)

	_, err := si.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "apikey", Value: strings.Repeat("b", 116)},
	})
	if err == nil ||
		!strings.Contains(err.Error(), `Your API Key did not work: Unexpected response from indexer request: "bad key"`) {
		t.Fatalf("err: %v", err)
	}
	if si.IsConfigured() {
		t.Fatal("must not be configured after API key failure")
	}
}

func TestSimurgAPIKeyAndParse(t *testing.T) {
	site := newSimurgSite(t)
	si := newTestSimurg(t, site)

	status, err := si.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "apikey", Value: simurgValidKey},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status = %v, want Completed", status)
	}
	if !si.IsConfigured() {
		t.Fatal("expected configured")
	}
	if site.lastAuth != "token "+simurgValidKey {
		t.Fatalf("Authorization = %q", site.lastAuth)
	}

	res, err := si.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "simurg",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 3 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// sorted desc by publish date: 801 (Nov 10), 802 (Nov 9), 803 (Nov 8)
	book := res.Releases[0]
	if book.Title != "Some Book [2019] PDF / E-Book" {
		t.Fatalf("book title: %q", book.Title)
	}
	foundEBook := false
	for _, c := range book.Category {
		if c == models.CatBooksEBook.ID {
			foundEBook = true
		}
	}
	if !foundEBook {
		t.Fatalf("book category: %v, want 7020 included", book.Category)
	}

	// no torrent/result category → format fallback: CBZ → BooksComics
	comic := res.Releases[1]
	foundComics := false
	for _, c := range comic.Category {
		if c == models.CatBooksComics.ID {
			foundComics = true
		}
	}
	if !foundComics {
		t.Fatalf("comic category: %v, want 7030 included", comic.Category)
	}

	// MP3 → AudioAudiobook
	audio := res.Releases[2]
	foundAudio := false
	for _, c := range audio.Category {
		if c == models.CatAudioAudiobook.ID {
			foundAudio = true
		}
	}
	if !foundAudio {
		t.Fatalf("audio category: %v, want 3030 included", audio.Category)
	}
	if audio.Size == nil || *audio.Size != 4194304 {
		t.Fatalf("audio size: %v", audio.Size)
	}
	if audio.Seeders == nil || *audio.Seeders != 7 || audio.Peers == nil || *audio.Peers != 9 {
		t.Fatalf("audio seeders/peers: %v/%v", audio.Seeders, audio.Peers)
	}
	if audio.Grabs == nil || *audio.Grabs != 21 {
		t.Fatalf("audio grabs: %v", audio.Grabs)
	}
	if audio.PublishDate.Format("2006-01-02 15:04:05") != "2023-11-08 09:00:00" {
		t.Fatalf("audio publish date: %v", audio.PublishDate)
	}
	// upstream PerformQuery stamps
	if audio.MinimumRatio == nil || *audio.MinimumRatio != 0.6 {
		t.Fatalf("minimum ratio: %v", audio.MinimumRatio)
	}
	if audio.MinimumSeedTime == nil || *audio.MinimumSeedTime != 259200 {
		t.Fatalf("minimum seed time: %v", audio.MinimumSeedTime)
	}

	// upstream GetDownloadUrl override: ajax.php download endpoint
	if link := audio.Link; link == nil ||
		link.String() != site.URL+"/ajax.php?action=download&id=803" {
		t.Fatalf("link: %v", link)
	}

	// with freeleech tokens enabled the download link is token-flipped
	si.Config.UseToken.Value = true
	res2, err := si.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "simurg",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery (tokens): %v", err)
	}
	if link := res2.Releases[0].Link; link == nil ||
		link.String() != site.URL+"/ajax.php?action=download&usetoken=1&id=801" {
		t.Fatalf("token link: %v", link)
	}

	// 3 requests: the ApplyConfiguration self-test runs an empty query (no
	// searchstr — upstream GazelleTracker.ApplyConfiguration → PerformQuery),
	// then the two searches; upstream param order is action, order_by,
	// order_way, then searchstr (GazelleTracker.cs:184-211)
	paths := site.paths()
	if len(paths) != 3 {
		t.Fatalf("paths: %v", paths)
	}
	if strings.Contains(paths[0], "searchstr") {
		t.Fatalf("self-test path must not carry searchstr: %v", paths[0])
	}
	if !strings.Contains(paths[1], "searchstr=simurg") || !strings.Contains(paths[2], "searchstr=simurg") {
		t.Fatalf("search paths: %v", paths[1:])
	}
}
