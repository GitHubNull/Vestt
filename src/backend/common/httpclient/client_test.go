package httpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, cfg Config, saver CookieSaver) *Client {
	t.Helper()
	if cfg.RetryAttempts == 0 {
		cfg.RetryAttempts = 1 // keep tests fast; retry behaviour tested separately
	}
	c, err := New(cfg, saver, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestRateLimitSerializesSameIndexer fires 20 concurrent requests at one
// indexer with a 50 ms requestDelay: they must run serially with ≥ delay
// between starts; a second indexer must not be blocked.
func TestRateLimitSerializesSameIndexer(t *testing.T) {
	var mu sync.Mutex
	var starts []time.Time
	var maxConcurrent atomic.Int32
	var current atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := current.Add(1)
		for {
			m := maxConcurrent.Load()
			if n <= m || maxConcurrent.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		current.Add(-1)
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	c := newTestClient(t, Config{RequestDelay: 50 * time.Millisecond}, nil)

	const total = 20
	var wg sync.WaitGroup
	begin := time.Now()
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Do(context.Background(), "slow", &Request{URL: srv.URL})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(begin)

	// serialization: never two handler invocations overlapping
	if maxConcurrent.Load() > 1 {
		t.Fatalf("requests overlapped: max concurrent = %d", maxConcurrent.Load())
	}
	// timing: (total-1) gaps of ≥ 50ms each, ±20% tolerance on the total
	minExpected := time.Duration(total-1) * 50 * time.Millisecond * 8 / 10
	if elapsed < minExpected {
		t.Fatalf("rate limit not enforced: %d requests in %v", total, elapsed)
	}

	// a second indexer is not delayed by the first
	start := time.Now()
	if _, err := c.Do(context.Background(), "other", &Request{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 45*time.Millisecond {
		t.Fatalf("other indexer was blocked: %v", d)
	}
}

// TestCookiePersistence: Set-Cookie is replayed automatically and the saver
// receives the jar; a fresh client restored via LoadCookies replays too.
func TestCookiePersistence(t *testing.T) {
	var mu sync.Mutex
	saved := map[string][]PersistedCookie{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc123", Path: "/"})
			_, _ = fmt.Fprint(w, "logged in")
			return
		}
		_, _ = fmt.Fprint(w, "cookie:"+r.Header.Get("Cookie"))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{}, func(id string, cookies []PersistedCookie) {
		mu.Lock()
		defer mu.Unlock()
		saved[id] = cookies
	})

	if _, err := c.Do(context.Background(), "tracker", &Request{URL: srv.URL + "/login"}); err != nil {
		t.Fatal(err)
	}

	res, err := c.Do(context.Background(), "tracker", &Request{URL: srv.URL + "/page"})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "cookie:session=abc123" {
		t.Fatalf("jar cookie not replayed: %q", res.Body)
	}

	mu.Lock()
	jar := saved["tracker"]
	mu.Unlock()
	if len(jar) != 1 || jar[0].Name != "session" || jar[0].Value != "abc123" {
		t.Fatalf("saver did not receive the jar: %+v", jar)
	}

	// new client + LoadCookies restores the jar (restart survival)
	c2 := newTestClient(t, Config{}, nil)
	c2.LoadCookies("tracker", jar)
	res, err = c2.Do(context.Background(), "tracker", &Request{URL: srv.URL + "/page"})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "cookie:session=abc123" {
		t.Fatalf("restored jar not replayed: %q", res.Body)
	}
}

// TestCookieOverride: an explicit Cookie header replaces the jar
// (upstream cookieOverride ?? CookieHeader).
func TestCookieOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.Header.Get("Cookie"))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{}, nil)
	_, _ = c.Do(context.Background(), "t", &Request{URL: srv.URL + "/x"})
	res, err := c.Do(context.Background(), "t", &Request{URL: srv.URL + "/x", Cookies: "manual=1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "manual=1" {
		t.Fatalf("override failed: %q", res.Body)
	}
}

// TestProxySOCKS5Failure: an unreachable socks5 proxy surfaces a network
// error; disabled proxy connects fine.
func TestProxySOCKS5Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "hi")
	}))
	defer srv.Close()

	deadPort := 1 // privileged port closed in tests
	c := newTestClient(t, Config{Proxy: ProxyConfig{Type: ProxySOCKS5, URL: "127.0.0.1", Port: &deadPort}}, nil)
	_, err := c.Do(context.Background(), "t", &Request{URL: srv.URL})
	if err == nil {
		t.Fatal("socks5 to dead port must fail")
	}

	direct := newTestClient(t, Config{Proxy: ProxyConfig{Type: ProxyDisabled}}, nil)
	res, err := direct.Do(context.Background(), "t", &Request{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "hi" {
		t.Fatalf("direct fetch failed: %q", res.Body)
	}
}

// TestCloudflareFlareSolverr: a CF challenge triggers exactly one solver
// call; the replay carries the injected cookie and succeeds.
func TestCloudflareFlareSolverr(t *testing.T) {
	var solverCalls atomic.Int32
	var replayCookie atomic.Value

	solver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		solverCalls.Add(1)
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if req["cmd"] != "request.get" {
			t.Errorf("cmd: %v", req["cmd"])
		}
		_, _ = fmt.Fprint(w, `{
			"status": "ok",
			"solution": {
				"url": "https://tracker.example/",
				"status": 200,
				"userAgent": "FlareSolverrUA/1.0",
				"cookies": [{"name": "cf_clearance", "value": "solved", "domain": "127.0.0.1", "path": "/", "expires": 0}]
			}
		}`)
	}))
	defer solver.Close()

	var challengeHits atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			replayCookie.Store(r.Header.Get("Cookie"))
		}
		if challengeHits.Add(1) == 1 {
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, "<html>__cf_chl_f_tk</html>")
			return
		}
		_, _ = fmt.Fprint(w, "real content")
	}))
	defer site.Close()

	c := newTestClient(t, Config{FlareSolverrURL: solver.URL}, nil)
	res, err := c.Do(context.Background(), "cftracker", &Request{URL: site.URL})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "real content" {
		t.Fatalf("body: %q", res.Body)
	}
	if solverCalls.Load() != 1 {
		t.Fatalf("solver must be called exactly once, got %d", solverCalls.Load())
	}
	if got := replayCookie.Load(); got != "cf_clearance=solved" {
		t.Fatalf("replay must carry the injected cookie: %v", got)
	}
}

