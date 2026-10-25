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
	"vestt/common/models"
	"vestt/common/services"
)

const immortalSeedBrowseHTML = `<html><body><a href="logout.php">Log out</a>
<table id="sortabletable">
<tr>
  <td><a href="browse.php?category=16">Movies HD</a></td>
  <td>
    <div><img src="/covers/1.jpg"/></div>
    <div>hover <div class="tooltip-content"><div>Full Movie Title</div><div>Action | Drama</div></div></div>
    <div>Uploaded: 2021-03-17 03:39 AM</div>
  </td>
  <td>x</td>
  <td>x</td>
  <td>1.5 GB</td>
  <td>25</td>
  <td>10</td>
  <td>5</td>
  <td>
    <div><a href="details.php?id=99">Short</a></div>
    <img title="Free Torrent - 24 Hours" src="free.png"/>
    <a href="download.php?id=99&amp;key=k1">DL</a>
  </td>
</tr>
<tr>
  <td><a href="browse.php?category=8">TV HD</a></td>
  <td>
    <div><div class="tooltip-content"><div>Second Title</div><div>Comedy</div></div></div>
    <div>Uploaded: 2022-06-01 11:30 PM</div>
  </td>
  <td>x</td>
  <td>x</td>
  <td>700 MB</td>
  <td>3</td>
  <td>2</td>
  <td>1</td>
  <td>
    <div><a href="details.php?id=100">Short2</a></div>
    <img title="Silver Torrent" src="silver.png"/>
    <img title="x2 Torrent" src="x2.png"/>
    <a href="download.php?id=100&amp;key=k2">DL</a>
  </td>
</tr>
</table>
</body></html>`

type immortalSeedSite struct {
	*httptest.Server

	mu          sync.Mutex
	hits        []string
	loginPosts  int
	seq         int
	validCookie string
}

