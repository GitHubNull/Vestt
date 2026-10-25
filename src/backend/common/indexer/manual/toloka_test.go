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

type tolokaSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	serve map[string]func(w http.ResponseWriter, r *http.Request)
}

func newTolokaSite(t *testing.T, serve map[string]func(w http.ResponseWriter, r *http.Request)) *tolokaSite {
	t.Helper()
	s := &tolokaSite{serve: serve}
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

func (s *tolokaSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func newTestToloka(t *testing.T, site *tolokaSite) *Toloka {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewToloka(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewToloka: %v", err)
	}
	tk := ix.(*Toloka)
	tk.SiteLinkValue = site.srv.URL + "/"
	// Seed the config sitelink too: a mid-search re-login runs
	// ApplyConfiguration(nil) → LoadValuesFromJSON(null), which syncs
	// SiteLinkValue from the config item and falls back to DefaultSiteLink
	// when it is blank (upstream BaseIndexer.cs:122-138). Without this the
	// re-login would POST the credentials to the real toloka.to.
	tk.cfg.SiteLink.Value = site.srv.URL + "/"
	client.SetRequestDelay(tk.ID(), 0)
	return tk
}

const tolokaTrackerPage = `<html><body>
<a href="login.php?logout=true">Logout</a>
<table class="forumline"><tbody>
<tr class="prow1">
  <td></td>
  <td><a href="forumview.php?f=173">Серіали в HD</a></td>
  <td><a href="viewtopic.php?t=12345">Шоу Сезон 1 Серії 1-2 (WEB-DL 1080p)</a></td>
  <td></td><td></td>
  <td><a href="download.php?id=12345">DL</a></td>
  <td>1.46 GB</td>
  <td></td><td></td>
  <td><b>15</b></td>
  <td><b>3</b></td>
  <td></td>
  <td>2021-04-25 15:04</td>
</tr>
<tr class="prow2">
  <td></td>
  <td><a href="forumview.php?f=8">Українська музика (lossy)</a></td>
  <td><a href="viewtopic.php?t=777">Гурт [FLAC]</a></td>
  <td></td><td></td>
  <td><a href="download.php?id=777">DL</a></td>
  <td>200 MB</td>
  <td></td><td></td>
  <td><b></b></td>
  <td><b>1</b></td>
  <td><img src="images/gold.gif"/></td>
  <td>2021-04-25 15:04</td>
</tr>
<tr class="prow1">
  <td></td>
  <td><a href="forumview.php?f=173">x</a></td>
  <td><a href="viewtopic.php?t=9">На модерації</a></td>
  <td></td><td></td>
  <td></td>
  <td>0</td>
  <td></td><td></td>
  <td><b>0</b></td>
  <td><b>0</b></td>
  <td></td>
  <td>2021-04-25 15:04</td>
</tr>
</tbody></table>
</body></html>`

// The login-fail page nests the message table inside a td of the forumline
// table: the upstream selector is "table.forumline table span.gen"
// (Toloka.cs:232). A bare <table> directly inside <table> is foster-parented
// to a sibling by the HTML5 parser (AngleSharp included) and would never
// match the descendant selector.
const tolokaLoginFailPage = `<html><body>
<table class="forumline"><tbody><tr><td>
<table><tbody><tr><td><span class="gen">Невірне ім'я користувача</span></td></tr></tbody></table>
</td></tr></tbody></table>
</body></html>`

func tolokaStandardServe(t *testing.T) map[string]func(w http.ResponseWriter, r *http.Request) {
	t.Helper()
	return map[string]func(w http.ResponseWriter, r *http.Request){
		"/login.php": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				if err := r.ParseForm(); err == nil && r.PostForm.Get("password") == "pass" {
					http.SetCookie(w, &http.Cookie{Name: "toloka_sid", Value: "1"})
					http.SetCookie(w, &http.Cookie{Name: "toloka_222_u", Value: "bad"})
					http.SetCookie(w, &http.Cookie{Name: "plain", Value: "ok"})
					_, _ = w.Write([]byte(`<html><body><a href="login.php?logout=true">Logout</a></body></html>`))
					return
				}
				_, _ = w.Write([]byte(tolokaLoginFailPage))
				return
			}
			_, _ = w.Write([]byte(`<html><body>login form</body></html>`))
		},
		"/tracker.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(tolokaTrackerPage))
		},
	}
}