// TestCloudflareNoSolver: without FlareSolverr the challenge surfaces as an
// error (mapped to IndexerError{Kind: Auth} by the base wrapper).
func TestCloudflareNoSolver(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, "<html>cf-browser-verification</html>")
	}))
	defer site.Close()

	c := newTestClient(t, Config{}, nil)
	_, err := c.Do(context.Background(), "t", &Request{URL: site.URL})
	var fsErr *ErrFlareSolverrFailed
	if !errors.As(err, &fsErr) {
		t.Fatalf("expected ErrFlareSolverrFailed, got %v", err)
	}
}

// TestTooManyRequests: 429 + Retry-After becomes a typed error.
func TestTooManyRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestClient(t, Config{}, nil)
	_, err := c.Do(context.Background(), "t", &Request{URL: srv.URL})
	var tm *TooManyRequests
	if !errors.As(err, &tm) {
		t.Fatalf("expected TooManyRequests, got %v", err)
	}
	if tm.RetryAfter != 120*time.Second {
		t.Fatalf("RetryAfter: %v", tm.RetryAfter)
	}
}

// TestSiteDown: 521/522/523/502/504 produce ErrSiteDown.
func TestSiteDown(t *testing.T) {
	for _, code := range []int{502, 504, 521, 522, 523} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		c := newTestClient(t, Config{}, nil)
		_, err := c.Do(context.Background(), "t", &Request{URL: srv.URL})
		var sd *ErrSiteDown
		if !errors.As(err, &sd) {
			t.Fatalf("status %d: expected ErrSiteDown, got %v", code, err)
		}
		srv.Close()
	}
}

// TestRetryOnServerError: one 500 then 200 succeeds within the retry budget.
func TestRetryOnServerError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	c := newTestClient(t, Config{RetryAttempts: 2}, nil)
	// shrink the backoff for the test via direct retry loop timing is
	// acceptable: 2s ± jitter — assert success only.
	start := time.Now()
	res, err := c.Do(context.Background(), "t", &Request{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Body) != "ok" {
		t.Fatalf("body: %q", res.Body)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", hits.Load())
	}
	if time.Since(start) < time.Second {
		t.Fatal("retry must wait the backoff delay")
	}
}

// TestHeadersAndDefaults: browser UA, Accept and Accept-Language defaults;
// explicit headers win.
func TestHeadersAndDefaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = fmt.Fprintf(w, "%s|%s|%s|%s|%s",
			r.Header.Get("User-Agent"), r.Header.Get("Accept"),
			r.Header.Get("Accept-Language"), r.Header.Get("Content-Type"), string(body))
	}))
	defer srv.Close()

	c := newTestClient(t, Config{}, nil)
	res, err := c.Do(context.Background(), "t", &Request{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(res.Body), "|")
	if parts[0] != DefaultUserAgent {
		t.Fatalf("default UA: %q", parts[0])
	}
	if parts[1] != "*/*" || parts[2] != "*" {
		t.Fatalf("default accept headers: %v", parts[1:3])
	}

	res, err = c.Do(context.Background(), "t", &Request{
		URL: srv.URL, Headers: map[string]string{"User-Agent": "Custom/9", "Accept": "text/html"},
		Body: map[string][]string{"k": {"v"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	parts = strings.Split(string(res.Body), "|")
	if parts[0] != "Custom/9" || parts[1] != "text/html" {
		t.Fatalf("explicit headers must win: %v", parts[:2])
	}
	if parts[3] != "application/x-www-form-urlencoded" || parts[4] != "k=v" {
		t.Fatalf("form post: %v", parts[3:])
	}
}

// TestFlareSolverrFailure: a solver error response surfaces an error.
func TestFlareSolverrFailure(t *testing.T) {
	solver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"status": "error", "message": "Error: Unable to process browser action. Timeout"}`)
	}))
	defer solver.Close()

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, "__cf_chl")
	}))
	defer site.Close()

	c := newTestClient(t, Config{FlareSolverrURL: solver.URL}, nil)
	_, err := c.Do(context.Background(), "t", &Request{URL: site.URL})
	var fsErr *ErrFlareSolverrFailed
	if !errors.As(err, &fsErr) {
		t.Fatalf("expected ErrFlareSolverrFailed, got %v", err)
	}
}
