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

type speedCDSite struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []string
	posts []map[string]string

	// step1Body/step2Body customize the checkpoint responses (empty = default)
	step1Body string
	step2Body string
	// step2Status overrides the second checkpoint status (0 = default 302)
	step2Status int
	// loggedIn toggles the /logout.php marker in browse responses
	loggedIn bool
	// browseBody overrides the browse response body (empty = default)
	browseBody string
}

func newSpeedCDSite(t *testing.T) *speedCDSite {
	t.Helper()
	s := &speedCDSite{loggedIn: true}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.Method+" "+r.URL.RequestURI())
		s.mu.Unlock()
		switch r.URL.Path {
		case "/checkpoint/API":
			_ = r.ParseForm()
			s.mu.Lock()
			s.posts = append(s.posts, map[string]string{"username": r.PostForm.Get("username")})
			s.mu.Unlock()
			w.Header().Add("Set-Cookie", "sid=step1; Path=/")
			body := s.step1Body
			if body == "" {
				body = `{"form": "<input name=\"a\" value=\"TOK123\"/>"}`
			}
			_, _ = w.Write([]byte(body))
		case "/checkpoint/":
			_ = r.ParseForm()
			s.mu.Lock()
			s.posts = append(s.posts, map[string]string{
				"pwd": r.PostForm.Get("pwd"),
				"a":   r.PostForm.Get("a"),
				"sid": r.Header.Get("Cookie"),
			})
			step2Status := s.step2Status
			step2Body := s.step2Body
			s.mu.Unlock()
			if step2Status != 0 {
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte(step2Body))
				return
			}
			http.Redirect(w, r, "/browse.php", http.StatusFound)
		case "/browse.php":
			w.Header().Set("Content-Type", "text/html")
			// Upstream captures the login Set-Cookie on the 302 hop
			// (HttpWebClient.cs:85 AllowAutoRedirect=false) and ConfigureIfOK
			// stores that hop's pairs (SpeedCD.cs:162). The Go stack follows
			// redirects transparently and only observes the final response,
			// so the fake site issues the login cookie here instead.
			w.Header().Add("Set-Cookie", "session=xyz; Path=/")
			_, _ = w.Write([]byte(`<html>/browse.php /logout.php</html>`))
		default: // /browse/* search paths
			w.Header().Set("Content-Type", "text/html")
			s.mu.Lock()
			custom := s.browseBody
			s.mu.Unlock()
			if custom != "" {
				_, _ = w.Write([]byte(custom))
				return
			}
			if s.loggedIn {
				_, _ = w.Write([]byte(speedCDBrowseHTML))
			} else {
				_, _ = w.Write([]byte(`<html><div class="boxContent"><table><tbody></tbody></table></div></html>`))
			}
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *speedCDSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *speedCDSite) forms() []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]string(nil), s.posts...)
}

func newTestSpeedCD(t *testing.T, site *speedCDSite) *SpeedCD {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewSpeedCD(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewSpeedCD: %v", err)
	}
	sc := ix.(*SpeedCD)
	sc.SiteLinkValue = site.srv.URL + "/"
	client.SetRequestDelay(sc.ID(), 0)
	return sc
}

func speedCDLoginItems(site *speedCDSite) []indexer.ConfigItem {
	return []indexer.ConfigItem{
		{ID: "sitelink", Value: site.srv.URL + "/"},
		{ID: "username", Value: "user"},
		{ID: "password", Value: "pass"},
	}
}

const speedCDBrowseHTML = `<html><body>/logout.php
<div class="boxContent"><table><tbody>
<tr>
  <td><a href="/browse/49">TV/HD</a></td>
  <td><div>
    <a href="/t/1111">Some[REQUEST] Title 1080p...</a>
    <span class="elapsedDate1" title="Friday, January 5, 2024 at 3:04PM"></span>
    <span class="flag">[Freeleech]</span>
  </div></td>
  <td></td>
  <td><a href="/download/1111">dl</a></td>
  <td></td>
  <td>1.5 GB</td>
  <td>10</td>
  <td>5</td>
  <td>2</td>
</tr>
<tr>
  <td><a href="/browse/2">TV/Episodes</a></td>
  <td><div>
    <a href="/t/2222">Other Title 720p</a>
    <span class="elapsedDate2" title="Saturday, January 6, 2024 at 4:30PM"></span>
  </div></td>
  <td></td>
  <td><a href="/download/2222">dl</a></td>
  <td></td>
  <td>700 MB</td>
  <td>20</td>
  <td>1</td>
  <td>0</td>
</tr>
</tbody></table></div></body></html>`

