// Package httpclient is the per-indexer HTTP request stack: rate limiting,
// cookie persistence, proxy, Cloudflare challenge handling and FlareSolverr.
// Behaviour is ported from Jackett.Common/Utils/Clients/WebClient.cs +
// BaseWebIndexer.cs retry policy (SPEC-04 §3.2).
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"vestt/common/utils"
)

// DefaultUserAgent mirrors BrowserUtil.ChromeUserAgent (Linux variant).
const DefaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36"

const (
	defaultConnectTimeout = 30 * time.Second
	defaultTotalTimeout   = 100 * time.Second
	// upstream BaseWebIndexer retry policy: 2 attempts, 2s base delay,
	// exponential with jitter.
	defaultRetryAttempts = 2
	retryBaseDelay       = 2 * time.Second
	maxRedirects         = 5
	// maxBodyBytes bounds a single response body (upstream reads fully;
	// Jackett pages are HTML, this only guards runaway responses).
	maxBodyBytes = 64 << 20
)

// ProxyType mirrors upstream ProxyType enum.
type ProxyType int

const (
	ProxyDisabled ProxyType = -1
	ProxyHTTP     ProxyType = 0
	ProxySOCKS4   ProxyType = 1
	ProxySOCKS5   ProxyType = 2
)

// ProxyConfig mirrors the server proxy configuration subset.
type ProxyConfig struct {
	Type     ProxyType
	URL      string // host or host:port (no scheme)
	Port     *int
	Username string
	Password string
}

// Config is the client stack configuration.
type Config struct {
	UserAgent              string
	RequestDelay           time.Duration // default per-indexer minimum interval
	TestRequestDelay       time.Duration // interval for IsTest requests (SPEC-04 §3.2)
	ConnectTimeout         time.Duration
	TotalTimeout           time.Duration
	RetryAttempts          int
	Proxy                  ProxyConfig
	FlareSolverrURL        string
	FlareSolverrMaxTimeout int // milliseconds
}

// Bool returns a bool pointer (for the tri-state EmulateBrowser field).
func Bool(v bool) *bool { return &v }

// PersistedCookie is the serializable form of a jar cookie
// (data-model.md §2.4 cookies_json).
type PersistedCookie struct {
	Name     string     `json:"name"`
	Domain   string     `json:"domain"`
	Value    string     `json:"value"`
	Path     string     `json:"path"`
	Expires  *time.Time `json:"expires,omitempty"`
	Secure   bool       `json:"secure,omitempty"`
	HttpOnly bool       `json:"http_only,omitempty"`
}

// CookieSaver persists a per-indexer cookie jar; wired to the store layer
// by the assembly (SPEC-04 §3.2 cookiejar.go).
type CookieSaver func(indexerID string, cookies []PersistedCookie)

// Request mirrors upstream WebRequest.
type Request struct {
	Method  string // GET (default) or POST
	URL     string
	Headers map[string]string
	Cookies string // raw Cookie header; overrides the jar when non-empty
	Body    url.Values
	RawBody string
	Referer string
	// EmulateBrowser mirrors the upstream nullable flag: nil = emulate
	// (client default true); explicit false sends no browser UA.
	EmulateBrowser *bool
	// IsTest marks requests issued by indexer self-tests: they use the
	// shorter TestRequestDelay interval (SPEC-04 §3.2).
	IsTest bool
}

// Result mirrors upstream WebResult (body + headers + status).
type Result struct {
	Body       []byte
	Headers    http.Header
	StatusCode int
}

// indexerState holds the per-indexer limiter, cookie jar and UA override.
type indexerState struct {
	limiter   *limiter
	jar       *jar
	userAgent string // FlareSolverr-provided override; empty = default
}

// Client is the stack entry point.
type Client struct {
	cfg    Config
	logger *slog.Logger

	hc *http.Client

	mu     sync.Mutex
	states map[string]*indexerState
	saver  CookieSaver
}

// New builds the stack. saver may be nil (cookies then live in memory only).
func New(cfg Config, saver CookieSaver, logger *slog.Logger) (*Client, error) {
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.TotalTimeout == 0 {
		cfg.TotalTimeout = defaultTotalTimeout
	}
	if cfg.RetryAttempts == 0 {
		cfg.RetryAttempts = defaultRetryAttempts
	}
	if cfg.FlareSolverrMaxTimeout == 0 {
		cfg.FlareSolverrMaxTimeout = 60000
	}

	transport, err := buildTransport(cfg.Proxy, cfg.ConnectTimeout)
	if err != nil {
		return nil, err
	}

	hc := &http.Client{
		Transport: transport,
		Timeout:   cfg.TotalTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("stopped after 5 redirects")
			}
			return nil
		},
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &Client{
		cfg:    cfg,
		logger: logger,
		hc:     hc,
		states: map[string]*indexerState{},
		saver:  saver,
	}, nil
}

