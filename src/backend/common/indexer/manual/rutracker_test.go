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

	"golang.org/x/text/encoding/htmlindex"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
	"vestt/common/utils/parseutil"
)

// rtTestSite is an httptest front-end recording every request (method +
// URI) plus the last POST body per path; serve decides the response and
// gets the per-path call index so tests can script multi-step flows.
type rtTestSite struct {
	srv    *httptest.Server
	mu     sync.Mutex
	hits   []string
	posts  map[string]string
	counts map[string]int
}

func newRtTestSite(t *testing.T, serve func(call int, w http.ResponseWriter, r *http.Request)) *rtTestSite {
	t.Helper()
	s := &rtTestSite{posts: map[string]string{}, counts: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			s.posts[r.URL.Path] = string(body)
		}
		s.counts[r.URL.Path]++
		call := s.counts[r.URL.Path]
		s.mu.Unlock()
		serve(call, w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *rtTestSite) hitList() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *rtTestSite) lastPost(path string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.posts[path]
}

// rtWrite writes s re-encoded from UTF-8 to windows-1251, mirroring the
// upstream site encoding.
func rtWrite(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	enc, err := htmlindex.Get("windows-1251")
	if err != nil {
		t.Fatalf("htmlindex.Get: %v", err)
	}
	encoded, err := enc.NewEncoder().Bytes([]byte(body))
	if err != nil {
		t.Fatalf("encode windows-1251: %v", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=windows-1251")
	_, _ = w.Write(encoded)
}

func newTestRuTracker(t *testing.T, site *rtTestSite) *RuTracker {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewRuTracker(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewRuTracker: %v", err)
	}
	rt := ix.(*RuTracker)
	client.SetRequestDelay(rt.ID(), 0)
	t.Cleanup(func() { client.SetRequestDelay(rt.ID(), 0) })
	if site != nil {
		rt.SiteLinkValue = site.srv.URL + "/"
	}
	return rt
}

// rtApplyOK logs the indexer in against the test site (POSTs valid
// credentials) and requires success.
func rtApplyOK(t *testing.T, ctx context.Context, rt *RuTracker, site *rtTestSite) {
	t.Helper()
	status, err := rt.ApplyConfiguration(ctx, []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "testuser"},
		{ID: "password", Value: "testpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status: %v, want ConfigRequiresTesting", status)
	}
	if !rt.IsConfigured() {
		t.Fatal("indexer must be configured after successful login")
	}
}

const rtLoggedInHeader = `<html><body><span id="logged-in-username">testuser</span>`

const rtEmptyResultsPage = rtLoggedInHeader + `
<table id="tor-tbl"><tbody></tbody></table></body></html>`

// rtResultsFixture has two fully populated rows (forum 9 = TV, forum 7 =
// Movies/Foreign) plus a row without a download link (under moderation).
const rtResultsFixture = rtLoggedInHeader + `
<table id="tor-tbl"><tbody>
<tr class="tCenter hl-tr" id="trs-1001">
  <td class="row1 f-name-col"><div class="f-name"><a href="viewforum.php?f=9">Русские сериалы</a></div></td>
  <td class="row1 t-title-col"><div class="wbr t-title"><a href="viewtopic.php?t=1001" class="tLink">Некий сериал / Some Show (Сезон 1, Серии 1-2 из 16) [2021, WEB-DL 1080p]</a></div></td>
  <td class="row1 u-name-col"><div><a class="u-name">UploaderOne</a></div></td>
  <td class="row1 tor-size" data-ts_text="1.46 GB"><a class="tr-dl" href="dl.php?t=1001">DL</a></td>
  <td class="row1">—</td>
  <td class="row1">—</td>
  <td class="row1"><b>15</b></td>
  <td class="row1">3</td>
  <td class="row1">120</td>
  <td class="row1" data-ts_text="1700000000">15-Ноя-23</td>
</tr>
<tr class="tCenter hl-tr" id="trs-1002">
  <td class="row1 f-name-col"><div class="f-name"><a href="viewforum.php?f=7">Зарубежное кино</a></div></td>
  <td class="row1 t-title-col"><div class="wbr t-title"><a href="viewtopic.php?t=1002" class="tLink">Ирландец / The Irishman (Мартин Скорсезе / Martin Scorsese) [2019, США, криминал, драма, WEB-DL 1080p]</a></div></td>
  <td class="row1 u-name-col"><div><a class="u-name">UploaderTwo</a></div></td>
  <td class="row1 tor-size" data-ts_text="2.05 GB"><a class="tr-dl" href="dl.php?t=1002">DL</a></td>
  <td class="row1">—</td>
  <td class="row1">—</td>
  <td class="row1"><b>42</b></td>
  <td class="row1">5</td>
  <td class="row1">999</td>
  <td class="row1" data-ts_text="1700000100">15-Ноя-23</td>
</tr>
<tr class="tCenter hl-tr" id="trs-1003">
  <td class="row1 f-name-col"><div class="f-name"><a href="viewforum.php?f=22">Наше кино</a></div></td>
  <td class="row1 t-title-col"><div class="wbr t-title"><a href="viewtopic.php?t=1003" class="tLink">Модерируемая раздача / Moderated release</a></div></td>
  <td class="row1 u-name-col"><div><a class="u-name">UploaderThree</a></div></td>
  <td class="row1 tor-size" data-ts_text="700 MB">DL pending</td>
  <td class="row1">—</td>
  <td class="row1">—</td>
  <td class="row1">2 дн</td>
  <td class="row1">0</td>
  <td class="row1">0</td>
  <td class="row1" data-ts_text="1699999900">15-Ноя-23</td>
</tr>
</tbody></table></body></html>`

func TestRuTrackerCaps(t *testing.T) {
	rt, err := NewRuTracker(services.Deps{})
	if err != nil {
		t.Fatalf("NewRuTracker: %v", err)
	}
	caps := rt.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != "q,season,ep" {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != "q" ||
		strings.Join(caps.MusicSearchParams, ",") != "q" ||
		strings.Join(caps.BookSearchParams, ",") != "q" {
		t.Fatalf("movie/music/book params: %v %v %v", caps.MovieSearchParams, caps.MusicSearchParams, caps.BookSearchParams)
	}
	if !caps.SupportsRawSearch {
		t.Fatal("SupportsRawSearch must be true")
	}

	// The full upstream table is transcribed verbatim (1325 rows).
	if len(rutrackerCategoryMappings) != 1325 {
		t.Fatalf("category mappings: %d, want 1325", len(rutrackerCategoryMappings))
	}
	if got := len(caps.Categories.GetTrackerCategories()); got != 1325 {
		t.Fatalf("tracker categories: %d, want 1325", got)
	}

	// tracker id → newznab (representative sample across the table).
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"22", 2000},   // Наше кино → Movies
		{"7", 2010},    // Зарубежное кино → Movies/Foreign
		{"9", 5000},    // Русские сериалы → TV
		{"33", 5070},   // Аниме → TV/Anime
		{"2185", 1090}, // Видео для консолей → Console/Other
		{"1012", 4000}, // Операционные системы от Microsoft → PC
		{"10", 8010},   // Разное (раздачи) → Other/Misc
		{"862", 7030},  // Комиксы, манга, ранобэ → Books/Comics
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

	// newznab → tracker ids (reverse direction).
	rev := caps.Categories.MapTorznabCapsToTrackers(&models.TorznabQuery{Categories: []int{2000}}, true)
	joined := strings.Join(rev, ",")
	if !strings.Contains(joined, "22") || !strings.Contains(joined, "1950") {
		t.Fatalf("MapTorznabCapsToTrackers(2000) = %v, want it to contain 22 and 1950", rev)
	}
}

func TestRuTrackerConfigForm(t *testing.T) {
	rt, err := NewRuTracker(services.Deps{})
	if err != nil {
		t.Fatalf("NewRuTracker: %v", err)
	}
	items, err := rt.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{
		"sitelink", "username", "password", "captchaimage", "captchatext", "",
		"searchbyuploader", "searchbyuploaderinfo", "usemagnetlinks",
		"striprussianletters", "addrus", "movetagsinfo",
		"movefirsttagstoendofreleasetitle", "movealltagstoendofreleasetitle",
		"captchainfo", "tags",
	}
	if len(ids) != len(want) {
		t.Fatalf("config ids: %#v", ids)
	}
	for i, w := range want {
		if w != "" && !strings.HasPrefix(ids[i], w) {
			t.Fatalf("config id %d: %q want prefix %q (all: %#v)", i, ids[i], w, ids)
		}
	}
	byID := map[string]map[string]any{}
	for _, it := range items {
		byID[it["id"].(string)] = it
	}
	// upstream defaults
	if byID["usemagnetlinks"]["value"] != false {
		t.Fatalf("usemagnetlinks default: %v", byID["usemagnetlinks"]["value"])
	}
	if byID["striprussianletters"]["value"] != true {
		t.Fatalf("striprussianletters default: %v", byID["striprussianletters"]["value"])
	}
	if byID["addrus..."] != nil {
		t.Fatal("addrus must not be a key")
	}
	if byID["movefirsttagstoendofreleasetitle"]["value"] != false ||
		byID["movealltagstoendofreleasetitle"]["value"] != false {
		t.Fatal("move-tags defaults must be false")
	}
	// hidden items never appear in the setup form
	for _, hidden := range []string{"cookieheader", "lasterror", "captchacookie"} {
		if _, ok := byID[hidden]; ok {
			t.Fatalf("hidden item %q must not be in the setup form", hidden)
		}
	}
}

func TestRuTrackerApplyConfigurationSuccess(t *testing.T) {
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forum/login.php" {
			w.Header().Set("Set-Cookie", "bb_session=rutracker-test-cookie; Path=/")
			rtWrite(t, w, rtLoggedInHeader+"</body></html>")
			return
		}
		http.NotFound(w, r)
	})
	rt := newTestRuTracker(t, site)
	rtApplyOK(t, context.Background(), rt, site)

	if got := site.lastPost("/forum/login.php"); got != "login_username=testuser&login_password=testpass&login=Login" {
		t.Fatalf("login POST body: %q", got)
	}
	if got := rt.CookieHeader(); got != "bb_session=rutracker-test-cookie" {
		t.Fatalf("cookie header after login: %q", got)
	}
}

