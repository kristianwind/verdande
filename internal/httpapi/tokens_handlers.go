package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// Personal API tokens: what a script, a CalDAV client or an MCP connector
// authenticates with. The store has held them since the first migration; this is
// the surface that lets somebody actually make one.
//
// Every route here needs a *session*, not merely an authenticated caller — see
// requireSession. Managing credentials is something a person does in a browser.

type apiTokenJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Prefix is the first few characters, which is all that is ever shown again.
	Prefix     string `json:"prefix"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

// createdTokenJSON is the only response that carries the token itself, and only
// the once. The store keeps a hash, so there is nothing to reveal later even if
// the interface offered to.
type createdTokenJSON struct {
	apiTokenJSON
	Token string `json:"token"`
}

func (s *Server) handleListAPITokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.db.ListAPITokens(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, "list api tokens", err)
		return
	}

	out := make([]apiTokenJSON, 0, len(tokens))
	for _, t := range tokens {
		j := apiTokenJSON{
			ID: t.ID, Name: t.Name, Prefix: t.Prefix,
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		}
		if t.LastUsedAt != nil {
			j.LastUsedAt = t.LastUsedAt.Format(time.RFC3339)
		}
		if t.ExpiresAt != nil {
			j.ExpiresAt = t.ExpiresAt.Format(time.RFC3339)
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

type createTokenRequest struct {
	Name string `json:"name"`
	// ExpiresInDays is optional; 0 means the token does not expire, which is what
	// a calendar client subscribed for years actually needs.
	ExpiresInDays int `json:"expires_in_days"`
}

func (s *Server) handleCreateAPIToken(w http.ResponseWriter, r *http.Request) {
	var req createTokenRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeFieldErrors(w, map[string]string{"name": "required"})
		return
	}
	if len([]rune(name)) > 80 {
		writeFieldErrors(w, map[string]string{"name": "must be at most 80 characters"})
		return
	}
	// A negative lifetime would mint a token that is already dead — accepted
	// silently it looks like the feature is broken rather than the input.
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 3650 {
		writeFieldErrors(w, map[string]string{
			"expires_in_days": "must be between 0 and 3650, where 0 means no expiry",
		})
		return
	}

	var expires *time.Time
	if req.ExpiresInDays > 0 {
		at := time.Now().UTC().AddDate(0, 0, req.ExpiresInDays)
		expires = &at
	}

	plaintext, token, err := s.db.CreateAPIToken(r.Context(), userFrom(r.Context()).ID, name, expires)
	if err != nil {
		s.internal(w, "create api token", err)
		return
	}

	j := createdTokenJSON{
		apiTokenJSON: apiTokenJSON{
			ID: token.ID, Name: token.Name, Prefix: token.Prefix,
			CreatedAt: token.CreatedAt.Format(time.RFC3339),
		},
		Token: plaintext,
	}
	if expires != nil {
		j.ExpiresAt = expires.Format(time.RFC3339)
	}
	s.log.Info("api token created", "user", userFrom(r.Context()).ID, "name", name)
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) handleDeleteAPIToken(w http.ResponseWriter, r *http.Request) {
	err := s.db.DeleteAPIToken(r.Context(), userFrom(r.Context()).ID, chi.URLParam(r, "tokenID"))
	if err != nil {
		s.storeError(w, "delete api token", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireSession refuses a caller who authenticated with an API token.
//
// authenticate() accepts a bearer token everywhere else on purpose: a token
// carries its owner's permissions exactly. Minting tokens is the one thing that
// must not follow that rule, because it is how a single leaked token becomes
// permanent access — the thief issues a second one and revoking the first
// changes nothing. Deleting is held to the same standard so a token cannot
// revoke the session-holder's other credentials either.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sessionFrom(r.Context()) == nil {
			writeError(w, http.StatusForbidden, CodeForbidden,
				"API tokens are managed while signed in, not with an API token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
