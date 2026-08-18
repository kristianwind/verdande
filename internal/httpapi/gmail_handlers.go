package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/gmail"
	"github.com/kristianwind/verdande/internal/store"
)

// defaultGmailSyncBudget bounds one "fetch now" when nothing else says otherwise.
// Well under the hundred seconds a proxy is willing to wait, so this server is the
// one that answers a slow mailbox.
const defaultGmailSyncBudget = 25 * time.Second

// gmailConfig is the OAuth client this instance authorises with.
//
// The environment wins when it is set. A value in the environment is a deliberate
// deployment decision — it is in the Rune's manifest, under version control,
// applied on every recreate — and a form that could quietly override it would make
// the manifest a lie. The stored pair is for the ordinary case, where nobody wants
// to redeploy a container to paste a client id.
// gmailClient is the API client, pointed wherever the config says Google is.
func (s *Server) gmailClient(accessToken string) *gmail.Client {
	return gmail.NewClient(accessToken).At(gmail.Endpoints{
		Token: s.cfg.GmailTokenURL, API: s.cfg.GmailAPIURL,
	})
}

func (s *Server) gmailConfig(ctx context.Context) gmail.Config {
	endpoints := gmail.Endpoints{Token: s.cfg.GmailTokenURL, API: s.cfg.GmailAPIURL}
	cfg := gmail.Config{
		ClientID:     s.cfg.GmailClientID,
		ClientSecret: s.cfg.GmailClientSecret,
		RedirectURL:  s.cfg.GmailRedirectURL(),
		Endpoints:    endpoints,
	}
	if cfg.ClientID != "" && cfg.ClientSecret != "" {
		return cfg
	}

	stored, err := s.db.InstanceSettings(ctx, "gmail")
	if err != nil {
		// Not fatal: an unconfigured Gmail and a database that could not answer
		// look the same from here, and the caller's next step is the same either
		// way — say it is not set up.
		return cfg
	}
	if id, _ := stored["client_id"].(string); cfg.ClientID == "" {
		cfg.ClientID = id
	}
	if secret, _ := stored["client_secret"].(string); cfg.ClientSecret == "" {
		cfg.ClientSecret = secret
	}
	return cfg
}

// gmailFromEnv says whether the deployment set the client, which the settings page
// needs so it can explain why the fields are not editable rather than accepting
// changes that will never take effect.
func (s *Server) gmailFromEnv() bool {
	return s.cfg.GmailClientID != "" && s.cfg.GmailClientSecret != ""
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
	cfg := s.gmailConfig(r.Context())

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

	token, err := s.gmailConfig(r.Context()).Exchange(r.Context(), code, gmail.PKCE{Verifier: verifier})
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

	// Best effort: a mailbox that connects but cannot name itself is still
	// connected, and the status endpoint asks again when it finds no address —
	// so this failing is a delay rather than a dead end.
	email, err := s.gmailClient(token.AccessToken).Profile(r.Context())
	if err != nil {
		s.log.Warn("gmail profile", "err", err)
	}

	// The one-time values are cleared: keeping a spent verifier serves nothing and
	// is one more secret at rest.
	delete(settings, "pkce_verifier")
	delete(settings, "pkce_state")
	delete(settings, "pkce_expires")

	if err := s.db.SetUserSettings(r.Context(), user.ID, "gmail", settings); err != nil {
		s.internal(w, r, "clear gmail handshake", err)
		return
	}

	// The mailbox itself, kept beside the ones read over IMAP. Connecting again
	// writes the same row: Gmail is one per person, because the OAuth flow is.
	box, err := s.db.MailboxOfKind(r.Context(), user.ID, "gmail")
	if err != nil {
		s.internal(w, r, "read gmail mailbox", err)
		return
	}
	if box == nil {
		box = &store.Mailbox{UserID: user.ID, Kind: "gmail", Trigger: "starred"}
	}
	box.Name = email
	box.Username = email
	box.RefreshToken = token.RefreshToken
	box.AccessToken = token.AccessToken
	box.ExpiresAt = token.ExpiresAt
	if err := s.db.SaveMailbox(r.Context(), box); err != nil {
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

	// A budget of its own, well under what sits in front of this server.
	//
	// The sync fetches up to twenty-five messages one at a time, each with its own
	// thirty-second timeout — so a slow mailbox can hold the request open for
	// minutes. Cloudflare gives up at a hundred seconds and answers with its own
	// 502 page, which is HTML: the browser gets no JSON, no error code and no
	// message, and shows "something went wrong". Nothing reaches the error log
	// either, because this handler never returned.
	//
	// Whatever was created by the deadline is kept: every task is committed as it
	// is made, so a short budget is a shorter run rather than a lost one. The
	// background job keeps the full one; it has nobody waiting.
	// An unset budget is not a budget of nothing: a zero here would expire the
	// context before the first call and make every sync a silent no-op.
	budget := s.cfg.GmailSyncBudget
	if budget <= 0 {
		budget = defaultGmailSyncBudget
	}
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()

	created, err := s.SyncGmail(ctx, user)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			// Not a failure: the mailbox is slow or busy, some tasks were made, and
			// the rest arrive on the next run ten minutes from now.
			writeJSON(w, http.StatusOK, map[string]any{"created": created, "partial": true})
			return
		}
		s.upstream(w, r, CodeGmailFailed, "sync gmail", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"created": created})
}

