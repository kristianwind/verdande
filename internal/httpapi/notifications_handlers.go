package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/push"
	"github.com/kristianwind/verdande/internal/store"
)

type notificationJSON struct {
	ID        string `json:"id"`
	ActorName string `json:"actor_name,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	Read      bool   `json:"read"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	list, err := s.db.ListNotifications(r.Context(), user.ID, 30)
	if err != nil {
		s.internal(w, r, "list notifications", err)
		return
	}
	unread, err := s.db.UnreadNotificationCount(r.Context(), user.ID)
	if err != nil {
		s.internal(w, r, "count notifications", err)
		return
	}

	out := make([]notificationJSON, 0, len(list))
	for _, n := range list {
		out = append(out, notificationJSON{
			ID: n.ID, ActorName: n.ActorName, ProjectID: n.ProjectID, TaskID: n.TaskID,
			Kind: n.Kind, Title: n.Title, Body: n.Body, Read: n.ReadAt != nil,
			CreatedAt: n.CreatedAt.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out, "unread": unread})
}

func (s *Server) handleMarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	err := s.db.MarkNotificationsRead(r.Context(), userFrom(r.Context()).ID, chi.URLParam(r, "notificationID"))
	if err != nil {
		s.internal(w, r, "mark notifications read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notify delivers one notification through every channel the person has: stored for
// the bell, pushed live to any open tab, and sent to their devices as Web Push.
//
// Never fails the request that caused it. Somebody assigning a task must not see an
// error because a push service was slow.
func (s *Server) notify(r *http.Request, n *store.Notification) {
	if n.UserID == "" || n.UserID == n.ActorID {
		return // nobody needs telling what they just did themselves
	}
	if err := s.db.CreateNotification(r.Context(), n); err != nil {
		s.log.Warn("create notification", "err", err)
		return
	}

	s.hub.PublishToUser(n.UserID, "notification", notificationJSON{
		ID: n.ID, ProjectID: n.ProjectID, TaskID: n.TaskID, Kind: n.Kind,
		Title: n.Title, Body: n.Body, CreatedAt: n.CreatedAt.Format(time.RFC3339),
	})

	// Detached from the request: a push round trip to Google or Mozilla has no
	// business holding up the response to the person who caused it.
	go s.pushToUser(n.UserID, n.Title, n.Body, n.ProjectID)
}

// notifyProject tells everybody who can see a project, except whoever did it.
func (s *Server) notifyProject(r *http.Request, projectID, taskID, actorID, kind, title, body string) {
	members, err := s.db.ProjectMemberIDs(r.Context(), projectID)
	if err != nil {
		s.log.Warn("project members for notification", "err", err)
		return
	}
	for _, userID := range members {
		if userID == actorID {
			continue
		}
		s.notify(r, &store.Notification{
			UserID: userID, ActorID: actorID, ProjectID: projectID, TaskID: taskID,
			Kind: kind, Title: title, Body: body,
		})
	}
}

// PushToUser is pushToUser for callers outside this package — the reminder job,
// which has the schedule but not the keys.
func (s *Server) PushToUser(userID, title, body, projectID string) {
	s.pushToUser(userID, title, body, projectID)
}

func (s *Server) pushToUser(userID, title, body, projectID string) {
	ctx, cancel := contextWithBackgroundTimeout(30 * time.Second)
	defer cancel()

	subs, err := s.db.ListPushSubscriptions(ctx, userID)
	if err != nil || len(subs) == 0 {
		return
	}
	public, private, err := s.vapidKeys(ctx)
	if err != nil {
		s.log.Warn("vapid keys", "err", err)
		return
	}

	payload := push.Payload{Title: title, Body: body, URL: "/projekt/" + projectID}
	for _, sub := range subs {
		err := push.Send(ctx, push.Subscription{
			Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth,
		}, payload, push.VAPID{
			Public: public, Private: private, Subject: "mailto:" + s.cfg.SMTP.From,
		})
		if err == nil {
			continue
		}
		s.log.Debug("web push failed", "err", err, "endpoint", sub.Endpoint)
		if recErr := s.db.RecordPushFailure(ctx, sub.Endpoint, push.IsGone(err)); recErr != nil {
			s.log.Warn("record push failure", "err", recErr)
		}
	}
}

func (s *Server) vapidKeys(ctx contextLike) (string, string, error) {
	return s.db.InstanceKeys(ctx, push.GenerateVAPIDKeys)
}

// --- subscription endpoints -----------------------------------------------------------

type pushSubscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// handlePushKey hands the browser the public half of the VAPID pair, which is what
// PushManager.subscribe needs before it will produce a subscription at all.
func (s *Server) handlePushKey(w http.ResponseWriter, r *http.Request) {
	public, _, err := s.vapidKeys(r.Context())
	if err != nil {
		s.internal(w, r, "vapid keys", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": public})
}

func (s *Server) handleSubscribePush(w http.ResponseWriter, r *http.Request) {
	var req pushSubscriptionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		writeFieldErrors(w, map[string]string{"endpoint": "an incomplete subscription"})
		return
	}

	err := s.db.SavePushSubscription(r.Context(), &store.PushSubscription{
		UserID:    userFrom(r.Context()).ID,
		Endpoint:  req.Endpoint,
		P256dh:    req.Keys.P256dh,
		Auth:      req.Keys.Auth,
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		s.internal(w, r, "save push subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnsubscribePush(w http.ResponseWriter, r *http.Request) {
	var req pushSubscriptionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if err := s.db.DeletePushSubscription(r.Context(), req.Endpoint); err != nil {
		s.internal(w, r, "delete push subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
