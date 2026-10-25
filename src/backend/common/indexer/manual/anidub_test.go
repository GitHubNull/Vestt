package manual

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

type anidubSite struct {
	srv      *httptest.Server
	mu       sync.Mutex
	hits     []string
	bodies   []string
	serve    map[string]string
	postBody string
}

func newAnidubSite(t *testing.T, serve map[string]string) *anidubSite {
	t.Helper()
	s := &anidubSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodPost {
			body := make([]byte, r.ContentLength)
			_, _ = io.ReadFull(r.Body, body)
			s.bodies = append(s.bodies, string(body))
			s.postBody = string(body)
		}
		page, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *anidubSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestAnidub(t *testing.T, site *anidubSite) *AniDUB {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewAniDUB(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewAniDUB: %v", err)
	}
	a := ix.(*AniDUB)
	a.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(a.ID(), 0)
	return a
}

const anidubHomePage = `<html><body>
<div id="dle-content">
  <div class="story"><div class="story_h"><div class="lcol">
    <h2><a href="https://tr.anidub.com/anime_tv/full/english-title.html">English Title</a></h2>
  </div></div></div>
  <div class="story"><div class="story_h"><div class="lcol">
    <h2><a href="https://tr.anidub.com/unknown_section/news.html">News</a></h2>
  </div></div></div>
</div>
</body></html>`

const anidubShowPage = `<html><body>
<div id="dle-content">
  <h1 id="news-title">Русское название / English Title TV-2 [1 из 12]</h1>
  <div class="poster_bg"><div class="poster"><img src="https://tr.anidub.com/uploads/poster.jpg"/></div></div>
  <ul class="story_inf"><li><b>Автор:</b> root</li><li><b>Дата:</b> 05-06-2024, 21:30</li></ul>
  <div id="tabs">
    <div class="torrent_c">
      <div id="tv1080">
        <div id="torrent_12345_info">
          <ul class="list down">
            <li class="li_distribute_m">15</li>
            <li class="li_swing_m">3</li>
            <li class="li_download_m">42</li>
            <li class="red">1.46 GB</li>
          </ul>
          <div class="tech"><pre>Video: h264</pre></div>
        </div>
      </div>
      <div id="bd720">
        <div id="torrent_12346_info">
          <ul class="list down">
            <li class="li_distribute_m">7</li>
            <li class="li_swing_m">1</li>
            <li class="li_download_m">10</li>
            <li class="red">700 MB</li>
          </ul>
        </div>
      </div>
    </div>
  </div>
</div>
</body></html>`

const anidubSearchPage = `<html><body>
<div id="dle-content">
  <div class="searchitem"><h3><a href="https://tr.anidub.com/anime_tv/full/english-title.html">English Title</a></h3></div>
  <div class="searchitem"><h3><a href="https://tr.anidub.com/anime_tv/full/unrelated-show.html">Unrelated Show</a></h3></div>
</div>
</body></html>`

const anidubLoginOK = `<html><body><a href="index.php?action=logout">logout</a></body></html>`
const anidubLoginError = `<html><body>
<div id="content"><div class="berror"><div class="berror_c">Неверный логин или пароль</div></div></div>
</body></html>`

func TestAnidubCaps(t *testing.T) {
	a, err := NewAniDUB(services.Deps{})
	if err != nil {
		t.Fatalf("NewAniDUB: %v", err)
	}
	caps := a.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MusicSearchParams, ",") != "q" || strings.Join(caps.BookSearchParams, ",") != "q" {
		t.Fatalf("music/book params: %v %v", caps.MusicSearchParams, caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"2", 5070}, {"14", 5070}, {"10", 5070}, {"11", 5070}, {"13", 6000},
		{"15", 7030}, {"16", 3000}, {"17", 3000}, {"3", 5070}, {"4", 5070},
		{"5", 5070}, {"9", 5000}, {"6", 5000}, {"7", 5000}, {"8", 5000},
		{"12", 8000}, {"1", 8000},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		if !anidubIntContains(got, tc.want) {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", tc.tracker, got, tc.want)
		}
	}
}

