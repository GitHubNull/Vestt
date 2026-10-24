package httpclient

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// buildTransport constructs the shared transport with the configured proxy
// (SPEC-04 §3.2 proxy.go: http/socks4/socks5).
func buildTransport(cfg ProxyConfig, connectTimeout time.Duration) (*http.Transport, error) {
	dialer := &net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}

	t := &http.Transport{
		DialContext:           dialer.DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   connectTimeout,
		ResponseHeaderTimeout: 0,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// upstream WebClient.InitProxy: disabled or empty proxy URL → direct
	if cfg.Type == ProxyDisabled || strings.TrimSpace(cfg.URL) == "" {
		return t, nil
	}

	switch cfg.Type {
	case ProxyDisabled:
		return t, nil
	case ProxyHTTP:
		proxyURL := &url.URL{Scheme: "http", Host: proxyHost(cfg)}
		if cfg.Username != "" || cfg.Password != "" {
			proxyURL.User = url.UserPassword(cfg.Username, cfg.Password)
		}
		t.Proxy = http.ProxyURL(proxyURL)
		return t, nil
	case ProxySOCKS5, ProxySOCKS4:
		auth := &socksAuth{username: cfg.Username, password: cfg.Password}
		var dial func(ctx context.Context, network, addr string) (net.Conn, error)
		if cfg.Type == ProxySOCKS5 {
			dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialSOCKS5(ctx, proxyHost(cfg), addr, auth)
			}
		} else {
			dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialSOCKS4(ctx, proxyHost(cfg), addr, auth)
			}
		}
		t.DialContext = dial
		return t, nil
	}
	return nil, fmt.Errorf("Proxy type '%d' is not implemented!", int(cfg.Type))
}

func proxyHost(cfg ProxyConfig) string {
	if cfg.Port != nil {
		return net.JoinHostPort(cfg.URL, strconv.Itoa(*cfg.Port))
	}
	return cfg.URL
}

type socksAuth struct {
	username, password string
}

// dialSOCKS5 is a minimal SOCKS5 client (RFC1928, no UDP, no BIND).
func dialSOCKS5(ctx context.Context, proxyAddr, target string, auth *socksAuth) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		conn.Close()
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		conn.Close()
		return nil, err
	}

	// greeting
	methods := []byte{0x00} // no auth
	if auth.username != "" || auth.password != "" {
		methods = append(methods, 0x02)
	}
	greeting := append([]byte{0x05, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		conn.Close()
		return nil, err
	}
	resp := make([]byte, 2)
	if _, err := readFull(conn, resp); err != nil {
		conn.Close()
		return nil, err
	}
	if resp[0] != 0x05 {
		conn.Close()
		return nil, errors.New("socks5: invalid version")
	}
	switch resp[1] {
	case 0x00:
	case 0x02:
		if auth.username == "" && auth.password == "" {
			conn.Close()
			return nil, errors.New("socks5: server requires auth but none configured")
		}
		ulen := len(auth.username)
		plen := len(auth.password)
		buf := make([]byte, 0, 3+ulen+plen)
		buf = append(buf, 0x01, byte(ulen))
		buf = append(buf, auth.username...)
		buf = append(buf, byte(plen))
		buf = append(buf, auth.password...)
		if _, err := conn.Write(buf); err != nil {
			conn.Close()
			return nil, err
		}
		if _, err := readFull(conn, resp); err != nil {
			conn.Close()
			return nil, err
		}
		if resp[1] != 0x00 {
			conn.Close()
			return nil, errors.New("socks5: authentication failed")
		}
	default:
		conn.Close()
		return nil, fmt.Errorf("socks5: unsupported auth method 0x%02x", resp[1])
	}

	// connect request
	ip := net.ParseIP(host)
	var addr []byte
	if ip4 := ip.To4(); ip4 != nil {
		addr = append([]byte{0x01}, ip4...)
	} else if ip16 := ip.To16(); ip16 != nil {
		addr = append([]byte{0x04}, ip16...)
	} else {
		if len(host) > 255 {
			conn.Close()
			return nil, errors.New("socks5: host too long")
		}
		addr = append([]byte{0x03, byte(len(host))}, host...)
	}
	req := append([]byte{0x05, 0x01, 0x00}, addr...)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, err
	}
	head := make([]byte, 4)
	if _, err := readFull(conn, head); err != nil {
		conn.Close()
		return nil, err
	}
	if head[0] != 0x05 || head[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("socks5: connect failed (code 0x%02x)", head[1])
	}
	// discard the bound address
	switch head[3] {
	case 0x01:
		if _, err := readFull(conn, make([]byte, 4+2)); err != nil {
			conn.Close()
			return nil, err
		}
	case 0x03:
		l := make([]byte, 1)
		if _, err := readFull(conn, l); err != nil {
			conn.Close()
			return nil, err
		}
		if _, err := readFull(conn, make([]byte, int(l[0])+2)); err != nil {
			conn.Close()
			return nil, err
		}
	case 0x04:
		if _, err := readFull(conn, make([]byte, 16+2)); err != nil {
			conn.Close()
			return nil, err
		}
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// dialSOCKS4 is a minimal SOCKS4a client (RFC1928 §2: 4a resolves remote
// hostnames, matching upstream SocksWebProxy behaviour).
func dialSOCKS4(ctx context.Context, proxyAddr, target string, auth *socksAuth) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		conn.Close()
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		conn.Close()
		return nil, err
	}

	ip := net.ParseIP(host)
	var ipBytes []byte
	user := auth.username
	if ip4 := ip.To4(); ip4 != nil {
		ipBytes = ip4
	} else {
		// SOCKS4a: 0.0.0.x + hostname
		ipBytes = []byte{0, 0, 0, 1}
		user = user + "\x00" + host
	}

	req := []byte{0x04, 0x01}
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	req = append(req, ipBytes...)
	req = append(req, user...)
	req = append(req, 0x00)
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, err
	}

	resp := make([]byte, 8)
	if _, err := readFull(conn, resp); err != nil {
		conn.Close()
		return nil, err
	}
	if resp[1] != 0x5a {
		conn.Close()
		return nil, fmt.Errorf("socks4: connect failed (code 0x%02x)", resp[1])
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
