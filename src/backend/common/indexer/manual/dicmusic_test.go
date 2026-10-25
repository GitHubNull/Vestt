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

const dicmusicBrowseFixture = `{
  "status": "success",
  "response": {
    "results": [
      {
        "groupId": 300,
        "groupName": "Test Album",
        "artist": "Test Artist",
        "cover": "",
        "tags": ["rock"],
        "groupTime": "1680000000",
        "groupYear": "2020",
        "releaseType": "Album",
        "torrents": [
          {
            "torrentId": 3001,
            "time": "2023-11-14 22:13:20",
            "size": 314572800,
            "seeders": 5,
            "leechers": 2,
            "snatches": 10,
            "fileCount": 8,
            "format": "FLAC",
            "encoding": "Lossless",
            "hasLog": true,
            "logScore": 100,
            "hasCue": true,
            "media": "CD",
            "category": "Music",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          },
          {
            "torrentId": 3002,
            "time": "2023-11-15 08:00:00",
            "size": 104857600,
            "seeders": 1,
            "leechers": 0,
            "snatches": 3,
            "fileCount": 4,
            "format": "MP3",
            "encoding": "320",
            "media": "WEB",
            "category": "Music",
            "isFreeleech": true,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      },
      {
        "groupId": 400,
        "groupName": "Different Album",
        "artist": "Other Artist",
        "cover": "",
        "tags": ["pop"],
        "groupTime": "1670000000",
        "groupYear": "2019",
        "releaseType": "Album",
        "torrents": [
          {
            "torrentId": 4001,
            "time": "2023-10-01 12:00:00",
            "size": 52428800,
            "seeders": 2,
            "leechers": 0,
            "snatches": 5,
            "fileCount": 2,
            "format": "MP3",
            "encoding": "V0",
            "media": "WEB",
            "category": "Applications",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      }
    ]
  }
}`

// dicmusicSite is a fake Gazelle tracker for DICMusic tests.
type dicmusicSite struct {
	*httptest.Server

	mu         sync.Mutex
	loginPosts int
	hits       []string
	seq        int
	sessions   map[string]bool
}

