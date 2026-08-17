package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/gmail"
	"github.com/kristianwind/verdande/internal/store"
)

func (s *Server) gmailConfig() gmail.Config {
	return gmail.Config{
		ClientID:     s.cfg.GmailClientID,
		ClientSecret: s.cfg.GmailClientSecret,
		RedirectURL:  s.cfg.GmailRedirectURL(),
	}
}

// handleGmailAuthorize starts the flow: it mints a PKCE pair, remembers it against
// the user, and hands back the Google URL to send the browser to.
//
// The verifier and state are stored server-side rather than put in a cookie,
// because the callback arrives as a top-level navigation from Google and a
// SameSite=Lax cookie is the kind of thing that works until a browser tightens its
// defaults.
func (s *Server) handleGmailAuthorize(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	cfg := s.gmailConfig()

	if !cfg.Configured() {
		writeError(w, http.StatusConflict, CodeGmailNotConfigured,
			"no Google OAuth client is registered on this server")
		return
	}

	pkce, err := gmail.NewPKCE()
	if err != nil {
		s.internal(w, r, "gmail pkce", err)
		return
	}

	settings, err := s.db.UserSettings(r.Context(), user.ID, "gmail")
	if err != nil {
		s.internal(w, r, "gmail settings", err)
		return
	}
	settings["pkce_verifier"] = pkce.Verifier
	settings["pkce_state"] = pkce.State
	// The attempt expires: a stored verifier that lives forever is a replay window
	// that lives forever.
	settings["pkce_expires"] = time.Now().Add(10 * time.Minute).Unix()

	if err := s.db.SetUserSettings(r.Context(), user.ID, "gmail", settings); err != nil {
		s.internal(w, r, "gmail settings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": cfg.AuthURL(pkce)})
}

// handleGmailCallback is where Google sends the browser back.
//
// It is a top-level navigation, so it answers with a redirect into the app rather
// than with JSON — the person is looking at a browser tab, not at a fetch response.
func (s *Server) handleGmailCallback(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	settingsURL := s.cfg.BaseURL + "/indstillinger"

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		// Somebody clicked "cancel" on the consent screen. Not an error worth a
		// page of its own.
		http.Redirect(w, r, settingsURL+"?gmail="+errParam, http.StatusFound)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Redirect(w, r, settingsURL+"?gmail=invalid", http.StatusFound)
		return
	}

	settings, err := s.db.UserSettings(r.Context(), user.ID, "gmail")
	if err != nil {
		s.internal(w, r, "gmail settings", err)
		return
	}
	verifier, _ := settings["pkce_verifier"].(string)
	expected, _ := settings["pkce_state"].(string)
	expires, _ := settings["pkce_expires"].(float64)

	// The state has to match, and match in constant time is not needed here — it
	// is a comparison of two values the server generated — but it does have to
	// match at all, or this endpoint accepts a code from anywhere.
	if verifier == "" || expected == "" || state != expected {
		http.Redirect(w, r, settingsURL+"?gmail=state", http.StatusFound)
		return
	}
	if int64(expires) < time.Now().Unix() {
		http.Redirect(w, r, settingsURL+"?gmail=expired", http.StatusFound)
		return
	}

	token, err := s.gmailConfig().Exchange(r.Context(), code, gmail.PKCE{Verifier: verifier})
	if err != nil {
		s.log.Error("gmail exchange", "err", err, "user", user.ID)
		http.Redirect(w, r, settingsURL+"?gmail=failed", http.StatusFound)
		return
	}
	if token.RefreshToken == "" {
		// Without one the connection dies in an hour. Google only withholds it
		// when the consent prompt was skipped, which access_type and prompt are
		// meant to prevent — so this means something is wrong with the client
		// registration rather than with this request.
		s.log.Error("gmail returned no refresh token", "user", user.ID)
		http.Redirect(w, r, settingsURL+"?gmail=norefresh", http.StatusFound)
		return
	}

	email, err := gmail.NewClient(token.AccessToken).Profile(r.Context())
	if err != nil {
		s.log.Warn("gmail profile", "err", err)
	}

	// The one-time values are cleared: keeping a spent verifier serves nothing and
	// is one more secret at rest.
	delete(settings, "pkce_verifier")
	delete(settings, "pkce_state")
	delete(settings, "pkce_expires")

	settings["refresh_token"] = token.RefreshToken
	settings["access_token"] = token.AccessToken
	settings["expires_at"] = token.ExpiresAt.Unix()
	settings["email"] = email
	if _, ok := settings["trigger"]; !ok {
		settings["trigger"] = "starred"
	}

	if err := s.db.SetUserSettings(r.Context(), user.ID, "gmail", settings); err != nil {
		s.internal(w, r, "save gmail tokens", err)
		return
	}
	s.log.Info("gmail connected", "user", user.ID, "mailbox", email)
	http.Redirect(w, r, settingsURL+"?gmail=connected", http.StatusFound)
}

