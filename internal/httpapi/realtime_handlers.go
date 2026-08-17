package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/kristianwind/verdande/internal/realtime"
)

// activity records what happened, and never fails the request that caused it.
// A history that is missing an entry is a smaller problem than a rename that was
// refused because its history entry could not be written.
func (s *Server) activity(r *http.Request, projectID, taskID, event string, payload map[string]any) {
	user := userFrom(r.Context())
	if user == nil || projectID == "" {
		return
	}
	if err := s.db.RecordActivity(r.Context(), projectID, taskID, user.ID, event, payload); err != nil {
		s.log.Warn("record activity", "err", err, "event", event)
	}
}

// publish pushes a change to everyone else looking at the project.
func (s *Server) publish(projectID, event string, payload any) {
	s.hub.Publish(projectID, event, payload)
}

// publishToOwner sends a change that belongs to a person rather than to a
// project: their projects, their labels, their saved filters.
//
// Project membership is what the socket subscribes to, and a project that has
// just been created has no subscribers — the person is not watching a thing that
// did not exist a moment ago. So these go to the user instead, which also
// reaches their other devices.
func (s *Server) publishToOwner(r *http.Request, event string, payload any) {
	if user := userFrom(r.Context()); user != nil {
		s.hub.PublishToUser(user.ID, event, payload)
	}
}

// handleWebSocket is the live-sync connection.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The socket is same-origin only. Without this, any page on the internet
		// could open a socket carrying the user's cookie and watch their tasks —
		// the same-origin policy does not apply to WebSocket handshakes.
		OriginPatterns: s.allowedOrigins(),
	})
	if err != nil {
		s.log.Debug("websocket handshake", "err", err)
		return
	}
	defer conn.CloseNow()

	projects, err := s.db.ListProjects(r.Context(), user.ID, true)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "could not load projects")
		return
	}
	ids := make([]string, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}

	client := s.hub.Register(user.ID, ids)
	defer s.hub.Unregister(client)

	// Detached from the request context: r.Context() is cancelled when the handler
	// returns, and this connection has to outlive that.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Reading is what notices a closed connection, and it is also what keeps the
	// pings flowing. Nothing a client sends is acted on — this stream is one-way
	// by design, so there is no client message that can change server state.
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()

	// A ping every 30 seconds keeps the connection alive through the idle timeouts
	// of a reverse proxy — Cloudflare Tunnel closes a silent socket after 100
	// seconds, which would otherwise look like the app randomly losing sync.
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-client.Events():
			if !ok {
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			raw, err := realtime.Encode(event)
			if err != nil {
				continue
			}
			writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, raw)
			writeCancel()
			if err != nil {
				return
			}

		case <-ping.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				return
			}
		}
	}
}

// allowedOrigins is the instance's own address, plus localhost when developing so
// the Vite dev server can connect.
func (s *Server) allowedOrigins() []string {
	origins := []string{stripScheme(s.cfg.BaseURL)}
	if s.cfg.Dev {
		origins = append(origins, "localhost:*", "127.0.0.1:*")
	}
	return origins
}

func stripScheme(url string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if len(url) > len(prefix) && url[:len(prefix)] == prefix {
			return url[len(prefix):]
		}
	}
	return url
}
