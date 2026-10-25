package engine

import (
	"context"
	"fmt"
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
	"vestt/common/indexer/configdata"
)

// loginFixture is a fake tracker site recording login submissions.
type loginFixture struct {
	srv *httptest.Server

	mu       sync.Mutex
	posts    []url.Values
	postPath []string
	headers  []http.Header
}

func newLoginFixture(t *testing.T, handler http.Handler) *loginFixture {
	t.Helper()
	f := &loginFixture{}
	f.srv = httptest.NewServer(handler)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *loginFixture) baseDef() *IndexerDefinition {
	return &IndexerDefinition{
		ID:          "logintest",
		Name:        "Login Test",
		Description: "test",
		Type:        "private",
		Language:    "en-US",
		Encoding:    "utf-8",
		Links:       []string{f.srv.URL},
		Caps: &CapsBlock{
			Categories: StringMap{{Key: "1", Value: "Movies"}},
			Modes: StringListMap{
				{Key: "search", Values: []string{"q"}},
				{Key: "tv-search", Values: []string{"q", "season", "ep"}},
			},
			Allowrawsearch: false,
		},
		Search: &SearchBlock{Path: "/torrents"},
	}
}

func (f *loginFixture) newIndexer(t *testing.T, def *IndexerDefinition) *Cardigann {
	t.Helper()
	x, err := NewCardigann(def, discardLogger())
	if err != nil {
		t.Fatalf("NewCardigann: %v", err)
	}
	client, err := httpclient.New(httpclient.Config{}, nil, discardLogger())
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	x.Client = client
	return x
}

func (f *loginFixture) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		f.posts = append(f.posts, r.PostForm)
		f.postPath = append(f.postPath, r.URL.Path)
	}
	f.headers = append(f.headers, r.Header.Clone())
}

func (f *loginFixture) lastPost() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posts) == 0 {
		return nil
	}
	return f.posts[len(f.posts)-1]
}

const loginPage = `<html><body>
<form method="post" action="/login">
  <input type="hidden" name="token" value="tok123"/>
  <input type="text" name="username"/>
  <input type="password" name="password"/>
  <input type="checkbox" name="remember"/>
  <input type="submit" name="submit" value="Login"/>
</form></body></html>`

func newFormFixture(t *testing.T, onLogin func(http.ResponseWriter, *http.Request)) *loginFixture {
	t.Helper()
	f := &loginFixture{}
	f.srv = httptest.NewServer(formLoginHandler(f, onLogin))
	t.Cleanup(f.srv.Close)
	return f
}

func formLoginHandler(f *loginFixture, onLogin func(http.ResponseWriter, *http.Request)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if r.URL.Path == "/login" {
			if r.Method == http.MethodGet {
				w.Header().Set("Content-Type", "text/html")
				_, _ = fmt.Fprint(w, loginPage)
				return
			}
			onLogin(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body><div id="loggedin">welcome</div></body></html>`)
	})
	return mux
}

func applyUserPass(t *testing.T, x *Cardigann) []indexer.ConfigItem {
	t.Helper()
	return []indexer.ConfigItem{
		{ID: "username", Value: "alice"},
		{ID: "password", Value: "secret"},
	}
}

// 1. form login success: hidden input carried, checkbox-without-checked
// dropped, session cookie stored, test selector verified.
func TestLoginFormSuccess(t *testing.T) {
	f := newFormFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.PostForm.Get("username") == "alice" && r.PostForm.Get("password") == "secret" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc"})
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body>ok</body></html>`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})
	def := f.baseDef()
	def.Settings = []*SettingsField{
		{Name: "username", Label: "Username", Type: "text"},
		{Name: "password", Label: "Password", Type: "password"},
	}
	def.Login = &LoginBlock{
		Method: "form",
		Path:   "/login",
		Inputs: StringMap{
			{Key: "username", Value: "{{ .Config.username }}"},
			{Key: "password", Value: "{{ .Config.password }}"},
		},
		Test: &PageTestBlock{Path: "/", Selector: "#loggedin"},
	}
	x := f.newIndexer(t, def)

	status, err := x.ApplyConfiguration(context.Background(), applyUserPass(t, x))
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if status != indexer.ConfigCompleted {
		t.Fatalf("status = %v", status)
	}
	post := f.lastPost()
	if got := post.Get("token"); got != "tok123" {
		t.Errorf("hidden token = %q, want tok123", got)
	}
	if post.Has("remember") {
		t.Errorf("unchecked checkbox must not be submitted")
	}
	if !x.IsConfigured() {
		t.Errorf("indexer must be configured after ApplyConfiguration")
	}
	if ck := x.CookieHeader(); !strings.Contains(ck, "session=abc") {
		t.Errorf("CookieHeader = %q, want session=abc", ck)
	}
}

