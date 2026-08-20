// Package safedial refuses to open a connection to this machine or to the
// network it sits on.
//
// The problem it solves is the same one three times: somewhere a user supplies a
// URL or a host, and the server fetches it. An AI provider's base address, a push
// endpoint, an IMAP host, a beacon collector. Each of them is a feature, and each
// of them is also a way to ask the server to reach something the caller cannot —
// the panel next door, a metadata endpoint, a database on the same bridge — and
// to report back whether it answered. On a homelab that is the whole point of the
// box being where it is.
//
// # Why the check is on the address and not on the URL
//
// Parsing the URL and refusing "localhost" or "127.0.0.1" is the obvious version
// and it does not work: a name resolves to whatever its owner says, so
// `evil.example.com` can answer 127.0.0.1 — and can answer differently the second
// time, after a parse-time check has passed. That is DNS rebinding, and the only
// place it can be caught is after resolution and before the connection, which is
// exactly where net.Dialer's Control hook runs.
//
// # What is refused
//
// Loopback, link-local (including 169.254.169.254, which is where cloud metadata
// lives), the private ranges, unique-local IPv6, and the unspecified address.
// Everything else is allowed: this is not an allowlist of the internet, it is a
// wall around the inside.
package safedial

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// ErrPrivate is returned when a connection was refused for where it pointed. It
// deliberately does not say which address it resolved to: the answer is itself
// the thing an attacker is fishing for.
type ErrPrivate struct{ Host string }

func (e *ErrPrivate) Error() string {
	return fmt.Sprintf("refusing to connect to %s: it resolves to an address on this machine or its private network", e.Host)
}

// Blocked reports whether an address must not be dialled.
func Blocked(ip net.IP) bool {
	return ip == nil ||
		ip.IsLoopback() ||
		ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsPrivate()
}

// Control is a net.Dialer Control function that refuses a private destination.
//
// It runs after the name has been resolved and before the socket is connected,
// which is the only moment where the address that will actually be used is known.
func Control(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if Blocked(ip) {
		return &ErrPrivate{Host: host}
	}
	return nil
}

// Dialer is a net.Dialer that will not reach inside.
func Dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{Timeout: timeout, Control: Control}
}

// Client is an http.Client for fetching something a user named.
//
// Redirects are followed but capped, and every hop goes through the same dialer —
// a redirect to 127.0.0.1 is the oldest way around a check that only looked at the
// URL somebody typed.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           Dialer(timeout).DialContext,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}

// AllowPrivate is the same client without the wall, for a destination the
// operator configured rather than a user — the panel, and nothing else. Named so
// that using it is a decision somebody wrote down.
func AllowPrivate(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// CheckURL reports why an address must not be fetched, or "" if it may be.
//
// A convenience for saying no at the moment somebody types an address, rather
// than later when a request quietly fails. It is NOT the protection — a name can
// resolve one way now and another way when it is dialled, which is why Control
// exists and runs every time. Anything this rejects is certainly wrong; anything
// it accepts is merely not obviously wrong.
func CheckURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "that is not a URL"
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil && Blocked(ip) {
		return "that address is on this machine or its private network"
	}
	// A bare name is looked up once so an obvious "localhost" is caught here. A
	// failure to resolve is not an error: the host may simply not exist yet, and
	// refusing to save an address because DNS was slow would be its own bug.
	if ips, err := net.LookupIP(host); err == nil {
		for _, ip := range ips {
			if Blocked(ip) {
				return "that address is on this machine or its private network"
			}
		}
	}
	return ""
}
