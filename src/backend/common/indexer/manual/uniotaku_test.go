package manual

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

const uniotakuBrowseJSON = `{
  "data": [
    [
      "<a href=\"torrents-details.php?id=101\">Anime Title Ep 01</a> <img src=\"images/free.gif\">",
      "<img src=\"images/cat.gif\" alt=\"Anime\">",
      "",
      5, 2, 10, "1.46 GB",
      "<a href=\"teams-view.php?id=7\">GroupName</a>"
    ],
    [
      "<a href=\"torrents-details.php?id=102\">Movie &amp; Title</a> <img src=\"images/silverdownload.gif\">",
      "<img src=\"images/cat.gif\" alt=\"Filme\">",
      "",
      3, 1, 5, "700 MB",
      ""
    ]
  ]
}`

// uniotakuSite is a fake UniOtaku DataTables endpoint for tests.
type uniotakuSite struct {
	*httptest.Server

	mu         sync.Mutex
	hits       []string
	loginPosts int
	loginOK    bool
}

func newUniotakuSite(t *testing.T, loginOK bool) *uniotakuSite {
	t.Helper()
	s := &uniotakuSite{loginOK: loginOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.hits = append(s.hits, r.URL.RequestURI())
		switch r.URL.Path {
		case "/account-login.php":
			s.loginPosts++
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if s.loginOK && r.PostForm.Get("username") == "okuser" && r.PostForm.Get("password") == "okpass" && r.PostForm.Get("manter") == "1" {
				http.Redirect(w, r, "/index.php", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><body><div class="login-content"><span class="text-red">Usuário ou senha inválidos</span></div></body></html>`)
		case "/index.php":
			// the jar only sees the final response's cookies (Vestt's client
			// stack follows redirects transparently), so uid/pass ride here
			w.Header().Add("Set-Cookie", "uid=42; path=/")
			w.Header().Add("Set-Cookie", "pass=secret; path=/")
			_, _ = fmt.Fprint(w, `<html><body>welcome</body></html>`)
		case "/torrents_.php":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = fmt.Fprint(w, uniotakuBrowseJSON)
		case "/torrents-details.php":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><body><a href="download.php?id=101">download</a></body></html>`)
		case "/download.php":
			w.Header().Set("Content-Type", "application/x-bittorrent")
			_, _ = fmt.Fprint(w, "d8:announce14:http://example")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *uniotakuSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestUniotaku(t *testing.T, site *uniotakuSite) *Uniotaku {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewUniotaku(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewUniotaku: %v", err)
	}
	u := ix.(*Uniotaku)
	u.SiteLinkValue = site.URL + "/"
	u.DefaultSiteLink = site.URL + "/"
	client.SetRequestDelay(u.ID(), 0)
	return u
}

func TestUniotakuCaps(t *testing.T) {
	ix, err := NewUniotaku(services.Deps{})
	if err != nil {
		t.Fatalf("NewUniotaku: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q" ||
		strings.Join(caps.MusicSearchParams, ",") != "q" ||
		strings.Join(caps.BookSearchParams, ",") != "q" {
		t.Fatalf("movie/music/book params: %v %v %v",
			caps.MovieSearchParams, caps.MusicSearchParams, caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"28", 5070}, {"47", 2020}, {"48", 5070}, {"49", 7030},
		{"50", 5050}, {"51", 3000}, {"55", 6000}, {"58", 5050},
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

func TestUniotakuConfigForm(t *testing.T) {
	ix, err := NewUniotaku(services.Deps{})
	if err != nil {
		t.Fatalf("NewUniotaku: %v", err)
	}
	u := ix.(*Uniotaku)
	items, err := u.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{"sitelink", "username", "password", "", "searchfreeleechonly", "sortby", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
}

func TestUniotakuApplyConfigurationAndSearch(t *testing.T) {
	site := newUniotakuSite(t, true)
	u := newTestUniotaku(t, site)

	status, err := u.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
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
	if !u.IsConfigured() {
		t.Fatal("expected configured")
	}

	res, err := u.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", SearchTerm: "some movie",
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r0 := res.Releases[0]
	if r0.Title != "Anime Title Ep 01 [GroupName]" {
		t.Fatalf("title: %q", r0.Title)
	}
	if r0.Details == nil || r0.Details.String() != site.URL+"/torrents-details.php?id=101" {
		t.Fatalf("details: %v", r0.Details)
	}
	if r0.Link == nil || r0.Link.String() != r0.Details.String() {
		t.Fatalf("link: %v", r0.Link)
	}
	foundAnime := false
	for _, c := range r0.Category {
		if c == models.CatTVAnime.ID {
			foundAnime = true
		}
	}
	if !foundAnime {
		t.Fatalf("category: %v, want 5070 included", r0.Category)
	}
	wantSize := parseutil.GetBytes("1.46 GB")
	if r0.Size == nil || *r0.Size != wantSize {
		t.Fatalf("size: %v, want %d", r0.Size, wantSize)
	}
	if r0.Seeders == nil || *r0.Seeders != 5 || r0.Peers == nil || *r0.Peers != 7 {
		t.Fatalf("seeders/peers: %v/%v", r0.Seeders, r0.Peers)
	}
	if r0.Grabs == nil || *r0.Grabs != 10 {
		t.Fatalf("grabs: %v", r0.Grabs)
	}
	// free.gif → freeleech
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r0.DownloadVolumeFactor)
	}
	if r0.UploadVolumeFactor == nil || *r0.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r0.UploadVolumeFactor)
	}
	if r0.MinimumRatio == nil || *r0.MinimumRatio != 0.7 {
		t.Fatalf("minimum ratio: %v", r0.MinimumRatio)
	}

	r1 := res.Releases[1]
	if r1.Title != "Movie & Title" {
		t.Fatalf("title 2: %q", r1.Title)
	}
	foundFilme := false
	for _, c := range r1.Category {
		if c == models.CatMoviesOther.ID {
			foundFilme = true
		}
	}
	if !foundFilme {
		t.Fatalf("category 2: %v, want 2020 included", r1.Category)
	}
	// silverdownload.gif → half download
	if r1.DownloadVolumeFactor == nil || *r1.DownloadVolumeFactor != 0.5 {
		t.Fatalf("dvf 2: %v", r1.DownloadVolumeFactor)
	}
	// upstream decrements PublishDate one minute per row
	if !r1.PublishDate.Equal(r0.PublishDate.Add(-time.Minute)) {
		t.Fatalf("publish dates: %v vs %v", r0.PublishDate, r1.PublishDate)
	}

	paths := site.paths()
	// "/account-login.php", "/index.php" (login redirect), then the browse request
	if len(paths) != 3 {
		t.Fatalf("paths: %v", paths)
	}
	wantPrefix := "/torrents_.php?categoria=0&grupo=0&status=0&ordenar=0&start=0&length=100&search[value]=%25some%25movie%25&search[regex]=false"
	if paths[2] != wantPrefix {
		t.Fatalf("search path:\n  got:  %v\n want: %v", paths[2], wantPrefix)
	}
}

func TestUniotakuSearchParamsVariants(t *testing.T) {
	site := newUniotakuSite(t, true)
	u := newTestUniotaku(t, site)
	u.cfg.Freeleech.Value = true
	u.cfg.SortBy.Value = "3"

	// single mapped category → categoria=<cat>; Movies/Other maps only to 47.
	// The base result filter drops the Anime row (requested cat 2020).
	res, err := u.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", Categories: []int{models.CatMoviesOther.ID},
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	paths := site.paths()
	if len(paths) != 1 || !strings.HasPrefix(paths[0], "/torrents_.php?categoria=47&grupo=0&status=1&ordenar=3") {
		t.Fatalf("paths: %v", paths)
	}

	// multiple mapped categories → categoria=0; no search term → empty search value
	if _, err := u.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", Categories: []int{models.CatTVAnime.ID},
	}, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths = site.paths()
	if len(paths) != 2 || !strings.HasPrefix(paths[1], "/torrents_.php?categoria=0&") ||
		!strings.Contains(paths[1], "search[value]=&") {
		t.Fatalf("paths: %v", paths)
	}
}

func TestUniotakuLoginFailure(t *testing.T) {
	site := newUniotakuSite(t, false)
	u := newTestUniotaku(t, site)

	_, err := u.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "baduser"},
		{ID: "password", Value: "badpass"},
	})
	if err == nil || !strings.Contains(err.Error(), "Usuário ou senha inválidos") {
		t.Fatalf("err: %v", err)
	}
	if u.IsConfigured() {
		t.Fatal("must not be configured after login failure")
	}
}

func TestUniotakuLoginUnknownError(t *testing.T) {
	site := newUniotakuSite(t, true)
	u := newTestUniotaku(t, site)

	// no uid/pass cookies in the response → error; the site always renders
	// the red message, so point the indexer at a bare path instead
	site2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `<html><body>ok</body></html>`)
	}))
	t.Cleanup(site2.Close)
	u.SiteLinkValue = site2.URL + "/"
	u.DefaultSiteLink = site2.URL + "/"

	_, err := u.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site2.URL + "/"},
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err == nil || !strings.Contains(err.Error(), "Unknown error message, please report.") {
		t.Fatalf("err: %v", err)
	}
}

func TestUniotakuParseError(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `not json`)
	}))
	t.Cleanup(site.Close)
	u := newTestUniotaku(t, &uniotakuSite{Server: site})

	_, err := u.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search"}, false)
	if err == nil || !strings.Contains(err.Error(), "Parse error") {
		t.Fatalf("err: %v", err)
	}
}

func TestUniotakuDownload(t *testing.T) {
	site := newUniotakuSite(t, true)
	u := newTestUniotaku(t, site)

	details, err := url.Parse(site.URL + "/torrents-details.php?id=101")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	blob, err := u.Download(context.Background(), details)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(blob) != "d8:announce14:http://example" {
		t.Fatalf("blob: %q", blob)
	}
	paths := site.paths()
	if len(paths) != 2 || paths[0] != "/torrents-details.php?id=101" || paths[1] != "/download.php?id=101" {
		t.Fatalf("paths: %v", paths)
	}
}
