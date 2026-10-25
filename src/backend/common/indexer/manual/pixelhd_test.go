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
	"time"

	"vestt/common/httpclient"
	"vestt/common/indexer"
	"vestt/common/models"
	"vestt/common/services"
)

const pixelHDLoginWithCaptcha = `<html><body>
<img src="/captcha.jpg" alt="FuckOff Image"/>
<form method="post" action="login.php">
<input type="text" name="user_field" maxlength="20"/>
<input type="password" name="pass_field" maxlength="40"/>
<input type="text" name="captcha_field" maxlength="6"/>
</form>
</body></html>`

const pixelHDLoginNoCaptcha = `<html><body>
<form method="post" action="login.php">
<input type="text" name="username" maxlength="20"/>
<input type="password" name="password" maxlength="40"/>
</form>
</body></html>`

const pixelHDBrowseHTML = `<html><body>
<div class="browsePoster">
  <img class="classBrowsePoster" src="posters/1.jpg"/>
  <a href="https://www.imdb.com/title/tt1234567/">imdb</a>
  <strong>Group Name [2020 ]<a title="View Torrent" href="torrents.php?id=10"></a></strong>
  <table>
    <tr class="group_torrent">
      <td>x</td><td>y</td>
      <td>1 day ago</td>
      <td>1.5 GB</td>
      <td>-</td>
      <td>10</td>
      <td>5</td>
      <td>2</td>
      <td><a href="torrents.php?id=10">Release.1080p</a></td>
      <td><a href="torrents.php?action=download&id=10&amp;auth=abc">DL</a></td>
    </tr>
  </table>
</div>
</body></html>`

type pixelHDSite struct {
	*httptest.Server

	mu      sync.Mutex
	hits    []string
	logins  []string
	captcha bool
}

