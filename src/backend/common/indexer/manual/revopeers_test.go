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
	"vestt/common/utils/parseutil"
)

// revoPeersBrowseFixture is a #torrents-table browse page; the first row is
// the header (skipped upstream via rows.Skip(1)).
const revoPeersBrowseFixture = `<html><body>
<table id="torrents-table">
<tbody>
<tr><th>Type</th><th></th><th></th><th>Name</th><th></th><th>Added</th><th>Size</th><th>Snatched</th><th>Seeders</th><th>Leechers</th></tr>
<tr>
  <td class="br_type"><a href="browse.php?cat=12">Movies/HD</a></td>
  <td></td>
  <td></td>
  <td><a href="download.php?id=401">DL</a></td>
  <td></td>
  <td><nobr>2024-03-02 14:25:36</nobr></td>
  <td>1.46 GB<br><a href="details.php?id=401&amp;filelist=1">12</a></td>
  <td>25</td>
  <td>12</td>
  <td>3</td>
  <td class="br_right"><a href="details.php?id=401"><b>Some Movie 2024 1080p</b></a> <a href="http://www.imdb.com/title/tt1234567">imdb</a></td>
</tr>
<tr>
  <td class="br_type"><a href="browse.php?cat=7">TV/XViD</a></td>
  <td></td>
  <td></td>
  <td>support banner, no link</td>
  <td></td>
  <td><nobr>2024-03-01 10:00:00</nobr></td>
  <td>700 MB</td>
  <td>1</td>
  <td>1</td>
  <td>0</td>
  <td class="br_right"><a href="details.php?id=402"><b>Banner</b></a></td>
</tr>
<tr>
  <td class="br_type"><a href="browse.php?cat=7">TV/XViD</a></td>
  <td></td>
  <td></td>
  <td><a href="download.php?id=403">DL</a></td>
  <td></td>
  <td><nobr>2024-02-28 08:00:00</nobr></td>
  <td>350 MB<br><a href="details.php?id=403&amp;filelist=1">3</a></td>
  <td>5</td>
  <td>2</td>
  <td>1</td>
  <td class="br_right"><a href="details.php?id=403"><b>[REQ] Fulfilled Show S01</b></a></td>
</tr>
</tbody>
</table>
</body></html>`

// revoPeersSite is a fake RevoPeers site for tests.
type revoPeersSite struct {
	*httptest.Server

	mu         sync.Mutex
	hits       []string
	loginPosts int
	loginOK    bool
}

