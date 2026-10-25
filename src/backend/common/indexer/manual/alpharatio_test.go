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

const alphaRatioBrowseFixture = `{
  "status": "success",
  "response": {
    "results": [
      {
        "groupId": 100,
        "groupName": "Some Show S01",
        "artist": "",
        "cover": "static/common/covers/100.jpg",
        "tags": ["comedy", "tt1234567"],
        "groupTime": "1699968000",
        "groupYear": "2023",
        "releaseType": "Unknown",
        "torrents": [
          {
            "torrentId": 2001,
            "time": "2023-11-14 22:13:20",
            "size": 2147483648,
            "seeders": 12,
            "leechers": 3,
            "snatches": 99,
            "fileCount": 5,
            "media": "WEB-DL",
            "resolution": "1080p",
            "container": "MKV",
            "codec": "x264",
            "category": "TvHD",
            "isFreeleech": false,
            "isPersonalFreeleech": false,
            "isNeutralLeech": false
          }
        ]
      }
    ]
  }
}`

// alphaRatioSite is a fake Gazelle tracker for AlphaRatio tests.
type alphaRatioSite struct {
	*httptest.Server

	mu         sync.Mutex
	loginPosts int
	hits       []string
	seq        int
	sessions   map[string]bool
}

func newAlphaRatioSite(t *testing.T, loginOK bool) *alphaRatioSite {
	t.Helper()
	s := &alphaRatioSite{sessions: map[string]bool{}}
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
			_, _ = fmt.Fprint(w, alphaRatioBrowseFixture)
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

func (s *alphaRatioSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestAlphaRatio(t *testing.T, site *alphaRatioSite) *AlphaRatio {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewAlphaRatio(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewAlphaRatio: %v", err)
	}
	a := ix.(*AlphaRatio)
	a.SiteLinkValue = site.URL + "/"
	a.DefaultSiteLink = site.URL + "/"
	return a
}

func TestAlphaRatioCaps(t *testing.T) {
	ix, err := NewAlphaRatio(services.Deps{})
	if err != nil {
		t.Fatalf("NewAlphaRatio: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{models.MovieParamQ, models.MovieParamImdbID}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 5030}, {"2", 5040}, {"3", 5045}, {"4", 5030}, {"5", 5030},
		{"6", 5040}, {"7", 5045}, {"8", 2030}, {"9", 2040}, {"10", 2045},
		{"11", 2030}, {"12", 2040}, {"13", 2045}, {"14", 6000}, {"15", 2050},
		{"16", 5070}, {"17", 5070}, {"18", 4050}, {"19", 1040}, {"20", 1180},
		{"21", 1030}, {"22", 4010}, {"23", 4030}, {"24", 4010}, {"25", 4040},
		{"26", 6000}, {"27", 7000}, {"28", 3030}, {"29", 3050}, {"30", 8000},
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

func TestAlphaRatioConfigForm(t *testing.T) {
	ix, err := NewAlphaRatio(services.Deps{})
	if err != nil {
		t.Fatalf("NewAlphaRatio: %v", err)
	}
	a := ix.(*AlphaRatio)
	items, err := a.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids, types []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
		types = append(types, it["type"].(string))
	}
	want := []string{
		"sitelink", "username", "password",
		"usefreeleechtokenswhenavailable", "searchfreeleechonly",
		"", "accountinactivity", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
	// the dynamic "Account Inactivity" item is a DisplayInfo
	if types[6] != "displayinfo" {
		t.Fatalf("accountinactivity type = %q", types[6])
	}
}

func TestAlphaRatioSearchURL(t *testing.T) {
	ix, err := NewAlphaRatio(services.Deps{})
	if err != nil {
		t.Fatalf("NewAlphaRatio: %v", err)
	}
	a := ix.(*AlphaRatio)

	q := models.NewTorznabQuery()
	q.SearchTerm = "Mr. Robot"
	q.Categories = []int{models.CatTVHD.ID}
	u := a.SearchURL(q)
	if !strings.HasPrefix(u, "https://alpharatio.cc/ajax.php?action=browse&order_by=time&order_way=desc&") {
		t.Fatalf("url = %s", u)
	}
	// dots are replaced by spaces AFTER encoding: "Mr.+Robot" -> "Mr +Robot"
	if !strings.Contains(u, "searchstr=Mr +Robot") {
		t.Fatalf("searchstr = %s", u)
	}
	if !strings.Contains(u, "filter_cat[2]=1") {
		t.Fatalf("filter_cat missing: %s", u)
	}

	// imdbInTags: imdb id goes to taglist, not cataloguenumber
	q2 := models.NewTorznabQuery()
	q2.ImdbID = "tt1234567"
	u2 := a.SearchURL(q2)
	if !strings.Contains(u2, "taglist=tt1234567") || strings.Contains(u2, "cataloguenumber") {
		t.Fatalf("imdbInTags url = %s", u2)
	}

	// freeleech-only config adds freetorrent=1
	a.Config.FreeleechOnly.Value = true
	if u3 := a.SearchURL(models.NewTorznabQuery()); !strings.Contains(u3, "freetorrent=1") {
		t.Fatalf("freeleech url = %s", u3)
	}
}

func TestAlphaRatioLoginAndParse(t *testing.T) {
	site := newAlphaRatioSite(t, true)
	a := newTestAlphaRatio(t, site)

	status, err := a.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status = %v, want RequiresTesting", status)
	}
	if !a.IsConfigured() {
		t.Fatal("expected configured after login")
	}

	res, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "show",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	if r.Title != "Some Show S01 [2023] WEB-DL / 1080p / MKV / x264" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Size == nil || *r.Size != 2147483648 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 12 || r.Peers == nil || *r.Peers != 15 {
		t.Fatalf("seeders/peers: %v/%v", r.Seeders, r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 99 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Files == nil || *r.Files != 5 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.PublishDate.Format("2006-01-02 15:04:05") != "2023-11-14 22:13:20" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	found := false
	for _, c := range r.Category {
		if c == models.CatTVHD.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v, want 5040 included", r.Category)
	}
	if r.Details == nil || r.Details.String() != site.URL+"/torrents.php?torrentid=2001" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Link == nil || r.Link.String() != site.URL+"/torrents.php?action=download&id=2001" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Poster == nil || r.Poster.String() != site.URL+"/static/common/covers/100.jpg" {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.Imdb == nil || *r.Imdb != 1234567 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	if r.Description != "Tags: comedy, tt1234567\n" {
		t.Fatalf("description: %q", r.Description)
	}
	// upstream PerformQuery post-parse stamps
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 259200 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}

	paths := site.paths()
	if len(paths) != 1 || !strings.HasPrefix(paths[0], "/ajax.php?action=browse&order_by=time&order_way=desc&") ||
		!strings.Contains(paths[0], "searchstr=show") {
		t.Fatalf("paths: %v", paths)
	}
}

func TestAlphaRatioLoginError(t *testing.T) {
	site := newAlphaRatioSite(t, false)
	a := newTestAlphaRatio(t, site)

	_, err := a.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "wrong"},
	})
	if err == nil || err.Error() != "Invalid username or password." {
		t.Fatalf("err: %v", err)
	}
	if a.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}
