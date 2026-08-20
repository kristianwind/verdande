package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/push"
	"github.com/kristianwind/verdande/internal/safedial"
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

// pushOutcome is what happened, per device.
//
// It exists because "nothing arrives" was unanswerable. A push that a service
// refuses is the ordinary failure here — a stale subscription, a VAPID subject the
// service will not take, a browser that revoked permission without telling the
// server — and every one of them looked exactly like silence.
type pushOutcome struct {
	Subscriptions int           `json:"subscriptions"`
	Sent          int           `json:"sent"`
	Failed        []pushFailure `json:"failed"`
}

// pushFailure names the service and quotes what it said.
//
// The host and not the endpoint: the rest of that URL is the credential the push
// service issued for this device, and it does not belong in an answer, a log line
// or a screenshot.
type pushFailure struct {
	Service string `json:"service"`
	Reason  string `json:"reason"`
}

func (s *Server) pushToUser(userID, title, body, projectID string) pushOutcome {
	ctx, cancel := contextWithBackgroundTimeout(30 * time.Second)
	defer cancel()

	var out pushOutcome
	subs, err := s.db.ListPushSubscriptions(ctx, userID)
	if err != nil || len(subs) == 0 {
		return out
	}
	out.Subscriptions = len(subs)

	public, private, err := s.vapidKeys(ctx)
	if err != nil {
		s.log.Warn("vapid keys", "err", err)
		out.Failed = append(out.Failed, pushFailure{Service: "verdande", Reason: err.Error()})
		return out
	}

	payload := push.Payload{Title: title, Body: body, URL: "/projekt/" + projectID}
	for _, sub := range subs {
		err := push.Send(ctx, push.Subscription{
			Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth,
		}, payload, push.VAPID{
			Public: public, Private: private, Subject: "mailto:" + s.cfg.SMTP.From,
		})
		if err == nil {
			out.Sent++
			continue
		}
		// Warn, not Debug.
		//
		// This was the one line that would have said why no notification ever
		// arrived, and at Debug it is not printed on an instance running at the
		// default level. A delivery that fails silently and a delivery that does
		// not happen look the same from the outside, and the difference is the
		// whole diagnosis.
		reason := pushReason(sub.Endpoint, err)
		s.log.Warn("web push refused", "reason", reason, "service", pushHost(sub.Endpoint), "user", userID)
		out.Failed = append(out.Failed, pushFailure{
			Service: pushHost(sub.Endpoint),
			Reason:  reason,
		})
		if recErr := s.db.RecordPushFailure(ctx, sub.Endpoint, push.IsGone(err)); recErr != nil {
			s.log.Warn("record push failure", "err", recErr)
		}
	}
	return out
}

// pushReason is what went wrong, with the endpoint taken back out of it.
//
// Go wraps a transport failure as `*url.Error`, and its message is `Post
// "https://fcm.googleapis.com/fcm/send/dQw4w9…": dial tcp: …` — the whole
// endpoint, which is the credential the push service issued for that device. It
// would have gone into the answer, the log line and any screenshot of either. A
// test written to prove the host was reported instead is what found it.
//
// Unwrapped first, so the cause is what is quoted. Then the endpoint is scrubbed
// anyway: the next error type to carry it will not come with a warning.
func pushReason(endpoint string, err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}
	reason := err.Error()
	if endpoint != "" {
		reason = strings.ReplaceAll(reason, endpoint, pushHost(endpoint))
	}
	return reason
}

// pushHost is the service an endpoint belongs to, and nothing else from it.
func pushHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "ukendt"
	}
	return u.Host
}

// handlePushTest sends one notification to this account's devices and says what
// happened to it.
//
// Reported as "der kommer ikke nogen". Everything about that was invisible: the
// settings page said "on for this device" as soon as a subscription existed, and a
// refusal from the push service went into a Debug line nobody sees. There was no
// way to tell "the browser never subscribed" from "the service refused it" from
// "nothing has come due yet" — three different problems with one appearance.
//
// It goes through the same path as a real notification rather than a shortcut, so
// what it proves is the thing that actually has to work.
//
// The words come from the client, which is where the dictionaries are. A server
// that carries its own copy of "This is a test" carries it in one language, and
// this one deliberately keeps its prose in the interface — see
// `internal/i18n`. The text is only ever shown back to the person who asked for
// it, on their own devices.
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "verdande"
	}
	// Bounded: this is written into a notification on somebody's lock screen, and
	// a megabyte of title is not a notification.
	if len(title) > 200 {
		title = title[:200]
	}
	body := strings.TrimSpace(req.Body)
	if len(body) > 500 {
		body = body[:500]
	}

	user := userFrom(r.Context())
	writeJSON(w, http.StatusOK, s.pushToUser(user.ID, title, body, ""))
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

	// The endpoint is an address this server will POST to, on a schedule the
	// caller chooses — set a reminder a minute out and the push job fetches it,
	// again every minute. Unchecked, that is a way to make the instance reach
	// anything it can route to, on demand.
	//
	// https only: a push service is on the public internet and speaks TLS. Nothing
	// legitimate needs http here, and allowing it is the difference between a
	// blind request and a readable one.
	if !strings.HasPrefix(req.Endpoint, "https://") {
		writeFieldErrors(w, map[string]string{"endpoint": "must be an https address"})
		return
	}
	if reason := safedial.CheckURL(req.Endpoint); reason != "" {
		writeFieldErrors(w, map[string]string{"endpoint": reason})
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
	// Scoped to the caller. It was not, so anybody who learned somebody else's
	// endpoint could unsubscribe their device — a small thing to do to somebody
	// and no reason at all to be able to.
	if err := s.db.DeletePushSubscription(r.Context(), userFrom(r.Context()).ID, req.Endpoint); err != nil {
		s.internal(w, r, "delete push subscription", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
