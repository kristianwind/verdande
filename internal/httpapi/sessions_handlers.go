package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// Where a person is signed in, and how to end one of them.
//
// `last_seen_at` has been written on every request since sessions existed —
// throttled to once a minute, precisely so this list could say "this device, 2
// minutes ago" — and nothing has ever read it. A session you cannot see is a
// session you cannot end.

type sessionJSON struct {
	ID string `json:"id"`
	// Current marks the one making the request, so the interface can say "denne
	// enhed" rather than making somebody work it out from a user agent.
	Current bool `json:"current"`
	// Device is a summary; UserAgent is what it was summarised from. Both, because
	// the summary is what you read and the original is what settles the question
	// when the summary is not enough.
	Device     string `json:"device"`
	UserAgent  string `json:"user_agent"`
	IP         string `json:"ip"`
	CreatedAt  string `json:"created_at"`
	LastSeenAt string `json:"last_seen_at"`
	ExpiresAt  string `json:"expires_at"`
}

// handleListSessions lists the caller's live sessions, most recently used first.
//
// The id here is the stored hash of the cookie, never the cookie itself: it cannot
// be pasted into a browser, and it is what the delete below needs.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	current := sessionFrom(r.Context())

	sessions, err := s.db.ListSessions(r.Context(), user.ID)
	if err != nil {
		s.internal(w, "list sessions", err)
		return
	}

	out := make([]sessionJSON, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, sessionJSON{
			ID:         sess.ID,
			Current:    current != nil && sess.ID == current.ID,
			Device:     describeUserAgent(sess.UA),
			UserAgent:  sess.UA,
			IP:         sess.IP,
			CreatedAt:  sess.CreatedAt.Format(time.RFC3339),
			LastSeenAt: sess.LastSeenAt.Format(time.RFC3339),
			ExpiresAt:  sess.ExpiresAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// handleDeleteSession signs one device out. Ending the current one is allowed and
// is simply logging out — refusing it would be a rule with no reason behind it,
// and somebody clearing every device wants this one gone too.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sessionID := chi.URLParam(r, "sessionID")

	if err := s.db.DeleteUserSession(r.Context(), sessionID, user.ID); err != nil {
		s.storeError(w, "delete session", err)
		return
	}
	if current := sessionFrom(r.Context()); current != nil && current.ID == sessionID {
		s.clearSessionCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

// describeUserAgent turns a user-agent string into something a person recognises.
//
// Deliberately a handful of substring tests rather than a parsing library. The
// question this answers is "is one of these me?", and for that a wrong guess
// between two Chromium forks costs nothing while a dependency that has to be kept
// current costs something every year. The raw string is sent alongside, so
// anything this gets wrong is still visible.
//
// Order matters: every browser's user agent claims to be several other browsers,
// so the most specific name has to be tested first.
func describeUserAgent(ua string) string {
	if strings.TrimSpace(ua) == "" {
		return "Ukendt enhed"
	}

	browser := ""
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/"), strings.Contains(ua, "Opera"):
		browser = "Opera"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	}

	system := ""
	switch {
	// Before "Mac OS X": an iPhone's user agent says both.
	case strings.Contains(ua, "iPhone"):
		system = "iPhone"
	case strings.Contains(ua, "iPad"):
		system = "iPad"
	case strings.Contains(ua, "Android"):
		system = "Android"
	case strings.Contains(ua, "Mac OS X"), strings.Contains(ua, "Macintosh"):
		system = "macOS"
	case strings.Contains(ua, "Windows"):
		system = "Windows"
	case strings.Contains(ua, "Linux"):
		system = "Linux"
	}

	switch {
	case browser != "" && system != "":
		return browser + " på " + system
	case browser != "":
		return browser
	case system != "":
		return system
	}
	// Nothing recognised: show the string itself rather than "Ukendt". A CalDAV
	// client or a script is a real session, and its own name is more useful than a
	// word that says the server gave up.
	return truncateRunes(ua, 60)
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