func anidubIntContains(list []int, v int) bool {
	for _, n := range list {
		if n == v {
			return true
		}
	}
	return false
}

func TestAnidubConfigForm(t *testing.T) {
	a, err := NewAniDUB(services.Deps{})
	if err != nil {
		t.Fatalf("NewAniDUB: %v", err)
	}
	items, err := a.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// sitelink, username, password, instructions (empty id, upstream name ""),
	// striprussiantitle, tags; hidden cookieheader/lasterror excluded.
	want := []string{"sitelink", "username", "password", "", "striprussiantitle", "tags"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("config ids: %v", ids)
	}
	if items[4]["value"] != true {
		t.Fatalf("striprussiantitle default: %v", items[4]["value"])
	}
}

// onSite rewrites the fixture's absolute upstream links to the httptest
// server so every fetch stays on the fake site.
func onSite(site *anidubSite, page string) string {
	return strings.ReplaceAll(page, "https://tr.anidub.com/", site.srv.URL+"/")
}

func TestAnidubLatestReleasesRequestAndParse(t *testing.T) {
	site := newAnidubSite(t, nil)
	// served before any request is made
	site.serve = map[string]string{
		"/":                                 onSite(site, anidubHomePage),
		"/anime_tv/full/english-title.html": anidubShowPage,
	}
	a := newTestAnidub(t, site)

	res, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// The /unknown_section/news.html link maps to no category and is skipped.
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	wantTitle := "English Title  S02E01-E1 [RUS] [HDTV 1080p]"
	if r.Title != wantTitle {
		t.Fatalf("title: %q want %q", r.Title, wantTitle)
	}
	if r.Guid == nil || !strings.HasSuffix(r.Guid.String(), "english-title.html?id=12345") {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.String(), "/engine/download.php?id=12345") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Size == nil || *r.Size != parseutil.GetBytes("1.46 GB") {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 15 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 18 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 42 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Description != "Video: h264" {
		t.Fatalf("description: %q", r.Description)
	}
	if r.PublishDate.Format("2006-01-02 15:04") != "2024-06-05 18:30" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if !anidubIntContains(r.Category, 5070) {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Poster == nil || r.Poster.String() != "https://tr.anidub.com/uploads/poster.jpg" {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}

	r2 := res.Releases[1]
	if !strings.HasSuffix(r2.Title, "[BDRip 720p]") {
		t.Fatalf("second title: %q", r2.Title)
	}
	if !strings.HasSuffix(r2.Guid.String(), "id=12346") {
		t.Fatalf("second guid: %v", r2.Guid)
	}

	paths := site.paths()
	// home page + the one categorised show page only
	if len(paths) != 2 || paths[0] != "GET /" || paths[1] != "GET /anime_tv/full/english-title.html" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestAnidubSearchRequestAndParse(t *testing.T) {
	site := newAnidubSite(t, nil)
	site.serve = map[string]string{
		"/index.php":                         onSite(site, anidubSearchPage),
		"/anime_tv/full/english-title.html":  anidubShowPage,
		"/anime_tv/full/unrelated-show.html": anidubShowPage,
		"/anime_tv/full/other-show.html":     anidubShowPage,
	}
	a := newTestAnidub(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "English Title"
	res, err := a.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// MatchQueryStringAND keeps only titles containing all query words.
	if len(res.Releases) == 0 {
		t.Fatal("expected filtered releases")
	}
	for _, r := range res.Releases {
		if !strings.Contains(r.Title, "English") {
			t.Fatalf("unfiltered release: %q", r.Title)
		}
	}

	paths := site.paths()
	if paths[0] != "POST /index.php?do=search" {
		t.Fatalf("paths: %v", paths)
	}
	site.mu.Lock()
	body := site.postBody
	site.mu.Unlock()
	wantPrefix := "do=search&subaction=search&search_start=1&full_search=1&result_from=1&story=english+title&"
	if !strings.HasPrefix(body, wantPrefix) {
		t.Fatalf("post body: %q", body)
	}
	if !strings.HasSuffix(body, "&showposts=1&catlist%5B%5D=0") {
		t.Fatalf("post body suffix: %q", body)
	}
}

func TestAnidubNormalizeSearchQuery(t *testing.T) {
	ix, err := NewAniDUB(services.Deps{})
	if err != nil {
		t.Fatalf("NewAniDUB: %v", err)
	}
	a := ix.(*AniDUB)
	q := models.NewTorznabQuery()
	q.SearchTerm = "Show S1E1"
	if got := a.normalizeSearchQuery(q); got != "show tv-1" {
		t.Fatalf("normalize 1: %q", got)
	}
	season := 2
	q = models.NewTorznabQuery()
	q.SearchTerm = "Show S1E1"
	q.Season = &season
	// upstream quirk: the stripped season leaves a double space before TV-2
	if got := a.normalizeSearchQuery(q); got != "show  tv-2" {
		t.Fatalf("normalize 2: %q", got)
	}
}

func TestAnidubApplyConfigurationLoginOK(t *testing.T) {
	site := newAnidubSite(t, map[string]string{
		"/index.php": anidubLoginOK,
	})
	a := newTestAnidub(t, site)
	status, err := a.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v", status)
	}
	if !a.IsConfigured() {
		t.Fatal("must be configured after login")
	}
	if a.SiteLink() != site.srv.URL+"/" {
		t.Fatalf("site link rewritten to default: %q", a.SiteLink())
	}
	site.mu.Lock()
	body := site.postBody
	site.mu.Unlock()
	if body != "login_name=user&login_password=pass&login=submit" {
		t.Fatalf("login body: %q", body)
	}
}

func TestAnidubApplyConfigurationLoginError(t *testing.T) {
	site := newAnidubSite(t, map[string]string{
		"/index.php": anidubLoginError,
	})
	a := newTestAnidub(t, site)
	_, err := a.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "wrong"},
	})
	if err == nil || err.Error() != "Exception (anidub): Неверный логин или пароль" {
		t.Fatalf("err: %v", err)
	}
	if a.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}