func TestSpeedCDCaps(t *testing.T) {
	ix, err := NewSpeedCD(services.Deps{})
	if err != nil {
		t.Fatalf("NewSpeedCD: %v", err)
	}
	caps := ix.Capabilities()
	wantTV := []string{models.TVParamQ, models.TVParamSeason, models.TVParamEp, models.TVParamImdbID}
	if strings.Join(caps.TVSearchParams, ",") != strings.Join(wantTV, ",") {
		t.Fatalf("TVSearchParams: %v", caps.TVSearchParams)
	}
	wantMovie := []string{models.MovieParamQ, models.MovieParamImdbID}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	if got := caps.Categories.MapTrackerCatToNewznab("49"); !containsInt(got, models.CatTVHD.ID) {
		t.Fatalf("cat 49: %v", got)
	}
	if got := caps.Categories.MapTrackerCatToNewznab("2"); !containsInt(got, models.CatTVSD.ID) {
		t.Fatalf("cat 2: %v", got)
	}
}

func TestSpeedCDConfigForm(t *testing.T) {
	ix, err := NewSpeedCD(services.Deps{})
	if err != nil {
		t.Fatalf("NewSpeedCD: %v", err)
	}
	items, err := ix.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	// ids mirror upstream ConfigurationItem.ID = Name.Replace(" ", "").ToLower()
	// (ConfigurationData.cs:105): the bools are named by their upstream labels
	// (ConfigurationDataSpeedCD.cs:15-16), the instructions item has an empty
	// name (ConfigurationDataBasicLogin.cs:18).
	want := []string{"sitelink", "username", "password", "", "searchfreeleechonly", "excludetorrentswithrarfiles", "accountinactivity", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids: %v", ids)
	}
}

func TestSpeedCDLoginSuccess(t *testing.T) {
	site := newSpeedCDSite(t)
	sc := newTestSpeedCD(t, site)

	status, err := sc.ApplyConfiguration(context.Background(), speedCDLoginItems(site))
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status: %v", status)
	}
	if !sc.IsConfigured() {
		t.Fatal("must be configured")
	}
	if sc.CookieHeader() != "session=xyz" {
		t.Fatalf("cookie header: %q", sc.CookieHeader())
	}

	paths := site.paths()
	if len(paths) != 3 || paths[0] != "POST /checkpoint/API" || paths[1] != "POST /checkpoint/" || paths[2] != "GET /browse.php" {
		t.Fatalf("paths: %v", paths)
	}
	forms := site.forms()
	if len(forms) != 2 {
		t.Fatalf("forms: %v", forms)
	}
	if forms[0]["username"] != "user" {
		t.Fatalf("step1 form: %v", forms[0])
	}
	if forms[1]["pwd"] != "pass" || forms[1]["a"] != "TOK123" {
		t.Fatalf("step2 form: %v", forms[1])
	}
	if !strings.Contains(forms[1]["sid"], "sid=step1") {
		t.Fatalf("step2 must carry step1 cookies: %q", forms[1]["sid"])
	}
}

func TestSpeedCDLoginErrorMessage(t *testing.T) {
	site := newSpeedCDSite(t)
	site.step2Status = http.StatusOK
	site.step2Body = `<html><body><h5>Invalid username or password</h5></body></html>`
	sc := newTestSpeedCD(t, site)

	_, err := sc.ApplyConfiguration(context.Background(), speedCDLoginItems(site))
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindConfig {
		t.Fatalf("err: %v", err)
	}
	if ie.Message != "Invalid username or password" {
		t.Fatalf("message: %q", ie.Message)
	}
	if sc.IsConfigured() {
		t.Fatal("must not be configured after failure")
	}
}

func TestSpeedCDLoginCaptchaOverride(t *testing.T) {
	site := newSpeedCDSite(t)
	site.step2Status = http.StatusOK
	site.step2Body = `<html><body><h5>Wrong Captcha!</h5>Wrong Captcha!</body></html>`
	sc := newTestSpeedCD(t, site)

	_, err := sc.ApplyConfiguration(context.Background(), speedCDLoginItems(site))
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) {
		t.Fatalf("err: %v", err)
	}
	want := "Captcha required due to a failed login attempt. Login via a browser to whitelist your IP and then reconfigure Jackett."
	if ie.Message != want {
		t.Fatalf("message: %q", ie.Message)
	}
}

