package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Ported from: Jackett.Server/Startup.cs L61-75 (ForwardedHeadersOptions).
// X-Forwarded-For is honored only while the chain of forwarding peers stays
// inside loopback or the private networks upstream trusts (10/8, 172.16/12,
// 192.168/16, fc00::/7, fe80::/10 — the v4 link-local range 169.254/16 is
// deliberately absent there); at most ForwardLimit (10) entries are unwrapped.
// chi's deprecated chimiddleware.RealIP trusted every caller unconditionally.
const forwardedLimit = 10

// RealIP rewrites r.RemoteAddr from X-Forwarded-For when the direct peer is a
// trusted proxy. Entries are consumed right-to-left (each entry is trusted
// only if the peer that appended it was trusted), matching ASP.NET Core's
// ForwardedHeaders walk. Untrusted or malformed input leaves RemoteAddr as-is.
func RealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host, port, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			if peer, err := netip.ParseAddr(host); err == nil && trustedProxy(peer.Unmap()) {
				r.RemoteAddr = net.JoinHostPort(forwardedFor(r), port)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func forwardedFor(r *http.Request) string {
	peer := remoteHost(r.RemoteAddr)
	entries := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	result := peer
	for i, count := len(entries)-1, 0; i >= 0 && count < forwardedLimit; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(entries[i]))
		if err != nil {
			break
		}
		ip = ip.Unmap()
		result = ip
		count++
		if !trustedProxy(ip) {
			break
		}
	}
	return result.String()
}

func remoteHost(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return netip.Addr{}
	}
	addr, _ := netip.ParseAddr(host)
	return addr.Unmap()
}

func trustedProxy(a netip.Addr) bool {
	return a.IsValid() && (a.IsLoopback() || a.IsPrivate() || (a.Is6() && a.IsLinkLocalUnicast()))
}