func TestRuTrackerApplyConfigurationLoginError(t *testing.T) {
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forum/login.php" {
			rtWrite(t, w, `<html><body><div class="msg-main">Неверное имя пользователя</div></body></html>`)
			return
		}
		http.NotFound(w, r)
	})
	rt := newTestRuTracker(t, site)
	status, err := rt.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "testuser"},
		{ID: "password", Value: "wrong"},
	})
	if err == nil {
		t.Fatal("login failure must return an error")
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status: %v, want ConfigCompleted on failure", status)
	}
	want := "Exception (rutracker): Неверное имя пользователя"
	if err.Error() != want {
		t.Fatalf("err: %q want %q", err.Error(), want)
	}
	if rt.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}

func TestRuTrackerApplyConfigurationDefaultErrorMessage(t *testing.T) {
	// no error node in the response → upstream falls back to the generic text
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		rtWrite(t, w, `<html><body><p>nothing here</p></body></html>`)
	})
	rt := newTestRuTracker(t, site)
	_, err := rt.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "u"},
		{ID: "password", Value: "p"},
	})
	if err == nil || err.Error() != "Exception (rutracker): RuTracker authentication failed" {
		t.Fatalf("err: %v", err)
	}
}

func TestRuTrackerLoginWithCaptcha(t *testing.T) {
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "bb_session=captcha-cookie; Path=/")
		rtWrite(t, w, rtLoggedInHeader+"</body></html>")
	})
	rt := newTestRuTracker(t, site)
	// captcha state normally comes from ConfigurationForSetup; prime it
	// directly (the captcha image host is hard-wired to static.rutracker.cc
	// upstream and cannot be redirected to the test server).
	rt.capSid = "SID123"
	rt.capCodeField = "cap_code_abc"
	_, err := rt.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "testuser"},
		{ID: "password", Value: "testpass"},
		{ID: "captchatext", Value: "XYZ"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	got := site.lastPost("/forum/login.php")
	want := "login_username=testuser&login_password=testpass&login=Login&cap_sid=SID123&cap_code_abc=XYZ"
	if got != want {
		t.Fatalf("login POST body:\n got %q\nwant %q", got, want)
	}
	// upstream clears the captcha state after use
	if rt.capSid != "" || rt.capCodeField != "" {
		t.Fatalf("captcha state must be cleared: %q %q", rt.capSid, rt.capCodeField)
	}
}

