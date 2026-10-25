package manual

import (
	"context"
	"errors"
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

type preToMeSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]string
}

func newPreToMeSite(t *testing.T, serve map[string]string) *preToMeSite {
	t.Helper()
	s := &preToMeSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		page, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *preToMeSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestPreToMe(t *testing.T, site *preToMeSite) *PreToMe {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewPreToMe(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewPreToMe: %v", err)
	}
	p := ix.(*PreToMe)
	p.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(p.ID(), 0)
	return p
}

// pretomeBrowsePage has two rows: a matching movie row and a row whose
// title must be dropped by the AND-filter.
const pretomeBrowsePage = `<html><body>
<table><tbody>
<tr class="browse">
  <td><a href="browse.php?cat[]=19&amp;tags=1080p">Movies/1080p</a></td>
  <td>x</td><td>x</td>
  <td>1</td>
  <td>x</td>
  <td>3 hours<br>ago</td>
  <td>x</td>
  <td>1.5 GB</td>
  <td>25</td>
  <td>12</td>
  <td>3</td>
  <td><a href="details.php?id=12345" title="Some Movie 2020 1080p">Some Movie</a></td>
  <td><a href="download.php/12345/some.torrent">DL</a></td>
</tr>
<tr class="browse">
  <td><a href="browse.php?cat[]=7&amp;tags=x264">TV/x264</a></td>
  <td>x</td><td>x</td>
  <td>2</td>
  <td>x</td>
  <td>1 week<br>ago</td>
  <td>x</td>
  <td>500 MB</td>
  <td>9</td>
  <td>4</td>
  <td>1</td>
  <td><a href="details.php?id=777" title="Completely Different Show S01E01">Different</a></td>
  <td><a href="download.php/777/other.torrent">DL</a></td>
</tr>
</tbody></table>
</body></html>`

func TestPreToMeCaps(t *testing.T) {
	ix, err := NewPreToMe(services.Deps{})
	if err != nil {
		t.Fatalf("NewPreToMe: %v", err)
	}
	caps := ix.Capabilities()
	for _, tc := range []struct {
		params []string
		want   []string
	}{
		{caps.TVSearchParams, []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}},
		{caps.MovieSearchParams, []string{models.MovieParamQ, models.MovieParamImdbID}},
		{caps.MusicSearchParams, []string{models.MusicParamQ}},
		{caps.BookSearchParams, []string{models.BookParamQ}},
	} {
		if strings.Join(tc.params, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("params: %v want %v", tc.params, tc.want)
		}
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"cat[]=22", 4000}, {"cat[]=22&tags=Windows", 4010}, {"cat[]=27", 7020},
		{"cat[]=4&tags=PC", 4050}, {"cat[]=19&tags=1080p", 2040},
		{"cat[]=19&tags=XviD", 2030}, {"cat[]=6&tags=FLAC", 3040},
		{"cat[]=52", 5060}, {"cat[]=7&tags=Documentary", 5080},
		{"cat[]=51&tags=2160p", 6045}, {"cat[]=31&tags=RARFiX", 8000},
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

func TestPreToMeConfigForm(t *testing.T) {
	ix, err := NewPreToMe(services.Deps{})
	if err != nil {
		t.Fatalf("NewPreToMe: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{"sitelink", "username", "password", "loginpinnumber", "", "accountinactivity", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestPreToMeApplyConfigurationOK(t *testing.T) {
	site := newPreToMeSite(t, nil)
	// the POST must redirect for upstream's RedirectingTo check
	site.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site.mu.Lock()
		site.hits = append(site.hits, r.Method+" "+r.URL.RequestURI())
		_, _ = io.ReadAll(r.Body)
		site.mu.Unlock()
		switch r.URL.Path {
		case "/takelogin.php":
			if r.Method == http.MethodPost {
				http.Redirect(w, r, "/index.php", http.StatusFound)
				return
			}
			_, _ = w.Write([]byte(`<html><body>login form</body></html>`))
		case "/index.php":
			_, _ = w.Write([]byte(`<html><body><a href="logout.php">Logout</a></body></html>`))
		default:
			http.NotFound(w, r)
		}
	})
	p := newTestPreToMe(t, site)

	status, err := p.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
		{ID: "loginpinnumber", Value: "1234"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status: %v, want ConfigRequiresTesting", status)
	}
	if !p.IsConfigured() {
		t.Fatal("must be configured after successful login")
	}
	// GET (login page) + POST (login) + followed redirect
	paths := site.paths()
	if len(paths) != 3 || !strings.HasPrefix(paths[0], "GET /takelogin.php") ||
		!strings.HasPrefix(paths[1], "POST /takelogin.php") || paths[2] != "GET /index.php" {
		t.Fatalf("hits: %v", paths)
	}
}

func TestPreToMeApplyConfigurationBadPin(t *testing.T) {
	site := newPreToMeSite(t, map[string]string{
		"/takelogin.php": `<html><body><table class="body_table"><tr><td>
			<font color="red">Invalid PIN</font></td></tr></table></body></html>`,
	})
	p := newTestPreToMe(t, site)

	_, err := p.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
		{ID: "loginpinnumber", Value: "0000"},
	})
	if err == nil || !strings.Contains(err.Error(), "Invalid PIN") {
		t.Fatalf("err: %v", err)
	}
	if p.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}