func TestAnidubSiteError(t *testing.T) {
	site := newAnidubSite(t, map[string]string{
		"/": "Fatal error: Uncaught exception in /var/www/site.php",
	})
	a := newTestAnidub(t, site)
	_, err := a.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err == nil || !strings.Contains(err.Error(), "There is a problem at the AniDUB WEB site") {
		t.Fatalf("err: %v", err)
	}
}

func TestAnidubCategoryPrefixOrder(t *testing.T) {
	// /anime_tv/full must win over /anime_tv (longest path first).
	ix, err := NewAniDUB(services.Deps{})
	if err != nil {
		t.Fatalf("NewAniDUB: %v", err)
	}
	a := ix.(*AniDUB)
	u := anidubMustParseURL(t, "https://tr.anidub.com/anime_tv/full/show.html")
	got := a.parseCategories(u)
	if !anidubIntContains(got, 5070) || !anidubIntContains(got, 100014) {
		t.Fatalf("categories: %v", got)
	}
	u = anidubMustParseURL(t, "https://tr.anidub.com/anime_tv/ongoing-show.html")
	got = a.parseCategories(u)
	if !anidubIntContains(got, 5070) || !anidubIntContains(got, 100002) || anidubIntContains(got, 100014) {
		t.Fatalf("categories for /anime_tv: %v", got)
	}
	if a.parseCategories(anidubMustParseURL(t, "https://tr.anidub.com/other/news.html")) != nil {
		t.Fatal("unknown path must map to nil (news topic)")
	}
}

func anidubMustParseURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	return u
}
