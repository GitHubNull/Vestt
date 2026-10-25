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

// torrentBytesBrowseFixture is an iso-8859-1 browse page; the \xE9 byte is a
// Latin-1 "é".
const torrentBytesBrowseFixture = `<html><body>
<table>
<tbody>
<tr><td class="colhead">Type</td><td class="colhead">Name</td><td class="colhead">Files</td><td class="colhead"></td><td class="colhead">Added</td><td class="colhead"></td><td class="colhead">Size</td><td class="colhead">Snatched</td><td class="colhead">Seeders</td><td class="colhead">Leechers</td></tr>
<tr>
  <td><a href="browse.php?cat=5"><img src="pic/cat5.gif" alt="Movies/HD"></a></td>
  <td><a href="download.php?id=301">[DL]</a><a href="details.php?id=301" title="Some Release 2024 1080p">Some Release 2024 1080p</a></td>
  <td>1</td>
  <td></td>
  <td>2024-03-02 14:25:36</td>
  <td></td>
  <td>1.46 GB</td>
  <td>25</td>
  <td>12</td>
  <td>3</td>
  <td><font color="green">FL</font></td>
</tr>
<tr>
  <td><a href="browse.php?cat=33"><img src="pic/cat33.gif" alt="TV/SD"></a></td>
  <td><a href="download.php?id=302">[DL]</a><a href="details.php?id=302">Caf` + "\xE9" + ` Show S01E01</a></td>
  <td>7</td>
  <td></td>
  <td>2024-03-01 10:00:00</td>
  <td></td>
  <td>700 MB</td>
  <td>----</td>
  <td>5</td>
  <td>1</td>
</tr>
</tbody>
</table>
</body></html>`

// torrentBytesSite is a fake TorrentBytes site for tests.
type torrentBytesSite struct {
	*httptest.Server

	mu         sync.Mutex
	hits       []string
	loginPosts []map[string]string
	loginOK    bool
}