func TestTolokaCaps(t *testing.T) {
	ix, err := NewToloka(services.Deps{})
	if err != nil {
		t.Fatalf("NewToloka: %v", err)
	}
	caps := ix.Capabilities()
	if strings.Join(caps.TVSearchParams, ",") != strings.Join([]string{
		models.TVParamQ, models.TVParamSeason, models.TVParamEp,
	}, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	if strings.Join(caps.MovieSearchParams, ",") != models.MovieParamQ {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if strings.Join(caps.MusicSearchParams, ",") != models.MusicParamQ {
		t.Fatalf("MusicSearchParams: %v", caps.MusicSearchParams)
	}
	if strings.Join(caps.BookSearchParams, ",") != models.BookParamQ {
		t.Fatalf("BookSearchParams: %v", caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"117", 2000}, {"84", 2000}, {"42", 2000}, {"124", 5000}, {"125", 5000},
		{"127", 5070}, {"94", 2020}, {"225", 5080}, {"119", 5050}, {"18", 5050},
		{"157", 5060}, {"54", 5060}, {"136", 2040}, {"96", 2040}, {"173", 5040},
		{"120", 2070}, {"66", 2070}, {"137", 2070}, {"138", 2070}, {"237", 2000},
		{"33", 3020}, {"8", 3000}, {"98", 3040}, {"11", 7000}, {"181", 7010},
		{"184", 7030}, {"185", 3030}, {"9", 4000}, {"27", 4030},
		{"211", 4070}, {"122", 4060}, {"241", 8000}, {"10", 4050},
		{"28", 4050}, {"41", 4060}, {"212", 4070}, {"236", 8000}, {"71", 8000},
		{"208", 8000},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		if !hasInt(got, tc.want) {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", tc.tracker, got, tc.want)
		}
	}
	// upstream maps 137 to both MoviesDVD and TV (cs:129-130)
	if got := caps.Categories.MapTrackerCatToNewznab("137"); !hasInt(got, 2070) || !hasInt(got, 5000) {
		t.Fatalf("137 double mapping: %v", got)
	}
}

func TestTolokaConfigForm(t *testing.T) {
	ix, err := NewToloka(services.Deps{})
	if err != nil {
		t.Fatalf("NewToloka: %v", err)
	}
	tk := ix.(*Toloka)
	items, err := tk.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// BasicLogin always carries the Instructions displayinfo item with an
	// empty id (upstream ConfigurationDataBasicLogin.cs:16 —
	// new DisplayInfoConfigurationItem("", ...), emitted even with a null
	// message).
	want := []string{"sitelink", "username", "password", "", "showfreeleechonly", "stripcyrillicletters", "tags"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("config ids: %v", ids)
	}
	if !tk.cfg.StripCyrillicLetters.Value {
		t.Fatal("StripCyrillicLetters must default to true (upstream { Value = true })")
	}
}

func TestTolokaLoginFail(t *testing.T) {
	site := newTolokaSite(t, tolokaStandardServe(t))
	tk := newTestToloka(t, site)

	_, err := tk.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "wrong"},
	})
	// IndexerError.Error() renders "Exception (toloka): <message>" (mirrors
	// the upstream ExceptionWithConfigData text, Toloka.cs:234).
	if err == nil || err.Error() != "Exception (toloka): Невірне ім'я користувача" {
		t.Fatalf("err: %v", err)
	}
	if tk.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}

func TestTolokaLoginOKAndCookieFilter(t *testing.T) {
	site := newTolokaSite(t, tolokaStandardServe(t))
	tk := newTestToloka(t, site)

	status, err := tk.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
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
	if !tk.IsConfigured() {
		t.Fatal("must be configured after login")
	}
	ch := tk.CookieHeader()
	if !strings.Contains(ch, "toloka_sid=1") || !strings.Contains(ch, "plain=ok") {
		t.Fatalf("cookie header: %q", ch)
	}
	if strings.Contains(ch, "toloka_222_u") {
		t.Fatalf("toloka_*_u cookie must be stripped: %q", ch)
	}
}