func TestPreToMeSearchRequestAndParse(t *testing.T) {
	site := newPreToMeSite(t, map[string]string{
		"/browse.php": pretomeBrowsePage,
	})
	p := newTestPreToMe(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	q.Categories = []int{2040} // Movies HD → cat[]=19&tags=1080p

	res, err := p.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// the "Completely Different Show" row fails the AND-filter and is skipped
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("hits: %v", paths)
	}
	req := paths[0]
	// query category 2040 (Movies HD) maps to cat[]=19 plus every Movies HD
	// tag; tags < 7 so they are sent with tf=any. Query param keys pass
	// through unencoded (QueryParams encodes values only).
	for _, fragment := range []string{
		"GET /browse.php?", "st=1", "search=Some+Movie",
		"cat[]=19", "tags=1080p%2C720p%2Cx264%2Cx265%2CBluRay", "tf=any",
	} {
		if !strings.Contains(req, fragment) {
			t.Fatalf("request %q missing %q", req, fragment)
		}
	}

	r := res.Releases[0]
	if r.Title != "Some Movie 2020 1080p" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Details == nil || r.Details.Path != "/details.php" || r.Details.Query().Get("id") != "12345" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Guid == nil || r.Guid.String() != r.Details.String() {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Link == nil || r.Link.Path != "/download.php/12345/some.torrent" {
		t.Fatalf("link: %v", r.Link)
	}
	// "3 hours<br>ago" → ~3h in the past
	if age := time.Since(r.PublishDate); age < 2*time.Hour || age > 5*time.Hour {
		t.Fatalf("publish date age: %v", age)
	}
	if r.Size == nil || *r.Size != 1536*1024*1024 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Files == nil || *r.Files != 1 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.Grabs == nil || *r.Grabs != 25 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 12 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 15 {
		t.Fatalf("peers: %v", r.Peers)
	}
	found := false
	for _, id := range r.Category {
		if id == 2040 {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v, want it to contain 2040", r.Category)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 0.75 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 216000 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf (ratioless): %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
}

func TestPreToMeImdbRequest(t *testing.T) {
	site := newPreToMeSite(t, map[string]string{
		"/browse.php": pretomeBrowsePage,
	})
	p := newTestPreToMe(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "movie"
	q.ImdbID = "tt1234567"

	_, err := p.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	req := site.paths()[0]
	for _, fragment := range []string{"search=tt1234567", "sd=1"} {
		if !strings.Contains(req, fragment) {
			t.Fatalf("request %q missing %q", req, fragment)
		}
	}
}

func TestPreToMeParseError(t *testing.T) {
	// "3 quux" matches the time-ago grammar but hits the unknown-unit error
	bad := strings.Replace(pretomeBrowsePage, "3 hours<br>ago", "3 quux", 1)
	site := newPreToMeSite(t, map[string]string{
		"/browse.php": bad,
	})
	p := newTestPreToMe(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := p.ResultsForQuery(context.Background(), q, false)
	if err == nil {
		t.Fatal("expected parse error")
	}
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindParse {
		t.Fatalf("err: %v", err)
	}
}