func TestRuTrackerConfigurationForSetupNoCaptcha(t *testing.T) {
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		rtWrite(t, w, `<html><body><form action="login.php"><input name="login_username"/></form></body></html>`)
	})
	rt := newTestRuTracker(t, site)
	if _, err := rt.ConfigurationForSetup(context.Background()); err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	if rt.cfg.CaptchaImage.Value != nil {
		t.Fatal("captcha image must be nil when the login page has none")
	}
	if got := site.hitList(); len(got) != 1 || got[0] != "GET /forum/login.php" {
		t.Fatalf("hits: %v", got)
	}
}

func TestRuTrackerSearchRequestPlainQ(t *testing.T) {
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		rtWrite(t, w, rtEmptyResultsPage)
	})
	rt := newTestRuTracker(t, site)
	rt.SetConfigured(true)
	rt.SetCookieHeader("bb_session=x")

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some Movie 2024!"
	if _, err := rt.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	want := []string{"GET /forum/tracker.php?nm=Some%25Movie%252024%25"}
	if got := site.hitList(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hits:\n got %v\nwant %v", got, want)
	}
}

func TestRuTrackerSearchRequestTVSeasonEpisode(t *testing.T) {
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		rtWrite(t, w, rtEmptyResultsPage)
	})
	rt := newTestRuTracker(t, site)
	rt.SetConfigured(true)
	rt.SetCookieHeader("bb_session=x")

	season := 2
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Show Name"
	q.Season = &season
	q.Episode = "3"
	if _, err := rt.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	want := "GET /forum/tracker.php?nm=Show%25Name+%D0%A2%D0%92+%7C+%D0%A1%D0%B5%D0%B7%D0%BE%D0%BD%3A+2+%D0%A1%D0%B5%D1%80%D0%B8%D0%B8%3A+3"
	if got := site.hitList(); len(got) != 1 || got[0] != want {
		t.Fatalf("hit:\n got %v\nwant %q", got, want)
	}
}

