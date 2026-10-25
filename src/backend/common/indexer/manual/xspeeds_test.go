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

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

type xSpeedsSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	posts []string
	serve map[string]func(w http.ResponseWriter, r *http.Request)
}

func newXSpeedsSite(t *testing.T, serve map[string]func(w http.ResponseWriter, r *http.Request)) *xSpeedsSite {
	t.Helper()
	s := &xSpeedsSite{serve: serve}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := ""
		if r.Method == http.MethodPost {
			buf := new(strings.Builder)
			_, _ = io.Copy(buf, r.Body)
			body = buf.String()
		}
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		if body != "" {
			s.posts = append(s.posts, body)
		}
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

func (s *xSpeedsSite) bodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.posts...)
}

func newTestXSpeeds(t *testing.T, site *xSpeedsSite) *XSpeeds {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewXSpeeds(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewXSpeeds: %v", err)
	}
	x := ix.(*XSpeeds)
	x.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(x.ID(), 0)
	return x
}

const xspeedsSearchPage = `<html><body>
<table id="sortabletable"><tbody>
<tr>
  <td><a href="browse.php?category=145">Movies HD</a></td>
  <td><div><img src="https://img.xspeeds.eu/cover.jpg"/></div>
      <div>Uploaded: <div>08-08-2015 12:51</div></div></td>
  <td><a href="download.php?id=111">DL</a></td>
  <td><div><a href="details.php?id=111">Some Movie 2015 1080p [R]</a></div></td>
  <td>1.5 GB</td>
  <td>25</td>
  <td>15</td>
  <td>3</td>
  <td></td>
</tr>
<tr>
  <td><a href="browse.php?category=126">TV</a></td>
  <td><div><div>Pre Release Time: 25-04-2021 15:00 Uploaded: 3 Weeks, 2 Days after Pre</div></div></td>
  <td><a href="download.php?id=222">DL</a></td>
  <td><div><a href="details.php?id=222">Some Show S01E01 720p</a></div></td>
  <td>500 MB</td>
  <td>9</td>
  <td>9</td>
  <td>4</td>
  <td><img alt="Free Torrent"/> <img alt="x2 Torrent"/></td>
</tr>
</tbody></table>
</body></html>`

const xspeedsLoginFailPage = `<html><body>
<div class="left_side">
<table><tr><th>Error</th></tr><tr><td>Invalid username or password</td></tr></table>
</div>
</body></html>`

