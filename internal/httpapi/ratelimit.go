package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// limiter is a fixed-window counter, per key, held in memory.
//
// In memory is the right scope here: verdande is one process with one SQLite file
// beneath it, so there is no second instance for a shared counter to coordinate
// with. Resetting the limits on restart is a real weakness, and an acceptable one —
// this exists to make online password guessing impractical, not to survive a
// determined attacker who can also restart the server.
type limiter struct {
	mu      sync.Mutex
	windows map[string]*window
	limit   int
	period  time.Duration
}

type window struct {
	count int
	until time.Time
}

func newLimiter(limit int, period time.Duration) *limiter {
	l := &limiter{windows: map[string]*window{}, limit: limit, period: period}
	go l.sweep()
	return l
}

// allow records an attempt and reports whether it may proceed, with how long to
// wait if not.
func (l *limiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	w, ok := l.windows[key]
	if !ok || now.After(w.until) {
		l.windows[key] = &window{count: 1, until: now.Add(l.period)}
		return true, 0
	}
	if w.count >= l.limit {
		return false, time.Until(w.until)
	}
	w.count++
	return true, 0
}

// reset clears a key. Called after a successful login, so somebody who mistyped
// their password four times is not still rate-limited once they get it right.
func (l *limiter) reset(key string) {
	l.mu.Lock()
	delete(l.windows, key)
	l.mu.Unlock()
}

// sweep drops expired windows. Without it the map is a slow memory leak keyed by
// every address that ever failed a login.
func (l *limiter) sweep() {
	for range time.Tick(5 * time.Minute) {
		l.mu.Lock()
		now := time.Now()
		for k, w := range l.windows {
			if now.After(w.until) {
				delete(l.windows, k)
			}
		}
		l.mu.Unlock()
	}
}

// rateLimit guards an endpoint by client address.
func (s *Server) rateLimit(l *limiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := l.allow(clientIP(r)); !ok {
			w.Header().Set("Retry-After", retryAfterSeconds(retry))
			writeError(w, http.StatusTooManyRequests, CodeRateLimited,
				"too many attempts; wait a moment and try again")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP is the address the middleware chain has already resolved through any
// proxy headers — behind a Cloudflare Tunnel that is the caller, not the tunnel.
// The port is dropped so repeated attempts from one machine share a bucket.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func retryAfterSeconds(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return itoa(secs)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
