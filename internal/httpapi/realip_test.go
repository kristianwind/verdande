package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kristianwind/verdande/internal/config"
)

// The address the rate limiter keys on must be one the caller cannot choose. A
// forwarded header is believed only from a trusted proxy, and ignored from a
// stranger connecting directly — the difference between a control and a suggestion.
func TestRealIPTrustsOnlyTheProxy(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	seen := ""
	h := realIP(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = clientIP(r)
	}))

	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{
			// A private peer is a proxy the operator put there: believe it.
			name:       "trusted proxy is believed",
			remoteAddr: "10.1.2.3:5000",
			xff:        "203.0.113.9",
			want:       "203.0.113.9",
		},
		{
			// A public peer connected straight to us; its header is a guess.
			name:       "public peer header ignored",
			remoteAddr: "198.51.100.7:5000",
			xff:        "203.0.113.9",
			want:       "198.51.100.7",
		},
		{
			// No header from a trusted proxy leaves the peer as the answer.
			name:       "trusted peer without header",
			remoteAddr: "127.0.0.1:5000",
			xff:        "",
			want:       "127.0.0.1",
		},
		{
			// A spoofed value from a stranger cannot become the key.
			name:       "spoof from outside does not win",
			remoteAddr: "198.51.100.7:5000",
			xff:        "127.0.0.1",
			want:       "198.51.100.7",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			h.ServeHTTP(httptest.NewRecorder(), r)
			if seen != tc.want {
				t.Errorf("keyed on %q, want %q", seen, tc.want)
			}
		})
	}
}

// "none" turns the trust off entirely, for an instance exposed directly.
func TestRealIPTrustNone(t *testing.T) {
	t.Setenv("VERDANDE_TRUSTED_PROXIES", "none")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := ""
	h := realIP(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = clientIP(r)
	}))
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "10.1.2.3:5000" // even a private peer is not trusted now
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "10.1.2.3" {
		t.Errorf("keyed on %q, want the peer 10.1.2.3", seen)
	}
}