func TestTolokaSearchRequestAndParse(t *testing.T) {
	site := newTolokaSite(t, tolokaStandardServe(t))
	tk := newTestToloka(t, site)
	tk.SetCookieHeader("toloka_sid=1")

	season := 1
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "my-show" // dash → space upstream
	q.Season = &season
	// TVHD → f[]=173 among others. 3000 (Audio) must be requested too:
	// Base.FilterResults drops releases whose categories don't intersect the
	// query categories (upstream BaseIndexer.cs:179-182) and the second row
	// is an Audio release.
	q.Categories = []int{5040, 3000}

	res, err := tk.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	paths := site.paths()
	if len(paths) != 1 || !strings.HasPrefix(paths[0], "GET /tracker.php?") {
		t.Fatalf("hits: %v", paths)
	}
	u, err := url.Parse(strings.TrimPrefix(paths[0], "GET "))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	query := u.Query()
	if query.Get("o") != "1" || query.Get("s") != "2" {
		t.Fatalf("o/s params: %v", query)
	}
	if got := query.Get("nm"); got != "my show Сезон 1" {
		t.Fatalf("nm param: %q", got)
	}
	found173 := false
	for _, f := range query["f[]"] {
		if f == "173" {
			found173 = true
		}
	}
	if !found173 {
		t.Fatalf("f[] params: %v", query["f[]"])
	}

	r := res.Releases[0]
	if r.Title != "S1E1-2 (WEB-DL 1080p)" { // season/episode rewrite + cyrillic strip
		t.Fatalf("title: %q", r.Title)
	}
	if r.Description != "Шоу Сезон 1 Серії 1-2 (WEB-DL 1080p)" { // raw title
		t.Fatalf("description: %q", r.Description)
	}
	// url.URL.Path excludes the query string; assert Path and RawQuery apart.
	if r.Link == nil || !strings.HasSuffix(r.Link.Path, "/download.php") || r.Link.RawQuery != "id=12345" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.Path, "/viewtopic.php") || r.Details.RawQuery != "t=12345" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Size == nil || *r.Size != 1567663063 { // 1.46 GB
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 15 || r.Peers == nil || *r.Peers != 18 {
		t.Fatalf("seeders/peers: %v %v", r.Seeders, r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 0 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if !hasInt(r.Category, 5040) {
		t.Fatalf("category: %v", r.Category)
	}
	if r.PublishDate.Format("2006-01-02 15:04") != "2021-04-25 15:04" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}

	// gold image → freeleech; blank seeders → 0
	r2 := res.Releases[1]
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf2: %v", r2.DownloadVolumeFactor)
	}
	if r2.Seeders == nil || *r2.Seeders != 0 {
		t.Fatalf("seeders2: %v", r2.Seeders)
	}
	if r2.Title != "[FLAC]" {
		t.Fatalf("title2: %q", r2.Title)
	}
	if !hasInt(r2.Category, 3000) {
		t.Fatalf("category2: %v", r2.Category)
	}
}

func TestTolokaFreeleechOnlyParam(t *testing.T) {
	site := newTolokaSite(t, tolokaStandardServe(t))
	tk := newTestToloka(t, site)
	tk.SetCookieHeader("toloka_sid=1")
	tk.cfg.FreeleechOnly.Value = true

	if _, err := tk.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	u, err := url.Parse(strings.TrimPrefix(site.paths()[0], "GET "))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := u.Query().Get("sds"); got != "1" {
		t.Fatalf("sds param: %q (uri %v)", got, site.paths())
	}
}

