// Package httpapi wires verdande's HTTP surface: the REST API under /api/v1, the
// WebSocket endpoint, and the static PWA that the same binary serves.
package httpapi

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/kristianwind/verdande/internal/config"
	"github.com/kristianwind/verdande/internal/store"
)

type Server struct {
	cfg    *config.Config
	db     *store.DB
	log    *slog.Logger
	web    fs.FS
	router chi.Router
}

// New builds the router. `web` is the built SvelteKit app, embedded into the binary
// at build time; a nil value serves an explanatory placeholder instead so that a
// backend-only build still runs and reports its health.
func New(cfg *config.Config, db *store.DB, log *slog.Logger, web fs.FS) *Server {
	s := &Server{cfg: cfg, db: db, log: log, web: web}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(requestLogger(log))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(securityHeaders)

	// Unauthenticated, and deliberately outside /api/v1: the Rune healthcheck and
	// Docker both need this to answer before anyone has logged in.
	r.Get("/healthz", s.handleHealth)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
	})

	r.NotFound(s.serveWeb)

	s.router = r
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.router.ServeHTTP(w, r) }

// handleHealth reports whether verdande can actually serve requests, which means
// reaching the database — a process that is up but cannot read its own data is not
// healthy, and answering 200 for it would keep a broken container in rotation.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 2*time.Second)
	defer cancel()

	var one int
	if err := s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		s.log.Error("health check failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unhealthy",
			"error":  "database unreachable",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// serveWeb serves the PWA, falling back to index.html so that client-side routes
// survive a page reload. A request under /api that reaches here is a genuine 404 and
// must say so in JSON rather than being handed the HTML shell — a client parsing
// "<!doctype html>" as a JSON error message is a confusing way to learn about a typo.
func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if s.web == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("verdande — API is running. The web interface was not built into this binary.\n"))
		return
	}

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}

	f, err := s.web.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		// A client-side route: hand back the shell and let the app route it.
		name = "index.html"
		f, err = s.web.Open(name)
	}
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	seeker, ok := f.(interface {
		Read([]byte) (int, error)
		Seek(int64, int) (int64, error)
	})
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot serve asset"})
		return
	}

	// Hashed build assets are immutable; the shell must never be cached, or a
	// deploy leaves people on the previous version until they hard-reload.
	if strings.HasPrefix(name, "_app/immutable/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, info.ModTime(), seeker)
}

// securityHeaders sets the headers that are the same on every response. The CSP is
// strict because verdande serves only its own assets: there is no CDN to allow, and
// nothing in the app loads a script from anywhere else.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'self'",
			"img-src 'self' data: blob:",
			"style-src 'self' 'unsafe-inline'",
			"script-src 'self'",
			"connect-src 'self'",
			"frame-ancestors 'none'",
			"base-uri 'none'",
			"form-action 'self'",
		}, "; "))
		next.ServeHTTP(w, r)
	})
}

func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			level := slog.LevelInfo
			if ww.Status() >= 500 {
				level = slog.LevelError
			}
			// The health check runs every few seconds forever; logging it at info
			// buries everything else.
			if r.URL.Path == "/healthz" && ww.Status() < 400 {
				level = slog.LevelDebug
			}
			log.Log(r.Context(), level, "request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration", time.Since(start).Round(time.Millisecond).String(),
				// RealIP has already resolved this through the proxy headers, so
				// behind a Cloudflare Tunnel it is the caller and not the tunnel.
				// The panel's log watchers roll failed logins up per address.
				"remote", r.RemoteAddr,
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already sent, so there is nothing to report to the
		// client; this only ever means a dropped connection.
		slog.Debug("write json response", "err", err)
	}
}

// NewLogger returns the process logger. Text in development because a person is
// reading it; JSON in production because the panel and log tooling are.
func NewLogger(dev bool) *slog.Logger {
	if dev {
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