// 2. form login with error selector hit (bad credentials page).
func TestLoginFormErrorSelector(t *testing.T) {
	f := newFormFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `<html><body><div class="error">Invalid credentials</div></body></html>`)
	})
	def := f.baseDef()
	def.Login = &LoginBlock{
		Method: "form",
		Path:   "/login",
		Error: []ErrorBlock{
			{Selector: ".error"},
		},
	}
	x := f.newIndexer(t, def)

	_, err := x.ApplyConfiguration(context.Background(), applyUserPass(t, x))
	if err == nil || !strings.Contains(err.Error(), "Error: Invalid credentials") {
		t.Fatalf("err = %v", err)
	}
	if x.IsConfigured() {
		t.Errorf("indexer must not be configured after login error")
	}
}

// 3. two-factor / extra inputs templated from config values.
func TestLoginFormTwoFactorInputs(t *testing.T) {
	f := newFormFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body>ok</body></html>`)
	})
	def := f.baseDef()
	def.Settings = []*SettingsField{
		{Name: "username", Label: "Username", Type: "text"},
		{Name: "password", Label: "Password", Type: "password"},
		{Name: "2facode", Label: "2FA code", Type: "text"},
	}
	def.Login = &LoginBlock{
		Method: "form",
		Path:   "/login",
		Inputs: StringMap{{Key: "otp", Value: "{{ .Config.2facode }}"}},
	}
	x := f.newIndexer(t, def)

	_, err := x.ApplyConfiguration(context.Background(), []indexer.ConfigItem{
		{ID: "username", Value: "alice"},
		{ID: "password", Value: "secret"},
		{ID: "2facode", Value: "123456"},
	})
	if err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if got := f.lastPost().Get("otp"); got != "123456" {
		t.Errorf("otp = %q, want 123456", got)
	}
}

// 4. cookie login method: no request, cookie header from the cookie setting.
func TestLoginCookieMethod(t *testing.T) {
	f := newLoginFixture(t, http.NotFoundHandler())
	def := f.baseDef()
	def.Settings = []*SettingsField{
		{Name: "cookie", Label: "Cookie", Type: "text"},
	}
	def.Login = &LoginBlock{Method: "cookie"}
	x := f.newIndexer(t, def)
	if item, ok := x.config.GetDynamic("cookie").(*configdata.StringItem); ok {
		item.Value = "uid=42; pass=zzz"
	} else {
		t.Fatalf("cookie setting missing")
	}

	if _, err := x.ApplyConfiguration(context.Background(), nil); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if got := x.CookieHeader(); got != "uid=42; pass=zzz" {
		t.Errorf("CookieHeader = %q", got)
	}
	if !x.IsConfigured() {
		t.Errorf("must be configured")
	}
}

// 5. submitpath overrides the form action.
func TestLoginFormSubmitpath(t *testing.T) {
	f := newFormFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body>ok</body></html>`)
	})
	def := f.baseDef()
	def.Login = &LoginBlock{
		Method:     "form",
		Path:       "/login",
		Submitpath: "/different-submit",
		Inputs: StringMap{
			{Key: "username", Value: "{{ .Config.username }}"},
			{Key: "password", Value: "{{ .Config.password }}"},
		},
	}
	x := f.newIndexer(t, def)

	if _, err := x.ApplyConfiguration(context.Background(), applyUserPass(t, x)); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.postPath) == 0 || f.postPath[len(f.postPath)-1] != "/different-submit" {
		t.Errorf("post paths = %v, want last /different-submit", f.postPath)
	}
}

