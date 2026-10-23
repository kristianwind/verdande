package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// What the iOS app needs from a server that a browser gets for free by being on
// the same origin. The app loads its interface from capacitor://localhost and
// talks to whichever server its owner typed in, so every request it makes is
// cross-origin: it needs CORS to read the answers, a way to open the live-sync
// socket without a cookie, and a way to ask "is this a Verdande?" before anyone
// has signed in.

// appCORS answers the app's origin, and nobody else's.
//
// No Access-Control-Allow-Credentials: the app carries its session as a bearer
// token, never a cookie, so a response to a request with a cookie on it is not
// something this header ever needs to unlock. Every other origin gets no CORS
// headers at all, exactly as before — the browser then keeps a foreign page from
// reading anything, which is the default this must not loosen.
func (s *Server) appCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !s.isAppOrigin(origin) {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Add("Vary", "Origin")
		// Read by the client: a rate limit's wait, and a download's file name.
		h.Set("Access-Control-Expose-Headers", "Retry-After, Content-Disposition")

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- the live-sync socket without a cookie -----------------------------------

// A browser cannot put a header on a WebSocket handshake, so the app's bearer
// token has no way onto it. Putting the token in the URL would work and would
// leave a credential that lasts months in every proxy log the URL passes. A
// ticket is the usual answer: asked for with the token, good for one connection,
// gone in thirty seconds.

const wsTicketTTL = 30 * time.Second

type wsTicket struct {
	userID  string
	expires time.Time
}

type wsTickets struct {
	mu sync.Mutex
	m  map[string]wsTicket
}

func newWSTickets() *wsTickets { return &wsTickets{m: map[string]wsTicket{}} }

func (t *wsTickets) issue(userID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	ticket := hex.EncodeToString(raw)

	t.mu.Lock()
	defer t.mu.Unlock()
	// Swept on the way in, so the map is bounded by thirty seconds of asking.
	now := time.Now()
	for k, v := range t.m {
		if now.After(v.expires) {
			delete(t.m, k)
		}
	}
	t.m[ticket] = wsTicket{userID: userID, expires: now.Add(wsTicketTTL)}
	return ticket, nil
}

// redeem spends a ticket. It is deleted before it is checked, so a second
// presentation finds nothing, whether the first one was in time or not.
func (t *wsTickets) redeem(ticket string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.m[ticket]
	delete(t.m, ticket)
	if !ok || time.Now().After(v.expires) {
		return "", false
	}
	return v.userID, true
}

func (s *Server) handleWSTicket(w http.ResponseWriter, r *http.Request) {
	ticket, err := s.wsTickets.issue(userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, "issue websocket ticket", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":     ticket,
		"expires_in": int(wsTicketTTL.Seconds()),
	})
}

// handleWebSocketEntry opens the socket with a ticket, or falls through to the
// ordinary session check.
//
// A socket opened with a ticket skips the origin check, and that is the point
// rather than a gap: the origin check exists because a browser attaches the
// cookie to a handshake from any page. A ticket is attached by nobody — it has to
// be asked for with a credential and put in the URL on purpose — so there is no
// ambient authority for a foreign page to borrow.
func (s *Server) handleWebSocketEntry(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		// The app never holds a cookie, so a handshake from its origin without a
		// ticket is not the app. Refused here rather than left to the origin
		// check, which compares host names only: capacitor://localhost and an
		// instance served at http://localhost have the same one.
		if s.isAppOrigin(r.Header.Get("Origin")) {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "the app opens this socket with a ticket")
			return
		}
		s.requireAuth(http.HandlerFunc(s.handleWebSocket)).ServeHTTP(w, r)
		return
	}
	userID, ok := s.wsTickets.redeem(ticket)
	if !ok {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "that ticket is not valid")
		return
	}
	user, err := s.db.UserByID(r.Context(), userID)
	if err != nil {
		s.storeError(w, r, "load user", err)
		return
	}
	ctx := context.WithValue(withUser(r.Context(), user), ctxTicket, true)
	s.handleWebSocket(w, r.WithContext(ctx))
}

// --- is this a Verdande? -----------------------------------------------------

// handlePing answers before anybody has signed in, which is when the app needs
// it: the person has typed an address and the app has to tell "that is a
// Verdande" from "that is a router's login page" before it shows a password
// field for it. The edition decides which half of the interface to show.
//
// No version. /version sits behind a login on purpose, and a public number is a
// list of the instances still running an old one.
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"product": "verdande",
		"edition": s.edition(),
		"api":     1,
	})
}
