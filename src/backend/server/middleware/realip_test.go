package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func serveRemote(t *testing.T, remote string, xff []string) string {
	t.Helper()
	var got string
	h := RealIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remote
	for _, v := range xff {
		req.Header.Add("X-Forwarded-For", v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestRealIPTrustedProxy(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		xff    []string
		want   string
	}{
		{"public peer spoof blocked", "9.9.9.9:5000", []string{"1.2.3.4"}, "9.9.9.9:5000"},
		{"no header passthrough", "127.0.0.1:45001", nil, "127.0.0.1:45001"},
		{"loopback single hop", "127.0.0.1:45001", []string{"1.2.3.4"}, "1.2.3.4:45001"},
		{"private proxy hop", "192.168.1.50:5000", []string{"1.2.3.4"}, "1.2.3.4:5000"},
		{"v6 link-local proxy", "[fe80::1]:5000", []string{"1.2.3.4"}, "1.2.3.4:5000"},
		{"v4 link-local not trusted", "169.254.1.1:5000", []string{"1.2.3.4"}, "169.254.1.1:5000"},
		{"mapped v4 loopback", "[::ffff:127.0.0.1]:45001", []string{"1.2.3.4"}, "1.2.3.4:45001"},
		{"walks past trusted chain", "127.0.0.1:45001", []string{"1.2.3.4, 10.0.0.9"}, "1.2.3.4:45001"},
		{"stops at untrusted hop", "127.0.0.1:45001", []string{"1.2.3.4, 9.9.9.9"}, "9.9.9.9:45001"},
		{"malformed entry stops walk", "127.0.0.1:45001", []string{"1.2.3.4, garbage"}, "127.0.0.1:45001"},
		{"multiple headers joined", "127.0.0.1:45001", []string{"1.2.3.4", " 10.0.0.9"}, "1.2.3.4:45001"},
		{"empty entry stops walk", "127.0.0.1:45001", []string{"1.2.3.4,", " 10.0.0.9"}, "10.0.0.9:45001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serveRemote(t, tc.remote, tc.xff); got != tc.want {
				t.Fatalf("RemoteAddr = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRealIPForwardLimit(t *testing.T) {
	// 12 trusted hops: only ForwardLimit (10) entries are unwrapped, so the
	// rightmost 10 are consumed and the result is the 3rd entry from the left.
	xff := "10.0.0.1,10.0.0.2,10.0.0.3,10.0.0.4,10.0.0.5,10.0.0.6,10.0.0.7,10.0.0.8,10.0.0.9,10.0.0.10,10.0.0.11,10.0.0.12"
	if got := serveRemote(t, "127.0.0.1:45001", []string{xff}); got != "10.0.0.3:45001" {
		t.Fatalf("RemoteAddr = %q, want 10.0.0.3:45001", got)
	}
}
