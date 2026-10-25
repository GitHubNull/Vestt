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
)

type tvStoreSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]func(w http.ResponseWriter, r *http.Request)
}

func newTVStoreSite(t *testing.T, serve map[string]func(w http.ResponseWriter, r *http.Request)) *tvStoreSite {
	t.Helper()
	s := &tvStoreSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		fn, ok := s.serve[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fn(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *tvStoreSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestTVStore(t *testing.T, site *tvStoreSite) *TVStore {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewTVStore(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewTVStore: %v", err)
	}
	tv := ix.(*TVStore)
	tv.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(tv.ID(), 0)
	return tv
}

// tvsTestPage builds a br_process.php payload: 3 page-info items followed by
// 27 parts per torrent (upstream comment, cs:152-157).
func tvsTestPage(total, perPage, thisPage string, rows ...[]string) string {
	parts := []string{total, perPage, thisPage}
	for _, row := range rows {
		parts = append(parts, row...)
	}
	return strings.Join(parts, `\`)
}

var tvsRow1 = []string{
	"1", "1727", "207244", "1x08 [WebDL-720p - Eng]", "gb", "0", "2018-03-09 08:11:53",
	`<a href='#m=4'><span class='mufaj'>dráma</span></a>, <a href='#m=13'><span class='mufaj'>romantika</span></a>&nbsp;`,
	"0", "0", "1", "191170047", "1", "0", "Anonymous", "50", "0", "0", "", "0", "4", "0", "174", "0", "", "0", "0",
}

var tvsRow2 = []string{
	"1", "855", "207243", "1-2. évad [HDTV-1080p]", "gb", "0", "2018-03-10 09:12:00",
	`<a href='#m=4'><span class='mufaj'>vígjáték</span></a>&nbsp;`,
	"0", "0", "2", "305729738", "1", "0", "Anonymous", "50", "0", "0", "", "0", "8", "2", "102", "0", "", "0", "0",
}

const tvsBrowsePage = `<html><body><script>
var catl = new Array();
catl[5]=1727; catIM[1727]='0133093';
catl[9]=855; catIM[855]='0944947';
</script></body></html>`

func tvsStandardServe(t *testing.T) map[string]func(w http.ResponseWriter, r *http.Request) {
	t.Helper()
	return map[string]func(w http.ResponseWriter, r *http.Request){
		"/torrent/browse.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(tvsBrowsePage))
		},
		"/torrent/br_process.php": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("func") == "getToggle" {
				switch r.URL.Query().Get("id") {
				case "207244":
					_, _ = w.Write([]byte(`0\\Some.Show.S01E08.720p.WEB-DL.x264-GRP.mkv\\Some.Show.S01E08.nfo`))
				case "207243":
					_, _ = w.Write([]byte(`0\\Some.Show.S01E01E02.1080p.HDTV.x264-GRP.mkv\\sample.txt`))
				default:
					_, _ = w.Write([]byte("none"))
				}
				return
			}
			_, _ = w.Write([]byte(tvsTestPage("2", "25", "2", tvsRow1, tvsRow2)))
		},
	}
}

func TestTVStoreCaps(t *testing.T) {
	ix, err := NewTVStore(services.Deps{})
	if err != nil {
		t.Fatalf("NewTVStore: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != strings.Join([]string{
		models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID,
	}, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join([]string{
		models.MovieParamQ, models.MovieParamImdbID,
	}, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"1", 5000}, {"2", 5040}, {"3", 5030},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		if len(got) != 1 || got[0] != tc.want {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v want [%d]", tc.tracker, got, tc.want)
		}
	}
}

func TestTVStoreConfigForm(t *testing.T) {
	ix, err := NewTVStore(services.Deps{})
	if err != nil {
		t.Fatalf("NewTVStore: %v", err)
	}
	tv := ix.(*TVStore)
	items, err := tv.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	if len(ids) != 5 || ids[0] != "sitelink" || ids[1] != "username" || ids[2] != "password" || ids[3] != "accountinactivity" || ids[4] != "tags" {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestTVStoreLoginFail(t *testing.T) {
	site := newTVStoreSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/login.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html><body>login form</body></html>"))
		},
		"/takelogin.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html><body>Hibás felhasználónév</body></html>"))
		},
	})
	tv := newTestTVStore(t, site)

	_, err := tv.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "wrong"},
	})
	// IndexerError.Error() renders "Exception (tvstore): <message>" (upstream
	// throws ExceptionWithConfigData("Error while trying to login."),
	// TVStore.cs:96-98).
	if err == nil || err.Error() != "Exception (tvstore): Error while trying to login." {
		t.Fatalf("err: %v", err)
	}
	if tv.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}

func TestTVStoreLoginOK(t *testing.T) {
	site := newTVStoreSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/login.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html><body>login form</body></html>"))
		},
		"/takelogin.php": func(w http.ResponseWriter, _ *http.Request) {
			http.SetCookie(w, &http.Cookie{Name: "uid", Value: "12345"})
			_, _ = w.Write([]byte("<html><body>Főoldal — logged in</body></html>"))
		},
	})
	tv := newTestTVStore(t, site)

	status, err := tv.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
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
	if !tv.IsConfigured() {
		t.Fatal("must be configured after login")
	}
	if !strings.Contains(tv.CookieHeader(), "uid=12345") {
		t.Fatalf("cookie header: %q", tv.CookieHeader())
	}
}

func TestTVStoreSearchRequestAndParse(t *testing.T) {
	site := newTVStoreSite(t, tvsStandardServe(t))
	tv := newTestTVStore(t, site)
	tv.SetCookieHeader("uid=12345")

	season := 1
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Some Show"
	q.Season = &season
	q.Episode = "2"

	res, err := tv.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	paths := site.paths()
	if len(paths) != 4 { // browse (imdb map) + list + 2× getToggle
		t.Fatalf("hits: %v", paths)
	}
	if paths[0] != "GET /torrent/browse.php" {
		t.Fatalf("first hit: %v", paths[0])
	}
	var searchHit string
	for _, p := range paths[1:] {
		if strings.HasPrefix(p, "GET /torrent/br_process.php?") && !strings.Contains(p, "func=getToggle") {
			searchHit = p
		}
	}
	if searchHit == "" {
		t.Fatalf("no search hit in %v", paths)
	}
	uri := strings.TrimPrefix(searchHit, "GET ")
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	query := u.Query()
	if query.Get("p") != "1" || query.Get("g") != "0" || query.Get("s") != "1" || query.Get("e") != "2" {
		t.Fatalf("query params: %v", query)
	}
	if query.Get("now") == "" {
		t.Fatal("now param missing")
	}
	if got := query.Get("c"); got != "U29tZSBTaG93" { // base64("Some Show")
		t.Fatalf("c param: %q", got)
	}

	r := res.Releases[0]
	if r.Title != "Some.Show.S01E08.720p.WEB-DL.x264-GRP" {
		t.Fatalf("title: %q", r.Title)
	}
	// url.URL.Path excludes the query string; assert Path and RawQuery apart.
	if r.Link == nil || !strings.HasSuffix(r.Link.Path, "/torrent/download.php") || r.Link.RawQuery != "id=207244" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Size == nil || *r.Size != 191170047 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Files == nil || *r.Files != 1 {
		t.Fatalf("files: %v", r.Files)
	}
	if r.Seeders == nil || *r.Seeders != 4 || r.Peers == nil || *r.Peers != 4 {
		t.Fatalf("seeders/peers: %v %v", r.Seeders, r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 174 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if !hasInt(r.Category, 5040) { // WEB-DL + 720p → TVHD
		t.Fatalf("category: %v", r.Category)
	}
	if r.PublishDate.Format("2006-01-02 15:04:05") != "2018-03-09 08:11:53" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Imdb == nil || *r.Imdb != 133093 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	if r.Description != "dráma, romantika" {
		t.Fatalf("description: %q", r.Description)
	}
	if len(r.Genres) != 2 || r.Genres[0] != "dráma" || r.Genres[1] != "romantika" {
		t.Fatalf("genres: %v", r.Genres)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 2 { // old single episode
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 172800 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}

	// season pack row: E01 stripped after S01, upload factor 4
	r2 := res.Releases[1]
	if r2.Title != "Some.Show.S01E02.1080p.HDTV.x264-GRP" {
		t.Fatalf("title2: %q", r2.Title)
	}
	if r2.UploadVolumeFactor == nil || *r2.UploadVolumeFactor != 4 {
		t.Fatalf("uvf2: %v", r2.UploadVolumeFactor)
	}
	if r2.Imdb == nil || *r2.Imdb != 944947 {
		t.Fatalf("imdb2: %v", r2.Imdb)
	}
}

func TestTVStoreSearchTermSeasonEpisodeExtraction(t *testing.T) {
	site := newTVStoreSite(t, tvsStandardServe(t))
	tv := newTestTVStore(t, site)
	tv.SetCookieHeader("uid=12345")

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Show S01E02"

	if _, err := tv.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	var searchHit string
	for _, p := range site.paths() {
		if strings.HasPrefix(p, "GET /torrent/br_process.php?") && !strings.Contains(p, "func=getToggle") {
			searchHit = p
		}
	}
	if searchHit == "" {
		t.Fatalf("hits: %v", site.paths())
	}
	u, err := url.Parse(strings.TrimPrefix(searchHit, "GET "))
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	query := u.Query()
	// SnnEnn stripped from the term, forwarded as s/e params
	if got := query.Get("c"); got != "U29tZSBTaG93" {
		t.Fatalf("c param: %q", got)
	}
	if query.Get("s") != "1" || query.Get("e") != "02" {
		t.Fatalf("s/e params: %v", query)
	}
}

func TestTVStoreImdbQuery(t *testing.T) {
	site := newTVStoreSite(t, tvsStandardServe(t))
	tv := newTestTVStore(t, site)
	tv.SetCookieHeader("uid=12345")

	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.ImdbID = "tt0944947"

	res, err := tv.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	var searchHit string
	for _, p := range site.paths() {
		if strings.HasPrefix(p, "GET /torrent/br_process.php?") && !strings.Contains(p, "func=getToggle") {
			searchHit = p
		}
	}
	u, _ := url.Parse(strings.TrimPrefix(searchHit, "GET "))
	if got := u.Query().Get("g"); got != "855" {
		t.Fatalf("g param: %q (hits %v)", got, site.paths())
	}

	// unknown imdb id → no search at all
	site2 := newTVStoreSite(t, tvsStandardServe(t))
	tv2 := newTestTVStore(t, site2)
	tv2.SetCookieHeader("uid=12345")
	q2 := models.NewTorznabQuery()
	q2.QueryType = "tvsearch"
	q2.ImdbID = "tt9999999"
	res2, err := tv2.ResultsForQuery(context.Background(), q2, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res2.Releases) != 0 {
		t.Fatalf("releases: %d", len(res2.Releases))
	}
	for _, p := range site2.paths() {
		if strings.HasPrefix(p, "GET /torrent/br_process.php") {
			t.Fatalf("search must not be issued for unknown imdb: %v", site2.paths())
		}
	}
}

func TestTVStoreEmptyResult(t *testing.T) {
	serve := tvsStandardServe(t)
	serve["/torrent/br_process.php"] = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(tvsTestPage("0", "25", "0")))
	}
	site := newTVStoreSite(t, serve)
	tv := newTestTVStore(t, site)
	tv.SetCookieHeader("uid=12345")

	res, err := tv.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
}

func TestTVStoreParseError(t *testing.T) {
	serve := tvsStandardServe(t)
	serve["/torrent/br_process.php"] = func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("garbage-without-delimiters"))
	}
	site := newTVStoreSite(t, serve)
	tv := newTestTVStore(t, site)
	tv.SetCookieHeader("uid=12345")

	_, err := tv.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err == nil {
		t.Fatal("expected parse error")
	}
	ie, ok := err.(*indexer.IndexerError)
	if !ok || ie.Kind != indexer.KindParse {
		t.Fatalf("err: %#v", err)
	}
}

func TestTVStoreQualityParser(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
	}{
		{"Some.Show.S01E01.720p.WEB-DL.x264-GRP", 5040},
		{"Some.Show.S01E01.480p.WEB-DL.x264-GRP", 5030},
		{"Some.Show.S01E01.1080p.HDTV.x264-GRP", 5040},
		{"Some.Show.S01E01.HDTV.x264-GRP", 5030},
		{"Some.Show.S01E01.720p.BluRay.x264-GRP", 5040},
		{"Some.Show.S01E01.576p.BluRay.Xvid-GRP", 5030},
		{"Some.Show.S01E01.DVDRip.Xvid-GRP", 5030},
		{"Some.Show.S01E01.hr-ws.PDTV.x264-GRP", 5040},
		{"Some.Show.S01E01.PDTV.x264-GRP", 5030},
		// 1080i-HDTV resolves through the hdtv branch, which returns TVSD for
		// anything but 720p/1080p; the RawHdRegex "1080i-HDTV" alternative is
		// never reached (upstream TvCategoryParser.cs:59-69 vs :25/:106).
		{"Some.Show.S01E01.1080i-HDTV.MPEG2-GRP", 5030},
		{"Some.Show.S01E01.SATRip.x264-GRP", 5000},
	} {
		if got := tvsParseTvShowQuality(tc.name); got != tc.want {
			t.Fatalf("tvsParseTvShowQuality(%q) = %d want %d", tc.name, got, tc.want)
		}
	}
}