// syncGmail creates a task for each matching message that has not produced one yet.
//
// Returns how many were made. Also used by the background job, which is why it
// takes a context and a user rather than a request.
func (s *Server) SyncGmail(ctx context.Context, user *store.User) (int, error) {
	box, err := s.db.MailboxOfKind(ctx, user.ID, "gmail")
	if err != nil {
		return 0, err
	}
	if box == nil || box.RefreshToken == "" {
		return 0, nil // not connected; nothing to do and nothing to complain about
	}
	query := gmail.Query(box.Trigger, box.Label)
	if query == "" {
		return 0, nil // connected but no trigger chosen
	}
	refresh := box.RefreshToken

	// The access token is refreshed only when it has actually expired.
	access := box.AccessToken
	if access == "" || box.ExpiresAt.Before(time.Now()) {
		token, err := s.gmailConfig(ctx).Refresh(ctx, refresh)
		if err != nil {
			return 0, err
		}
		access = token.AccessToken
		box.AccessToken = access
		box.ExpiresAt = token.ExpiresAt
		if err := s.db.SaveMailbox(ctx, box); err != nil {
			return 0, err
		}
	}

	client := s.gmailClient(access)
	ids, err := client.List(ctx, query, 25)
	if err != nil {
		if err == gmail.ErrUnauthorized {
			// The person revoked access, or changed their password. Forget the
			// tokens rather than retrying every ten minutes forever.
			s.log.Info("gmail access was revoked; disconnecting", "user", user.ID)
			_ = s.db.DeleteMailbox(ctx, user.ID, box.ID)
		}
		return 0, err
	}

	// Which messages have already become tasks. Kept as a list on the settings
	// rather than as a table: it is bounded by the query's own 30-day window, and
	// a table would be one more thing to migrate for a feature this small.
	seen := map[string]bool{}
	for _, id := range box.Seen {
		seen[id] = true
	}

	inbox, err := s.db.InboxID(ctx, user.ID)
	if err != nil {
		return 0, err
	}
	projectID := box.ProjectID
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
		// A spent budget ends the run rather than turning into twenty-five failed
		// fetches logged one at a time.
		if ctx.Err() != nil {
			return created, ctx.Err()
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
		keep := make([]string, 0, len(ids))
		for _, id := range ids {
			if seen[id] {
				keep = append(keep, id)
			}
		}
		box.Seen = keep
		box.LastSyncAt = time.Now()
		if err := s.db.SaveMailbox(ctx, box); err != nil {
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

// --- the OAuth client itself ------------------------------------------------------

// The registration Google requires, enterable rather than deployed.
//
// The one click that opens Google's consent screen already exists; what it needs
// behind it is a registered client, and that is not something any app can conjure:
// Google issues no Gmail access to an unregistered client, and `gmail.readonly` is
// a restricted scope, so an id shipped inside a public image would be both
// extractable from it and unusable without Google's security review of every
// deployment. The registration is a one-off. Having to edit a Rune's manifest and
// recreate the container to paste the result was not.
//
// Administrators only, and sessions only: this is the instance's identity to
// Google, and a leaked API token must not be able to read or replace it.

type gmailClientJSON struct {
	ClientID string `json:"client_id"`
	// Secret is written and never read back, like the AI key: a settings page that
	// repopulates a password field is one that will eventually leak into a
	// screenshot.
	ClientSecret string `json:"client_secret,omitempty"`
	HasSecret    bool   `json:"has_secret"`
	// FromEnv says the deployment set this, in which case the stored pair is
	// ignored and the fields are not worth editing. Sent so the page can say why
	// rather than accepting changes that will never take effect.
	FromEnv bool `json:"from_env"`
	// RedirectURI is what has to be registered with Google, spelled out. It is
	// derived from VERDANDE_BASE_URL and must match exactly — it is the single
	// most likely thing to be wrong, and the error Google gives for it names
	// neither value.
	RedirectURI string `json:"redirect_uri"`
}

func (s *Server) handleGetGmailClient(w http.ResponseWriter, r *http.Request) {
	stored, err := s.db.InstanceSettings(r.Context(), "gmail")
	if err != nil {
		s.internal(w, r, "gmail client", err)
		return
	}
	out := gmailClientJSON{
		FromEnv:     s.gmailFromEnv(),
		RedirectURI: s.cfg.GmailRedirectURL(),
	}
	if out.FromEnv {
		out.ClientID = s.cfg.GmailClientID
		out.HasSecret = true
	} else {
		out.ClientID, _ = stored["client_id"].(string)
		secret, _ := stored["client_secret"].(string)
		out.HasSecret = secret != ""
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSetGmailClient(w http.ResponseWriter, r *http.Request) {
	var req gmailClientJSON
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if s.gmailFromEnv() {
		writeError(w, http.StatusConflict, CodeConflict,
			"the Gmail client is set in the environment and cannot be changed here")
		return
	}

	stored, err := s.db.InstanceSettings(r.Context(), "gmail")
	if err != nil {
		s.internal(w, r, "gmail client", err)
		return
	}
	// An empty secret means "leave it alone", not "delete it" — otherwise
	// correcting a typo in the client id would silently clear the secret.
	secret, _ := stored["client_secret"].(string)
	if req.ClientSecret != "" {
		secret = strings.TrimSpace(req.ClientSecret)
	}
	id := strings.TrimSpace(req.ClientID)

	if fields := map[string]string{}; id == "" && secret != "" {
		fields["client_id"] = "required"
		writeFieldErrors(w, fields)
		return
	}

	if err := s.db.SetInstanceSettings(r.Context(), "gmail", map[string]any{
		"client_id": id, "client_secret": secret,
	}); err != nil {
		s.internal(w, r, "save gmail client", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// gmailProfile fetches the mailbox address and stores it, refreshing the access
// token first if it has expired.
//
// Shared by the connect callback and the status endpoint. The callback is the
// natural place to learn the address, and it is also the one moment that cannot be
// retried — so the status endpoint asks again when it finds nothing, and the answer
// corrects itself on the next look.
func (s *Server) gmailProfile(ctx context.Context, box *store.Mailbox) (string, error) {
	access := box.AccessToken
	if access == "" || box.ExpiresAt.Before(time.Now()) {
		token, err := s.gmailConfig(ctx).Refresh(ctx, box.RefreshToken)
		if err != nil {
			return "", err
		}
		access = token.AccessToken
		box.AccessToken = access
		box.ExpiresAt = token.ExpiresAt
	}

	email, err := s.gmailClient(access).Profile(ctx)
	if err != nil {
		return "", err
	}
	box.Name = email
	box.Username = email
	if err := s.db.SaveMailbox(ctx, box); err != nil {
		return "", err
	}
	return email, nil
}