// 6. login error blocks: 401 surfaces the dedicated message.
func TestLoginUnauthorized(t *testing.T) {
	f := newFormFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	def := f.baseDef()
	def.Login = &LoginBlock{
		Method: "form",
		Path:   "/login",
		Inputs: StringMap{
			{Key: "username", Value: "{{ .Config.username }}"},
			{Key: "password", Value: "{{ .Config.password }}"},
		},
	}
	x := f.newIndexer(t, def)

	_, err := x.ApplyConfiguration(context.Background(), applyUserPass(t, x))
	if err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("err = %v", err)
	}
}

// 7. captcha flow: GET config captures the image; submitted text is posted.
func TestLoginCaptchaImage(t *testing.T) {
	mux := http.NewServeMux()
	f := &loginFixture{}
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body><form method="post" action="/login">
			  <img id="captcha" src="/captcha.jpg"/>
			  <input type="text" name="username"/><input type="password" name="password"/>
			  <input type="text" name="captcha"/>
			</form></body></html>`)
		case "/captcha.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = fmt.Fprint(w, "FAKEJPEG")
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<html><body>ok</body></html>`)
		}
	})

	def := f.baseDef()
	def.Login = &LoginBlock{
		Method:  "form",
		Path:    "/login",
		Captcha: &CaptchaBlock{Type: "image", Selector: "#captcha", Input: "captcha"},
	}
	x := f.newIndexer(t, def)

	// GET config captures the captcha
	items, err := x.ConfigurationForSetup(context.Background())
	if err != nil {
		t.Fatalf("ConfigurationForSetup: %v", err)
	}
	var sawImage, sawText bool
	for _, it := range items {
		if it["id"] == "captchaimage" {
			sawImage = true
			if !strings.HasPrefix(it["value"].(string), "data:image/jpeg;base64,") {
				t.Errorf("captcha image value not a JPEG data URI: %v", it["value"])
			}
		}
		if it["id"] == "captchatext" {
			sawText = true
		}
	}
	if !sawImage || !sawText {
		t.Fatalf("captcha items missing: image=%v text=%v", sawImage, sawText)
	}

	// user fills the captcha text, then configures
	if item, ok := x.config.GetDynamic("CaptchaText").(*configdata.StringItem); ok {
		item.Value = "XY42"
	} else {
		t.Fatalf("CaptchaText dynamic missing")
	}
	if _, err := x.ApplyConfiguration(context.Background(), applyUserPass(t, x)); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if got := f.lastPost().Get("captcha"); got != "XY42" {
		t.Errorf("captcha posted = %q, want XY42", got)
	}
}

// 8. login headers injected (templated with base variables).
func TestLoginHeadersInjected(t *testing.T) {
	f := newFormFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body>ok</body></html>`)
	})
	def := f.baseDef()
	def.Login = &LoginBlock{
		Method:  "form",
		Path:    "/login",
		Headers: StringListMap{{Key: "X-Custom", Values: []string{"site={{ .Config.sitelink }}"}}},
	}
	x := f.newIndexer(t, def)

	if _, err := x.ApplyConfiguration(context.Background(), applyUserPass(t, x)); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for _, h := range f.headers {
		if strings.Contains(h.Get("X-Custom"), f.srv.URL) {
			found = true
		}
	}
	if !found {
		t.Errorf("X-Custom header with sitelink not observed in %d requests", len(f.headers))
	}
}

// 9. get login method builds a query string from inputs.
func TestLoginGetMethod(t *testing.T) {
	var gotQuery string
	f := &loginFixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<html><body>ok</body></html>`)
	}))
	t.Cleanup(f.srv.Close)
	def := f.baseDef()
	def.Login = &LoginBlock{
		Method: "get",
		Path:   "/login",
		Inputs: StringMap{{Key: "username", Value: "{{ .Config.username }}"}, {Key: "key", Value: "K"}},
	}
	x := f.newIndexer(t, def)

	if _, err := x.ApplyConfiguration(context.Background(), []indexer.ConfigItem{{ID: "username", Value: "bob"}}); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	q, err := url.QueryUnescape(gotQuery)
	if err != nil || !strings.Contains(q, "username=bob") || !strings.Contains(q, "key=K") {
		t.Errorf("query = %q (err %v)", gotQuery, err)
	}
}