func TestSpeedCDLoginMissingH5Fallback(t *testing.T) {
	site := newSpeedCDSite(t)
	site.step2Status = http.StatusOK
	site.step2Body = `<html><body>nope</body></html>`
	sc := newTestSpeedCD(t, site)

	_, err := sc.ApplyConfiguration(context.Background(), speedCDLoginItems(site))
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Message != "Login failed." {
		t.Fatalf("err: %v", err)
	}
}

func TestSpeedCDLoginTokenParseError(t *testing.T) {
	site := newSpeedCDSite(t)
	site.step1Body = `garbage without token`
	sc := newTestSpeedCD(t, site)

	_, err := sc.ApplyConfiguration(context.Background(), speedCDLoginItems(site))
	if err == nil || err.Error() != "Error parsing the login form" {
		t.Fatalf("err: %v", err)
	}
}

func loginSpeedCD(t *testing.T, sc *SpeedCD, site *speedCDSite) {
	t.Helper()
	if _, err := sc.ApplyConfiguration(context.Background(), speedCDLoginItems(site)); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
}

func TestSpeedCDSearchRequestURLs(t *testing.T) {
	site := newSpeedCDSite(t)
	sc := newTestSpeedCD(t, site)
	loginSpeedCD(t, sc, site)

	// category-scoped TVHD query → /browse/52/49/q/{term}
	q := models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.SearchTerm = "some term"
	q.Categories = []int{models.CatTVHD.ID}
	if _, err := sc.perform(context.Background(), q); err != nil {
		t.Fatalf("perform: %v", err)
	}

	// freeleech + exclude archives flags
	sc.cfg.Freeleech.Value = true
	sc.cfg.ExcludeArchives.Value = true
	if _, err := sc.perform(context.Background(), q); err != nil {
		t.Fatalf("perform: %v", err)
	}
	sc.cfg.Freeleech.Value = false
	sc.cfg.ExcludeArchives.Value = false

	// imdb query with season → deep/q/tt…+S02*
	q = models.NewTorznabQuery()
	q.QueryType = "tvsearch"
	q.ImdbID = "tt1234567"
	season := 2
	q.Season = &season
	if _, err := sc.perform(context.Background(), q); err != nil {
		t.Fatalf("perform: %v", err)
	}

	// pagination: limit 100, offset 100 → p 2
	q = models.NewTorznabQuery()
	q.Limit = 100
	q.Offset = 100
	if _, err := sc.perform(context.Background(), q); err != nil {
		t.Fatalf("perform: %v", err)
	}

	got := site.paths()
	if len(got) != 7 { // 3 login + 4 searches
		t.Fatalf("paths: %v", got)
	}
	if got[3] != "GET /browse/52/49/q/some+term" {
		t.Fatalf("category path: %q", got[3])
	}
	if got[4] != "GET /browse/52/49/freeleech/norar/q/some+term" {
		t.Fatalf("flags path: %q", got[4])
	}
	// the imdb query specifies no categories → upstream MapTorznabCapsToTrackers
	// yields no tracker categories (BaseIndexer.cs:184 / TorznabCapabilitiesCategories)
	if got[5] != "GET /browse/deep/q/tt1234567+S02*" {
		t.Fatalf("imdb path: %q", got[5])
	}
	if !strings.HasSuffix(got[6], "/p/2") {
		t.Fatalf("pagination path: %q", got[6])
	}
}

