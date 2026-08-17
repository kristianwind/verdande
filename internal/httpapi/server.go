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
	"github.com/kristianwind/verdande/internal/mail"
	"github.com/kristianwind/verdande/internal/mcp"
	"github.com/kristianwind/verdande/internal/realtime"
	"github.com/kristianwind/verdande/internal/store"
)

type Server struct {
	cfg  *config.Config
	db   *store.DB
	log  *slog.Logger
	web  fs.FS
	mail *mail.Sender
	hub  *realtime.Hub
	mcp  *mcp.Server

	// Password guessing is the attack this application is actually exposed to, so
	// the endpoints that check a secret are limited separately and more tightly
	// than the rest of the API.
	loginLimiter *limiter
	resetLimiter *limiter

	// Computed once from the built index.html; see csp.go.
	csp string

	router chi.Router
}

// New builds the router. `web` is the built SvelteKit app, embedded into the binary
// at build time; a nil value serves an explanatory placeholder instead so that a
// backend-only build still runs and reports its health.
func New(cfg *config.Config, db *store.DB, log *slog.Logger, web fs.FS) *Server {
	s := &Server{
		cfg: cfg, db: db, log: log, web: web,
		mail:         mail.New(cfg.SMTP, cfg.BaseURL, log),
		hub:          realtime.NewHub(log),
		loginLimiter: newLimiter(10, 15*time.Minute),
		resetLimiter: newLimiter(5, time.Hour),
	}
	s.csp = contentSecurityPolicy(scriptHashes(web))
	s.mcp = s.buildMCP()

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(requestLogger(log))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(s.securityHeaders)

	// Unauthenticated, and deliberately outside /api/v1: the Rune healthcheck and
	// Docker both need this to answer before anyone has logged in.
	r.Get("/healthz", s.handleHealth)

	// The calendar feed is outside /api/v1 and outside the session: a calendar
	// client cannot log in, so the token in the path is the whole credential.
	r.Get("/ics/{token}", s.handleICSFeed)

	// CalDAV, outside /api/v1 because clients do well-known-path discovery
	// against the root and authenticate with Basic rather than a session.
	r.Get("/.well-known/caldav", s.wellKnownCalDAV)

	// Inbound mail. Delivered by the mail server rather than by a browser, and
	// authenticated by the token in the recipient address.
	r.Post("/inbound/mail", s.handleInboundMail)
	r.Route("/caldav", s.caldavRoutes)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.requireCSRF)

		r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})

		// Reachable without being signed in — these are how you become signed in.
		r.Route("/auth", func(r chi.Router) {
			r.Get("/setup", s.handleSetupState)
			r.Method(http.MethodPost, "/setup",
				s.rateLimit(s.loginLimiter, http.HandlerFunc(s.handleBootstrap)))
			r.Method(http.MethodPost, "/login",
				s.rateLimit(s.loginLimiter, http.HandlerFunc(s.handleLogin)))
			r.Method(http.MethodPost, "/signup",
				s.rateLimit(s.loginLimiter, http.HandlerFunc(s.handleSignup)))
			r.Method(http.MethodPost, "/password/forgot",
				s.rateLimit(s.resetLimiter, http.HandlerFunc(s.handleForgotPassword)))
			r.Method(http.MethodPost, "/password/reset",
				s.rateLimit(s.resetLimiter, http.HandlerFunc(s.handleResetPassword)))

			// The second step of a login: a session that has passed the password
			// but not yet the code, and can reach nothing else.
			r.Group(func(r chi.Router) {
				r.Use(s.requirePendingSession)
				r.Method(http.MethodPost, "/login/totp",
					s.rateLimit(s.loginLimiter, http.HandlerFunc(s.handleLoginTOTP)))
			})

			r.Group(func(r chi.Router) {
				r.Use(s.requireAuth)
				r.Get("/me", s.handleMe)
				r.Post("/logout", s.handleLogout)
				r.Post("/password/change", s.handleChangePassword)
				r.Post("/totp/setup", s.handleTOTPSetup)
				r.Post("/totp/confirm", s.handleTOTPConfirm)
				r.Post("/totp/disable", s.handleTOTPDisable)
				r.Get("/recovery-codes", s.handleRecoveryCodesCount)
				r.Post("/recovery-codes", s.handleRecoveryCodesRegenerate)
			})
		})

		// Everything past this point needs a complete login.
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)

			r.Get("/ws", s.handleWebSocket)

			// The MCP endpoint. Reachable with a personal API token, so a connector
			// sees exactly what its owner sees.
			r.Post("/mcp", s.handleMCP)

			r.Get("/today", s.handleToday)
			r.Get("/filters/preview", s.handlePreviewFilter)

			r.Route("/filters", func(r chi.Router) {
				r.Get("/", s.handleListFilters)
				r.Post("/", s.handleCreateFilter)
				r.Get("/{filterID}/tasks", s.handleRunFilter)
				r.Patch("/{filterID}", s.handleUpdateFilter)
				r.Delete("/{filterID}", s.handleDeleteFilter)
			})

			r.Get("/feed", s.handleGetFeed)

			r.Route("/notifications", func(r chi.Router) {
				r.Get("/", s.handleListNotifications)
				r.Post("/read", s.handleMarkNotificationsRead)
				r.Post("/{notificationID}/read", s.handleMarkNotificationsRead)
			})

			r.Route("/push", func(r chi.Router) {
				r.Get("/key", s.handlePushKey)
				r.Post("/subscribe", s.handleSubscribePush)
				r.Post("/unsubscribe", s.handleUnsubscribePush)
			})

			r.Route("/attachments/{attachmentID}", func(r chi.Router) {
				r.Get("/", s.handleDownloadAttachment)
				r.Delete("/", s.handleDeleteAttachment)
			})

			r.Route("/comments/{commentID}", func(r chi.Router) {
				r.Patch("/", s.handleUpdateComment)
				r.Delete("/", s.handleDeleteComment)
			})
			r.Post("/feed/rotate", s.handleRotateFeed)

			r.Get("/mail-address", s.handleGetMailAddress)
			r.Post("/mail-address/rotate", s.handleRotateMailAddress)

			r.Route("/ai", func(r chi.Router) {
				r.Get("/settings", s.handleGetAISettings)
				r.Put("/settings", s.handleSetAISettings)
				r.Post("/summary", s.handleAISummary)
				r.Post("/tasks/{taskID}/split", s.handleAISplit)
			})

			r.Route("/gmail", func(r chi.Router) {
				r.Get("/", s.handleGetGmail)
				r.Put("/", s.handleSetGmail)
				r.Delete("/", s.handleDisconnectGmail)
			})

			r.Route("/import", func(r chi.Router) {
				r.Post("/todoist", s.handleImportTodoist)
				r.Post("/csv", s.handleImportCSV)
			})

			r.Get("/export/account", s.handleExportAccount)
			r.Get("/export/projects/{projectID}.csv", s.handleExportProject)
			r.Get("/export/projects/{projectID}.ics", s.handleExportProjectICS)

			r.Route("/templates", func(r chi.Router) {
				r.Get("/", s.handleListTemplates)
				r.Post("/", s.handleSaveTemplate)
				r.Post("/{templateID}/use", s.handleUseTemplate)
				r.Delete("/{templateID}", s.handleDeleteTemplate)
			})

			r.Delete("/reminders/{reminderID}", s.handleDeleteReminder)

			r.Route("/labels", func(r chi.Router) {
				r.Get("/", s.handleListLabels)
				r.Post("/", s.handleCreateLabel)
				r.Patch("/{labelID}", s.handleUpdateLabel)
				r.Delete("/{labelID}", s.handleDeleteLabel)
			})
			r.Get("/upcoming", s.handleUpcoming)
			r.Get("/search", s.handleSearch)

			r.Route("/projects", func(r chi.Router) {
				r.Get("/", s.handleListProjects)
				r.Post("/", s.handleCreateProject)

				r.Route("/{projectID}", func(r chi.Router) {
					// Read access is the floor for the whole subtree; the writes
					// below tighten it to editor or owner individually.
					r.Use(s.requireProject(store.RoleViewer))

					r.Get("/", s.handleGetProject)
					r.Get("/sections", s.handleListSections)
					r.Get("/members", s.handleListMembers)
					r.Get("/activity", s.handleActivity)

					r.Group(func(r chi.Router) {
						r.Use(s.requireProject(store.RoleEditor))
						r.Post("/sections", s.handleCreateSection)
					})

					r.Group(func(r chi.Router) {
						r.Use(s.requireProject(store.RoleOwner))
						r.Patch("/", s.handleUpdateProject)
						r.Delete("/", s.handleDeleteProject)
						r.Post("/invites", s.handleInvite)
						r.Delete("/members/{userID}", s.handleRemoveMember)
					})
				})
			})

			// Restoring is outside the block above: a trashed project is invisible
			// to the permission check by design, so the handler resolves ownership
			// itself.
			r.Post("/trash/projects/{projectID}/restore", s.handleRestoreProject)

			r.Route("/sections/{sectionID}", func(r chi.Router) {
				r.Patch("/", s.handleUpdateSection)
				r.Delete("/", s.handleDeleteSection)
			})

			r.Route("/tasks", func(r chi.Router) {
				r.Get("/", s.handleListTasks)
				r.Post("/", s.handleCreateTask)
				r.Post("/quick-add", s.handleQuickAdd)
				r.Get("/quick-add/preview", s.handleQuickAddPreview)

				r.Route("/{taskID}", func(r chi.Router) {
					r.Get("/", s.handleGetTask)
					r.Patch("/", s.handleUpdateTask)
					r.Delete("/", s.handleDeleteTask)
					r.Get("/comments", s.handleListComments)
					r.Post("/comments", s.handleCreateComment)
					r.Post("/attachments", s.handleUploadAttachment)
					r.Get("/reminders", s.handleListReminders)
					r.Post("/reminders", s.handleCreateReminder)
					r.Post("/complete", s.handleCompleteTask)
					r.Post("/reopen", s.handleReopenTask)
					r.Post("/move", s.handleMoveTask)
				})
			})
		})
	})

	r.NotFound(s.serveWeb)

	s.router = r
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.router.ServeHTTP(w, r) }

// Mail and Hub are exposed so the background jobs can deliver through the same
// sender and the same live connections the request path uses — a reminder should
// arrive in an open tab exactly as a colleague's edit does.
func (s *Server) Mail() *mail.Sender { return s.mail }
func (s *Server) Hub() *realtime.Hub { return s.hub }

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

// securityHeaders sets the headers that are the same on every response.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", s.csp)
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