func newImmortalSeedSite(t *testing.T, loginOK bool) *immortalSeedSite {
	t.Helper()
	s := &immortalSeedSite{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/takelogin.php":
			s.loginPosts++
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if loginOK && r.PostForm.Get("username") == "okuser" && r.PostForm.Get("password") == "okpass" {
				s.seq++
				s.validCookie = fmt.Sprintf("uid=%d", s.seq)
				w.Header().Add("Set-Cookie", s.validCookie+"; path=/")
				http.Redirect(w, r, "/index.php", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body><div id="main"><table><tr><td>ERROR: bad credentials</td></tr></table></div></body></html>`)
		case "/index.php":
			if s.validCookie != "" {
				w.Header().Add("Set-Cookie", s.validCookie+"; path=/")
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body><a href="logout.php">Log out</a></body></html>`)
		case "/browse.php":
			s.hits = append(s.hits, r.URL.RequestURI())
			w.Header().Set("Content-Type", "text/html")
			if s.validCookie != "" && strings.Contains(r.Header.Get("Cookie"), s.validCookie) {
				_, _ = fmt.Fprint(w, immortalSeedBrowseHTML)
				return
			}
			// session expired: no logout.php → the indexer re-logins
			_, _ = fmt.Fprint(w, `<html><body><a href="login.php">Login</a></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *immortalSeedSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *immortalSeedSite) expireSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.validCookie = ""
}

func newTestImmortalSeed(t *testing.T, site *immortalSeedSite) *ImmortalSeed {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewImmortalSeed(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewImmortalSeed: %v", err)
	}
	i := ix.(*ImmortalSeed)
	i.SiteLinkValue = site.URL + "/"
	i.DefaultSiteLink = site.URL + "/"
	return i
}

func TestImmortalSeedCaps(t *testing.T) {
	ix, err := NewImmortalSeed(services.Deps{})
	if err != nil {
		t.Fatalf("NewImmortalSeed: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{models.MovieParamQ}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"16", 2040}, {"8", 5040}, {"7", 5060}, {"22", 7020}, {"3", 8000},
		{"32", 5070}, {"37", 3040}, {"25", 4050}, {"59", 2045},
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

func TestImmortalSeedConfigForm(t *testing.T) {
	ix, err := NewImmortalSeed(services.Deps{})
	if err != nil {
		t.Fatalf("NewImmortalSeed: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{
		"sitelink", "username", "password", "filterfreeleechonly",
		"sortby", "order", "accountinactivity", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
	// the sort select carries the upstream options in declaration order
	for _, it := range items {
		if it["id"].(string) == "sortby" {
			opts := it["options"].(map[string]any)
			if opts["added"] != "created" || opts["seeders"] != "seeders" ||
				opts["size"] != "size" || opts["name"] != "title" {
				t.Fatalf("sortby options: %v", opts)
			}
		}
	}
}

func TestImmortalSeedLoginAndParse(t *testing.T) {
	site := newImmortalSeedSite(t, true)
	i := newTestImmortalSeed(t, site)

	status, err := i.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting || !i.IsConfigured() {
		t.Fatalf("status/configured: %v %v", status, i.IsConfigured())
	}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some.Show"
	q.Categories = []int{models.CatMoviesHD.ID}
	res, err := i.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// the Base category gate drops the TV row for a Movies-only query
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r := res.Releases[0]
	if r.Title != "Full Movie Title" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Description != "Action,Drama" {
		t.Fatalf("description: %q", r.Description)
	}
	if strings.Join(r.Genres, ",") != "Action,Drama" {
		t.Fatalf("genres: %v", r.Genres)
	}
	// Free Torrent image → 100% freeleech
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.Size == nil || *r.Size != 1610612736 { // 1.5 GiB
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 10 || r.Peers == nil || *r.Peers != 15 {
		t.Fatalf("seeders/peers: %v/%v", r.Seeders, r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 25 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	found := false
	for _, c := range r.Category {
		if c == models.CatMoviesHD.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v, want 2040 included", r.Category)
	}
	if r.Poster == nil || !strings.HasSuffix(r.Poster.String(), "/covers/1.jpg") {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.PublishDate.Format("2006-01-02 15:04") != "2021-03-17 03:39" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.String(), "/download.php?id=99&key=k1") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.String(), "/details.php?id=99") {
		t.Fatalf("details: %v", r.Details)
	}

	// request generation: keyword collapsing ([ -._]+ → space) + selectedcats2
	hits := site.paths()
	last := hits[len(hits)-1]
	if !strings.HasPrefix(last, "/browse.php?category=0&include_dead_torrents=yes&sort=added&order=desc&") {
		t.Fatalf("search url: %s", last)
	}
	if !strings.Contains(last, "do=search") || !strings.Contains(last, "keywords=Some+Show") ||
		!strings.Contains(last, "search_type=t_name") || !strings.Contains(last, "selectedcats2=16") {
		t.Fatalf("search params: %s", last)
	}

	// uncategorized query returns both rows
	res2, err := i.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res2.Releases) != 2 {
		t.Fatalf("releases: %d", len(res2.Releases))
	}
	r2 := res2.Releases[1]
	if r2.Title != "Second Title" {
		t.Fatalf("r2 title: %q", r2.Title)
	}
	// Silver Torrent → 50% download; x2 Torrent → double upload
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 0.5 {
		t.Fatalf("r2 dvf: %v", r2.DownloadVolumeFactor)
	}
	if r2.UploadVolumeFactor == nil || *r2.UploadVolumeFactor != 2 {
		t.Fatalf("r2 uvf: %v", r2.UploadVolumeFactor)
	}
	foundTV := false
	for _, c := range r2.Category {
		if c == models.CatTVHD.ID {
			foundTV = true
		}
	}
	if !foundTV {
		t.Fatalf("r2 category: %v, want 5040 included", r2.Category)
	}
	if r2.Poster != nil {
		t.Fatalf("r2 poster must be nil: %v", r2.Poster)
	}
	if r2.Size == nil || *r2.Size != 700*1024*1024 {
		t.Fatalf("r2 size: %v", r2.Size)
	}
}

func TestImmortalSeedReloginOnExpiredSession(t *testing.T) {
	site := newImmortalSeedSite(t, true)
	i := newTestImmortalSeed(t, site)

	_, err := i.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}

	site.expireSessions()
	res, err := i.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	// one failed browse + the retry after the re-login
	if hits := site.paths(); len(hits) != 2 {
		t.Fatalf("browse hits: %v", hits)
	}
}

func TestImmortalSeedLoginError(t *testing.T) {
	site := newImmortalSeedSite(t, false)
	i := newTestImmortalSeed(t, site)

	_, err := i.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "wrong"},
	})
	if err == nil || err.Error() != "ERROR: bad credentials" {
		t.Fatalf("err: %v", err)
	}
	if i.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}

func TestImmortalSeedFreeleechOnlyFilter(t *testing.T) {
	site := newImmortalSeedSite(t, true)
	i := newTestImmortalSeed(t, site)

	_, err := i.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	i.cfg.Freeleech.Value = true

	res, err := i.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if *res.Releases[0].DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", res.Releases[0].DownloadVolumeFactor)
	}
}

func TestImmortalSeedDateFallback(t *testing.T) {
	// a row whose date cell is empty → PublishDate = DateTime.Now
	html := `<html><body><a href="logout.php"></a><table id="sortabletable"><tr>
  <td><a href="browse.php?category=14">Movies SD</a></td>
  <td><div></div><div>   </div></td>
  <td>x</td><td>x</td><td>500 MB</td><td>1</td><td>1</td><td>0</td>
  <td><div><a href="details.php?id=1">t</a></div><a href="download.php?id=1">d</a></td>
</tr></table></body></html>`
	ix, err := NewImmortalSeed(services.Deps{})
	if err != nil {
		t.Fatalf("NewImmortalSeed: %v", err)
	}
	releases, err := ix.(*ImmortalSeed).parseBrowse(html)
	if err != nil {
		t.Fatalf("parseBrowse: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases: %d", len(releases))
	}
	if age := time.Since(releases[0].PublishDate); age < 0 || age > time.Minute {
		t.Fatalf("publish date should fall back to now: %v", releases[0].PublishDate)
	}
}