// state returns (creating on demand) the per-indexer state.
func (c *Client) state(indexerID string) *indexerState {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.states[indexerID]
	if !ok {
		st = &indexerState{
			limiter: newLimiter(c.testDelay, func() time.Duration { return c.cfg.RequestDelay }),
			jar:     newJar(),
		}
		c.states[indexerID] = st
	}
	return st
}

func (c *Client) testDelay() time.Duration { return c.cfg.TestRequestDelay }

// SetRequestDelay sets the minimum request interval for one indexer
// (upstream requestDelay per indexer).
func (c *Client) SetRequestDelay(indexerID string, d time.Duration) {
	st := c.state(indexerID)
	st.limiter.setDelay(func() time.Duration { return d })
}

// SetUserAgent overrides the User-Agent for one indexer (FlareSolverr).
func (c *Client) SetUserAgent(indexerID, ua string) {
	c.state(indexerID).userAgent = ua
}

// ErrSiteDown mirrors upstream CheckSiteDown: 502/504/521/522/523.
type ErrSiteDown struct {
	URL    string
	Status int
}

func (e *ErrSiteDown) Error() string {
	return fmt.Sprintf("Request to %s failed (Error %d) - The tracker seems to be down.", e.URL, e.Status)
}

// ErrFlareSolverrFailed is returned when a Cloudflare challenge could not
// be solved (no FlareSolverr configured or solver error).
type ErrFlareSolverrFailed struct{ Message string }

func (e *ErrFlareSolverrFailed) Error() string { return e.Message }

// TooManyRequests mirrors upstream TooManyRequestsException.
type TooManyRequests struct {
	Message    string
	RetryAfter time.Duration
}

func (e *TooManyRequests) Error() string { return e.Message }

// Do executes one request through the full stack (rate limit → send →
// cookies → cloudflare → retries).
func (c *Client) Do(ctx context.Context, indexerID string, req *Request) (*Result, error) {
	st := c.state(indexerID)

	var lastErr error
	var lastRes *Result
	attempts := c.cfg.RetryAttempts + 1
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := retryBaseDelay << (attempt - 1)
			// upstream Polly UseJitter: factor in [0.5, 1.5)
			delay = time.Duration(float64(delay) * (0.5 + rand.Float64()))
			c.logger.Warn(fmt.Sprintf("Request to %s failed with error '%v'. Retrying in %vs.", indexerID, lastErr, delay.Seconds()))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		res, retriable, err := c.doOnce(ctx, indexerID, st, req)
		if err == nil {
			if !retriable {
				return res, nil
			}
			// server-error status: retriable per upstream
			// { Result: { HasHttpServerError: true } } predicate
			lastRes = res
			lastErr = fmt.Errorf("Remote server returned %d", res.StatusCode) //nolint:staticcheck // 上游文案 BaseIndexer.cs:569
			continue
		}
		lastErr = err
		if !retriable {
			return nil, err
		}
	}
	if lastRes != nil {
		// retries exhausted on a server error: upstream Polly returns the
		// last (unsuccessful) result rather than throwing
		return lastRes, nil
	}
	return nil, lastErr
}