// TestRuTrackerSearchSessionRefreshOrder pins the upstream cookie-session
// request order (RuTracker.cs PerformQuery): the search URL is requested
// FIRST; only when the logged-in marker is missing does the indexer re-login
// (POST) and repeat the SAME search URL.
func TestRuTrackerSearchSessionRefreshOrder(t *testing.T) {
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/forum/tracker.php":
			if call == 1 {
				// session expired: no logged-in marker
				rtWrite(t, w, `<html><body><p>login required</p></body></html>`)
				return
			}
			rtWrite(t, w, rtEmptyResultsPage)
		case "/forum/login.php":
			w.Header().Set("Set-Cookie", "bb_session=refreshed; Path=/")
			rtWrite(t, w, rtLoggedInHeader+"</body></html>")
		default:
			http.NotFound(w, r)
		}
	})
	rt := newTestRuTracker(t, site)
	rtApplyOK(t, context.Background(), rt, site)

	// clobber the stored session so the first search sees an expired page
	rt.SetCookieHeader("bb_session=expired")

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Show"
	res, err := rt.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	// the setup login (rtApplyOK) is hit 0; then the search → re-login →
	// re-search sequence
	want := []string{
		"POST /forum/login.php",
		"GET /forum/tracker.php?nm=Show",
		"POST /forum/login.php",
		"GET /forum/tracker.php?nm=Show",
	}
	if got := site.hitList(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("request order:\n got %v\nwant %v", got, want)
	}
}

