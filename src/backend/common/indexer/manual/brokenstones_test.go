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

const brokenStonesBrowseFixture = `{
  "status": "success",
  "response": {
    "results": [
      {
        "groupId": 50,
        "groupName": "Cool App 2.0",
        "artist": "",
        "cover": "",
        "tags": ["macos"],
        "groupTime": "1690000000",
        "groupYear": "0",
        "releaseType": "Unknown",
        "torrents": [
          {
            "torrentId": 9001,
            "time": "2023-08-20 10:00:00",
            "size": 104857600,
            "seeders": 7,
            "leechers": 1,
            "snatches": 30,
            "fileCount": 1,
            "category": "MacOS Apps",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          },
          {
            "torrentId": 9002,
            "time": "2023-08-21 11:00:00",
            "size": 209715200,
            "seeders": 3,
            "leechers": 0,
            "snatches": 12,
            "fileCount": 2,
            "category": "iOS Games",
            "isFreeleech": true,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      }
    ]
  }
}`

// brokenStonesSite is a fake Gazelle tracker for BrokenStones tests.
type brokenStonesSite struct {
	*httptest.Server

	mu         sync.Mutex
	loginPosts int
	seq        int
	sessions   map[string]bool
}

func newBrokenStonesSite(t *testing.T, loginOK bool) *brokenStonesSite {
	t.Helper()
	s := &brokenStonesSite{sessions: map[string]bool{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/login.php":
			s.loginPosts++
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if loginOK && r.PostForm.Get("username") == "okuser" && r.PostForm.Get("password") == "okpass" && r.PostForm.Get("keeplogged") == "1" {
				s.seq++
				sess := fmt.Sprintf("session=s%d", s.seq)
				s.sessions[sess] = true
				w.Header().Add("Set-Cookie", sess+"; path=/")
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body><div id="loginform"><table><tr><td>form fields</td></tr></table>
	Invalid username or password.</div></body></html>`)
		case "/ajax.php":
			valid := false
			for sess := range s.sessions {
				if strings.Contains(r.Header.Get("Cookie"), sess) {
					valid = true
				}
			}
			if !valid {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, `<html><body>login required</body></html>`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, brokenStonesBrowseFixture)
		case "/":
			if s.seq > 0 {
				w.Header().Add("Set-Cookie", fmt.Sprintf("session=s%d; path=/", s.seq))
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body><a href="/logout.php">Log out</a></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func newTestBrokenStones(t *testing.T, site *brokenStonesSite) *BrokenStones {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewBrokenStones(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewBrokenStones: %v", err)
	}
	b := ix.(*BrokenStones)
	b.SiteLinkValue = site.URL + "/"
	b.DefaultSiteLink = site.URL + "/"
	return b
}

func TestBrokenStonesCaps(t *testing.T) {
	ix, err := NewBrokenStones(services.Deps{})
	if err != nil {
		t.Fatalf("NewBrokenStones: %v", err)
	}
	caps := ix.Capabilities()
	// upstream SetCapabilities leaves every search-param list empty
	if len(caps.TVSearchParams) != 0 || len(caps.MovieSearchParams) != 0 ||
		len(caps.MusicSearchParams) != 0 || len(caps.BookSearchParams) != 0 {
		t.Fatalf("search params must be empty: %v %v %v %v",
			caps.TVSearchParams, caps.MovieSearchParams, caps.MusicSearchParams, caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 4030}, {"2", 4030}, {"3", 4060}, {"4", 4060},
		{"5", 8000}, {"6", 3000}, {"7", 8000}, {"8", 8000},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		// AddCategoryMapping with a desc also creates a custom 1:1 category
		// (id ≥ 100000) that rides along in the mapping — upstream parity.
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

func TestBrokenStonesConfigForm(t *testing.T) {
	ix, err := NewBrokenStones(services.Deps{})
	if err != nil {
		t.Fatalf("NewBrokenStones: %v", err)
	}
	b := ix.(*BrokenStones)
	items, err := b.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// has2FA adds the cookiehint/cookie pair before the freeleech token toggle
	want := []string{
		"sitelink", "username", "password", "cookiehint", "cookie",
		"usefreeleechtokenswhenavailable", "", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
}

func TestBrokenStonesSearchURL(t *testing.T) {
	ix, err := NewBrokenStones(services.Deps{})
	if err != nil {
		t.Fatalf("NewBrokenStones: %v", err)
	}
	b := ix.(*BrokenStones)
	q := models.NewTorznabQuery()
	q.SearchTerm = "Cool.App"
	u := b.SearchURL(q)
	if !strings.HasPrefix(u, "https://brokenstones.is/ajax.php?action=browse&order_by=time&order_way=desc&") {
		t.Fatalf("url = %s", u)
	}
	// dots are replaced by spaces AFTER encoding
	if !strings.Contains(u, "searchstr=Cool App") {
		t.Fatalf("searchstr = %s", u)
	}
}

func TestBrokenStonesLoginAndParse(t *testing.T) {
	site := newBrokenStonesSite(t, true)
	b := newTestBrokenStones(t, site)

	status, err := b.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status = %v, want RequiresTesting", status)
	}

	// enable freeleech tokens: non-freeleech torrents get &usetoken=1
	b.Config.UseToken.Value = true

	res, err := b.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "app",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// sorted by PublishDate descending: 9002 (08-21), 9001 (08-20)
	first := res.Releases[0]
	if first.Title != "Cool App 2.0" {
		t.Fatalf("title[0]: %q", first.Title)
	}
	if first.PublishDate.Format("2006-01-02 15:04:05") != "2023-08-21 11:00:00" {
		t.Fatalf("date[0]: %v", first.PublishDate)
	}
	if first.Size == nil || *first.Size != 209715200 {
		t.Fatalf("size[0]: %v", first.Size)
	}
	if first.Seeders == nil || *first.Seeders != 3 || first.Peers == nil || *first.Peers != 3 {
		t.Fatalf("seeders/peers[0]: %v/%v", first.Seeders, first.Peers)
	}
	found := false
	for _, c := range first.Category {
		if c == models.CatPCMobileiOS.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category[0]: %v, want 4060 included", first.Category)
	}
	// freeleech torrent: DVF is stamped after the download link is built, so
	// the link still carries &usetoken=1 (upstream quirk, replicated:
	// GazelleTracker.cs line 513 computes GetDownloadUrl before the
	// DownloadVolumeFactor assignments and `null != 0` is true in C#).
	if first.DownloadVolumeFactor == nil || *first.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf[0]: %v", first.DownloadVolumeFactor)
	}
	if first.Link == nil ||
		!strings.Contains(first.Link.String(), "torrents.php?action=download&usetoken=1&id=9002") {
		t.Fatalf("link[0]: %v", first.Link)
	}

	second := res.Releases[1]
	if second.PublishDate.Format("2006-01-02 15:04:05") != "2023-08-20 10:00:00" {
		t.Fatalf("date[1]: %v", second.PublishDate)
	}
	if second.Grabs == nil || *second.Grabs != 30 {
		t.Fatalf("grabs[1]: %v", second.Grabs)
	}
	found = false
	for _, c := range second.Category {
		if c == models.CatPCMac.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category[1]: %v, want 4030 included", second.Category)
	}
	// regular torrent: token used when available
	if second.DownloadVolumeFactor == nil || *second.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf[1]: %v", second.DownloadVolumeFactor)
	}
	if second.Link == nil ||
		!strings.Contains(second.Link.String(), "torrents.php?action=download&usetoken=1&id=9001") {
		t.Fatalf("link[1]: %v", second.Link)
	}
	if second.Details == nil || second.Details.String() != site.URL+"/torrents.php?torrentid=9001" {
		t.Fatalf("details[1]: %v", second.Details)
	}
}

func TestBrokenStonesLoginError(t *testing.T) {
	site := newBrokenStonesSite(t, false)
	b := newTestBrokenStones(t, site)

	_, err := b.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "wrong"},
	})
	if err == nil || err.Error() != "Invalid username or password." {
		t.Fatalf("err: %v", err)
	}
	if b.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}