// doOnce runs one attempt including rate limit, cookies and CF handling.
func (c *Client) doOnce(ctx context.Context, indexerID string, st *indexerState, req *Request) (*Result, bool, error) {
	st.limiter.wait(ctx, req.IsTest)

	httpReq, err := c.buildHTTPRequest(indexerID, st, req)
	if err != nil {
		return nil, false, err
	}
	httpReq = httpReq.WithContext(ctx)

	c.logger.Debug(fmt.Sprintf("WebClient.GetResultAsync(Method: %s Url: %s)", httpReq.Method, utils.Sanitize(httpReq.URL.String())))

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		// network failures are retriable (upstream { Exception: {} } predicate)
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, true, err
	}

	// feed response cookies into the per-indexer jar and persist
	if cookies := resp.Cookies(); len(cookies) > 0 {
		st.jar.setCookies(resp.Request.URL, cookies)
		if c.saver != nil {
			c.saver(indexerID, st.jar.snapshot())
		}
	}

	result := &Result{Body: body, Headers: resp.Header, StatusCode: resp.StatusCode}

	if isSiteDown(resp.StatusCode) {
		return nil, false, &ErrSiteDown{URL: req.URL, Status: resp.StatusCode}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, false, &TooManyRequests{
			Message:    fmt.Sprintf("Remote server returned %d", resp.StatusCode),
			RetryAfter: parseRetryAfter(resp.Header),
		}
	}

	if isCloudflareChallenge(resp, body) {
		solved, err := c.solveChallenge(ctx, indexerID, st, req.URL)
		if err != nil {
			return nil, false, err
		}
		if !solved {
			return nil, false, &ErrFlareSolverrFailed{
				Message: fmt.Sprintf("Remote server returned Cloudflare challenge for %s", req.URL),
			}
		}
		// replay the original request once with injected credentials
		return c.doOnce(ctx, indexerID, st, req)
	}

	return result, isRetriableStatus(resp.StatusCode), nil
}

func isSiteDown(status int) bool {
	switch status {
	case http.StatusBadGateway, http.StatusGatewayTimeout, 521, 522, 523:
		return true
	}
	return false
}

func isRetriableStatus(status int) bool {
	if status == http.StatusRequestTimeout {
		return true
	}
	return status >= 500 && status <= 599
}

// buildHTTPRequest assembles the net/http request: headers, cookies, body.
func (c *Client) buildHTTPRequest(indexerID string, st *indexerState, req *Request) (*http.Request, error) {
	var bodyReader *strings.Reader
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	if len(req.Body) > 0 {
		encoded := req.Body.Encode()
		if method == http.MethodGet {
			method = http.MethodPost
		}
		bodyReader = strings.NewReader(encoded)
	} else if req.RawBody != "" {
		bodyReader = strings.NewReader(req.RawBody)
	} else {
		bodyReader = strings.NewReader("")
	}

	httpReq, err := http.NewRequest(method, req.URL, bodyReader)
	if err != nil {
		return nil, err
	}

	// headers: caller headers win; upstream PrepareRequest adds
	// Accept/Accept-Language only when missing.
	headers := map[string]string{}
	for k, v := range req.Headers {
		headers[strings.ToLower(k)] = v
	}
	if _, ok := headers["accept"]; !ok {
		headers["accept"] = "*/*"
	}
	if _, ok := headers["accept-language"]; !ok {
		headers["accept-language"] = "*"
	}
	if req.Referer != "" {
		headers["referer"] = req.Referer
	}
	ua := st.userAgent
	if ua == "" {
		ua = c.cfg.UserAgent
	}
	_, hasUA := headers["user-agent"]
	if !hasUA && (req.EmulateBrowser == nil || *req.EmulateBrowser) {
		headers["user-agent"] = ua
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	if len(req.Body) > 0 {
		httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	// cookies: explicit override wins (upstream cookieOverride ?? CookieHeader)
	if req.Cookies != "" {
		httpReq.Header.Set("Cookie", req.Cookies)
	} else if u, err := url.Parse(req.URL); err == nil {
		jarCookies := st.jar.cookiesFor(u)
		if len(jarCookies) > 0 {
			parts := make([]string, 0, len(jarCookies))
			for _, ck := range jarCookies {
				parts = append(parts, ck.Name+"="+ck.Value)
			}
			httpReq.Header.Set("Cookie", strings.Join(parts, "; "))
		}
	}

	return httpReq, nil
}

// parseRetryAfter mirrors upstream: integer seconds, HTTP-date, else 0.
func parseRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t)
	}
	return 0
}

// LoadCookies seeds a per-indexer jar from the persisted store.
func (c *Client) LoadCookies(indexerID string, cookies []PersistedCookie) {
	c.state(indexerID).jar.load(cookies)
}

// Cookies returns the persisted snapshot of an indexer jar.
func (c *Client) Cookies(indexerID string) []PersistedCookie {
	return c.state(indexerID).jar.snapshot()
}

// CookieHeader returns the jar's cookies for u in Cookie-header form
// ("k=v; k2=v2"). Cardigann login stores this string into the CookieHeader
// config item (upstream WebResult.Cookies semantics).
func (c *Client) CookieHeader(indexerID string, u *url.URL) string {
	cookies := c.state(indexerID).jar.cookiesFor(u)
	parts := make([]string, 0, len(cookies))
	for _, ck := range cookies {
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	return strings.Join(parts, "; ")
}