func newPixelHDSite(t *testing.T, captcha bool) *pixelHDSite {
	t.Helper()
	s := &pixelHDSite{captcha: captcha}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits = append(s.hits, r.URL.RequestURI())
		s.mu.Unlock()
		switch r.URL.Path {
		case "/login.php":
			if r.Method == http.MethodGet {
				// upstream loginPage.Cookies: the login page answers with
				// a captcha-related cookie
				w.Header().Add("Set-Cookie", "captcha=xyz; path=/")
			}
			if r.Method == http.MethodPost {
				if err := r.ParseForm(); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				s.mu.Lock()
				s.logins = append(s.logins, r.PostForm.Encode())
				s.mu.Unlock()
				userField, passField := "username", "password"
				captchaField := ""
				if captcha {
					userField, passField, captchaField = "user_field", "pass_field", "captcha_field"
				}
				ok := r.PostForm.Get(userField) == "okuser" &&
					r.PostForm.Get(passField) == "okpass" &&
					r.PostForm.Get("keeplogged") == "1"
				if captcha && r.PostForm.Get(captchaField) != "ABCD" {
					ok = false
				}
				if ok {
					w.Header().Add("Set-Cookie", "session=pix123; path=/")
					w.Header().Set("Content-Type", "text/html")
					_, _ = fmt.Fprint(w, `<html><body><a href="logout.php">Log out</a></body></html>`)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, `<span class="warning" id="no-cookies">enable cookies<br/></span><span class="warning">Invalid login<br/></span>`)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			if captcha {
				_, _ = fmt.Fprint(w, pixelHDLoginWithCaptcha)
			} else {
				_, _ = fmt.Fprint(w, pixelHDLoginNoCaptcha)
			}
		case "/captcha.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10})
		case "/torrents.php":
			if !strings.Contains(r.Header.Get("Cookie"), "session=pix123") {
				http.Redirect(w, r, "/login.php", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, pixelHDBrowseHTML)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *pixelHDSite) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

func (s *pixelHDSite) loginPosts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logins...)
}

func newTestPixelHD(t *testing.T, site *pixelHDSite) *PixelHD {
	t.Helper()
	client, err := httpclient.New(httpclient.Config{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	ix, err := NewPixelHD(services.Deps{Client: client})
	if err != nil {
		t.Fatalf("NewPixelHD: %v", err)
	}
	p := ix.(*PixelHD)
	p.SiteLinkValue = site.URL + "/"
	p.DefaultSiteLink = site.URL + "/"
	return p
}

func TestPixelHDCaps(t *testing.T) {
	ix, err := NewPixelHD(services.Deps{})
	if err != nil {
		t.Fatalf("NewPixelHD: %v", err)
	}
	caps := ix.Capabilities()
	wantMovie := []string{models.MovieParamQ, models.MovieParamImdbID}
	if strings.Join(caps.MovieSearchParams, ",") != strings.Join(wantMovie, ",") {
		t.Fatalf("MovieSearchParams: %v", caps.MovieSearchParams)
	}
	got := caps.Categories.MapTrackerCatToNewznab("1")
	found := false
	for _, id := range got {
		if id == models.CatMoviesHD.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("MapTrackerCatToNewznab(%q) = %v, want it to contain %d", "1", got, models.CatMoviesHD.ID)
	}
}

func TestPixelHDConfigForm(t *testing.T) {
	// upstream GetConfigurationForSetup fetches the live login page, so a
	// client (fake site) is required even for the form rendering.
	site := newPixelHDSite(t, true)
	p := newTestPixelHD(t, site)
	items, err := p.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var ids []string
	for _, it := range items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{"sitelink", "username", "password", "captchaimage", "captchatext", "", "tags"}
	if strings.Join(ids, "|") != strings.Join(want, "|") {
		t.Fatalf("config ids = %v, want %v", ids, want)
	}
}

func TestPixelHDSetupFetchesCaptcha(t *testing.T) {
	site := newPixelHDSite(t, true)
	p := newTestPixelHD(t, site)

	items, err := p.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var types []string
	for _, it := range items {
		types = append(types, it["type"].(string))
	}
	if types[3] != "displayimage" {
		t.Fatalf("captcha item type: %v", types)
	}
	// the captcha image was fetched and embedded as a data URI
	var captchaValue string
	for _, it := range items {
		if it["id"].(string) == "captchaimage" {
			captchaValue = it["value"].(string)
		}
	}
	if !strings.HasPrefix(captchaValue, "data:image/jpeg;base64,") {
		t.Fatalf("captcha value: %q", captchaValue)
	}
	// field names were discovered from the live login page
	if p.inputUsername != "user_field" || p.inputPassword != "pass_field" || !p.hasCaptcha || p.inputCaptcha != "captcha_field" {
		t.Fatalf("discovered fields: %q %q %v %q", p.inputUsername, p.inputPassword, p.hasCaptcha, p.inputCaptcha)
	}
	if p.cfg.CaptchaCookie.Value == "" {
		t.Fatal("captcha cookie must be captured from the login page")
	}
}

func TestPixelHDLoginAndQuery(t *testing.T) {
	site := newPixelHDSite(t, true)
	p := newTestPixelHD(t, site)

	status, err := p.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
		{ID: "captchatext", Value: "ABCD"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting || !p.IsConfigured() {
		t.Fatalf("status/configured: %v %v", status, p.IsConfigured())
	}
	// posted with the discovered (rotating) field names
	posts := site.loginPosts()
	if len(posts) != 1 || !strings.Contains(posts[0], "user_field=okuser") ||
		!strings.Contains(posts[0], "pass_field=okpass") ||
		!strings.Contains(posts[0], "captcha_field=ABCD") ||
		!strings.Contains(posts[0], "keeplogged=1") {
		t.Fatalf("login posts: %v", posts)
	}

	q := models.NewTorznabQuery()
	q.QueryType = "search"
	q.SearchTerm = "group name"
	res, err := p.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	if len(res.Releases) != 1 {
		t.Fatalf("releases: %d", len(res.Releases))
	}
	r := res.Releases[0]
	// group strong text (" ]" → "]") + torrent title
	if r.Title != "Group Name [2020] Release.1080p" {
		t.Fatalf("title: %q", r.Title)
	}
	if r.Size == nil || *r.Size != 1610612736 { // 1.5 GiB
		t.Fatalf("size: %v", r.Size)
	}
	if r.Seeders == nil || *r.Seeders != 5 || r.Peers == nil || *r.Peers != 7 {
		t.Fatalf("seeders/peers: %v/%v", r.Seeders, r.Peers)
	}
	if r.Grabs == nil || *r.Grabs != 10 {
		t.Fatalf("grabs: %v", r.Grabs)
	}
	if r.Imdb == nil || *r.Imdb != 1234567 {
		t.Fatalf("imdb: %v", r.Imdb)
	}
	if r.MinimumRatio == nil || *r.MinimumRatio != 1 || r.MinimumSeedTime == nil || *r.MinimumSeedTime != 259200 {
		t.Fatalf("minimum ratio/seed time: %v/%v", r.MinimumRatio, r.MinimumSeedTime)
	}
	if r.PublishDate.IsZero() {
		t.Fatal("publish date must be parsed from time-ago")
	}
	if age := time.Since(r.PublishDate); age < 23*time.Hour || age > 25*time.Hour {
		t.Fatalf("publish date age: %v", age)
	}
	if r.Poster == nil || !strings.HasSuffix(r.Poster.String(), "/posters/1.jpg") {
		t.Fatalf("poster: %v", r.Poster)
	}
	if r.Link == nil || !strings.HasSuffix(r.Link.String(), "/torrents.php?action=download&id=10&auth=abc") {
		t.Fatalf("link: %v", r.Link)
	}
	if r.Details == nil || !strings.HasSuffix(r.Details.String(), "/torrents.php?id=10") {
		t.Fatalf("details: %v", r.Details)
	}
	if len(r.Category) != 1 || r.Category[0] != models.CatMoviesHD.ID {
		t.Fatalf("category: %v", r.Category)
	}

	// keyword search: order_by/order_way + duplicate groupname (upstream quirk)
	paths := site.paths()
	last := paths[len(paths)-1]
	if !strings.HasPrefix(last, "/torrents.php?order_by=time&order_way=desc&") ||
		!strings.Contains(last, "groupname=group+name&groupname=group+name") {
		t.Fatalf("search url: %s", last)
	}
}

func TestPixelHDImdbQuery(t *testing.T) {
	site := newPixelHDSite(t, true)
	p := newTestPixelHD(t, site)

	_, err := p.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
		{ID: "captchatext", Value: "ABCD"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}

	q := models.NewTorznabQuery()
	q.QueryType = "movie"
	q.ImdbID = "tt1234567"
	_, err = p.ResultsForQuery(context.Background(), q, false)
	if err != nil {
		t.Fatalf("ResultsForQuery: %v", err)
	}
	paths := site.paths()
	last := paths[len(paths)-1]
	// imdbid replaces the first groupname; the unconditional groupname stays
	if !strings.Contains(last, "imdbid=tt1234567") ||
		strings.Contains(last, "groupname=tt1234567") ||
		!strings.Contains(last, "groupname=") {
		t.Fatalf("imdb search url: %s", last)
	}
}

func TestPixelHDLoginError(t *testing.T) {
	site := newPixelHDSite(t, true)
	p := newTestPixelHD(t, site)

	_, err := p.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "wrong"},
		{ID: "captchatext", Value: "ABCD"},
	})
	if err == nil || err.Error() != "Invalid login" {
		t.Fatalf("err: %v", err)
	}
	if p.IsConfigured() {
		t.Fatal("must not be configured after failed login")
	}
}

func TestPixelHDNoCaptchaFlow(t *testing.T) {
	site := newPixelHDSite(t, false)
	p := newTestPixelHD(t, site)

	status, err := p.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "sitelink", Value: site.URL + "/"},
		{ID: "username", Value: "okuser"},
		{ID: "password", Value: "okpass"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigRequiresTesting {
		t.Fatalf("status: %v", status)
	}
	posts := site.loginPosts()
	if len(posts) != 1 || strings.Contains(posts[0], "captcha") {
		t.Fatalf("login posts: %v", posts)
	}
}