// 10. selectorinputs extract values from the landing page.
func TestLoginSelectorInputs(t *testing.T) {
	page := `<html><body><form method="post" action="/login">
	  <input type="hidden" id="csrftok" name="csrf" value="csrf42"/>
	  <input type="text" name="username"/><input type="password" name="password"/>
	</form></body></html>`
	f := &loginFixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/login" && r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, page)
			return
		}
		_, _ = fmt.Fprint(w, `<html><body>ok</body></html>`)
	}))
	t.Cleanup(f.srv.Close)
	def := f.baseDef()
	def.Login = &LoginBlock{
		Method:         "form",
		Path:           "/login",
		SelectorInputs: SelectorInputMap{{Key: "csrf", Value: &SelectorBlock{Selector: "#csrftok", Attribute: "value"}}},
	}
	x := f.newIndexer(t, def)

	if _, err := x.ApplyConfiguration(context.Background(), applyUserPass(t, x)); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if got := f.lastPost().Get("csrf"); got != "csrf42" {
		t.Errorf("csrf = %q, want csrf42", got)
	}
}

// 11. checkbox "checked" semantics: a checked box is submitted.
func TestLoginFormCheckedCheckbox(t *testing.T) {
	page := `<html><body><form method="post" action="/login">
	  <input type="text" name="username"/><input type="password" name="password"/>
	  <input type="checkbox" name="remember" value="yes" checked/>
	</form></body></html>`
	f := &loginFixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/login" && r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, page)
			return
		}
		_, _ = fmt.Fprint(w, `<html><body>ok</body></html>`)
	}))
	t.Cleanup(f.srv.Close)
	def := f.baseDef()
	def.Login = &LoginBlock{
		Method: "form",
		Path:   "/login",
		Inputs: StringMap{
			{Key: "username", Value: "{{ .Config.username }}"},
			{Key: "password", Value: "{{ .Config.password }}"},
		},
	}
	x := f.newIndexer(t, def)

	if _, err := x.ApplyConfiguration(context.Background(), applyUserPass(t, x)); err != nil {
		t.Fatalf("ApplyConfiguration: %v", err)
	}
	if got := f.lastPost().Get("remember"); got != "yes" {
		t.Errorf("remember = %q, want yes", got)
	}
}

// 12. login test selector mismatch fails the configuration.
func TestLoginTestSelectorMismatch(t *testing.T) {
	f := &loginFixture{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/login" && r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, loginPage)
			return
		}
		_, _ = fmt.Fprint(w, `<html><body>no marker here</body></html>`)
	}))
	t.Cleanup(f.srv.Close)
	def := f.baseDef()
	def.Login = &LoginBlock{
		Method: "form",
		Path:   "/login",
		Test:   &PageTestBlock{Path: "/", Selector: "#must-exist"},
	}
	x := f.newIndexer(t, def)

	_, err := x.ApplyConfiguration(context.Background(), applyUserPass(t, x))
	if err == nil || !strings.Contains(err.Error(), "didn't match") {
		t.Fatalf("err = %v", err)
	}
}

// helpers referenced above
var _ = io.Discard
var _ = slog.Default
