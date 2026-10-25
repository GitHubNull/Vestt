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
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/indexer/configdata"
	"vestt/common/models"
	"vestt/common/services"
)

const gpwBrowseFixture = `{
  "status": "success",
  "response": {
    "results": [
      {
        "groupId": 300,
        "groupName": "The Movie &amp; Its Friends",
        "groupSubName": "Subtitle Name",
        "cover": "static/common/covers/300.jpg",
        "tags": ["drama"],
        "groupTime": "1699968000",
        "groupYear": "2020",
        "releaseType": "Unknown",
        "doubanId": "1234567",
        "imdbId": "tt0112471",
        "torrents": [
          {
            "torrentId": 4001,
            "time": "2023-11-14 22:13:20",
            "size": 2147483648,
            "seeders": 12,
            "leechers": 3,
            "snatches": 99,
            "fileCount": 5,
            "codec": "x264",
            "source": "Blu-ray",
            "resolution": "1080p",
            "container": "MKV",
            "processing": "",
            "freeType": "12",
            "isPersonalFreeleech": false,
            "fileName": "the.movie.2020.1080p.mkv.torrent"
          },
          {
            "torrentId": 4002,
            "time": "2023-11-15 08:00:00",
            "size": 1073741824,
            "seeders": 1,
            "leechers": 0,
            "snatches": 3,
            "fileCount": 1,
            "codec": "",
            "freeType": "",
            "isPersonalFreeleech": true,
            "fileName": "personal.mkv"
          },
          {
            "torrentId": 4003,
            "time": "2023-11-16 09:00:00",
            "size": 1073741824,
            "seeders": 2,
            "leechers": 1,
            "snatches": 0,
            "fileCount": 1,
            "codec": "HEVC",
            "freeType": "2",
            "isPersonalFreeleech": false,
            "fileName": "neutral.mkv"
          }
        ]
      }
    ]
  }
}`

// gpwSite is a fake Gazelle tracker for GreatPosterWall tests.
type gpwSite struct {
	*httptest.Server

	mu         sync.Mutex
	loginPosts int
	hits       []string
	seq        int
	sessions   map[string]bool
}