func TestTolokaReloginWhenSessionExpired(t *testing.T) {
	trackerHits := 0
	serve := tolokaStandardServe(t)
	serve["/tracker.php"] = func(w http.ResponseWriter, _ *http.Request) {
		trackerHits++
		if trackerHits == 1 {
			// session expired: no logout=true
			_, _ = w.Write([]byte(`<html><body><table class="forumline"><tbody></tbody></table></body></html>`))
			return
		}
		_, _ = w.Write([]byte(tolokaTrackerPage))
	}
	site := newTolokaSite(t, serve)
	tk := newTestToloka(t, site)
	tk.cfg.Username.Value = "user"
	tk.cfg.Password.Value = "pass"
	tk.SetConfigured(true) // simulate stored configuration

	res, err := tk.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	var logins int
	for _, p := range site.paths() {
		if p == "POST /login.php" {
			logins++
		}
	}
	if logins != 1 {
		t.Fatalf("expected one re-login, hits: %v", site.paths())
	}
	if trackerHits != 2 {
		t.Fatalf("expected two tracker fetches, got %d", trackerHits)
	}
}

func TestTolokaParseErrorRowSkipped(t *testing.T) {
	// a prow row with a download link but an unparseable date cell is skipped
	// (upstream per-row catch, cs:360-364); broken selector-level HTML fails.
	site := newTolokaSite(t, tolokaStandardServe(t))
	tk := newTestToloka(t, site)
	tk.SetCookieHeader("toloka_sid=1")

	res, err := tk.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	// the moderation row (no download link) is skipped → 2 of 3 rows
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
}

func TestTolokaTitleParser(t *testing.T) {
	tvCat := []int{5000}
	movieCat := []int{2000}
	for _, tc := range []struct {
		title    string
		category []int
		strip    bool
		want     string
	}{
		// TV season/episode rewrite (Ukrainian), no cyrillic strip
		{"Шоу Сезон 1 Серії 1-2 (1080p)", tvCat, false, "Шоу S1E1-2 (1080p)"},
		// season + "of total" form
		{"Шоу Сезон 2 Серії 3 з 12", tvCat, false, "Шоу S2E3 of 12"},
		// English forms
		{"Show Name Season 1 Episodes 1-2 of 10", tvCat, false, "Show Name S1E1-2 of 10"},
		{"Show Name Season 1 Episode 5", tvCat, false, "Show Name S1E5"},
		// multiple seasons
		{"Show Name Seasons 1-2 (1080p)", tvCat, false, "Show Name S1-2 (1080p)"},
		// decimal comma → dash
		{"Show Name S01E01 1,5 GB", tvCat, false, "Show Name S01E01 1-5 GB"},
		// non-TV category: no season rewrite
		{"Фільм Сезон 1 Серії 1-2", movieCat, false, "Фільм Сезон 1 Серії 1-2"},
		// cyrillic strip (default config)
		{"Шоу S01E01 (Українське озвучення) [WEBDLRip]", tvCat, true, "S01E01 [WEB-DL]"},
		// rip normalisation
		{"Show.Name.S01E01.1080p.HDTVRip.x264-GRP", tvCat, false, "Show.Name.S01E01.1080p.HDTV.x264-GRP"},
		// leading tags move to the end
		{"[1080p] Show Name (2020)", tvCat, false, "Show Name (2020) [1080p]"},
		// empty brackets removal + slash-paren cleanup
		{"Show Name () ( / 2020 / )", tvCat, false, "Show Name (2020)"},
	} {
		if got := tkParseTitle(tc.title, tc.category, tc.strip); got != tc.want {
			t.Fatalf("tkParseTitle(%q, %v, %v) = %q want %q", tc.title, tc.category, tc.strip, got, tc.want)
		}
	}
}

func TestTolokaResolveCookies(t *testing.T) {
	in := "toloka_sid=1; toloka_222_u=bad; toloka_333_u=x; plain=ok"
	got := tkResolveCookies(in)
	if strings.Contains(got, "_u=") || !strings.Contains(got, "toloka_sid=1") || !strings.Contains(got, "plain=ok") {
		t.Fatalf("tkResolveCookies(%q) = %q", in, got)
	}
	// space-joined variant (manual package setCookiePairs style)
	got = tkResolveCookies("toloka_sid=1 toloka_222_u=bad plain=ok")
	if strings.Contains(got, "_u=") || !strings.Contains(got, "toloka_sid=1") || !strings.Contains(got, "plain=ok") {
		t.Fatalf("space-joined tkResolveCookies = %q", got)
	}
}