func TestRuTrackerCreateSearchURLs(t *testing.T) {
	ix, err := NewRuTracker(services.Deps{})
	if err != nil {
		t.Fatalf("NewRuTracker: %v", err)
	}
	rt := ix.(*RuTracker)
	rt.SiteLinkValue = "https://example.test/"

	// empty term: getnew view, empty nm param
	q := models.NewTorznabQuery()
	urls := rt.createSearchURLs(q)
	if len(urls) != 1 || urls[0] != "https://example.test/forum/tracker.php?nm=" {
		t.Fatalf("empty search urls: %v", urls)
	}

	// search by uploader adds pn=
	rt.cfg.SearchByUploader.Value = "SomeUploader"
	urls = rt.createSearchURLs(models.NewTorznabQuery())
	if len(urls) != 1 || urls[0] != "https://example.test/forum/tracker.php?pn=SomeUploader&nm=" {
		t.Fatalf("uploader urls: %v", urls)
	}
	rt.cfg.SearchByUploader.Value = ""

	// categories map to forum ids via f= (Movies 2000 → forum 22 first);
	// upstream GetQueryString UrlEncodes the comma separators as %2C
	catq := models.NewTorznabQuery()
	catq.Categories = []int{2000}
	urls = rt.createSearchURLs(catq)
	if len(urls) != 1 ||
		!strings.HasPrefix(urls[0], "https://example.test/forum/tracker.php?nm=&f=22%2C941%2C1666") ||
		!strings.Contains(urls[0], "%2C1950%2C") {
		t.Fatalf("category urls: %v", urls)
	}
}

func TestRuTrackerParseFixture(t *testing.T) {
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/forum/tracker.php":
			rtWrite(t, w, rtResultsFixture)
		case "/forum/login.php":
			rtWrite(t, w, rtLoggedInHeader+"</body></html>")
		default:
			http.NotFound(w, r)
		}
	})
	rt := newTestRuTracker(t, site)
	rtApplyOK(t, context.Background(), rt, site)

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some"
	res, err := rt.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// 3 rows in the fixture; the one under moderation (no tr-dl) is skipped
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	// ordered by descending publish date: the Irishman row (1700000100) first
	r0 := res.Releases[0]
	if r0.Title != "The Irishman [2019, WEB-DL 1080p]" {
		t.Fatalf("title[0]: %q", r0.Title)
	}
	if r0.PublishDate.Unix() != 1700000100 {
		t.Fatalf("publish date[0]: %v", r0.PublishDate)
	}
	wantSize := parseutil.GetBytes("2.05 GB")
	if r0.Size == nil || *r0.Size != wantSize {
		t.Fatalf("size[0]: %v want %d", r0.Size, wantSize)
	}
	if r0.Seeders == nil || *r0.Seeders != 42 {
		t.Fatalf("seeders[0]: %v", r0.Seeders)
	}
	if r0.Peers == nil || *r0.Peers != 47 { // leechers 5 + seeders 42
		t.Fatalf("peers[0]: %v (seeders %v)", *r0.Peers, *r0.Seeders)
	}
	if r0.Grabs == nil || *r0.Grabs != 999 {
		t.Fatalf("grabs[0]: %v", r0.Grabs)
	}
	found2010 := false
	for _, c := range r0.Category {
		if c == 2010 {
			found2010 = true
		}
	}
	if !found2010 {
		t.Fatalf("category[0]: %v want it to contain 2010", r0.Category)
	}
	base := site.srv.URL + "/forum/"
	if r0.Details == nil || r0.Details.String() != base+"viewtopic.php?t=1002" {
		t.Fatalf("details[0]: %v", r0.Details)
	}
	if r0.Link == nil || r0.Link.String() != base+"dl.php?t=1002" {
		t.Fatalf("link[0]: %v", r0.Link)
	}
	if r0.Guid == nil || r0.Guid.String() != base+"viewtopic.php?t=1002" {
		t.Fatalf("guid[0]: %v", r0.Guid)
	}
	if r0.Description != "Uploader: UploaderTwo<br>Ирландец / The Irishman (Мартин Скорсезе / Martin Scorsese) [2019, США, криминал, драма, WEB-DL 1080p]" {
		t.Fatalf("description[0]: %q", r0.Description)
	}
	if r0.DownloadVolumeFactor == nil || *r0.DownloadVolumeFactor != 1 ||
		r0.UploadVolumeFactor == nil || *r0.UploadVolumeFactor != 1 {
		t.Fatalf("volume factors[0]: %v %v", r0.DownloadVolumeFactor, r0.UploadVolumeFactor)
	}

	r1 := res.Releases[1]
	if r1.Title != "Some Show (S1E1-2 of 16) [2021, WEB-DL 1080p]" {
		t.Fatalf("title[1]: %q", r1.Title)
	}
	if r1.PublishDate.Unix() != 1700000000 {
		t.Fatalf("publish date[1]: %v", r1.PublishDate)
	}
	found5000 := false
	for _, c := range r1.Category {
		if c == 5000 {
			found5000 = true
		}
	}
	if !found5000 {
		t.Fatalf("category[1]: %v want it to contain 5000", r1.Category)
	}
	if r1.Link == nil || r1.Link.String() != base+"dl.php?t=1001" {
		t.Fatalf("link[1]: %v", r1.Link)
	}

	// search request went to tracker.php with the wildcard-sanitized term
	got := site.hitList()
	if len(got) != 2 || got[1] != "GET /forum/tracker.php?nm=Some" {
		t.Fatalf("hits: %v", got)
	}
}

