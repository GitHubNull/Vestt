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

type shazbatSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	posts []map[string]string
	// serve maps path → body (searches, show pages, torrents)
	serve map[string]string
	// loginBody overrides the POST /login response body
	loginBody string
	// searchBody overrides GET /search (empty = use serve map)
	searchBody string
}

func newShazbatSite(t *testing.T) *shazbatSite {
	t.Helper()
	s := &shazbatSite{serve: map[string]string{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		s.mu.Unlock()
		switch {
		case r.URL.Path == "/login" && r.Method == http.MethodPost:
			_ = r.ParseForm()
			form := map[string]string{}
			for k := range r.PostForm {
				form[k] = r.PostForm.Get(k)
			}
			form["__cookie"] = r.Header.Get("Cookie")
			s.mu.Lock()
			s.posts = append(s.posts, form)
			s.mu.Unlock()
			w.Header().Add("Set-Cookie", "session=abc; Path=/")
			body := s.loginBody
			if body == "" {
				body = `<html>user <span class="glyphicon-log-out"></span></html>`
			}
			_, _ = w.Write([]byte(body))
		case r.URL.Path == "/search":
			s.mu.Lock()
			body := s.searchBody
			s.mu.Unlock()
			if body == "" {
				s.mu.Lock()
				body = s.serve["/search"]
				s.mu.Unlock()
			}
			_, _ = w.Write([]byte(body))
		case r.URL.Path == "/show":
			switch r.URL.Query().Get("id") {
			case "42":
				_, _ = w.Write([]byte(shazbatShow42Page))
			case "43":
				_, _ = w.Write([]byte(shazbatShow43Page))
			default:
				http.NotFound(w, r)
			}
		default:
			s.mu.Lock()
			body, ok := s.serve[r.URL.Path]
			s.mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *shazbatSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *shazbatSite) forms() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]string(nil), s.posts...)
}

func newTestShazbat(t *testing.T, site *shazbatSite) *Shazbat {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewShazbat(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewShazbat: %v", err)
	}
	sh := ix.(*Shazbat)
	sh.SiteLinkValue = site.srv.URL + "/"
	// tests must not pay the upstream 5.1s request delay
	client.SetRequestDelay(sh.ID(), 0)
	return sh
}

func shazbatLoginItems(site *shazbatSite) []indexer.ConfigItem {
	return []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	}
}

// shazbatSearchPage: one HD torrent row (with timestamp + poster + genre),
// the global freeleech banner and two show divs.
const shazbatSearchPage = `<html><body>
<span>Freeleech until: <span class="datetime">2024-05-01</span></span>
<table id="torrent-table">
<tr class="eprow">
  <td></td>
  <td></td>
  <td>Some Show S01E01 1080p <span>1080p</span> <label class="label-tag">Drama</label></td>
  <td> (1610612736) :10 / :2 </td>
  <td><a href="load_torrent?x=1">dl</a><a href="torrent_info?y=2">info</a>
      <div style="cursor: pointer; background-image:url('/img/poster.jpg');"></div>
      <span class="datetime" data-timestamp="1700000000"></span></td>
</tr>
</table>
<div class="show" data-id="42"></div>
<div class="show" data-id="43"></div>
</body></html>`

// shazbatShow42Page: SD row with its own timestamp.
const shazbatShow42Page = `<html><body>
<table>
<tr class="eprow">
  <td></td>
  <td></td>
  <td>Some Show S01E02 480p</td>
  <td> (536870912) :1 / :0 </td>
  <td><a href="load_torrent?x=3">dl</a><a href="torrent_info?y=4">info</a>
      <span class="datetime" data-timestamp="1700001000"></span></td>
</tr>
</table>
</body></html>`

// shazbatShow43Page: SD row WITHOUT a timestamp → running date minus 1 min.
const shazbatShow43Page = `<html><body>
<table>
<tr class="eprow">
  <td></td>
  <td></td>
  <td>Some Show S01E03 480p</td>
  <td> (268435456) :0 / :1 </td>
  <td><a href="load_torrent?x=5">dl</a><a href="torrent_info?y=6">info</a></td>
</tr>
</table>
</body></html>`

