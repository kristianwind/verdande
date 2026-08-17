package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/auth"
	"github.com/kristianwind/verdande/internal/store"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxSession
)

// sessionCookie is prefixed with __Host- when served over HTTPS. That prefix is
// enforced by the browser: it refuses the cookie unless it is Secure, path=/ and
// has no Domain — which means a subdomain cannot set a cookie that overwrites this
// one. Over plain HTTP the prefix is invalid, so a local dev instance uses the
// bare name.
const (
	sessionCookieSecure   = "__Host-verdande_session"
	sessionCookieInsecure = "verdande_session"
)

func (s *Server) cookieName() string {
	if s.secureCookies() {
		return sessionCookieSecure
	}
	return sessionCookieInsecure
}

func (s *Server) secureCookies() bool {
	return strings.HasPrefix(s.cfg.BaseURL, "https://")
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:  s.cookieName(),
		Value: token,
		Path:  "/",
		// httpOnly: the frontend never reads this, so a cross-site script that
		// gets to run cannot exfiltrate the session.
		HttpOnly: true,
		Secure:   s.secureCookies(),
		// Lax rather than Strict: Strict would mean following an invite link from
		// an email lands you logged out, which looks exactly like a broken invite.
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
		MaxAge:   int(ttl.Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// requireAuth rejects anything without a complete login.
//
// A session still waiting on its TOTP code is refused here with totp_required, so
// the two-step login cannot be skipped by simply calling the endpoint you wanted.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, session, err := s.authenticate(r)
		if err != nil {
			// A database that could not be read is not a person who is not signed
			// in. Answering 401 for both means an infrastructure problem shows up
			// as everybody being logged out at once — the frontend clears its state
			// and shows the sign-in screen — and nothing in the logs says why.
			if !errors.Is(err, errNoCredentials) && !errors.Is(err, store.ErrNotFound) {
				s.internal(w, r, "authenticate", err)
				return
			}
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "not signed in")
			return
		}
		if session != nil && session.PendingTOTP {
			writeError(w, http.StatusUnauthorized, CodeTOTPRequired,
				"this login still needs its verification code")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		if session != nil {
			ctx = context.WithValue(ctx, ctxSession, session)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requirePendingSession guards the second step of a login, and nothing else.
func (s *Server) requirePendingSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, session, err := s.authenticate(r)
		if err != nil || session == nil || !session.PendingTOTP {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "no login is in progress")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, user)
		ctx = context.WithValue(ctx, ctxSession, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := userFrom(r.Context()); u == nil || !u.IsAdmin {
			writeError(w, http.StatusForbidden, CodeForbidden, "administrator access is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authenticate resolves a request to a user, by session cookie or by API token.
//
// Both are accepted everywhere. An API token belongs to a person and carries their
// permissions exactly — there is no separate service identity to reason about, and
// a script therefore cannot reach anything its owner could not.
// errNoCredentials means the request carried nothing to authenticate with, as
// opposed to carrying something that turned out to be wrong — or to the lookup
// itself failing, which callers must not treat as a failed login.
var errNoCredentials = errors.New("no credentials")

func (s *Server) authenticate(r *http.Request) (*store.User, *store.Session, error) {
	if token := bearerToken(r); token != "" && auth.IsAPIToken(token) {
		user, err := s.db.UserByAPIToken(r.Context(), token)
		if err != nil {
			return nil, nil, err
		}
		return user, nil, nil
	}

	cookie, err := r.Cookie(s.cookieName())
	if err != nil || cookie.Value == "" {
		return nil, nil, errNoCredentials
	}
	session, user, err := s.db.SessionByToken(r.Context(), cookie.Value)
	if err != nil {
		return nil, nil, err
	}
	return user, session, nil
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// withUser puts a user on the context. Used by the CalDAV handlers, which
// authenticate with Basic rather than through the session middleware.
func withUser(ctx context.Context, user *store.User) context.Context {
	return context.WithValue(ctx, ctxUser, user)
}

func userFrom(ctx context.Context) *store.User {
	u, _ := ctx.Value(ctxUser).(*store.User)
	return u
}

func sessionFrom(ctx context.Context) *store.Session {
	s, _ := ctx.Value(ctxSession).(*store.Session)
	return s
}

// requireCSRF blocks cross-site form posts.
//
// SameSite=Lax already stops the browser sending the cookie on a cross-site POST,
// which covers current browsers. This is the second lock: a state-changing request
// must either be same-origin by its own headers or carry an API token, which a
// cross-site form cannot set. Checked here rather than with a token in every form,
// because a token the frontend has to remember to include is a token it will one
// day forget.
func (s *Server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		// A bearer token cannot be attached by a cross-site form or image tag.
		if bearerToken(r) != "" {
			next.ServeHTTP(w, r)
			return
		}

		// Sec-Fetch-Site is set by the browser and cannot be forged by page script.
		switch r.Header.Get("Sec-Fetch-Site") {
		case "same-origin", "same-site", "none":
			next.ServeHTTP(w, r)
			return
		case "cross-site":
			writeError(w, http.StatusForbidden, CodeForbidden, "cross-site request refused")
			return
		}

		// Older clients send no Sec-Fetch-Site; fall back to comparing Origin.
		if origin := r.Header.Get("Origin"); origin != "" && !s.isOwnOrigin(origin) {
			writeError(w, http.StatusForbidden, CodeForbidden, "cross-site request refused")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) isOwnOrigin(origin string) bool {
	if strings.EqualFold(origin, s.cfg.BaseURL) {
		return true
	}
	// A local dev frontend on another port talks to the API directly.
	return s.cfg.Dev && strings.HasPrefix(origin, "http://localhost:")
}

// requireProject checks the caller's standing in the {projectID} of the route
// before any handler under it runs.
//
// Doing this as middleware rather than as a first line in each handler is what
// makes it hard to forget: a new endpoint added under the route is guarded by
// virtue of where it sits, not by the author remembering to add a check.
func (s *Server) requireProject(min store.Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			projectID := chi.URLParam(r, "projectID")
			user := userFrom(r.Context())
			if user == nil || projectID == "" {
				writeError(w, http.StatusNotFound, CodeNotFound, "not found")
				return
			}
			if _, err := store.RequireProjectRole(r.Context(), s.db, projectID, user.ID, min); err != nil {
				// 404 rather than 403: a 403 confirms the project exists, which
				// is what somebody trying ids is hoping to learn.
				writeError(w, http.StatusNotFound, CodeNotFound, "not found")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