func TestXSpeedsCaps(t *testing.T) {
	ix, err := NewXSpeeds(services.Deps{})
	if err != nil {
		t.Fatalf("NewXSpeeds: %v", err)
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
	if strings.Join(caps.BookSearchParams, ",") != models.BookParamQ {
		t.Fatalf("BookSearchParams: %v", caps.BookSearchParams)
	}
	for _, tc := range []struct {
		tracker string
		want    int
	}{
		{"70", 5070}, {"113", 5070}, {"112", 2020}, {"150", 4000}, {"166", 4030},
		{"153", 7000}, {"154", 3030}, {"68", 2020}, {"140", 5080}, {"10", 2070},
		{"109", 2050}, {"131", 5060}, {"134", 5060}, {"72", 2010}, {"116", 5020},
		{"114", 2010}, {"115", 5020}, {"103", 1090}, {"105", 1090}, {"104", 1180},
		{"106", 1040}, {"6", 4050}, {"108", 4000}, {"107", 4030}, {"11", 2000},
		{"118", 2045}, {"162", 2040}, {"143", 2040}, {"119", 2040}, {"144", 2030},
		{"12", 2000}, {"117", 2045}, {"163", 2040}, {"145", 2040}, {"100", 2040},
		{"146", 2030}, {"13", 3000}, {"135", 3040}, {"151", 3000}, {"136", 3000},
		{"148", 3020}, {"9", 8000}, {"125", 8000}, {"54", 5050}, {"83", 5050},
		{"139", 5000}, {"138", 5000}, {"167", 2000}, {"20", 5060}, {"88", 5060},
		{"86", 5060}, {"89", 5060}, {"126", 5000}, {"127", 5045}, {"164", 5040},
		{"129", 5040}, {"130", 5040}, {"128", 5030}, {"149", 5000}, {"21", 5030},
		{"120", 5045}, {"165", 5045}, {"76", 5040}, {"97", 5040}, {"147", 5030},
	} {
		got := caps.Categories.MapTrackerCatToNewznab(tc.tracker)
		if !hasInt(got, tc.want) {
			t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", tc.tracker, got, tc.want)
		}
	}
	// upstream maps TOTM/TOTW to both TV and Movies (cs:143-146)
	if got := caps.Categories.MapTrackerCatToNewznab("139"); !hasInt(got, 5000) || !hasInt(got, 2000) {
		t.Fatalf("TOTM double mapping: %v", got)
	}
	if got := caps.Categories.MapTrackerCatToNewznab("138"); !hasInt(got, 5000) || !hasInt(got, 2000) {
		t.Fatalf("TOTW double mapping: %v", got)
	}
}

func TestXSpeedsConfigForm(t *testing.T) {
	ix, err := NewXSpeeds(services.Deps{})
	if err != nil {
		t.Fatalf("NewXSpeeds: %v", err)
	}
	x := ix.(*XSpeeds)
	items, err := x.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// form ids are name-derived upstream (ConfigurationData.cs:105):
	// "" from the empty Instructions item (ConfigurationDataBasicLogin.cs:20),
	// "sortby"/"order" from the select display names (XSpeeds.cs:51,63),
	// "filterfreeleechonly" (XSpeeds.cs:72), "accountinactivity" (cs:73)
	want := []string{"sitelink", "username", "password", "", "sortby", "order", "filterfreeleechonly", "accountinactivity", "tags"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("config ids: %v", ids)
	}
	if x.cfg.SortRequestedFromSite.Value != "added" || x.cfg.OrderRequestedFromSite.Value != "desc" {
		t.Fatalf("sort/order defaults: %q %q", x.cfg.SortRequestedFromSite.Value, x.cfg.OrderRequestedFromSite.Value)
	}
}

func TestXSpeedsCaptchaInSetupForm(t *testing.T) {
	site := newXSpeedsSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/login.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<html><body><form><img id="regimage" src="/captcha.jpg"/><input name="imagestring"/></form></body></html>`))
		},
		"/captcha.jpg": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("jpeg-bytes"))
		},
	})
	x := newTestXSpeeds(t, site)

	items, err := x.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	joined := strings.Join(ids, ",")
	if !strings.Contains(joined, "captchatext") || !strings.Contains(joined, "captchaimage") {
		t.Fatalf("captcha items missing: %v", ids)
	}
}

func TestXSpeedsLoginFail(t *testing.T) {
	site := newXSpeedsSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/takelogin.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(xspeedsLoginFailPage))
		},
	})
	x := newTestXSpeeds(t, site)

	_, err := x.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "wrong"},
	})
	// the upstream exception message is the bare site text (XSpeeds.cs:226);
	// the "Exception (xspeeds):" prefix is the IndexerError rendering
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindConfig {
		t.Fatalf("err: %v", err)
	}
	if ie.Message != "Invalid username or password" {
		t.Fatalf("message: %q", ie.Message)
	}
	if x.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}

func TestXSpeedsLoginOK(t *testing.T) {
	site := newXSpeedsSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/takelogin.php": func(w http.ResponseWriter, _ *http.Request) {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc"})
			_, _ = w.Write([]byte(`<html><body><a href="logout.php">Logout</a></body></html>`))
		},
	})
	x := newTestXSpeeds(t, site)

	status, err := x.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
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
	if !x.IsConfigured() {
		t.Fatal("must be configured after login")
	}
	if !strings.Contains(x.CookieHeader(), "session=abc") {
		t.Fatalf("cookie header: %q", x.CookieHeader())
	}
}

func TestXSpeedsSearchRequestAndParse(t *testing.T) {
	site := newXSpeedsSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/browse.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(xspeedsSearchPage))
		},
	})
	x := newTestXSpeeds(t, site)
	x.SetCookieHeader("session=abc")

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "some.movie" // exercises the [ -._]+ → space collapsing

	res, err := x.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	bodies := site.bodies()
	if len(bodies) != 1 {
		t.Fatalf("posts: %v", bodies)
	}
	// MoviesHD maps to 6 tracker cats upstream; with no query categories every
	// tracker cat matches — both cases collapse to "0" (FirstIfSingleOrDefault).
	wantBody := "do=search&category=0&include_dead_torrents=yes&sort=added&order=desc" +
		"&keywords=some+movie&search_type=t_name"
	if bodies[0] != wantBody {
		t.Fatalf("post body: %q want %q", bodies[0], wantBody)
	}

	r := res.Releases[0]
	if r.Title != "Some Movie 2015 1080p" { // [R] suffix stripped
		t.Fatalf("title: %q", r.Title)
	}
	if r.Link == nil || r.Link.Path != "/download.php" || r.Link.RawQuery != "id=111" {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || r.Details.Path != "/details.php" || r.Details.RawQuery != "id=111" {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Size == nil || *r.Size != 1610612736 {
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 15 || r.Peers == nil || *r.Peers != 18 {
		t.Fatalf("seeders/peers: %v %v", r.Seeders, r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 25 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if !hasInt(r.Category, 2040) {
		t.Fatalf("category: %v", r.Category)
	}
	if r.PublishDate.Format("2006-01-02 15:04") != "2015-08-08 12:51" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}
	if r.Poster == nil || r.Poster.String() != "https://img.xspeeds.eu/cover.jpg" {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 1 || r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("factors: %v %v", r.DownloadVolumeFactor, r.UploadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 0.8 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}

	// freeleech row: dvf 0, uvf 2, pre-release date (first match wins)
	r2 := res.Releases[1]
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 0 {
		t.Fatalf("dvf2: %v", r2.DownloadVolumeFactor)
	}
	if r2.UploadVolumeFactor == nil || *r2.UploadVolumeFactor != 2 {
		t.Fatalf("uvf2: %v", r2.UploadVolumeFactor)
	}
	if r2.PublishDate.Format("2006-01-02 15:04") != "2021-04-25 15:00" {
		t.Fatalf("publish date2: %v", r2.PublishDate)
	}
	if !hasInt(r2.Category, 5000) {
		t.Fatalf("category2: %v", r2.Category)
	}
}

func TestXSpeedsMultiCategoryAndImdbSearch(t *testing.T) {
	site := newXSpeedsSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/browse.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`<html><body></body></html>`))
		},
	})
	x := newTestXSpeeds(t, site)
	x.SetCookieHeader("session=abc")

	// multi-category → category=0 (not supported upstream)
	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "show"
	q.Categories = []int{2000, 5000}
	if _, err := x.ResultsForQuery(context.Background(), q, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	bodies := site.bodies()
	if len(bodies) != 1 || !strings.HasPrefix(bodies[0], "do=search&category=0&") {
		t.Fatalf("multi-cat body: %v", bodies)
	}

	// imdb query; MoviesBluRay maps to exactly one tracker cat (109)
	q2 := models.NewTorznabQuery()
	q2.QueryType = "movie"
	q2.ImdbID = "tt1234567"
	q2.Categories = []int{2050}
	if _, err := x.ResultsForQuery(context.Background(), q2, false); err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	bodies = site.bodies()
	want := "do=search&category=109&include_dead_torrents=yes&sort=added&order=desc" +
		"&keywords=tt1234567&search_type=t_both"
	if len(bodies) != 2 || bodies[1] != want {
		t.Fatalf("imdb body: %v want %q", bodies, want)
	}
}

func TestXSpeedsFreeleechFilter(t *testing.T) {
	site := newXSpeedsSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/browse.php": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(xspeedsSearchPage))
		},
	})
	x := newTestXSpeeds(t, site)
	x.SetCookieHeader("session=abc")
	x.cfg.Freeleech.Value = true

	res, err := x.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	if *res.Releases[0].DownloadVolumeFactor != 0 {
		t.Fatalf("dvf: %v", res.Releases[0].DownloadVolumeFactor)
	}
	bodies := site.bodies()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "&sort=free&order=desc") {
		t.Fatalf("freeleech sort body: %v", bodies)
	}
}

func TestXSpeedsParseError(t *testing.T) {
	site := newXSpeedsSite(t, map[string]func(w http.ResponseWriter, r *http.Request){
		"/browse.php": func(w http.ResponseWriter, _ *http.Request) {
			// a row matching the details selector but without td:nth-of-type(3) a
			_, _ = w.Write([]byte(`<html><body><table id="sortabletable"><tbody>
				<tr>
				  <td><a href="browse.php?category=1">Cat</a></td>
				  <td><div><div>01-01-2020 10:00</div></div></td>
				  <td></td>
				  <td><div><a href="details.php?id=1">Broken</a></div></td>
				  <td>1 GB</td><td>1</td><td>1</td><td>1</td>
				</tr>
			</tbody></table></body></html>`))
		},
	})
	x := newTestXSpeeds(t, site)
	x.SetCookieHeader("session=abc")

	_, err := x.ResultsForQuery(context.Background(), &models.TorznabQuery{QueryType: "search", IsTest: true}, false)
	if err == nil {
		t.Fatal("expected parse error")
	}
	ie, ok := err.(*indexer.IndexerError)
	if !ok || ie.Kind != indexer.KindParse {
		t.Fatalf("err: %#v", err)
	}
}

func TestXSpeedsCleanTitle(t *testing.T) {
	if got := xsCleanTitle(""); got != "" {
		t.Fatalf("empty: %q", got)
	}
	if got := xsCleanTitle("Some Title [R]"); got != "Some Title" {
		t.Fatalf("[R] strip: %q", got)
	}
	if got := xsCleanTitle("Some Title [r]"); got != "Some Title" {
		t.Fatalf("[r] strip (ignorecase): %q", got)
	}
	if got := xsCleanTitle("Some Title"); got != "Some Title" {
		t.Fatalf("plain: %q", got)
	}
}
