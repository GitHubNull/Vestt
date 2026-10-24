package utils

import "testing"

func TestSanitize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"query apikey", "GET /torznab?q=x&apikey=secret123", "GET /torznab?q=x&apikey=***"},
		{"query password", "password=hunter2&user=bob", "password=***&user=bob"},
		{"query cookieheader", "cookieheader=uid=1;pass=abc", "cookieheader=***"},
		{"json api_key", `{"api_key":"abc","port":9117}`, `{"api_key":"***","port":9117}`},
		{"json password", `{"password":"topsecret"}`, `{"password":"***"}`},
		{"case insensitive", "APIKEY=xyz", "APIKEY=***"},
		{"passkey alias", "passkey=deadbeef", "passkey=***"},
		{"untouched", "search completed in 12ms", "search completed in 12ms"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in); got != tc.want {
				t.Fatalf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
