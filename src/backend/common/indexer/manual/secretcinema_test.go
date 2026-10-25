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

const secretCinemaBrowseFixture = `{
  "status": "success",
  "response": {
    "results": [
      {
        "groupId": 700,
        "groupName": "Some Movie",
        "artist": "",
        "cover": "",
        "tags": [],
        "groupTime": "1698800000",
        "groupYear": "1977",
        "releaseType": "",
        "torrents": [
          {
            "torrentId": 701,
            "time": "2023-11-01 10:00:00",
            "size": 1000000000,
            "seeders": 5,
            "leechers": 2,
            "snatches": 10,
            "fileCount": 1,
            "media": "BDMV",
            "category": "Movies",
            "releaseName": "Some.Movie.1977.COMPLETE.BLURAY",
            "remasterTitle": "",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      },
      {
        "groupId": 702,
        "groupName": "Another &amp; Film",
        "artist": "",
        "cover": "",
        "tags": ["drama"],
        "groupTime": "1698700000",
        "groupYear": "1980",
        "releaseType": "",
        "torrents": [
          {
            "torrentId": 702,
            "time": "2023-11-02 12:00:00",
            "size": 2000000000,
            "seeders": 3,
            "leechers": 1,
            "snatches": 5,
            "fileCount": 2,
            "media": "SD",
            "category": "Movies",
            "releaseName": "",
            "remasterTitle": "4K Remaster",
            "isFreeleech": true,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      },
      {
        "groupId": 703,
        "groupName": "Music Album",
        "artist": "Some Artist",
        "cover": "",
        "tags": [],
        "groupTime": "1698600000",
        "groupYear": "2021",
        "releaseType": "Album",
        "torrents": [
          {
            "torrentId": 703,
            "time": "2023-11-03 08:00:00",
            "size": 50000000,
            "seeders": 9,
            "leechers": 0,
            "snatches": 30,
            "fileCount": 8,
            "format": "FLAC",
            "encoding": "Lossless",
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

// secretCinemaSite is a fake Gazelle tracker for SecretCinema tests.
type secretCinemaSite struct {
	*httptest.Server

	mu         sync.Mutex
	loginPosts int
	hits       []string
}

func newSecretCinemaSite(t *testing.T, loginOK bool) *secretCinemaSite {
	t.Helper()
	s := &secretCinemaSite{}
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
				w.Header().Add("Set-Cookie", "session=sc1; path=/")
				http.Redirect(w, r, "/index.php", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body><div id="loginform"><table><tr><td>form fields</td></tr></table>
	Invalid username or password.</div></body></html>`)
		case "/index.php":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body><a href="/logout.php">Log out</a></body></html>`)
		case "/ajax.php":
			s.hits = append(s.hits, r.URL.RequestURI())
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, secretCinemaBrowseFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *secretCinemaSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestSecretCinema(t *testing.T, site *secretCinemaSite) *SecretCinema {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewSecretCinema(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewSecretCinema: %v", err)
	}
	s := ix.(*SecretCinema)
	s.SiteLinkValue = site.URL + "/"
	s.DefaultSiteLink = site.URL + "/"
	client.SetRequestDelay(s.ID(), 0)
	return s
}

func TestSecretCinemaCaps(t *testing.T) {
	ix, err := NewSecretCinema(services.Deps{})
	if err != nil {
		t.Fatalf("NewSecretCinema: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.MovieSearchParams, ",") != "q,imdbid" {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	wantMusic := []string{"q", "album", "artist", "label", "year"}
	if strings.Join(caps.MusicSearchParams, ",") != strings.Join(wantMusic, ",") {
		t.Fatalf("MusicSearchParams: %v", caps.MusicSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 2000}, {"2", 3000},
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

func TestSecretCinemaConfigForm(t *testing.T) {
	ix, err := NewSecretCinema(services.Deps{})
	if err != nil {
		t.Fatalf("NewSecretCinema: %v", err)
	}
	s := ix.(*SecretCinema)
	items, err := s.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// no API key, no 2FA cookie, no freeleech tokens (ratioless); the empty
	// instructions info item rides with id ""
	want := []string{"sitelink", "username", "password", "", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
}

func TestSecretCinemaApplyConfigurationAndSearch(t *testing.T) {
	site := newSecretCinemaSite(t, true)
	s := newTestSecretCinema(t, site)

	status, err := s.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status = %v, want RequiresTesting", status)
	}
	if !s.IsConfigured() {
		t.Fatal("expected configured")
	}

	res, err := s.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "some movie",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// upstream filters results with MatchQueryStringAND; "some movie" only
	// matches the first release ("Some.Movie.1977.COMPLETE.BLURAY")
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	// releaseName carries a year → used verbatim as the title
	if r.Title != "Some.Movie.1977.COMPLETE.BLURAY" {
		t.Fatalf("title: %q", r.Title)
	}
	// media "BDMV": Movies(2000) replaced by MoviesBluRay(2050)
	foundBR := false
	for _, c := range r.Category {
		if c == models.CatMoviesBluRay.ID {
			foundBR = true
		}
		if c == models.CatMovies.ID {
			t.Fatalf("category must not contain 2000 after media swap: %v", r.Category)
		}
	}
	if !foundBR {
		t.Fatalf("category: %v, want 2050 included", r.Category)
	}
	if r.Size == nil || *r.Size != 1000000000 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 5 || r.Peers == nil || *r.Peers != 7 {
		t.Fatalf("seeders/peers: %v/%v", r.Seeders, r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 10 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	// upstream re-parses time with a +0200 offset (AdjustToUniversal)
	if r.PublishDate.UTC().Format("2006-01-02 15:04:05") != "2023-11-01 08:00:00" {
		t.Fatalf("publish date: %v", r.PublishDate.UTC())
	}
	if r.Details == nil || r.Details.String() != site.URL+"/torrents.php?torrentid=701" {
		t.Fatalf("details: %v", r.Details)
	}
	// ratioless tracker: no freeleech tokens; plain download link
	if r.Link == nil || r.Link.String() != site.URL+"/torrents.php?action=download&id=701" {
		t.Fatalf("link: %v", r.Link)
	}

	paths := site.paths()
	if len(paths) != 1 || !strings.HasPrefix(paths[0], "/ajax.php?action=browse&order_by=time&order_way=desc&searchstr=some+movie") {
		t.Fatalf("paths: %v", paths)
	}
}

func TestSecretCinemaParseVariants(t *testing.T) {
	site := newSecretCinemaSite(t, true)
	s := newTestSecretCinema(t, site)
	// bypass the AND-filter to inspect all three fixture rows
	releases, err := s.PerformQuery(context.Background(), &models.TorznabQuery{QueryType: "search"})
	if err != nil {
		t.Fatalf("PerformQuery: %v", err)
	}
	if len(releases) != 3 {
		t.Fatalf("releases: %d", len(releases))
	}

	// sorted by PublishDate descending: 703 (Nov 3), 702 (Nov 2), 701 (Nov 1)
	music := releases[0]
	if music.Title != "Some Artist - Music Album [2021] [Album] FLAC / Lossless / CD" {
		t.Fatalf("music title: %q", music.Title)
	}
	foundAudio := false
	for _, c := range music.Category {
		if c == models.CatAudio.ID {
			foundAudio = true
		}
	}
	if !foundAudio {
		t.Fatalf("music category: %v, want 3000 included", music.Category)
	}
	if music.DownloadVolumeFactor == nil || *music.DownloadVolumeFactor != 1 {
		t.Fatalf("music dvf: %v", music.DownloadVolumeFactor)
	}

	sd := releases[1]
	// no year-carrying releaseName → groupName (html-decoded) + year + media,
	// SD → DVDRip, remasterTitle appended
	if sd.Title != "Another & Film (1980) DVDRip / 4K Remaster" {
		t.Fatalf("sd title: %q", sd.Title)
	}
	foundSD := false
	for _, c := range sd.Category {
		if c == models.CatMoviesSD.ID {
			foundSD = true
		}
	}
	if !foundSD {
		t.Fatalf("sd category: %v, want 2030 included", sd.Category)
	}
	// isFreeleech on the SD torrent
	if sd.DownloadVolumeFactor == nil || *sd.DownloadVolumeFactor != 0 {
		t.Fatalf("sd dvf: %v", sd.DownloadVolumeFactor)
	}
}

func TestSecretCinemaLoginFailure(t *testing.T) {
	site := newSecretCinemaSite(t, false)
	s := newTestSecretCinema(t, site)

	_, err := s.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "baduser"},
		{ID: "password", Value: "badpass"},
	})
	if err == nil || !strings.Contains(err.Error(), "Invalid username or password.") {
		t.Fatalf("err: %v", err)
	}
	if s.IsConfigured() {
		t.Fatal("must not be configured after login failure")
	}
}

func TestSecretCinemaSearchFiltersNonMatching(t *testing.T) {
	site := newSecretCinemaSite(t, true)
	s := newTestSecretCinema(t, site)

	res, err := s.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "zeppelin",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d, want 0 (AND-filter)", len(res.Releases))
	}
}

func TestSecretCinemaImdbSearchURL(t *testing.T) {
	site := newSecretCinemaSite(t, true)
	s := newTestSecretCinema(t, site)

	_, err := s.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "movie", ImdbID: "tt1234567", Categories: []int{models.CatMovies.ID},
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("paths: %v", paths)
	}
	if !strings.Contains(paths[0], "cataloguenumber=tt1234567") {
		t.Fatalf("imdb query must use cataloguenumber: %v", paths[0])
	}
	if !strings.Contains(paths[0], "filter_cat%5B1%5D=1") && !strings.Contains(paths[0], "filter_cat[1]=1") {
		t.Fatalf("movie category must set filter_cat[1]: %v", paths[0])
	}
}