func TestSpeedCDSearchParse(t *testing.T) {
	site := newSpeedCDSite(t)
	sc := newTestSpeedCD(t, site)
	loginSpeedCD(t, sc, site)

	// ResultsForQuery needs a query type: upstream CanHandleQuery rejects an
	// empty QueryType (BaseIndexer.cs:275-311); TestIndexer searches with
	// QueryType "search" (indexer.TestQuery).
	res, err := sc.ResultsForQuery(context.Background(), indexer.TestQuery(), false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 2 {
		t.Fatalf("releases: %d", len(res.Releases))
	}

	r := res.Releases[0]
	// [REQUEST] stripped, trailing dots trimmed (upstream CleanTitle replaces
	// the tag with string.Empty and never collapses spaces, SpeedCD.cs:295)
	if r.Title != "Some Title 1080p" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.Path, "/download/1111") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.Path, "/t/1111") {
		t.Fatalf("details: %v", r.Details)
	}
	if r.Guid == nil || r.Guid.String() != r.Link.String() {
		t.Fatalf("guid: %v", r.Guid)
	}
	if r.Size == nil || *r.Size != 1610612736 { // 1.5 GB
		t.Fatalf("size: %v", r.Size)
	}
	if r.Grabs == nil || *r.Grabs != 10 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Seeders == nil || *r.Seeders != 5 || r.Peers == nil || *r.Peers != 7 {
		t.Fatalf("seeders/peers: %v %v", r.Seeders, r.Peers)
	}
	if r.DownloadVolumeFactor == nil || *r.DownloadVolumeFactor != 0 { // [Freeleech]
		t.Fatalf("dvf: %v", r.DownloadVolumeFactor)
	}
	if r.UploadVolumeFactor == nil || *r.UploadVolumeFactor != 1 {
		t.Fatalf("uvf: %v", r.UploadVolumeFactor)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 {
		t.Fatalf("minimum ratio: %v", r.MinimumRatio)
	}
	if r.MinimumSeedTime == nil || *r.MinimumSeedTime != 259200 {
		t.Fatalf("minimum seed time: %v", r.MinimumSeedTime)
	}
	if !containsInt(r.Category, models.CatTVHD.ID) {
		t.Fatalf("category: %v", r.Category)
	}
	if r.PublishDate.UTC().Format("2006-01-02 15:04") != "2024-01-05 15:04" {
		t.Fatalf("publish date: %v", r.PublishDate)
	}

	r2 := res.Releases[1]
	if r2.Title != "Other Title 720p" {
		t.Fatalf("title2: %q", r2.Title)
	}
	if r2.DownloadVolumeFactor == nil || *r2.DownloadVolumeFactor != 1 {
		t.Fatalf("dvf2: %v", r2.DownloadVolumeFactor)
	}
	if !containsInt(r2.Category, models.CatTVSD.ID) {
		t.Fatalf("category2: %v", r2.Category)
	}
	if r2.Size == nil || *r2.Size != 734003200 { // 700 MB
		t.Fatalf("size2: %v", r2.Size)
	}
}

func TestSpeedCDReloginOnMissingLogout(t *testing.T) {
	site := newSpeedCDSite(t)
	site.loggedIn = false
	sc := newTestSpeedCD(t, site)
	loginSpeedCD(t, sc, site)

	// browse responses lack /logout.php → doLogin runs again, then retry
	// (TestQuery: upstream CanHandleQuery rejects an empty QueryType,
	// BaseIndexer.cs:275-311; TestIndexer uses QueryType "search")
	res, err := sc.ResultsForQuery(context.Background(), indexer.TestQuery(), false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 0 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	paths := site.paths()
	if len(paths) != 8 { // 3 login + browse + 2 checkpoint + browse.php + browse
		t.Fatalf("paths: %v", paths)
	}
	if paths[4] != "POST /checkpoint/API" || paths[5] != "POST /checkpoint/" {
		t.Fatalf("re-login not attempted: %v", paths)
	}
}

func TestSpeedCDParseError(t *testing.T) {
	site := newSpeedCDSite(t)
	site.browseBody = `<html><body>/logout.php
<div class="boxContent"><table><tbody>
<tr>
  <td><a href="/browse/49">TV/HD</a></td>
  <td><div>
    <a href="/t/1111">Bad Date Title</a>
    <span class="elapsedDate1" title="not a date"></span>
  </div></td>
  <td></td>
  <td><a href="/download/1111">dl</a></td>
  <td></td>
  <td>1.5 GB</td>
  <td>10</td>
  <td>5</td>
  <td>2</td>
</tr>
</tbody></table></div></body></html>`
	sc := newTestSpeedCD(t, site)
	loginSpeedCD(t, sc, site)

	// an unparseable publish date aborts the whole query (upstream OnParseError)
	// (TestQuery: upstream CanHandleQuery rejects an empty QueryType,
	// BaseIndexer.cs:275-311; TestIndexer uses QueryType "search")
	_, err := sc.ResultsForQuery(context.Background(), indexer.TestQuery(), false)
	var ie *indexer.IndexerError
	if !errors.As(err, &ie) || ie.Kind != indexer.KindParse || ie.Message != "Parse error" {
		t.Fatalf("err: %v", err)
	}
}