func newGPWSite(t *testing.T, loginOK bool) *gpwSite {
	t.Helper()
	s := &gpwSite{sessions: map[string]bool{}}
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
			_, _ = fmt.Fprint(w, gpwBrowseFixture)
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

func (s *gpwSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestGPW(t *testing.T, site *gpwSite) *GreatPosterWall {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewGreatPosterWall(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewGreatPosterWall: %v", err)
	}
	g := ix.(*GreatPosterWall)
	g.SiteLinkValue = site.URL + "/"
	g.DefaultSiteLink = site.URL + "/"
	return g
}

func TestGreatPosterWallCaps(t *testing.T) {
	ix, err := NewGreatPosterWall(services.Deps{})
	if err != nil {
		t.Fatalf("NewGreatPosterWall: %v", err)
	}
	caps := ix.Capabilities()
	wantMovie := []string{models.MovieParamQ, models.MovieParamImdbID, models.MovieParamGenre}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	got := caps.Categories.MapTrackerCatToNewznab("1")
	// AddCategoryMapping with a desc also creates a custom 1:1 category
	// (id ≥ 100000) that rides along in the mapping — upstream parity.
	found := false
	for _, id := range got {
		if id == models.CatMovies.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", "1", got, models.CatMovies.ID)
	}
}

func TestGreatPosterWallConfigForm(t *testing.T) {
	ix, err := NewGreatPosterWall(services.Deps{})
	if err != nil {
		t.Fatalf("NewGreatPosterWall: %v", err)
	}
	g := ix.(*GreatPosterWall)
	items, err := g.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{
		"sitelink", "username", "password", "cookiehint", "cookie",
		"usefreeleechtokenswhenavailable", "searchfreeleechonly", "",
		"usethefirsttorrentfilenameasthetitle", "accountinactivity", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
	// the showFilename dynamic defaults to false (upstream { Value = false })
	showFilename, ok := g.Config.GetDynamic("showFilename").(*configdata.BoolItem)
	if !ok || showFilename.Value {
		t.Fatalf("showFilename dynamic: %v %v", ok, showFilename)
	}
}

func TestGreatPosterWallImdbSearchUsesSearchstr(t *testing.T) {
	site := newGPWSite(t, true)
	g := newTestGPW(t, site)

	status, err := g.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
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

	q := models.NewTorznabQuery()
	q.QueryType = "movie"
	q.ImdbID = "tt0112471"
	res, err := g.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 3 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	// GPW moves the imdb id into the search term: no cataloguenumber/taglist
	hit := site.paths()[0]
	if !strings.Contains(hit, "searchstr=tt0112471") ||
		strings.Contains(hit, "cataloguenumber") || strings.Contains(hit, "taglist") {
		t.Fatalf("search url: %s", hit)
	}
}

func TestGreatPosterWallParse(t *testing.T) {
	site := newGPWSite(t, true)
	g := newTestGPW(t, site)

	_, err := g.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}

	res, err := g.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "movie",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 3 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	// the Gazelle base sorts by PublishDate descending: Nov 16, Nov 15, Nov 14
	r := res.Releases[0]
	// Nov 16 torrent: freeType "2" → neutral leech (DVF 0 + UVF 0)
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 || r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 0 {
		t.Fatalf("neutral leech factors: %v/%v", r.DownloadVolumeFactor, r.UploadVolumeFactor)
	}
	if r.Title != "The Movie & Its Friends [2020] HEVC" {
		t.Fatalf("title: %q", r.Title)
	}

	// Nov 15 torrent: personal freeleech overrides (no freeType) → DVF 0
	r2 := res.Releases[1]
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 0 {
		t.Fatalf("personal freeleech dvf: %v", r2.DownloadVolumeFactor)
	}
	if r2.UploadVolumeFactor == nil || *r2.UploadVolumeFactor != 1 {
		t.Fatalf("personal freeleech uvf: %v", r2.UploadVolumeFactor)
	}
	if r2.Title != "The Movie & Its Friends [2020]" {
		t.Fatalf("r2 title: %q", r2.Title)
	}

	// Nov 14 torrent: the fully-populated row
	r3 := res.Releases[2]
	// title override: groupName (html decoded) + [year] + codec flags
	if r3.Title != "The Movie & Its Friends [2020] x264 / Blu-ray / 1080p / MKV" {
		t.Fatalf("r3 title: %q", r3.Title)
	}
	if r3.Description != "Subtitle Name" {
		t.Fatalf("description: %q", r3.Description)
	}
	if r3.Size == nil || *r3.Size != 2147483648 {
		t.Fatalf("size: %v", r3.Size)
	}
	if r3.Seeders == nil || *r3.Seeders != 12 || r3.Peers == nil || *r3.Peers != 15 {
		t.Fatalf("seeders/peers: %v/%v", r3.Seeders, r3.Peers)
	}
	if r3.Grabs == nil || *r3.Grabs != 99 {
		t.Fatalf("grabs: %v", r3.Grabs)
	}
	// freeType "12" → 50% download
	if r3.DownloadVolumeFactor == nil || *r3.DownloadVolumeFactor != 0.5 {
		t.Fatalf("dvf: %v", r3.DownloadVolumeFactor)
	}
	if r3.UploadVolumeFactor == nil || *r3.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r3.UploadVolumeFactor)
	}
	if r3.DoubanId == nil || *r3.DoubanId != 1234567 {
		t.Fatalf("douban: %v", r3.DoubanId)
	}
	if r3.Imdb == nil || *r3.Imdb != 112471 {
		t.Fatalf("imdb: %v", r3.Imdb)
	}
	if r3.MinimumRatio == nil || *r3.MinimumRatio != 1 || r3.MinimumSeedTime == nil || *r3.MinimumSeedTime != 172800 {
		t.Fatalf("minimum ratio/seed time: %v/%v", r3.MinimumRatio, r3.MinimumSeedTime)
	}
	found := false
	for _, c := range r3.Category {
		if c == models.CatMovies.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v, want 2000 included", r3.Category)
	}
	// Chinese time (+08:00) converted to UTC
	if r3.PublishDate.Format("2006-01-02 15:04:05") != "2023-11-14 14:13:20" || r3.PublishDate.Location() != time.UTC {
		t.Fatalf("publish date: %v", r3.PublishDate)
	}
	if r3.Details == nil || !strings.HasSuffix(r3.Details.String(), "/torrents.php?torrentid=4001") {
		t.Fatalf("details: %v", r3.Details)
	}
	if r3.Link == nil || !strings.HasSuffix(r3.Link.String(), "/torrents.php?action=download&id=4001") {
		t.Fatalf("link: %v", r3.Link)
	}
}

func TestGreatPosterWallShowFilenameOption(t *testing.T) {
	site := newGPWSite(t, true)
	g := newTestGPW(t, site)

	_, err := g.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	showFilename := g.Config.GetDynamic("showFilename").(*configdata.BoolItem)
	showFilename.Value = true

	res, err := g.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "movie",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// every release title is replaced by the torrent filename
	want := map[string]bool{
		"the.movie.2020.1080p.mkv.torrent": true,
		"personal.mkv":                     true,
		"neutral.mkv":                      true,
	}
	for _, r := range res.Releases {
		if !want[r.Title] {
			t.Fatalf("title: %q", r.Title)
		}
		delete(want, r.Title)
	}
	if len(want) != 0 {
		t.Fatalf("missing titles: %v", want)
	}
}

func TestGreatPosterWallLoginError(t *testing.T) {
	site := newGPWSite(t, false)
	g := newTestGPW(t, site)

	_, err := g.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "wrong"},
	})
	if err == nil || err.Error() != "Invalid username or password." {
		t.Fatalf("err: %v", err)
	}
	if g.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}