func TestRuTrackerParseUseMagnetLinks(t *testing.T) {
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		rtWrite(t, w, rtResultsFixture)
	})
	rt := newTestRuTracker(t, site)
	rt.SetConfigured(true)
	rt.SetCookieHeader("bb_session=x")
	rt.cfg.UseMagnetLinks.Value = true

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "Some"
	res, err := rt.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	// with UseMagnetLinks the details page is the download link
	base := site.srv.URL + "/forum/"
	if got := res.Releases[1].Link; got == nil || got.String() != base+"viewtopic.php?t=1001" {
		t.Fatalf("magnet-mode link: %v", got)
	}
}

func TestRuTrackerDownloadFetchesMagnet(t *testing.T) {
	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Thing"
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		rtWrite(t, w, `<html><body><table class="attach"><tr><td>`+
			`<a class="magnet-link" href="`+magnet+`">magnet</a></td></tr></table></body></html>`)
	})
	rt := newTestRuTracker(t, site)
	rt.SetConfigured(true)
	rt.SetCookieHeader("bb_session=x")
	rt.cfg.UseMagnetLinks.Value = true

	link, err := url.Parse(site.srv.URL + "/forum/viewtopic.php?t=555")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	got, err := rt.Download(context.Background(), link)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != magnet {
		t.Fatalf("download body: %q want %q", got, magnet)
	}
	if hits := site.hitList(); len(hits) != 1 || hits[0] != "GET /forum/viewtopic.php?t=555" {
		t.Fatalf("hits: %v", hits)
	}
}

func TestRuTrackerDownloadMissingMagnetIsError(t *testing.T) {
	site := newRtTestSite(t, func(call int, w http.ResponseWriter, r *http.Request) {
		rtWrite(t, w, `<html><body><table class="attach"></table></body></html>`)
	})
	rt := newTestRuTracker(t, site)
	rt.cfg.UseMagnetLinks.Value = true

	link, err := url.Parse(site.srv.URL + "/forum/viewtopic.php?t=555")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	_, err = rt.Download(context.Background(), link)
	if err == nil || !strings.Contains(err.Error(), "Failed to fetch magnet link from") {
		t.Fatalf("err: %v", err)
	}
}

