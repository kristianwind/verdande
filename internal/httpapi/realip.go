package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/kristianwind/verdande/internal/config"
)

// realIP rewrites r.RemoteAddr to the client address a trusted proxy reported,
// and does nothing at all when the peer is not trusted.
//
// This replaces chi's middleware.RealIP, which is deprecated precisely because it
// believes a forwarded header from anyone: an attacker sets X-Forwarded-For to a
// new value on every request and gets a fresh rate-limit bucket each time, and
// writes whatever address they like into the audit log. The one control that
// exists for when a password is already gone is keyed on this address, so it has
// to be an address the caller cannot choose.
//
// The gate is the peer. A header is read only when the machine that opened the
// connection is one of cfg.TrustedProxies — a reverse proxy or tunnel the operator
// put there. A connection straight off the internet is trusted for nothing, so its
// header is ignored and it is keyed on the address it actually came from. Behind a
// proxy that overwrites the client's own header (nginx, Caddy and cloudflared all
// do) the value is then the real client and cannot be forged from outside.
func realIP(cfg *config.Config) func(http.Handler) http.Handler {
	trusted := cfg.TrustedProxies
	header := cfg.RealIPHeader
	if header == "" {
		header = "X-Forwarded-For"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ip := trustedClientIP(r, trusted, header); ip != "" {
				// Port preserved off the original peer only for shape; downstream
				// clientIP() splits it away, and the forwarded value carries none.
				r.RemoteAddr = ip
			}
			next.ServeHTTP(w, r)
		})
	}
}

// trustedClientIP returns the forwarded client address if the peer may be believed,
// or "" to leave RemoteAddr untouched.
func trustedClientIP(r *http.Request, trusted []netip.Prefix, header string) string {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	peerAddr, err := netip.ParseAddr(peer)
	if err != nil || !isTrusted(peerAddr, trusted) {
		return ""
	}

	raw := r.Header.Get(header)
	if raw == "" {
		return ""
	}
	// The leftmost entry is the original client. That entry is only as trustworthy
	// as the proxy that wrote it — but the trusted proxy is exactly the one that
	// replaces a client-supplied header, so on the deployments this is written for
	// it is the real client, and on a direct connection we never reach this line.
	first := raw
	if i := strings.IndexByte(raw, ','); i >= 0 {
		first = raw[:i]
	}
	first = strings.TrimSpace(first)
	if _, err := netip.ParseAddr(first); err != nil {
		return ""
	}
	return first
}

func isTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