// handleGmailSyncNow polls immediately, so somebody who has just connected can see
// it work rather than wondering whether it did.
func (s *Server) handleGmailSyncNow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	created, err := s.SyncGmail(r.Context(), user)
	if err != nil {
		writeError(w, http.StatusBadGateway, CodeInternal, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"created": created})
}

// syncGmail creates a task for each matching message that has not produced one yet.
//
// Returns how many were made. Also used by the background job, which is why it
// takes a context and a user rather than a request.
func (s *Server) SyncGmail(ctx context.Context, user *store.User) (int, error) {
	settings, err := s.db.UserSettings(ctx, user.ID, "gmail")
	if err != nil {
		return 0, err
	}
	str := func(key string) string {
		v, _ := settings[key].(string)
		return v
	}

	refresh := str("refresh_token")
	if refresh == "" {
		return 0, nil // not connected; nothing to do and nothing to complain about
	}
	query := gmail.Query(str("trigger"), str("label"))
	if query == "" {
		return 0, nil // connected but no trigger chosen
	}

	// The access token is refreshed only when it has actually expired.
	access := str("access_token")
	expires, _ := settings["expires_at"].(float64)
	if access == "" || int64(expires) <= time.Now().Unix() {
		token, err := s.gmailConfig().Refresh(ctx, refresh)
		if err != nil {
			return 0, err
		}
		access = token.AccessToken
		settings["access_token"] = access
		settings["expires_at"] = token.ExpiresAt.Unix()
		if err := s.db.SetUserSettings(ctx, user.ID, "gmail", settings); err != nil {
			return 0, err
		}
	}

	client := gmail.NewClient(access)
	ids, err := client.List(ctx, query, 25)
	if err != nil {
		if err == gmail.ErrUnauthorized {
			// The person revoked access, or changed their password. Forget the
			// tokens rather than retrying every ten minutes forever.
			s.log.Info("gmail access was revoked; disconnecting", "user", user.ID)
			_ = s.db.SetUserSettings(ctx, user.ID, "gmail", map[string]any{})
		}
		return 0, err
	}

	// Which messages have already become tasks. Kept as a list on the settings
	// rather than as a table: it is bounded by the query's own 30-day window, and
	// a table would be one more thing to migrate for a feature this small.
	seen := map[string]bool{}
	if raw, ok := settings["seen"].([]any); ok {
		for _, v := range raw {
			if id, ok := v.(string); ok {
				seen[id] = true
			}
		}
	}

	inbox, err := s.db.InboxID(ctx, user.ID)
	if err != nil {
		return 0, err
	}
	projectID := str("project_id")
	if projectID == "" {
		projectID = inbox
	} else if _, err := store.RequireProjectRole(ctx, s.db, projectID, user.ID, store.RoleEditor); err != nil {
		projectID = inbox
	}

	created := 0
	for _, id := range ids {
		if seen[id] {
			continue
		}
		msg, err := client.Get(ctx, id)
		if err != nil {
			s.log.Warn("gmail get message", "err", err, "id", id)
			continue
		}

		subject := strings.TrimSpace(msg.Subject)
		if subject == "" {
			subject = "(uden emne)"
		}
		sender := gmail.SenderName(msg.From)

		task := &store.Task{
			ProjectID: projectID,
			Content:   sender + ": " + subject,
			// The deep link is the point: the task is a pointer back to the mail,
			// not a copy of it.
			Description: msg.Snippet + "\n\n" + msg.Link,
			Priority:    4,
			CreatedBy:   user.ID,
		}
		if err := s.db.CreateTask(ctx, task, nil); err != nil {
			s.log.Warn("gmail create task", "err", err)
			continue
		}
		seen[id] = true
		created++
		s.hub.Publish(projectID, "task.created", toTaskJSON(*task))
	}

	if created > 0 {
		// Only the ids Gmail still returns are kept, so the list cannot grow
		// without bound as messages fall out of the 30-day window.
		keep := make([]any, 0, len(ids))
		for _, id := range ids {
			if seen[id] {
				keep = append(keep, id)
			}
		}
		settings["seen"] = keep
		settings["last_sync"] = time.Now().Unix()
		if err := s.db.SetUserSettings(ctx, user.ID, "gmail", settings); err != nil {
			return created, err
		}
	}
	return created, nil
}

// --- version and updates ------------------------------------------------------------

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	status := s.updates.Status(r.Context())
	// The notice is for whoever would do something about it. Telling an ordinary
	// member that the server is out of date is telling them about somebody else's
	// job.
	if user := userFrom(r.Context()); user == nil || !user.IsAdmin {
		status.Available = false
		status.Notes = ""
		status.URL = ""
		status.Latest = ""
	}
	writeJSON(w, http.StatusOK, status)
}