func newRevoPeersSite(t *testing.T, loginOK bool) *revoPeersSite {
	t.Helper()
	s := &revoPeersSite{loginOK: loginOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.hits = append(s.hits, r.URL.RequestURI())
		switch r.URL.Path {
		case "/login.php":
			w.Header().Add("Set-Cookie", "PHPSESSID=rvseed; path=/")
			_, _ = fmt.Fprint(w, `<html><body>login form</body></html>`)
		case "/takelogin.php":
			s.loginPosts++
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if s.loginOK && r.PostForm.Get("username") == "okuser" && r.PostForm.Get("password") == "okpass" {
				w.Header().Add("Set-Cookie", "uid=rv1; path=/")
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, `<html><body>welcome <a href="/logout.php">logout</a></body></html>`)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body>login failed</body></html>`)
		case "/browse.php":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, revoPeersBrowseFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *revoPeersSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestRevoPeers(t *testing.T, site *revoPeersSite) *RevoPeers {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewRevoPeers(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewRevoPeers: %v", err)
	}
	r := ix.(*RevoPeers)
	r.SiteLinkValue = site.URL + "/"
	r.DefaultSiteLink = site.URL + "/"
	client.SetRequestDelay(r.ID(), 0)
	return r
}

func TestRevoPeersCaps(t *testing.T) {
	ix, err := NewRevoPeers(services.Deps{})
	if err != nil {
		t.Fatalf("NewRevoPeers: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q,imdbid" {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if strings.Join(caps.BookSearchParams, ",") != "q" {
		t.Fatalf("BookSearchParams: %v", caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"23", 5070}, {"1", 4020}, {"51", 2045}, {"12", 2040},
		{"70", 5045}, {"47", 6010}, {"52", 5040},
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

func TestRevoPeersConfigForm(t *testing.T) {
	ix, err := NewRevoPeers(services.Deps{})
	if err != nil {
		t.Fatalf("NewRevoPeers: %v", err)
	}
	r := ix.(*RevoPeers)
	items, err := r.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{"sitelink", "username", "password", "", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
}

func TestRevoPeersApplyConfigurationAndSearch(t *testing.T) {
	site := newRevoPeersSite(t, true)
	r := newTestRevoPeers(t, site)

	status, err := r.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	// upstream quirk: Completed (not RequiresTesting)
	if status != indexer.ConfigCompleted {
		t.Fatalf("status = %v, want Completed", status)
	}
	if !r.IsConfigured() {
		t.Fatal("expected configured")
	}
	if site.loginPosts != 1 {
		t.Fatalf("loginPosts: %d", site.loginPosts)
	}

	// the query must cover both fixture categories: the upstream base drops
	// releases whose categories don't intersect the expanded query categories
	// (BaseIndexer.cs:174-183 FilterResults)
	res, err := r.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "some movie",
		Categories: []int{models.CatMoviesHD.ID, models.CatTVSD.ID},
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// 3 rows: header skipped, banner skipped (no download link) → 2 releases
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r0 := res.Releases[0]
	if r0.Title != "Some Movie 2024 1080p" {
		t.Fatalf("title: %q", r0.Title)
	}
	if r0.Guid == nil || r0.Guid.String() != site.URL+"/details.php?id=401" {
		t.Fatalf("guid: %v", r0.Guid)
	}
	if r0.Link == nil || r0.Link.String() != site.URL+"/download.php?id=401" {
		t.Fatalf("link: %v", r0.Link)
	}
	foundHD := false
	for _, c := range r0.Category {
		if c == models.CatMoviesHD.ID {
			foundHD = true
		}
	}
	if !foundHD {
		t.Fatalf("category: %v, want 2040 included", r0.Category)
	}
	if r0.PublishDate != time.Date(2024, 3, 2, 14, 25, 36, 0, time.Local) {
		t.Fatalf("publish date: %v", r0.PublishDate)
	}
	wantSize := parseutil.GetBytes("1.46 GB")
	if r0.Size == nil || *r0.Size != wantSize {
		t.Fatalf("size: %v, want %d", r0.Size, wantSize)
	}
	if r0.Files == nil || *r0.Files != 12 {
		t.Fatalf("files: %v", r0.Files)
	}
	if r0.Grabs == nil || *r0.Grabs != 25 {
		t.Fatalf("grabs: %v", r0.Grabs)
	}
	if r0.Seeders == nil || *r0.Seeders != 12 || r0.Peers == nil || *r0.Peers != 15 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.Imdb == nil || *r0.Imdb != 1234567 {
		t.Fatalf("imdb: %v", r0.Imdb)
	}
	if r0.MinimumRatio == nil || *r0.MinimumRatio != 1 {
		t.Fatalf("minimum ratio: %v", r0.MinimumRatio)
	}
	if r0.MinimumSeedTime == nil || *r0.MinimumSeedTime != 172800 {
		t.Fatalf("minimum seed time: %v", r0.MinimumSeedTime)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}
	if r0.UploadVolumeFactor == nil || *r0.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r0.UploadVolumeFactor)
	}

	// [REQ] prefix stripped
	r1 := res.Releases[1]
	if r1.Title != "Fulfilled Show S01" {
		t.Fatalf("title 2: %q", r1.Title)
	}
	if r1.Imdb != nil {
		t.Fatalf("imdb 2 must be nil: %v", r1.Imdb)
	}
	foundSD := false
	for _, c := range r1.Category {
		if c == models.CatTVSD.ID {
			foundSD = true
		}
	}
	if !foundSD {
		t.Fatalf("category 2: %v, want 5030 included", r1.Category)
	}

	paths := site.paths()
	// "/login.php", "/takelogin.php", then the browse request
	if len(paths) != 3 {
		t.Fatalf("paths: %v", paths)
	}
	want := "/browse.php?incldead=1&titleonly=1&search=some+movie&c12=1&c11=1&c41=1&c7=1"
	if paths[2] != want {
		t.Fatalf("search path:\n  got:  %v\n want: %v", paths[2], want)
	}
}

func TestRevoPeersImdbSearch(t *testing.T) {
	site := newRevoPeersSite(t, true)
	r := newTestRevoPeers(t, site)

	if _, err := r.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "movie", ImdbID: "tt7654321",
	}, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 1 || paths[0] != "/browse.php?incldead=1&titleonly=0&search=tt7654321" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestRevoPeersLoginFailure(t *testing.T) {
	site := newRevoPeersSite(t, false)
	r := newTestRevoPeers(t, site)

	_, err := r.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "baduser"},
		{ID: "password", Value: "badpass"},
	})
	if err == nil ||
		!strings.Contains(err.Error(), "Login failed! Check the username and password. If they are ok, try logging on the website.") {
		t.Fatalf("err: %v", err)
	}
	if r.IsConfigured() {
		t.Fatal("must not be configured after login failure")
	}
}

func TestRevoPeersParseError(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// upstream rows.Skip(1) (RevoPeers.cs:181) treats the first row as the
		// header, so a junk row only errors when it comes after one
		_, _ = fmt.Fprint(w, `<html><body><table id="torrents-table"><tbody>`+
			`<tr><th>Name</th></tr>`+
			`<tr><td>junk</td></tr>`+
			`</tbody></table></body></html>`)
	}))
	t.Cleanup(site.Close)
	r := newTestRevoPeers(t, &revoPeersSite{Server: site})

	_, err := r.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err == nil || !strings.Contains(err.Error(), "Parse error") {
		t.Fatalf("err: %v", err)
	}
}