func TestRuTrackerTitleParser(t *testing.T) {
	tv := []int{models.CatTV.ID}
	movies := []int{models.CatMovies.ID}
	audio := []int{models.CatAudio.ID}

	cases := []struct {
		name  string
		title string
		cat   []int
		strip bool
		moveA bool
		moveF bool
		rus   bool
		want  string
	}{
		{
			name:  "tv season episode of",
			title: "Некий сериал / Some Show (Сезон 1, Серии 1-2 из 16) [2021, WEB-DL 1080p]",
			cat:   tv,
			strip: true,
			// upstream quirk: [\w?] consumes only the first digit of "16"
			want: "Some Show (S1E1-2 of 16) [2021, WEB-DL 1080p]",
		},
		{
			name:  "movie director stripped",
			title: "Ирландец / The Irishman (Мартин Скорсезе / Martin Scorsese) [2019, США, криминал, драма, WEB-DL 1080p]",
			cat:   movies,
			strip: true,
			want:  "The Irishman [2019, WEB-DL 1080p]",
		},
		{
			name:  "cyrillic kept when strip disabled",
			title: "Некий сериал / Some Show",
			cat:   tv,
			strip: false,
			want:  "Некий сериал / Some Show",
		},
		{
			name:  "add RUS",
			title: "Some Show (S01E01)",
			cat:   tv,
			strip: true,
			rus:   true,
			want:  "Some Show (S01E01) RUS",
		},
		{
			name:  "move all tags",
			title: "Some Show [2021] (1080p) extra",
			cat:   movies,
			strip: false,
			moveA: true,
			want:  "Some Show extra (1080p) [2021]",
		},
		{
			name:  "move first tags",
			title: "[2021] Some Show [1080p] extra",
			cat:   tv,
			strip: false,
			moveF: true,
			want:  "Some Show [1080p] extra [2021]",
		},
		{
			name:  "rerelease detection",
			title: "Artist - Album 1984 / 2002 [FLAC]",
			cat:   audio,
			strip: false,
			want:  "Artist - Album 1984 [FLAC](Re-release)",
		},
		{
			name:  "quality renames",
			title: "Some Show HDTVRip XviD-Rip",
			cat:   tv,
			strip: false,
			// upstream replaces the matched "-Rip" with "Rip" verbatim
			// (the dash is consumed by the match): "XviD-Rip" → "XviDRip"
			want: "Some Show HDTV XviDRip",
		},
		{
			name:  "2160p 4K dedup",
			title: "Some Movie 2160p, 4K HDR",
			cat:   movies,
			strip: false,
			want:  "Some Movie 2160p HDR",
		},
		{
			name:  "tv comma episode",
			title: "Some Show Сезон 2,03 [2019]",
			cat:   tv,
			strip: false,
			want:  "Some Show S2-03 [2019]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := (rtTitleParser{}).Parse(tc.title, tc.cat, tc.strip, tc.moveA, tc.moveF, tc.rus)
			if got != tc.want {
				t.Fatalf("Parse(%q):\n got %q\nwant %q", tc.title, got, tc.want)
			}
		})
	}
}

func TestRuTrackerBalancedHelpers(t *testing.T) {
	// rtFindBalancedTags mirrors the upstream balanced-group regexes:
	// nested groups collapse into the outermost span, unbalanced openings
	// are skipped one position at a time (the inner balanced group still
	// matches on its own).
	tags := rtFindBalancedTags("a (b (c) d) e", '(', ')')
	if len(tags) != 1 || "a (b (c) d) e"[tags[0][0]:tags[0][1]] != "(b (c) d)" {
		t.Fatalf("tags: %v", tags)
	}
	tags = rtFindBalancedTags("((a) b", '(', ')')
	if len(tags) != 1 || "((a) b"[tags[0][0]:tags[0][1]] != "(a)" {
		t.Fatalf("unbalanced tags: %v", tags)
	}

	// FindSubstringsBetween pairs nested brackets (inner match first).
	subs := rtFindSubstringsBetween("x [a [b] c] y", '[', ']', true)
	if len(subs) != 2 || subs[0] != "[b]" || subs[1] != "[a [b] c]" {
		t.Fatalf("subs: %v", subs)
	}
}

func TestRuTrackerHelpers(t *testing.T) {
	if got := rtDistinctStrings([]string{"a", "b", "a", "c", "b"}); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("distinct: %v", got)
	}
	chunks := rtChunkStrings([]string{"1", "2", "3"}, 2)
	if len(chunks) != 2 || strings.Join(chunks[0], ",") != "1,2" || strings.Join(chunks[1], ",") != "3" {
		t.Fatalf("chunks: %v", chunks)
	}
	decoded, err := rtDecode([]byte("plain ascii"))
	if err != nil || decoded != "plain ascii" {
		t.Fatalf("decode ascii: %q %v", decoded, err)
	}
}