func TestShazbatCaps(t *testing.T) {
	ix, err := NewShazbat(services.Deps{})
	if err != nil {
		t.Fatalf("NewShazbat: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if len(caps.MovieSearchParams) != 0 {
		t.Fatalf("MovieSearchParams must be empty: %v", caps.MovieSearchParams)
	}
	if got := caps.Categories.MapTrackerCatToNewznab("1"); !containsInt(got, models.CatTV.ID) {
		t.Fatalf("cat 1: %v", got)
	}
	if got := caps.Categories.MapTrackerCatToNewznab("3"); !containsInt(got, models.CatTVHD.ID) {
		t.Fatalf("cat 3: %v", got)
	}
}

func TestShazbatConfigForm(t *testing.T) {
	ix, err := NewShazbat(services.Deps{})
	if err != nil {
		t.Fatalf("NewShazbat: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// upstream item ids derive from the display NAME via
	// Name.Replace(" ", "").ToLower() (ConfigurationData.cs:105), not from
	// the C# property name — so "(sub-requests when searching)" rides along
	want := []string{
		"sitelink", "username", "password",
		"showpagesfetchlimit(sub-requestswhensearching)", "showpagesfetchlimitwarning", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestShazbatLoginSuccess(t *testing.T) {
	site := newShazbatSite(t)
	sh := newTestShazbat(t, site)

	status, err := sh.ApplyConfiguration(context.Background(), shazbatLoginItems(site))
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status: %v", status)
	}
	if !sh.IsConfigured() {
		t.Fatal("must be configured")
	}
	if sh.CookieHeader() != "session=abc" {
		t.Fatalf("cookie header: %q", sh.CookieHeader())
	}
	forms := site.forms()
	if len(forms) != 1 {
		t.Fatalf("forms: %v", forms)
	}
	f := forms[0]
	for k, want := range map[string]string{
		"referer":     "",
		"query":       "",
		"tv_timezone": "0",
		"username":    "user",
		"password":    "pass",
	} {
		if f[k] != want {
			t.Fatalf("form field %q = %q want %q (form: %v)", k, f[k], want, f)
		}
	}
}

func TestShazbatLoginFail(t *testing.T) {
	site := newShazbatSite(t)
	site.loginBody = `<html>invalid login</html>`
	sh := newTestShazbat(t, site)

	_, err := sh.ApplyConfiguration(context.Background(), shazbatLoginItems(site))
	var ie *indexer.IndexerError
	if err == nil || !errors.As(err, &ie) {
		t.Fatalf("err: %v", err)
	}
	if ie.Kind != indexer.KindConfig || ie.Message != "The username and password entered do not match." {
		t.Fatalf("err: %v", ie)
	}
	if sh.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func loginShazbat(t *testing.T, sh *Shazbat, site *shazbatSite) {
	t.Helper()
	if _, err := sh.ApplyConfiguration(context.Background(), shazbatLoginItems(site)); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
}

func TestShazbatSearchFlowWithShowPages(t *testing.T) {
	site := newShazbatSite(t)
	site.serve["/search"] = shazbatSearchPage
	sh := newTestShazbat(t, site)
	loginShazbat(t, sh, site)

	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "some show"
	before := time.Now()
	res, err := sh.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 3 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	paths := site.paths()
	if len(paths) != 4 {
		t.Fatalf("paths: %v", paths)
	}
	if paths[1] != "GET /search?search=some+show&portlet=true" {
		t.Fatalf("search path: %q", paths[1])
	}
	if paths[2] != "GET /show?id=42&show_mode=torrents&portlet=true&tab=true" {
		t.Fatalf("show42 path: %q", paths[2])
	}
	if paths[3] != "GET /show?id=43&show_mode=torrents&portlet=true&tab=true" {
		t.Fatalf("show43 path: %q", paths[3])
	}

	r := res.Releases[0]
	if r.Title != "Some Show S01E01 1080p" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.Path, "/load_torrent") || r.Link.Query().Get("x") != "1" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.Path, "/torrent_info") || r.Details.Query().Get("y") != "2" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Size == nil || *r.Size != 1610612736 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 10 || r.Peers == nil || *r.Peers != 12 {
		t.Fatalf("seeders/peers: %v %v", r.Seeders, r.Peers)
	}
	if !containsInt(r.Category, models.CatTVHD.ID) { // 1080p → TVHD
		t.Fatalf("category: %v", r.Category)
	}
	if len(r.Genres) != 1 || r.Genres[0] != "Drama" {
		t.Fatalf("genres: %v", r.Genres)
	}
	if r.Poster == nil || !strings.HasSuffix(r.Poster.Path, "/img/poster.jpg") {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.PublishDate.Unix() != 1700000000 {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	// global freeleech banner on the search page → dvf 0 everywhere
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 172800 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}

	// SD row from the show page
	r2 := res.Releases[1]
	if r2.Title != "Some Show S01E02 480p" {
		t.Fatalf("title2: %q", r2.Title)
	}
	if !containsInt(r2.Category, models.CatTVSD.ID) {
		t.Fatalf("category2: %v", r2.Category)
	}
	if r2.PublishDate.Unix() != 1700001000 {
		t.Fatalf("publish date2: %v", r2.PublishDate)
	}

	// row without a timestamp: the running date restarts at DateTime.Now for
	// every ParseResults call (Shazbat.cs:207) and never carries across
	// pages, so this row gets Now minus one minute (Shazbat.cs:230-233)
	r3 := res.Releases[2]
	if r3.Title != "Some Show S01E03 480p" {
		t.Fatalf("title3: %q", r3.Title)
	}
	want := before.Add(-time.Minute)
	if d := r3.PublishDate.Sub(want); d < -30*time.Second || d > 30*time.Second {
		t.Fatalf("publish date3: %v want ~%v", r3.PublishDate, want)
	}
}

func TestShazbatSearchTermFilter(t *testing.T) {
	site := newShazbatSite(t)
	site.serve["/search"] = shazbatSearchPage
	sh := newTestShazbat(t, site)
	loginShazbat(t, sh, site)

	// "zephyr" matches neither row title → everything filtered out
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "some show zephyr"
	res, err := sh.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %+v", res.Releases)
	}
}

func TestShazbatEmptyTermBrowsesTorrentsPage(t *testing.T) {
	site := newShazbatSite(t)
	site.serve["/torrents"] = shazbatShow42Page
	sh := newTestShazbat(t, site)
	loginShazbat(t, sh, site)

	// a real browse query arrives as t=search; an untyped query is rejected
	// by the CanHandleQuery gate like upstream (BaseIndexer.cs:275-311)
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	res, err := sh.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 || res.Releases[0].Title != "Some Show S01E02 480p" {
		t.Fatalf("releases: %+v", res.Releases)
	}
	paths := site.paths()
	if len(paths) != 2 || paths[1] != "GET /torrents" {
		t.Fatalf("paths: %v", paths)
	}
	// no global freeleech banner → dvf 1
	if *res.Releases[0].DownloadVolumeFactor != 1 {
		t.Fatalf("dvf: %v", res.Releases[0].DownloadVolumeFactor)
	}
}

func TestShazbatReloginOnExpiredSession(t *testing.T) {
	site := newShazbatSite(t)
	site.loginBody = `<html>user <span class="glyphicon-log-out"></span></html>`
	sh := newTestShazbat(t, site)
	loginShazbat(t, sh, site)

	// first search response claims the session expired
	site.searchBody = `please <b>Sign In Now</b> to continue`
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "some show"
	if _, err := sh.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// second search response is a valid page without rows
	site.searchBody = `<html><body>no rows</body></html>`
	if _, err := sh.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}

	paths := site.paths()
	// setup login + search("sign in now") + relogin + ONE retried search (the
	// retried response is not re-checked for relogin, Shazbat.cs:292-296) +
	// the second ResultsForQuery's own search
	if len(paths) != 5 {
		t.Fatalf("paths: %v", paths)
	}
	if paths[0] != "POST /login" || paths[2] != "POST /login" {
		t.Fatalf("re-login not attempted: %v", paths)
	}
	if !strings.HasPrefix(paths[1], "GET /search?") || paths[1] != paths[3] {
		t.Fatalf("search not retried: %v", paths)
	}
	if !strings.HasPrefix(paths[4], "GET /search?") {
		t.Fatalf("second query did not search: %v", paths)
	}
}

func TestShazbatFixSearchTerm(t *testing.T) {
	cases := []struct{ in, want string }{
		// `\b[S|E]\d+\b` (Shazbat.cs:275) cannot match inside "S02E03": the
		// digits after S run into E without a word boundary and the E itself
		// is not at a boundary → nothing is stripped (upstream quirk)
		{"Some Show S02E03", "some show s02e03"},
		{"Show Name 2020", "show name"},
		{"Show.Name.(2019).S01.720p", "show name 720p"},
		{"My Show 2020.05.01", "my show"},
		{"Already clean", "already clean"},
	}
	for _, tc := range cases {
		if got := shFixSearchTerm(tc.in); got != tc.want {
			t.Fatalf("shFixSearchTerm(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestShazbatShowPagesFetchLimitFallback(t *testing.T) {
	sh, err := NewShazbat(services.Deps{})
	if err != nil {
		t.Fatalf("NewShazbat: %v", err)
	}
	s := sh.(*Shazbat)
	if got := s.showPagesFetchLimit(); got != 2 {
		t.Fatalf("default limit: %d", got)
	}
	s.cfg.ShowPagesFetchLimit.Value = "9"
	if got := s.showPagesFetchLimit(); got != 2 {
		t.Fatalf("out-of-range limit must fall back to 2: %d", got)
	}
	s.cfg.ShowPagesFetchLimit.Value = "3"
	if got := s.showPagesFetchLimit(); got != 3 {
		t.Fatalf("limit: %d", got)
	}
}
