// Package realtime fans changes out to the browsers that are looking at them.
//
// The model is deliberately small: the server says "something happened in this
// project, here it is", and every other client viewing that project applies it.
// There is no operational transform and no conflict resolution, because there is no
// shared document — a task is edited by one person at a time, and the last write
// wins, which is what people expect from a to-do list.
package realtime

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// Event is what a client receives.
type Event struct {
	Type      string `json:"type"`
	ProjectID string `json:"project_id,omitempty"`
	Payload   any    `json:"payload,omitempty"`
	At        string `json:"at"`
}

// Client is one open connection. Delivery is through a buffered channel so that one
// slow or stalled browser cannot hold up the request that produced the event.
type Client struct {
	UserID string
	// Projects is the set this connection should hear about, refreshed when the
	// user's memberships change.
	Projects map[string]bool

	send   chan Event
	hub    *Hub
	mu     sync.RWMutex
	closed bool
}

type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]bool
	log     *slog.Logger
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{clients: map[*Client]bool{}, log: log}
}

// sendBuffer is how many events a connection may fall behind by before it is
// dropped. Sixteen covers a burst — pasting in a list of tasks — while still
// letting go of a connection that has genuinely stopped reading, rather than
// growing a queue for it forever.
const sendBuffer = 16

func (h *Hub) Register(userID string, projects []string) *Client {
	set := make(map[string]bool, len(projects))
	for _, p := range projects {
		set[p] = true
	}
	c := &Client{
		UserID:   userID,
		Projects: set,
		send:     make(chan Event, sendBuffer),
		hub:      h,
	}
	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()
	return c
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	c.close()
}

// Publish sends an event to every connection watching a project.
//
// Never blocks. A client whose buffer is full is dropped rather than waited for:
// the alternative is that one browser left open on a suspended laptop stalls the
// HTTP handler of whoever is actually working.
func (h *Hub) Publish(projectID, eventType string, payload any) {
	event := Event{
		Type:      eventType,
		ProjectID: projectID,
		Payload:   payload,
		At:        time.Now().UTC().Format(time.RFC3339),
	}

	h.mu.RLock()
	var stalled []*Client
	for c := range h.clients {
		if !c.Projects[projectID] {
			continue
		}
		select {
		case c.send <- event:
		default:
			stalled = append(stalled, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range stalled {
		h.log.Debug("dropping a connection that stopped reading", "user", c.UserID)
		h.Unregister(c)
	}
}

// PublishToUser reaches one person on all their devices — for notifications, which
// belong to a person rather than to a project.
func (h *Hub) PublishToUser(userID, eventType string, payload any) {
	event := Event{
		Type:    eventType,
		Payload: payload,
		At:      time.Now().UTC().Format(time.RFC3339),
	}

	h.mu.RLock()
	var stalled []*Client
	for c := range h.clients {
		if c.UserID != userID {
			continue
		}
		select {
		case c.send <- event:
		default:
			stalled = append(stalled, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range stalled {
		h.Unregister(c)
	}
}

// Events is the stream a connection writes to its socket.
func (c *Client) Events() <-chan Event { return c.send }

func (c *Client) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.send)
	}
}

// SetProjects updates what a connection hears about, without reconnecting — which
// is what happens the moment somebody is added to a shared project.
func (c *Client) SetProjects(projects []string) {
	set := make(map[string]bool, len(projects))
	for _, p := range projects {
		set[p] = true
	}
	c.hub.mu.Lock()
	c.Projects = set
	c.hub.mu.Unlock()
}

// Count reports open connections, for the health endpoint and for tests.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Encode is here rather than in the transport so the wire format has one definition
// regardless of whether it leaves over a WebSocket or, on a fallback, an HTTP poll.
func Encode(e Event) ([]byte, error) { return json.Marshal(e) }