func newTorrentBytesSite(t *testing.T, loginOK bool) *torrentBytesSite {
	t.Helper()
	s := &torrentBytesSite{loginOK: loginOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.hits = append(s.hits, r.URL.RequestURI())
		switch r.URL.Path {
		case "/":
			w.Header().Add("Set-Cookie", "PHPSESSID=tbseed; path=/")
			_, _ = fmt.Fprint(w, `<html><body>login form</body></html>`)
		case "/takelogin.php":
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			s.loginPosts = append(s.loginPosts, map[string]string{
				"username": r.PostForm.Get("username"),
				"password": r.PostForm.Get("password"),
				"returnto": r.PostForm.Get("returnto"),
				"login":    r.PostForm.Get("login"),
			})
			if s.loginOK && r.PostForm.Get("username") == "okuser" && r.PostForm.Get("password") == "okpass" {
				w.Header().Add("Set-Cookie", "uid=tb1; path=/")
				w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
				_, _ = fmt.Fprint(w, `<html><body>welcome <a href="/my.php">my</a></body></html>`)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
			_, _ = fmt.Fprint(w, `<html><body><table><tr><td class="embedded">Login failed!</td></tr></table></body></html>`)
		case "/browse.php":
			w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
			_, _ = fmt.Fprint(w, torrentBytesBrowseFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *torrentBytesSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestTorrentBytes(t *testing.T, site *torrentBytesSite) *TorrentBytes {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewTorrentBytes(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewTorrentBytes: %v", err)
	}
	tb := ix.(*TorrentBytes)
	tb.SiteLinkValue = site.URL + "/"
	tb.DefaultSiteLink = site.URL + "/"
	client.SetRequestDelay(tb.ID(), 0)
	return tb
}

func TestTorrentBytesCaps(t *testing.T) {
	ix, err := NewTorrentBytes(services.Deps{})
	if err != nil {
		t.Fatalf("NewTorrentBytes: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{"q", "season", "ep", "imdbid"}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q,imdbid" {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"23", 5070}, {"52", 4030}, {"5", 2040}, {"49", 2045},
		{"19", 2030}, {"32", 5045}, {"39", 6040}, {"24", 6060},
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

func TestTorrentBytesConfigForm(t *testing.T) {
	ix, err := NewTorrentBytes(services.Deps{})
	if err != nil {
		t.Fatalf("NewTorrentBytes: %v", err)
	}
	tb := ix.(*TorrentBytes)
	items, err := tb.ConfigurationForSetup(context.Background())
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

func TestTorrentBytesApplyConfigurationAndSearch(t *testing.T) {
	site := newTorrentBytesSite(t, true)
	tb := newTestTorrentBytes(t, site)

	status, err := tb.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
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
	if !tb.IsConfigured() {
		t.Fatal("expected configured")
	}
	if len(site.loginPosts) != 1 {
		t.Fatalf("loginPosts: %d", len(site.loginPosts))
	}
	post := site.loginPosts[0]
	if post["username"] != "okuser" || post["password"] != "okpass" ||
		post["returnto"] != "/" || post["login"] != "Log in!" {
		t.Fatalf("login post: %v", post)
	}

	// TV/SD must be requested too: Base.FilterResults drops releases whose
	// categories don't intersect the query categories (upstream
	// BaseIndexer.cs:179-182) and the second row is a TV/SD release.
	res, err := tb.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "some movie",
		Categories: []int{models.CatMoviesHD.ID, models.CatTVSD.ID},
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r0 := res.Releases[0]
	if r0.Title != "Some Release 2024 1080p" {
		t.Fatalf("title: %q", r0.Title)
	}
	if r0.Description != r0.Title {
		t.Fatalf("description: %q", r0.Description)
	}
	if r0.Guid == nil || r0.Guid.String() != site.URL+"/details.php?id=301" {
		t.Fatalf("guid: %v", r0.Guid)
	}
	if r0.Details == nil || r0.Details.String() != r0.Guid.String() {
		t.Fatalf("details: %v", r0.Details)
	}
	if r0.Link == nil || r0.Link.String() != site.URL+"/download.php?id=301" {
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
	if r0.Seeders == nil || *r0.Seeders != 12 || r0.Peers == nil || *r0.Peers != 15 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.Files == nil || *r0.Files != 1 {
		t.Fatalf("files: %v", r0.Files)
	}
	if r0.Grabs == nil || *r0.Grabs != 25 {
		t.Fatalf("grabs: %v", r0.Grabs)
	}
	// green "FL" font → freeleech
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}
	if r0.UploadVolumeFactor == nil || *r0.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r0.UploadVolumeFactor)
	}

	r1 := res.Releases[1]
	// no title attribute → link text; iso-8859-1 é decoding
	if r1.Title != "Café Show S01E01" {
		t.Fatalf("title 2: %q", r1.Title)
	}
	// grabs "----" → null upstream
	if r1.Grabs != nil {
		t.Fatalf("grabs 2 must be nil: %v", r1.Grabs)
	}
	if r1.DownloadVolumeFactor == nil || *r1.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf 2: %v", r1.DownloadVolumeFactor)
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
	// "/", "/takelogin.php", then the browse request
	if len(paths) != 3 {
		t.Fatalf("paths: %v", paths)
	}
	want := "/browse.php?incldead=1&search=some+movie&sc=1&c5=1&c46=1&c33=1"
	if paths[2] != want {
		t.Fatalf("search path:\n  got:  %v\n want: %v", paths[2], want)
	}
}

func TestTorrentBytesImdbSearch(t *testing.T) {
	site := newTorrentBytesSite(t, true)
	tb := newTestTorrentBytes(t, site)

	if _, err := tb.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "movie", ImdbID: "tt1234567",
	}, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 1 || paths[0] != "/browse.php?incldead=1&search=tt1234567&sc=2" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestTorrentBytesLoginFailure(t *testing.T) {
	site := newTorrentBytesSite(t, false)
	tb := newTestTorrentBytes(t, site)

	_, err := tb.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "baduser"},
		{ID: "password", Value: "badpass"},
	})
	if err == nil || !strings.Contains(err.Error(), "Login failed!") {
		t.Fatalf("err: %v", err)
	}
	if tb.IsConfigured() {
		t.Fatal("must not be configured after login failure")
	}
}

func TestTorrentBytesParseError(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body><table><tbody><tr><td class="colhead">x</td></tr><tr><td>no links here</td></tr></tbody></table></body></html>`)
	}))
	t.Cleanup(site.Close)
	tb := newTestTorrentBytes(t, &torrentBytesSite{Server: site})

	_, err := tb.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err == nil || !strings.Contains(err.Error(), "Parse error") {
		t.Fatalf("err: %v", err)
	}
}
