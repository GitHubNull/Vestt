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
)

type bitHDTVSite struct {
	srv     *httptest.Server
	mu      sync.Mutex
	hits    []string
	cookies []string
	serve   map[string]string
}

func newBitHDTVSite(t *testing.T, serve map[string]string) *bitHDTVSite {
	t.Helper()
	s := &bitHDTVSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.cookies = append(s.cookies, r.Header.Get("Cookie"))
		body, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=iso-8859-1")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *bitHDTVSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

const bhdTestCookie = "uid=123; pass=foobar"

func newTestBitHDTV(t *testing.T, site *bitHDTVSite) *BitHDTV {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewBitHDTV(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewBitHDTV: %v", err)
	}
	b := ix.(*BitHDTV)
	b.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(b.ID(), 0)
	return b
}

// bhdFixtureHTML is the upstream #torrents-index-table layout: 10 <td> cells
// per row; the date and time are separated by a <br> (TextContent glues them
// together, matching the upstream "yyyy-MM-ddHH:mm:ss" ParseExact format).
const bhdFixtureHTML = `<html><body>
<table id="torrents-index-table"><tbody id="torrents-index-table-body">
<tr bgcolor="#DDDDDD">
  <td></td>
  <td><a href="torrents.php?cat=7"><img src="cat7.png"/></a></td>
  <td><a href="details.php?id=1001" title="Some.Movie.2024.1080p.BluRay.x264-GRP">DL</a>
      <font class="small">[ Action / Adventure ]</font></td>
  <td>3</td>
  <td>x</td>
  <td>2024-03-02<br/>14:25:36</td>
  <td>1.47 GB</td>
  <td>42</td>
  <td>5</td>
  <td>2</td>
  <td><a href="download.php?id=1001&amp;type=torrent">dl1</a></td>
</tr>
<tr>
  <td></td>
  <td><a href="torrents.php?cat=12"><img src="cat12.png"/></a></td>
  <td><a href="details.php?id=1002" title="Some.Show.S02.720p.WEB-DL-GRP">DL</a></td>
  <td>12</td>
  <td>x</td>
  <td>2024-01-11<br/>09:00:00</td>
  <td>500 MB</td>
  <td>7</td>
  <td>10</td>
  <td>5</td>
  <td><a href="download.php?id=1002&amp;type=torrent">dl2</a></td>
</tr>
</tbody></table>
</body></html>`

func TestBitHDTVCaps(t *testing.T) {
	ix, err := NewBitHDTV(services.Deps{})
	if err != nil {
		t.Fatalf("NewBitHDTV: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep,imdbid,genre" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q,imdbid,genre" {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"6", 3040}, {"7", 2000}, {"8", 3020}, {"9", 8000},
		{"10", 5000}, {"12", 5000}, {"11", 6000},
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

func TestBitHDTVConfigForm(t *testing.T) {
	ix, err := NewBitHDTV(services.Deps{})
	if err != nil {
		t.Fatalf("NewBitHDTV: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// NOTE: upstream AddDynamic("freeleech", …) renames the item id to the
	// dynamic key "freeleech"; configdata items cannot override their id,
	// so the declared field keeps its name-derived id (see 澄清记录 in
	// bithdtv.go).
	want := []string{
		"sitelink", "cookie", "cookieinstructions", "",
		"searchfreeleechonly", "accountinactivity", "ratelimits", "tags",
	}
	if len(ids) != len(want) {
		t.Fatalf("config ids: %v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("config id %d: %q want %q (all: %v)", i, ids[i], want[i], ids)
		}
	}
}

func TestBitHDTVApplyConfigurationCookieRequest(t *testing.T) {
	site := newBitHDTVSite(t, map[string]string{
		"/torrents.php": bhdFixtureHTML,
	})
	b := newTestBitHDTV(t, site)

	_, err := b.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "cookie", Value: bhdTestCookie},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if !b.IsConfigured() {
		t.Fatal("must be configured")
	}
	if b.CookieHeader() != bhdTestCookie {
		t.Fatalf("cookie header: %q", b.CookieHeader())
	}
	if len(site.cookies) == 0 || site.cookies[0] != bhdTestCookie {
		t.Fatalf("sent cookies: %v", site.cookies)
	}
	// empty search => cat=0, options=0
	if len(site.paths()) != 1 || site.paths()[0] != "/torrents.php?cat=0&search=&options=0" {
		t.Fatalf("paths: %v", site.paths())
	}
}

func TestBitHDTVParseReleaseFields(t *testing.T) {
	site := newBitHDTVSite(t, map[string]string{
		"/torrents.php": bhdFixtureHTML,
	})
	b := newTestBitHDTV(t, site)

	res, err := b.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r := res.Releases[0]
	if r.Title != "Some Movie 2024 1080p BluRay x264-GRP" {
		t.Fatalf("title: %q", r.Title)
	}
	if len(r.Category) == 0 || r.Category[0] != 2000 {
		t.Fatalf("category: %v", r.Category)
	}
	if r.Size == nil || *r.Size != 1578400481 { // 1.47 GiB (1024-based, upstream GetBytes)
		t.Fatalf("size: %v", bhdI64(r.Size))
	}
	if r.Seeders == nil || *r.Seeders != 5 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 7 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 42 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Files == nil || *r.Files != 3 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.PublishDate.Format("2006-01-02 15:04:05") != "2024-03-02 14:25:36" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Description != "Action,Adventure" {
		t.Fatalf("description: %q", r.Description)
	}
	if len(r.Genres) != 2 || r.Genres[0] != "Action" || r.Genres[1] != "Adventure" {
		t.Fatalf("genres: %v", r.Genres)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.Path, "details.php") {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Guid == nil || r.Guid.String() != r.Details.String() {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Link == nil || !strings.Contains(r.Link.String(), "download.php?id=1001") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 172800 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}
	// grey row: DL x1, UL x2
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 2 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}

	r2 := res.Releases[1]
	// default (no bgcolor): normal factors
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 1 || *r2.UploadVolumeFactor != 1 {
		t.Fatalf("default factors: %v %v", r2.DownloadVolumeFactor, r2.UploadVolumeFactor)
	}
	if len(r2.Category) == 0 || r2.Category[0] != 5000 {
		t.Fatalf("category 12: %v", r2.Category)
	}
}

func TestBitHDTVSearchRequests(t *testing.T) {
	site := newBitHDTVSite(t, map[string]string{
		"/torrents.php": bhdFixtureHTML,
	})
	b := newTestBitHDTV(t, site)

	// text search with freeleech + a single mapped category
	b.cfg.Freeleech.Value = true
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "some movie"
	q.Categories = []int{2000}
	if _, err := b.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	last := site.paths()[len(site.paths())-1]
	if last != "/torrents.php?cat=7&free=2&search=some+movie&options=0" {
		t.Fatalf("text search path: %q", last)
	}

	// imdb query: two requests, options=4 then replaced with options=1
	site.mu.Lock()
	site.hits = nil
	site.mu.Unlock()
	q2 := models.NewTorznabQuery()
	q2.QueryType = "movie"
	q2.ImdbID = "tt1234567"
	if _, err := b.ResultsForQuery(context.Background(), q2, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 2 {
		t.Fatalf("paths: %v", paths)
	}
	if paths[0] != "/torrents.php?cat=0&free=2&search=tt1234567&options=4" {
		t.Fatalf("imdb path 0: %q", paths[0])
	}
	if paths[1] != "/torrents.php?cat=0&free=2&search=tt1234567&options=1" {
		t.Fatalf("imdb path 1: %q", paths[1])
	}

	// genre query: options=2, search = query + " " + genre
	site.mu.Lock()
	site.hits = nil
	site.mu.Unlock()
	q3 := models.NewTorznabQuery()
	q3.QueryType = "tvsearch"
	q3.SearchTerm = "some show"
	q3.Genre = "Drama"
	if _, err := b.ResultsForQuery(context.Background(), q3, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	last = site.paths()[len(site.paths())-1]
	if last != "/torrents.php?cat=0&free=2&search=some+show+Drama&options=2" {
		t.Fatalf("genre path: %q", last)
	}
}

func TestBitHDTVErrorPaths(t *testing.T) {
	// rate-limit banner
	siteLimited := newBitHDTVSite(t, map[string]string{
		"/torrents.php": "<html>Request limit per User exceeded! Please try later!</html>",
	})
	b := newTestBitHDTV(t, siteLimited)
	_, err := b.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err == nil || !strings.Contains(err.Error(), "Request limit per User exceeded! Please try later!") {
		t.Fatalf("err: %v", err)
	}

	// empty result page (no table body) => 0 releases => cookie failure on Apply
	siteEmpty := newBitHDTVSite(t, map[string]string{
		"/torrents.php": "<html><body>nothing here</body></html>",
	})
	b2 := newTestBitHDTV(t, siteEmpty)
	_, err = b2.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: siteEmpty.srv.URL + "/"},
		{ID: "cookie", Value: bhdTestCookie},
	})
	if err == nil || err.Error() != "Your cookie did not work: Found 0 results in the tracker" {
		t.Fatalf("err: %v", err)
	}
	if b2.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}

	// broken row (missing cells) => parse error wrapped by ApplyConfiguration
	siteBroken := newBitHDTVSite(t, map[string]string{
		"/torrents.php": `<table id="torrents-index-table"><tbody id="torrents-index-table-body"><tr><td>only one cell</td></tr></tbody></table>`,
	})
	b3 := newTestBitHDTV(t, siteBroken)
	_, err = b3.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: siteBroken.srv.URL + "/"},
		{ID: "cookie", Value: bhdTestCookie},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "Your cookie did not work: ") {
		t.Fatalf("err: %v", err)
	}
}

func bhdI64(v *int64) int64 {
	if v == nil {
		return -1
	}
	return *v
}
