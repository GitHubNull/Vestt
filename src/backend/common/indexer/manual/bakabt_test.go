package manual

import (
	"context"
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
	"vestt/common/utils/parseutil"
)

type bakabtSite struct {
	srv      *httptest.Server
	mu       sync.Mutex
	hits     []string
	postBody string
	serve    map[string]string
}

func newBakabtSite(t *testing.T, serve map[string]string) *bakabtSite {
	t.Helper()
	s := &bakabtSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodPost {
			body := make([]byte, r.ContentLength)
			_, _ = io.ReadFull(r.Body, body)
			s.postBody = string(body)
			w.Header().Set("Set-Cookie", "session=bakabt-test-cookie; Path=/")
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

func (s *bakabtSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestBakabt(t *testing.T, site *bakabtSite) *BakaBT {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewBakaBT(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewBakaBT: %v", err)
	}
	b := ix.(*BakaBT)
	b.SiteLinkValue = site.srv.URL + "/"
	return b
}

const bakabtLoginForm = `<html><body>
<form method="post" action="login.php">
  <input type="hidden" name="loginKey" value="key123"/>
  <input type="text" name="username"/>
</form>
</body></html>`

const bakabtLoginError = `<html><body>
<form id="loginForm"><input name="username"/></form>
<div id="loginError">Invalid username or password</div>
</body></html>`

const bakabtBrowsePage = `<html><body>
<table class="torrents">
  <tr class="torrent">
    <td class="category"><span title="Anime Series"></span></td>
    <td class="name"><a class="title" href="torrent/12345/some-anime">Some Anime | Romaji Anime (BD 1080p)</a><span class="tags">completed</span></td>
    <td class="peers">456 / <a href="download/12345.torrent">12</a><a href="torrent/12345/some-anime#comments">3</a></td>
    <td class="size">700 MB</td>
    <td class="added">22 Jul 15</td>
  </tr>
  <tr class="torrent_alt">
    <td class="category"><span title="Anime Series"></span></td>
    <td class="name"><span class="hentai"></span><a class="title" href="torrent/12346/hentai-show">Hentai Show Season 2 [1080p]</a></td>
    <td class="peers">1k / <a href="download/12346.torrent">5</a><a href="#">1</a></td>
    <td class="size">200.5 MB</td>
    <td class="added">yesterday</td>
    <td><span class="freeleech">Freeleech</span></td>
  </tr>
</table>
</body></html>`

func TestBakabtCaps(t *testing.T) {
	b, err := NewBakaBT(services.Deps{})
	if err != nil {
		t.Fatalf("NewBakaBT: %v", err)
	}
	caps := b.Capabilities()
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
		{"1", 5070}, {"2", 5070}, {"3", 3050}, {"4", 7030}, {"5", 5070},
		{"6", 5050}, {"7", 7050}, {"8", 3020}, {"9", 7020},
		{"11", 6000}, {"12", 6000}, {"13", 6000}, {"14", 6000}, {"15", 6000},
		{"16", 6000}, {"17", 6000}, {"18", 6000}, {"19", 6000},
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

func TestBakabtConfigForm(t *testing.T) {
	b, err := NewBakaBT(services.Deps{})
	if err != nil {
		t.Fatalf("NewBakaBT: %v", err)
	}
	items, err := b.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{
		"sitelink", "username", "password", "",
		"showfreeleechonly", "addreleasesforromajititle",
		"appendseasonforsonarrcompatibility", "accountinactivity", "tags",
	}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("config ids:\n got: %#v\nwant: %#v", ids, want)
	}
	// upstream defaults: AddRomajiTitle=true, others false
	if items[5]["value"] != true || items[4]["value"] != false || items[6]["value"] != false {
		t.Fatalf("bool defaults: %v %v %v", items[4]["value"], items[5]["value"], items[6]["value"])
	}
}

func TestBakabtSearchRequestAndParse(t *testing.T) {
	site := newBakabtSite(t, map[string]string{
		"/browse.php": bakabtBrowsePage,
	})
	b := newTestBakabt(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Anime E01"
	res, err := b.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// row 1 carries two titles (EN + Romaji) but both share one Guid, and
	// the shared ResultsForQuery template de-dupes by Guid (upstream
	// BaseIndexer.cs GroupBy(r => r.Guid)); row 2 adds one release.
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	// upstream quirk: the " | " separator consumes the space before the
	// release info, so the first title has no space before "(".
	if r.Title != "Some Anime(BD 1080p)" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Guid == nil || r.Guid.String() != site.srv.URL+"/torrent/12345/some-anime" {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Link == nil || r.Link.String() != site.srv.URL+"/download/12345.torrent" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Grabs == nil || *r.Grabs != 456 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 12 || r.Peers == nil || *r.Peers != 15 {
		t.Fatalf("seeders/peers: %v %v", r.Seeders, r.Peers)
	}
	if r.Size == nil || *r.Size != parseutil.GetBytes("700 MB") {
		t.Fatalf("size: %v", r.Size)
	}
	if r.PublishDate.Format("2006-01-02") != "2015-07-22" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 0.5 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}
	if len(r.Category) == 0 || r.Category[0] != 5070 {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Description != "completed" {
		t.Fatalf("description: %q", r.Description)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}

	// row 2: hentai icon rewrites "Anime Series" → "Hentai Series" (XXX),
	// grabs "1k" → 1000 (upstream quirk), freeleech → dvf 0
	r3 := res.Releases[1]
	if len(r3.Category) == 0 || r3.Category[0] != 6000 {
		t.Fatalf("hentai category: %v", r3.Category)
	}
	if r3.Grabs == nil || *r3.Grabs != 1000 {
		t.Fatalf("k-grabs: %v", r3.Grabs)
	}
	if r3.DownloadVolumeFactor == nil || *r3.DownloadVolumeFactor != 0 {
		t.Fatalf("freeleech dvf: %v", r3.DownloadVolumeFactor)
	}

	// the trailing " E01" is stripped from the search URL
	paths := site.paths()
	if len(paths) != 1 || paths[0] != "GET /browse.php?only=0&hentai=1&incomplete=1&lossless=1&hd=1&multiaudio=1&bonus=1&reorder=1&q=Some+Anime" {
		t.Fatalf("paths: %v", paths)
	}
}

func TestBakabtAppendSeasonAndRomajiOptions(t *testing.T) {
	site := newBakabtSite(t, map[string]string{
		"/browse.php": bakabtBrowsePage,
	})
	b := newTestBakabt(t, site)
	b.cfg.AppendSeason.Value = true
	b.cfg.AddRomajiTitle.Value = false

	res, err := b.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	// AddRomajiTitle=false: only the second title is emitted; the inserted
	// " Season 1 " leaves the upstream double space (titleSeries trailing space).
	if res.Releases[0].Title != "Romaji Anime  Season 1 (BD 1080p)" {
		t.Fatalf("title: %q", res.Releases[0].Title)
	}
	// "Season 2" already present → no append
	if res.Releases[1].Title != "Hentai Show Season 2 [1080p]" {
		t.Fatalf("title 2: %q", res.Releases[1].Title)
	}
}

func TestBakabtFreeleechOnlySkipsNonFreeleech(t *testing.T) {
	site := newBakabtSite(t, map[string]string{
		"/browse.php": bakabtBrowsePage,
	})
	b := newTestBakabt(t, site)
	b.cfg.FreeleechOnly.Value = true

	res, err := b.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
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

func TestBakabtApplyConfigurationLoginOK(t *testing.T) {
	site := newBakabtSite(t, map[string]string{
		"/login.php": bakabtLoginForm,
	})
	// the POST to login.php has no dedicated page: serve the form for both
	b := newTestBakabt(t, site)
	status, err := b.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status: %v", status)
	}
	if !b.IsConfigured() {
		t.Fatal("must be configured after login")
	}
	site.mu.Lock()
	body := site.postBody
	site.mu.Unlock()
	// upstream Dictionary insertion order: username, password, returnto, loginKey
	want := "username=user&password=pass&returnto=%2Findex.php&loginKey=key123"
	if body != want {
		t.Fatalf("login body: %q want %q", body, want)
	}
}

func TestBakabtApplyConfigurationLoginError(t *testing.T) {
	site := newBakabtSite(t, map[string]string{
		"/login.php": bakabtLoginError,
	})
	b := newTestBakabt(t, site)
	_, err := b.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "wrong"},
	})
	if err == nil || err.Error() != "Exception (bakabt): Invalid username or password" {
		t.Fatalf("err: %v", err)
	}
	if b.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}

func TestBakabtSessionRefreshOnLoginForm(t *testing.T) {
	site := newBakabtSite(t, map[string]string{
		"/browse.php": `<html><body><div id="loginForm">expired</div></body></html>`,
		"/login.php":  bakabtLoginForm,
	})
	b := newTestBakabt(t, site)
	b.SetConfigured(true)

	res, err := b.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	_ = res
	// browse (loginForm) → GET login.php → POST login.php → browse again
	paths := site.paths()
	want := []string{
		"GET /browse.php?only=0&hentai=1&incomplete=1&lossless=1&hd=1&multiaudio=1&bonus=1&reorder=1&q=",
		"GET /login.php",
		"POST /login.php",
		"GET /browse.php?only=0&hentai=1&incomplete=1&lossless=1&hd=1&multiaudio=1&bonus=1&reorder=1&q=",
	}
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Fatalf("paths: %v", paths)
	}
}

func TestBakabtDownloadFollowsDownloadLink(t *testing.T) {
	site := newBakabtSite(t, map[string]string{
		"/download/12345":      `<html><body><a class="download_link" href="files/12345.torrent">dl</a></body></html>`,
		"/files/12345.torrent": "TORRENT-BYTES",
	})
	b := newTestBakabt(t, site)

	data, err := b.Download(context.Background(), anidubMustParseURL(t, site.srv.URL+"/download/12345"))
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(data) != "TORRENT-BYTES" {
		t.Fatalf("data: %q", data)
	}
}

func TestBakabtDownloadMissingLinkIsError(t *testing.T) {
	site := newBakabtSite(t, map[string]string{
		"/download/12345": `<html><body>no link here</body></html>`,
	})
	b := newTestBakabt(t, site)
	_, err := b.Download(context.Background(), anidubMustParseURL(t, site.srv.URL+"/download/12345"))
	if err == nil || err.Error() != "Unable to find download link." {
		t.Fatalf("err: %v", err)
	}
}
