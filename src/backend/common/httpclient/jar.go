package httpclient

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// jarEntry is one stored cookie.
type jarEntry struct {
	name     string
	value    string
	domain   string // leading-dot form marks domain cookies
	path     string
	expires  time.Time // zero = session cookie
	secure   bool
	httpOnly bool
}

// jar is a minimal RFC6265-ish cookie jar with a snapshot capability
// (net/http/cookiejar does not expose its entries for persistence).
type jar struct {
	mu      sync.Mutex
	entries []jarEntry
}

func newJar() *jar { return &jar{} }

func defaultPath(u *url.URL) string {
	if u.Path == "" || !strings.HasPrefix(u.Path, "/") {
		return "/"
	}
	i := strings.LastIndex(u.Path, "/")
	if i == 0 {
		return "/"
	}
	return u.Path[:i]
}

// setCookies mirrors net/http cookie storage: domain default from the
// request host, path default from the request path; expired cookies are
// removed.
func (j *jar) setCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cookies {
		host := u.Hostname()
		domain := c.Domain
		if domain == "" {
			domain = host
		} else {
			domain = strings.TrimPrefix(strings.ToLower(domain), ".")
			if domain != host && !strings.HasSuffix(host, "."+domain) {
				continue // foreign domain
			}
		}
		path := c.Path
		if path == "" {
			path = defaultPath(u)
		}
		// expiry: Max-Age wins over Expires
		expires := time.Time{}
		if c.MaxAge < 0 {
			j.removeLocked(domain, path, c.Name)
			continue
		}
		if c.MaxAge > 0 {
			expires = time.Now().Add(time.Duration(c.MaxAge) * time.Second)
		} else if !c.Expires.IsZero() {
			expires = c.Expires
		}
		if !expires.IsZero() && time.Now().After(expires) {
			j.removeLocked(domain, path, c.Name)
			continue
		}
		j.removeLocked(domain, path, c.Name)
		j.entries = append(j.entries, jarEntry{
			name:     c.Name,
			value:    c.Value,
			domain:   domain,
			path:     path,
			expires:  expires,
			secure:   c.Secure,
			httpOnly: c.HttpOnly,
		})
	}
}

func (j *jar) removeLocked(domain, path, name string) {
	out := j.entries[:0]
	for _, e := range j.entries {
		if e.domain == domain && e.path == path && e.name == name {
			continue
		}
		out = append(out, e)
	}
	j.entries = out
}

// cookiesFor returns matching cookies for a request URL.
func (j *jar) cookiesFor(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	host := u.Hostname()
	reqPath := u.Path
	if reqPath == "" {
		reqPath = "/" // origin-form requests use "/" for the root path
	}
	now := time.Now()
	out := make([]*http.Cookie, 0, len(j.entries))
	for _, e := range j.entries {
		if e.domain != host && !strings.HasSuffix(host, "."+e.domain) {
			continue
		}
		if !strings.HasPrefix(reqPath, e.path) {
			continue
		}
		if e.secure && u.Scheme != "https" {
			continue
		}
		if !e.expires.IsZero() && now.After(e.expires) {
			continue
		}
		out = append(out, &http.Cookie{Name: e.name, Value: e.value})
	}
	// longest path first (RFC6265 §5.4 ordering)
	sort.SliceStable(out, func(i, j2 int) bool { return len(out[i].Value) < len(out[j2].Value) })
	return out
}

// snapshot returns the persisted shape of all non-expired cookies.
func (j *jar) snapshot() []PersistedCookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	out := make([]PersistedCookie, 0, len(j.entries))
	for _, e := range j.entries {
		if !e.expires.IsZero() && now.After(e.expires) {
			continue
		}
		pc := PersistedCookie{
			Name:     e.name,
			Domain:   e.domain,
			Value:    e.value,
			Path:     e.path,
			Secure:   e.secure,
			HttpOnly: e.httpOnly,
		}
		if !e.expires.IsZero() {
			t := e.expires.UTC()
			pc.Expires = &t
		}
		out = append(out, pc)
	}
	return out
}

// load seeds the jar from the persisted shape.
func (j *jar) load(cookies []PersistedCookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, pc := range cookies {
		e := jarEntry{
			name:     pc.Name,
			value:    pc.Value,
			domain:   strings.TrimPrefix(strings.ToLower(pc.Domain), "."),
			path:     pc.Path,
			secure:   pc.Secure,
			httpOnly: pc.HttpOnly,
		}
		if e.domain == "" {
			continue
		}
		if e.path == "" {
			e.path = "/"
		}
		if pc.Expires != nil {
			e.expires = *pc.Expires
		}
		if !e.expires.IsZero() && time.Now().After(e.expires) {
			continue
		}
		j.entries = append(j.entries, e)
	}
}