func newDICMusicSite(t *testing.T, loginOK bool) *dicmusicSite {
	t.Helper()
	s := &dicmusicSite{sessions: map[string]bool{}}
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
			s.hits = append(s.hits, r.URL.RequestURI())
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
			_, _ = fmt.Fprint(w, dicmusicBrowseFixture)
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

func (s *dicmusicSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestDICMusic(t *testing.T, site *dicmusicSite) *DICMusic {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewDICMusic(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewDICMusic: %v", err)
	}
	d := ix.(*DICMusic)
	d.SiteLinkValue = site.URL + "/"
	d.DefaultSiteLink = site.URL + "/"
	return d
}

func TestDICMusicCaps(t *testing.T) {
	ix, err := NewDICMusic(services.Deps{})
	if err != nil {
		t.Fatalf("NewDICMusic: %v", err)
	}
	caps := ix.Capabilities()
	want := []string{
		models.MusicParamQ, models.MusicParamAlbum, models.MusicParamArtist,
		models.MusicParamLabel, models.MusicParamYear,
	}
	if strings.Join(caps.MusicSearchParams, ",") != strings.Join(want, ",") {
		t.Fatalf("MusicSearchParams: %v", caps.MusicSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 3000}, {"2", 4000},
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

func TestDICMusicConfigForm(t *testing.T) {
	ix, err := NewDICMusic(services.Deps{})
	if err != nil {
		t.Fatalf("NewDICMusic: %v", err)
	}
	d := ix.(*DICMusic)
	items, err := d.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{
		"sitelink", "username", "password", "cookiehint", "cookie",
		"usefreeleechtokenswhenavailable", "searchfreeleechonly", "", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
}

func TestDICMusicSearchURL(t *testing.T) {
	ix, err := NewDICMusic(services.Deps{})
	if err != nil {
		t.Fatalf("NewDICMusic: %v", err)
	}
	d := ix.(*DICMusic)

	q := models.NewTorznabQuery()
	q.Artist = "Some Artist"
	q.Label = "Some Label"
	year := 2020
	q.Year = &year
	q.Album = "Some Album"
	u := d.SearchURL(q)
	if !strings.HasPrefix(u, "https://dicmusic.com/ajax.php?action=browse&order_by=time&order_way=desc&") {
		t.Fatalf("url = %s", u)
	}
	for _, part := range []string{
		"artistname=Some+Artist", "recordlabel=Some+Label",
		"year=2020", "groupname=Some+Album",
	} {
		if !strings.Contains(u, part) {
			t.Fatalf("%s missing in %s", part, u)
		}
	}
}

func TestDICMusicLoginParseAndTitleFilter(t *testing.T) {
	site := newDICMusicSite(t, true)
	d := newTestDICMusic(t, site)

	status, err := d.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status = %v, want RequiresTesting", status)
	}

	// "Test Album" matches the first group only ("Test Artist - Test Album"),
	// the "Other Artist - Different Album" group is filtered out upstream by
	// MatchQueryStringAND.
	res, err := d.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "Test Album",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// sorted by PublishDate descending: 3002 (11-15), 3001 (11-14)
	first := res.Releases[0]
	if first.Title != "Test Artist - Test Album [2020] [Album] MP3 / 320 / WEB" {
		t.Fatalf("title[0]: %q", first.Title)
	}
	if first.PublishDate.Format("2006-01-02 15:04:05") != "2023-11-15 08:00:00" {
		t.Fatalf("date[0]: %v", first.PublishDate)
	}
	if first.Size == nil || *first.Size != 104857600 {
		t.Fatalf("size[0]: %v", first.Size)
	}
	if first.Seeders == nil || *first.Seeders != 1 || first.Peers == nil || *first.Peers != 1 {
		t.Fatalf("seeders/peers[0]: %v/%v", first.Seeders, first.Peers)
	}
	found := false
	for _, c := range first.Category {
		if c == models.CatAudio.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category[0]: %v, want 3000 included", first.Category)
	}
	// freeleech torrents are not token-flipped
	if first.DownloadVolumeFactor == nil || *first.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf[0]: %v", first.DownloadVolumeFactor)
	}
	if first.Link == nil || !strings.Contains(first.Link.String(), "id=3002") ||
		strings.Contains(first.Link.String(), "usetoken") {
		t.Fatalf("link[0]: %v", first.Link)
	}

	second := res.Releases[1]
	if second.Title != "Test Artist - Test Album [2020] [Album] FLAC / Lossless / Log (100%) / Cue / CD" {
		t.Fatalf("title[1]: %q", second.Title)
	}
	if second.Grabs == nil || *second.Grabs != 10 || second.Files == nil || *second.Files != 8 {
		t.Fatalf("grabs/files[1]: %v/%v", second.Grabs, second.Files)
	}
	if second.Details == nil || second.Details.String() != site.URL+"/torrents.php?torrentid=3001" {
		t.Fatalf("details[1]: %v", second.Details)
	}
	if second.Description != "Tags: rock\n" || len(second.Genres) != 1 || second.Genres[0] != "rock" {
		t.Fatalf("description/genres[1]: %q %v", second.Description, second.Genres)
	}
}

func TestDICMusicFreeleechOnlySkipsNonFreeleech(t *testing.T) {
	site := newDICMusicSite(t, true)
	d := newTestDICMusic(t, site)
	d.Config.FreeleechOnly.Value = true

	if _, err := d.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	}); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}

	res, err := d.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "Test Album",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d, want 1 (freeleech only)", len(res.Releases))
	}
	if res.Releases[0].Link == nil || !strings.Contains(res.Releases[0].Link.String(), "id=3002") {
		t.Fatalf("link: %v", res.Releases[0].Link)
	}
	paths := site.paths()
	if len(paths) != 1 || !strings.Contains(paths[0], "freetorrent=1") {
		t.Fatalf("paths: %v, want freetorrent=1", paths)
	}
}

func TestDICMusicLoginError(t *testing.T) {
	site := newDICMusicSite(t, false)
	d := newTestDICMusic(t, site)

	_, err := d.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "wrong"},
	})
	if err == nil || err.Error() != "Invalid username or password." {
		t.Fatalf("err: %v", err)
	}
	if d.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}
