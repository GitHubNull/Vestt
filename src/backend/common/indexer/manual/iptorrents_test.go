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

type ipSite struct {
	srv *httptest.Server

	mu         sync.Mutex
	hits       []string
	lastUA     string
	lastRef    string
	lastCookie string
	body       string
}

func newIPSite(t *testing.T, body string) *ipSite {
	t.Helper()
	s := &ipSite{body: body}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.lastUA = r.Header.Get("User-Agent")
		s.lastRef = r.Header.Get("Referer")
		s.lastCookie = r.Header.Get("Cookie")
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *ipSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestIPT(t *testing.T, site *ipSite) *IPTorrents {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewIPTorrents(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewIPTorrents: %v", err)
	}
	i := ix.(*IPTorrents)
	i.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(i.ID(), 0)
	return i
}

// ipTorrentsFixture is a full torrents page: 10-column row, header-driven
// column indexes, freeleech span, tags + uploader in div.sub.
const ipTorrentsFixture = `<html><body>
<a href="/lout.php">logout</a>
<table id="torrents">
<thead><tr>
<th>Sort by name</th><th>Category</th><th>Comments</th><th>Time</th>
<th>Sort by size</th><th>Sort by files</th><th>Bookmark</th>
<th>Sort by snatches</th><th>Sort by seeders</th><th>Sort by leechers</th>
</tr></thead>
<tbody>
<tr>
<td><a href="?72"><img src="/cat.gif" alt="Movies"></a></td>
<td><a class="hv" href="/details.php?id=123">Some.Movie.2024.1080p.BluRay.x264-GRP</a>
<div class="sub">drama, thriller | 1 day ago by SomeUser</div></td>
<td></td><td></td>
<td>1.5 GB</td>
<td>12<br>Go to files</td>
<td></td>
<td>34</td><td>5</td><td>2</td>
<td><a href="/download.php/123/Some.Movie.torrent">dl</a><span class="free">FREE</span></td>
</tr>
</tbody>
</table>
</body></html>`

func TestIPTorrentsCaps(t *testing.T) {
	ix, err := NewIPTorrents(services.Deps{})
	if err != nil {
		t.Fatalf("NewIPTorrents: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID, models.TVParamGenre}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{models.MovieParamQ, models.MovieParamImdbID, models.MovieParamGenre}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if strings.Join(caps.MusicSearchParams, ",") != models.MusicParamQ {
		t.Fatalf("MusicSearchParams: %v", caps.MusicSearchParams)
	}
	if strings.Join(caps.BookSearchParams, ",") != models.BookParamQ {
		t.Fatalf("BookSearchParams: %v", caps.BookSearchParams)
	}
	if !caps.TVSearchImdbAvailable {
		t.Fatal("TVSearchImdbAvailable must be true")
	}
}

// ipCategoryTable mirrors the upstream SetCapabilities mapping table:
// every torznab category must map back to its tracker URL path segment.
var ipCategoryTable = []struct {
	tracker string
	torznab int
}{
	{"72", 2000}, {"87", 2060}, {"77", 2030}, {"101", 2045}, {"89", 2050},
	{"90", 2040}, {"96", 2030}, {"6", 2070}, {"48", 2040}, {"54", 2000},
	{"62", 2030}, {"38", 2010}, {"68", 2000}, {"20", 2080}, {"100", 2040},
	{"7", 2030},
	{"73", 5000}, {"26", 5080}, {"55", 5060}, {"78", 5030}, {"23", 5040},
	{"24", 5030}, {"25", 5030}, {"66", 5030}, {"82", 5020}, {"65", 5000},
	{"83", 5020}, {"79", 5030}, {"22", 5010}, {"5", 5040}, {"99", 5040},
	{"4", 5030},
	{"74", 1000}, {"2", 1090}, {"47", 1090}, {"43", 4050}, {"45", 4050},
	{"71", 1180}, {"50", 1030}, {"44", 1040},
	{"75", 3000}, {"3", 3010}, {"80", 3040}, {"93", 3000}, {"37", 3020},
	{"21", 3050},
	{"76", 8000}, {"60", 5070}, {"1", 4010}, {"86", 4010}, {"64", 3030},
	{"35", 7000}, {"102", 7000}, {"94", 7030}, {"95", 7050}, {"98", 8000},
	{"69", 4030}, {"92", 7010}, {"58", 4040}, {"36", 8000},
	{"88", 6000}, {"85", 6070}, {"8", 6000}, {"81", 6000}, {"91", 6050},
	{"84", 6060},
}

func TestIPTorrentsCategoryMappingFullTable(t *testing.T) {
	ix, err := NewIPTorrents(services.Deps{})
	if err != nil {
		t.Fatalf("NewIPTorrents: %v", err)
	}
	cats := ix.Capabilities().Categories

	for _, tc := range ipCategoryTable {
		// tracker → torznab
		got := cats.MapTrackerCatToNewznab(tc.tracker)
		found := false
		for _, id := range got {
			if id == tc.torznab {
				found = true
			}
		}
		if !found {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", tc.tracker, got, tc.torznab)
		}

		// torznab → tracker URL path segment (upstream MapTorznabCapsToTrackers)
		q := models.NewTorznabQuery()
		q.Categories = []int{tc.torznab}
		segments := cats.MapTorznabCapsToTrackers(q, false)
		found = false
		for _, seg := range segments {
			if seg == tc.tracker {
				found = true
			}
		}
		if !found {
			t.Fatalf("MapTorznabCapsToTrackers(%d) = %v, want it to contain %q", tc.torznab, segments, tc.tracker)
		}
	}
}

func TestIPTorrentsConfigForm(t *testing.T) {
	ix, err := NewIPTorrents(services.Deps{})
	if err != nil {
		t.Fatalf("NewIPTorrents: %v", err)
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
		"sitelink", "cookie", "cookieinstructions", "user-agent",
		"useragentinstructions", "", "sortrequestedfromsite",
		"searchfreeleechonly", "accountinactivity", "tags",
	}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
	// the sort select keeps the upstream option table
	var sortItem map[string]any
	for _, it := range items {
		if it["id"].(string) == "sortrequestedfromsite" {
			sortItem = it
		}
	}
	if sortItem == nil {
		t.Fatal("sort item missing")
	}
	if sortItem["value"].(string) != "time" {
		t.Fatalf("sort default: %v", sortItem["value"])
	}
}

func TestIPTorrentsRequestGeneration(t *testing.T) {
	site := newIPSite(t, ipTorrentsFixture)
	i := newTestIPT(t, site)
	i.cfg.Freeleech.Value = true
	i.cfg.Sort.Value = "size"
	i.cfg.UserAgent.Value = "CustomAgent/1.0"

	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Some Movie"
	q.Categories = []int{2000}
	q.Limit = 100
	q.Offset = 100

	res, err := i.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("paths: %v", paths)
	}
	// 2000 (Movies) → tracker segments 72, 54, 68 in declaration order;
	// upstream encodes query VALUES only (keys ride along raw).
	wantParts := []string{
		"t?72=", "&54=", "&68=", "free=on",
		"q=%2B%28Some+Movie%29", "o=size", "p=2",
	}
	for _, part := range wantParts {
		if !strings.Contains(paths[0], part) {
			t.Fatalf("request %q missing part %q", paths[0], part)
		}
	}

	site.mu.Lock()
	ua, ref := site.lastUA, site.lastRef
	site.mu.Unlock()
	if ua != "CustomAgent/1.0" {
		t.Fatalf("User-Agent header: %q", ua)
	}
	if ref != site.srv.URL+"/t" {
		t.Fatalf("Referer: %q", ref)
	}
}

func TestIPTorrentsImdbRequestGeneration(t *testing.T) {
	site := newIPSite(t, ipTorrentsFixture)
	i := newTestIPT(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.ImdbID = "tt0133093"

	if _, err := i.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	if len(paths) != 1 {
		t.Fatalf("paths: %v", paths)
	}
	if !strings.Contains(paths[0], "q=%2B%28tt0133093%29") || !strings.Contains(paths[0], "qf=all") {
		t.Fatalf("imdb request: %q", paths[0])
	}
}

func TestIPTorrentsParseFixture(t *testing.T) {
	site := newIPSite(t, ipTorrentsFixture)
	i := newTestIPT(t, site)
	i.SetCookieHeader("ipcookie=xyz")

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie"
	res, err := i.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r := res.Releases[0]
	if r.Title != "Some.Movie.2024.1080p.BluRay.x264-GRP" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Size == nil || *r.Size != 1610612736 { // 1.5 * 1024^3
		t.Fatalf("size: %v", r.Size)
	}
	if r.Files == nil || *r.Files != 12 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.Grabs == nil || *r.Grabs != 34 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 5 {
		t.Fatalf("seeders: %v", r.Seeders)
	}
	if r.Peers == nil || *r.Peers != 7 {
		t.Fatalf("peers: %v", r.Peers)
	}
	if age := time.Since(r.PublishDate); age < 0 || age > 48*time.Hour {
		t.Fatalf("publish date (FromTimeAgo '1 day ago'): %v", r.PublishDate)
	}
	if r.Description != "Tags: drama, thriller Uploaded by: SomeUser" {
		t.Fatalf("description: %q", r.Description)
	}
	if strings.Join(r.Genres, ",") != "drama,thriller" {
		t.Fatalf("genres: %v", r.Genres)
	}
	found := false
	for _, c := range r.Category {
		if c == 2000 {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v", r.Category)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf (span.free): %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 || r.MinimumSeedTime == nil || *r.MinimumSeedTime != 1209600 {
		t.Fatalf("minimums: %v %v", r.MinimumRatio, r.MinimumSeedTime)
	}
	if r.Link == nil || !strings.Contains(r.Link.Path, "/download.php/123") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || !strings.Contains(r.Details.Path, "/details.php") {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Guid == nil || r.Guid.String() != r.Details.String() {
		t.Fatalf("guid: %v", r.Guid)
	}

	site.mu.Lock()
	cookie := site.lastCookie
	site.mu.Unlock()
	if cookie != "ipcookie=xyz" {
		t.Fatalf("cookie sent: %q", cookie)
	}
}

// ipTorrentsFixtureNoHeaders exercises the default column indexes
// (size=5, grabs/seeders/leechers = 7/8/9 for 10-cell rows, no files column).
const ipTorrentsFixtureNoHeaders = `<html><body>
<a href="/lout.php">logout</a>
<table id="torrents">
<tbody>
<tr>
<td><a href="?73"><img src="/cat.gif" alt="TV"></a></td>
<td><a class="hv" href="/details.php?id=55">Some.Show.S01.1080p</a>
<a href="/download.php/55/Some.Show.torrent">dl</a>
<div class="sub">comedy | 3 hours ago by Uploader</div></td>
<td></td><td></td><td></td>
<td>700 MB</td>
<td></td>
<td>9</td><td>8</td><td>1</td>
</tr>
</tbody>
</table>
</body></html>`

func TestIPTorrentsParseDefaultColumnIndexes(t *testing.T) {
	site := newIPSite(t, ipTorrentsFixtureNoHeaders)
	i := newTestIPT(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Show"
	res, err := i.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	if r.Title != "Some.Show.S01.1080p" {
		t.Fatalf("title: %q", r.Title)
	}
	// files column disabled → nil
	if r.Files != nil {
		t.Fatalf("files must be nil without the header: %v", r.Files)
	}
	if r.Grabs == nil || *r.Grabs != 9 || r.Seeders == nil || *r.Seeders != 8 || r.Peers == nil || *r.Peers != 9 {
		t.Fatalf("stats: %v %v %v", r.Grabs, r.Seeders, r.Peers)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	found := false
	for _, c := range r.Category {
		if c == 5000 {
			found = true
		}
	}
	if !found {
		t.Fatalf("category: %v", r.Category)
	}
}

func TestIPTorrentsNotLoggedInIsAuthError(t *testing.T) {
	site := newIPSite(t, `<html><body><div>please log in</div></body></html>`)
	i := newTestIPT(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := i.ResultsForQuery(context.Background(), q, false)
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) {
		t.Fatalf("expected IndexerError, got %T: %v", err, err)
	}
	if ie.Kind != indexer.KindAuth {
		t.Fatalf("kind: %v", ie.Kind)
	}
	if !strings.Contains(ie.Message, "not logged in") {
		t.Fatalf("message: %q", ie.Message)
	}
}

func TestIPTorrentsNoTorrentsFoundIsConfigError(t *testing.T) {
	site := newIPSite(t, `<html><body><a href="/lout.php">logout</a>No Torrents Found!</body></html>`)
	i := newTestIPT(t, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	_, err := i.ResultsForQuery(context.Background(), q, false)
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) {
		t.Fatalf("expected IndexerError, got %T: %v", err, err)
	}
	if ie.Kind != indexer.KindConfig {
		t.Fatalf("kind: %v", ie.Kind)
	}
}

func TestIPTorrentsApplyConfiguration(t *testing.T) {
	site := newIPSite(t, ipTorrentsFixture)
	i := newTestIPT(t, site)

	status, err := i.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "cookie", Value: "uid=123; pass=abc"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v", status)
	}
	if !i.IsConfigured() {
		t.Fatal("must be configured after success")
	}
	site.mu.Lock()
	cookie := site.lastCookie
	site.mu.Unlock()
	if cookie != "uid=123; pass=abc" {
		t.Fatalf("cookie sent: %q", cookie)
	}
}

func TestIPTorrentsApplyConfigurationBadCookie(t *testing.T) {
	site := newIPSite(t, `<html><body>please log in</body></html>`)
	i := newTestIPT(t, site)

	_, err := i.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "cookie", Value: "badcookie"},
	})
	if err == nil || !strings.Contains(err.Error(), "Your cookie did not work") {
		t.Fatalf("err: %v", err)
	}
	if i.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestIPCleanTitle(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Some.Movie.2024.1080p", "Some.Movie.2024.1080p"},
		{"Some Movie [REQ]", "Some Movie"},
		{"Some Movie (Requested)", "Some Movie"},
		{"[REQ]Some Movie", "Some Movie"},
		{"[eng] Anime.Show.S01.1080p-grp", "Anime.Show.S01.1080p-grp"},
		{"Drop\x01invalid\x02chars", "Dropinvalidchars"},
	}
	for _, tc := range cases {
		if got := ipCleanTitle(tc.in); got != tc.want {
			t.Fatalf("ipCleanTitle(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}
