package manual

import (
	"context"
	"fmt"
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

// --- fake site fixtures ------------------------------------------------------

const lostFilmEpisodePage = `<html><body>
<div class="title-block"><div class="details-pane"><div class="left-box">Дата выхода eng: 09 марта 2012 г.</div></div></div>
<div class="external-btn" onclick="PlayEpisode('123002005')"></div>
</body></html>`

const lostFilmDiscoveryPage = `<html><body>
<div class="row"><a href="/series/Some_Show/season_2/episode_5/">Some Show 2 season 5 series</a></div>
</body></html>`

// lostFilmSeriesPage renders the series/season page; codePrefix is the
// PlayEpisode id prefix (series id), slug the series url slug.
func lostFilmSeriesPage(codePrefix, slug string) string {
	return fmt.Sprintf(`<html><body>
<div class="serie-block">
<div class="movie-details-block">
<div class="external-btn inactive" onclick="PlayEpisode('%s001999')"></div>
<div class="haveseen-btn" data-code="seen-1"></div>
</div>
<table class="movie-parts-list"><tbody>
<tr>
<td class="delta" onclick="goTo('/series/%s/season_1/episode_1/',false)"><span class="small-text">Eng: 15.01.2017</span></td>
<td class="gamma"><div>1 серия</div></td>
<td class="zeta"><div class="external-btn" onclick="PlayEpisode('%s001001')"></div></td>
</tr>
<tr class="not-available">
<td class="delta"><span class="small-text">Eng: 22.01.2017</span></td>
<td class="gamma"><div>2 серия</div></td>
<td class="zeta"></td>
</tr>
</tbody></table>
</div>
</body></html>`, codePrefix, slug, codePrefix)
}

// lostFilmPackPage renders the season_1 page with an ACTIVE season button
// (season pack available).
func lostFilmPackPage() string {
	return `<html><body>
<div class="serie-block">
<div class="movie-details-block">
<div class="external-btn" onclick="PlayEpisode('126001999')"></div>
<div class="haveseen-btn" data-code="seen-1"></div>
</div>
<table class="movie-parts-list"><tbody>
<tr>
<td class="delta" onclick="goTo('/series/Pack_Show/season_1/episode_1/',false)"><span class="small-text">Eng: 15.01.2017</span></td>
<td class="gamma"><div>1 серия</div></td>
<td class="zeta"><div class="external-btn" onclick="PlayEpisode('126001001')"></div></td>
</tr>
</tbody></table>
</div>
</body></html>`
}

// lostFilmTrackerPage renders the generated tracker page (the final
// redirection target) for the given series title.
func lostFilmTrackerPage(title, file string) string {
	return fmt.Sprintf(`<html><body>
<div class="inner-box--subtitle">%s, сериал</div>
<div class="inner-box--text">(Episode Name)</div>
<div class="inner-box--item">
<div class="inner-box--desc">Видео: 1080p WEB-DL. Размер: 1.46 ГБ. Перевод: Профессиональный</div>
<div class="inner-box--link"><a href="https://fake.invalid/download/%s">Скачать</a></div>
</div>
</body></html>`, title, file)
}

// lostFilmSearchResponse mimics the ajaxik.php series search endpoint.
func lostFilmSearchResponse(val string) string {
	switch val {
	case "Some Show":
		return `{"data":{"series":[{"link":"/series/Some_Show","title_orig":"Some Show"}]}}`
	case "The Show":
		return `{"data":{"series":[{"link":"/series/The_Show","title_orig":"The Show"},{"link":"/series/The_Show_Other","title_orig":"The Show Other"}]}}`
	case "Pack Show":
		return `{"data":{"series":[{"link":"/series/Pack_Show","title_orig":"Pack Show"}]}}`
	default:
		return `{"data":{"series":[]}}`
	}
}

// lostFilmSite is a fake LostFilm.tv site.
type lostFilmSite struct {
	*httptest.Server

	mu             sync.Mutex
	searchVals     []string
	ajaxPosts      []string
	gets           []string
	vSearch        []string
	loginFail      string // "", "1" or "3"
	loginPosts     int
	logoutPosts    int
	episodeMode    int // 0 normal, 1 login-page once, 2 503 Service
	reloginArmed   bool
	reloginFired   bool
	captchaOnLogin bool
}

func newLostFilmSite(t *testing.T) *lostFilmSite {
	t.Helper()
	s := &lostFilmSite{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.URL.Path == "/ajaxik.php" && r.Method == http.MethodPost:
			_ = r.ParseForm()
			body := r.PostForm.Encode()
			s.ajaxPosts = append(s.ajaxPosts, body)
			act, typ := r.PostForm.Get("act"), r.PostForm.Get("type")
			switch {
			case act == "users" && typ == "logout":
				s.logoutPosts++
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"result":"ok"}`)
			case act == "users" && typ == "login":
				s.loginPosts++
				w.Header().Add("Set-Cookie", "lf_session=abc123; path=/")
				w.Header().Set("Content-Type", "application/json")
				switch s.loginFail {
				case "1":
					_, _ = fmt.Fprint(w, `{"error":1}`)
				case "3":
					_, _ = fmt.Fprint(w, `{"error":3}`)
				default:
					_, _ = fmt.Fprint(w, `{"success":true}`)
				}
			case act == "common" && typ == "search":
				val := r.PostForm.Get("val")
				s.searchVals = append(s.searchVals, val)
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, lostFilmSearchResponse(val))
			default:
				http.NotFound(w, r)
			}
		case r.URL.Path == "/login":
			if s.captchaOnLogin {
				w.Header().Add("Set-Cookie", "cf=1; path=/")
				_, _ = fmt.Fprint(w, `<html><body><img id="captcha_pictcha" src="/captcha/img.png"></body></html>`)
				return
			}
			_, _ = fmt.Fprint(w, `<html><body><form id="login-form"></form></body></html>`)
		case r.URL.Path == "/captcha/img.png":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("fake-jpeg-bytes"))
		case r.URL.Path == "/new":
			s.gets = append(s.gets, r.URL.RequestURI())
			_, _ = fmt.Fprint(w, lostFilmDiscoveryPage)
		case r.URL.Path == "/series/Some_Show/season_2/episode_5" ||
			r.URL.Path == "/series/Some_Show/season_2/episode_5/":
			s.gets = append(s.gets, r.URL.RequestURI())
			if s.episodeMode == 2 {
				_, _ = fmt.Fprint(w, "503 Service Temporarily Unavailable")
				return
			}
			if s.episodeMode == 1 && s.reloginArmed && !s.reloginFired {
				s.reloginFired = true
				_, _ = fmt.Fprint(w, `<html><body><a href="/login">Login</a></body></html>`)
				return
			}
			_, _ = fmt.Fprint(w, lostFilmEpisodePage)
		case r.URL.Path == "/series/The_Show/seasons":
			s.gets = append(s.gets, r.URL.RequestURI())
			_, _ = fmt.Fprint(w, lostFilmSeriesPage("124", "The_Show"))
		case r.URL.Path == "/series/The_Show_Other/seasons":
			s.gets = append(s.gets, r.URL.RequestURI())
			_, _ = fmt.Fprint(w, lostFilmSeriesPage("125", "The_Show_Other"))
		case r.URL.Path == "/series/Pack_Show/season_1":
			s.gets = append(s.gets, r.URL.RequestURI())
			_, _ = fmt.Fprint(w, lostFilmPackPage())
		case r.URL.Path == "/v_search.php":
			s.vSearch = append(s.vSearch, r.URL.Query().Encode())
			c := r.URL.Query().Get("c")
			// upstream takes the meta content verbatim after "url=" (+1), so
			// the fixture URL carries no quotes
			_, _ = fmt.Fprintf(w, `<html><head><meta http-equiv="refresh" content="0; url=/dl/%s"></head></html>`, c)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			s.gets = append(s.gets, r.URL.RequestURI())
			switch r.URL.Path {
			case "/dl/123":
				_, _ = fmt.Fprint(w, lostFilmTrackerPage("Some Show", "12345"))
			case "/dl/124":
				_, _ = fmt.Fprint(w, lostFilmTrackerPage("The Show", "12400"))
			case "/dl/125":
				_, _ = fmt.Fprint(w, lostFilmTrackerPage("The Show Other", "12500"))
			case "/dl/126":
				_, _ = fmt.Fprint(w, lostFilmTrackerPage("Pack Show", "12600"))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *lostFilmSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.gets...)
}

func newTestLostFilm(t *testing.T, site *lostFilmSite) *LostFilm {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewLostFilm(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewLostFilm: %v", err)
	}
	l := ix.(*LostFilm)
	l.SiteLinkValue = site.URL + "/"
	l.DefaultSiteLink = site.URL + "/"
	return l
}

// configureLostFilm logs the indexer in against the fake site.
func configureLostFilm(t *testing.T, l *LostFilm, site *lostFilmSite) {
	t.Helper()
	status, err := l.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "user@example.com"},
		{ID: "password", Value: "secret"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status = %v, want RequiresTesting", status)
	}
	if !l.IsConfigured() {
		t.Fatal("expected configured after login")
	}
}

func assertLostFilmRelease(t *testing.T, r *models.ReleaseInfo, wantTitle string) {
	t.Helper()
	if r.Title != wantTitle {
		t.Fatalf("title = %q want %q", r.Title, wantTitle)
	}
	if r.Link == nil {
		t.Fatal("link is nil")
	}
	if r.Guid == nil || r.Guid.String() != r.Link.String() {
		t.Fatalf("guid = %v, want link %v", r.Guid, r.Link)
	}
	if r.Size == nil || *r.Size != parseutil.GetBytes("1.46 GB") {
		t.Fatalf("size = %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 1 || r.Peers == nil || *r.Peers != 2 {
		t.Fatalf("seeders/peers = %v/%v", r.Seeders, r.Peers)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf = %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf = %v", r.UploadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 || r.MinimumSeedTime == nil || *r.MinimumSeedTime != 172800 {
		t.Fatalf("minimum ratio/seed time = %v/%v", r.MinimumRatio, r.MinimumSeedTime)
	}
	if len(r.Category) != 1 || r.Category[0] != models.CatTV.ID {
		t.Fatalf("category = %v", r.Category)
	}
}

func TestLostFilmCaps(t *testing.T) {
	ix, err := NewLostFilm(services.Deps{})
	if err != nil {
		t.Fatalf("NewLostFilm: %v", err)
	}
	if ix.ID() != "lostfilm" || ix.Name() != "LostFilm.tv" || ix.Type() != "semi-private" || ix.Language() != "ru-RU" {
		t.Fatalf("identity: %s / %s / %s / %s", ix.ID(), ix.Name(), ix.Type(), ix.Language())
	}
	lf := ix.(*LostFilm)
	if len(ix.AlternativeSiteLinks()) != 11 || len(lf.LegacySiteLinks) != 3 {
		t.Fatalf("site links: %d alt / %d legacy", len(ix.AlternativeSiteLinks()), len(lf.LegacySiteLinks))
	}
	caps := ix.Capabilities()
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams = %v", caps.TVSearchParams)
	}
	wantMovie := []string{models.MovieParamQ}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams = %v", caps.MovieSearchParams)
	}
	got := caps.Categories.MapTrackerCatToNewznab("1")
	if len(got) != 1 || got[0] != models.CatTV.ID {
		t.Fatalf("MapTrackerCatToNewznab(1) = %v", got)
	}
}

func TestLostFilmConfigForm(t *testing.T) {
	ix, err := NewLostFilm(services.Deps{})
	if err != nil {
		t.Fatalf("NewLostFilm: %v", err)
	}
	l := ix.(*LostFilm)
	items, err := l.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids, types []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
		types = append(types, it["type"].(string))
	}
	want := []string{"sitelink", "username", "password", "captchaimage", "captchatext", "", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
	if types[3] != "displayimage" {
		t.Fatalf("captchaimage type = %q", types[3])
	}
}

func TestLostFilmConfigurationForSetupCaptcha(t *testing.T) {
	site := newLostFilmSite(t)
	site.captchaOnLogin = true
	l := newTestLostFilm(t, site)

	items, err := l.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	if string(l.cfg.CaptchaImage.Value) != "fake-jpeg-bytes" {
		t.Fatalf("captcha image = %q", l.cfg.CaptchaImage.Value)
	}
	if !strings.Contains(l.cfg.CaptchaCookie.Value, "cf=1") {
		t.Fatalf("captcha cookie = %q", l.cfg.CaptchaCookie.Value)
	}
	var captchaItem map[string]any
	for _, it := range items {
		if it["id"] == "captchaimage" {
			captchaItem = it
		}
	}
	if captchaItem == nil || !strings.HasPrefix(captchaItem["value"].(string), "data:image/jpeg;base64,") {
		t.Fatalf("captcha item = %v", captchaItem)
	}

	// without a captcha on the login page the image is emptied
	site.captchaOnLogin = false
	if _, err := l.ConfigurationForSetup(context.Background()); err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	if len(l.cfg.CaptchaImage.Value) != 0 {
		t.Fatalf("captcha image must be empty, got %q", l.cfg.CaptchaImage.Value)
	}
}

func TestLostFilmLoginSuccess(t *testing.T) {
	site := newLostFilmSite(t)
	l := newTestLostFilm(t, site)
	configureLostFilm(t, l, site)

	if site.logoutPosts != 1 || site.loginPosts != 1 {
		t.Fatalf("logout/login posts = %d/%d", site.logoutPosts, site.loginPosts)
	}
	if len(site.ajaxPosts) != 2 || !strings.Contains(site.ajaxPosts[1], "mail=user%40example.com") ||
		!strings.Contains(site.ajaxPosts[1], "pass=secret") || !strings.Contains(site.ajaxPosts[1], "rem=1") {
		t.Fatalf("login post = %v", site.ajaxPosts)
	}
}

func TestLostFilmLoginEmailValidation(t *testing.T) {
	site := newLostFilmSite(t)
	l := newTestLostFilm(t, site)

	_, err := l.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "notanemail"},
		{ID: "password", Value: "secret"},
	})
	if err == nil || err.Error() != "Exception (lostfilm): Username must be an e-mail address" {
		t.Fatalf("err = %v", err)
	}
	if len(site.ajaxPosts) != 0 {
		t.Fatalf("no posts expected, got %v", site.ajaxPosts)
	}
}

func TestLostFilmLoginErrorMapping(t *testing.T) {
	site := newLostFilmSite(t)
	site.loginFail = "3"
	l := newTestLostFilm(t, site)
	_, err := l.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "user@example.com"},
		{ID: "password", Value: "wrong"},
	})
	if err == nil || err.Error() != "Exception (lostfilm): E-mail or password is incorrect" {
		t.Fatalf("err = %v", err)
	}

	site.loginFail = "1"
	l = newTestLostFilm(t, site)
	_, err = l.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "user@example.com"},
		{ID: "password", Value: "secret"},
		{ID: "captchatext", Value: "ZZZ"},
	})
	if err == nil || err.Error() != "Exception (lostfilm): Captcha is incorrect" {
		t.Fatalf("err = %v", err)
	}
	// captcha text is posted when supplied
	if len(site.ajaxPosts) == 0 || !strings.Contains(site.ajaxPosts[len(site.ajaxPosts)-1], "need_captcha=1") ||
		!strings.Contains(site.ajaxPosts[len(site.ajaxPosts)-1], "captcha=ZZZ") {
		t.Fatalf("posts = %v", site.ajaxPosts)
	}
}

func TestLostFilmSearchSeasonEpisodeChain(t *testing.T) {
	site := newLostFilmSite(t)
	l := newTestLostFilm(t, site)
	configureLostFilm(t, l, site)
	site.mu.Lock()
	site.gets = nil
	site.searchVals = nil
	site.vSearch = nil
	site.mu.Unlock()

	season := 2
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Some Show"
	q.Season = &season
	q.Episode = "5"
	res, err := l.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	assertLostFilmRelease(t, r, "Some Show - S2E5 - Episode Name - rus 1080p WEBDL (LostFilm)")
	if r.Link.String() != "https://fake.invalid/download/12345" {
		t.Fatalf("link = %v", r.Link)
	}
	if r.Details == nil || r.Details.String() != site.URL+"/series/Some_Show/season_2/episode_5" {
		t.Fatalf("details = %v", r.Details)
	}
	if r.PublishDate.Format("2006-01-02") != "2012-03-09" {
		t.Fatalf("publish date = %v", r.PublishDate)
	}

	// the two-request chain: series search POST, then the episode page,
	// v_search redirector and the tracker page
	if strings.Join(site.searchVals, "|") != "Some Show" {
		t.Fatalf("searchVals = %v", site.searchVals)
	}
	wantGets := []string{
		"/series/Some_Show/season_2/episode_5",
		"/dl/123",
	}
	if strings.Join(site.paths(), "|") != strings.Join(wantGets, "|") {
		t.Fatalf("gets = %v", site.paths())
	}
	if strings.Join(site.vSearch, "|") != "c=123&e=5&s=2" {
		t.Fatalf("vSearch = %v", site.vSearch)
	}
}

func TestLostFilmSearchKeywordMorphing(t *testing.T) {
	site := newLostFilmSite(t)
	l := newTestLostFilm(t, site)
	configureLostFilm(t, l, site)
	site.mu.Lock()
	site.gets = nil
	site.searchVals = nil
	site.vSearch = nil
	site.mu.Unlock()

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "The Show Two Words"
	res, err := l.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	assertLostFilmRelease(t, res.Releases[0], "The Show - S1E1 - Episode Name - rus 1080p WEBDL (LostFilm)")
	assertLostFilmRelease(t, res.Releases[1], "The Show Other - S1E1 - Episode Name - rus 1080p WEBDL (LostFilm)")
	if res.Releases[0].PublishDate.Format("2006-01-02") != "2017-01-15" {
		t.Fatalf("publish date = %v", res.Releases[0].PublishDate)
	}
	if res.Releases[0].Details == nil || res.Releases[0].Details.String() != site.URL+"/series/The_Show/season_1/episode_1/" {
		t.Fatalf("details = %v", res.Releases[0].Details)
	}

	// the search term is reduced word by word until series are found
	wantVals := []string{"The Show Two Words", "The Show Two", "The Show"}
	if strings.Join(site.searchVals, "|") != strings.Join(wantVals, "|") {
		t.Fatalf("searchVals = %v", site.searchVals)
	}
	// per series: one filtered fetch ("Two Words"), one with "Two", one with
	// the empty filter — then the v_search redirector and tracker page
	if strings.Join(site.vSearch, "|") != "c=124&e=1&s=1|c=125&e=1&s=1" {
		t.Fatalf("vSearch = %v", site.vSearch)
	}
}

func TestLostFilmSeasonPack(t *testing.T) {
	site := newLostFilmSite(t)
	l := newTestLostFilm(t, site)
	configureLostFilm(t, l, site)
	site.mu.Lock()
	site.gets = nil
	site.searchVals = nil
	site.vSearch = nil
	site.mu.Unlock()

	season := 1
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "Pack Show"
	q.Season = &season
	res, err := l.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	// episode 999 is the whole-season marker → "S1" in the title
	assertLostFilmRelease(t, res.Releases[0], "Pack Show - S1 - Episode Name - rus 1080p WEBDL (LostFilm)")
	if res.Releases[0].PublishDate.Format("2006-01-02") != "2017-01-15" {
		t.Fatalf("publish date = %v", res.Releases[0].PublishDate)
	}
	if strings.Join(site.vSearch, "|") != "c=126&e=999&s=1" {
		t.Fatalf("vSearch = %v", site.vSearch)
	}
}

func TestLostFilmNewReleasesDiscovery(t *testing.T) {
	site := newLostFilmSite(t)
	l := newTestLostFilm(t, site)
	configureLostFilm(t, l, site)
	site.mu.Lock()
	site.gets = nil
	site.mu.Unlock()

	res, err := l.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", IsTest: true,
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	assertLostFilmRelease(t, res.Releases[0], "Some Show - S2E5 - Episode Name - rus 1080p WEBDL (LostFilm)")
	// the discovery row href carries a trailing slash upstream
	wantGets := []string{"/new", "/series/Some_Show/season_2/episode_5/", "/dl/123"}
	if strings.Join(site.paths(), "|") != strings.Join(wantGets, "|") {
		t.Fatalf("gets = %v", site.paths())
	}
}

func TestLostFilmReloginOnLoginRedirect(t *testing.T) {
	site := newLostFilmSite(t)
	l := newTestLostFilm(t, site)
	configureLostFilm(t, l, site)
	site.episodeMode = 1
	site.reloginArmed = true
	site.mu.Lock()
	site.gets = nil
	logouts := site.logoutPosts
	logins := site.loginPosts
	site.mu.Unlock()

	res, err := l.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", IsTest: true,
	}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	// the login page triggered ApplyConfiguration(null): logout + login again
	site.mu.Lock()
	defer site.mu.Unlock()
	if site.logoutPosts != logouts+1 || site.loginPosts != logins+1 {
		t.Fatalf("relogin posts: logout %d→%d login %d→%d", logouts, site.logoutPosts, logins, site.loginPosts)
	}
}

func TestLostFilmServiceUnavailable(t *testing.T) {
	site := newLostFilmSite(t)
	l := newTestLostFilm(t, site)
	configureLostFilm(t, l, site)
	site.episodeMode = 2

	_, err := l.ResultsForQuery(context.Background(), &models.TorznabQuery{
		QueryType: "search", IsTest: true,
	}, false)
	if err == nil || !strings.Contains(err.Error(), "503 Service") {
		t.Fatalf("err = %v", err)
	}
}
