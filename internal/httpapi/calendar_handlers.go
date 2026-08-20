package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kristianwind/verdande/internal/gcal"
	"github.com/kristianwind/verdande/internal/store"
)

// defaultCalendarSyncBudget bounds one "fetch now" when nothing else says
// otherwise. Well under the hundred seconds a proxy is willing to wait, so this
// server is the one that answers a slow Google rather than Cloudflare's own HTML
// page — which is how a failure arrives with no code, no message and nothing in
// the error log.
const defaultCalendarSyncBudget = 25 * time.Second

// The window verdande keeps a copy of.
//
// A month grid pages, so asking Google on every arrow press would mean a round trip
// for a question answered ninety seconds ago — a calendar slower than the calendar
// it is showing. A cached window is the alternative, and a window has to end
// somewhere. Ninety days back is roughly "when was that", a year forward is past
// anything anybody has booked; between them they cover every screen the grid can
// reach without a person noticing an edge.
//
// The edge is still real, and the events endpoint says where it is rather than
// answering an empty list for a month outside it. A calendar that quietly shows no
// events is a calendar that lies about a day being clear.
const (
	calendarWindowBack   = 90
	calendarWindowAhead  = 365
	calendarDateLayout   = "2006-01-02"
	calendarProviderName = "google"
)

func (s *Server) calendarClient(accessToken string) *gcal.Client {
	return gcal.NewClient(accessToken).At(s.cfg.CalendarAPIURL)
}

// calendarConfig is the same registration Gmail signs in through, asking for a
// different scope and coming back to a different URI.
//
// One OAuth client, two features. Google issues a refresh token per authorisation
// and keeps up to a hundred of them live per account, each carrying the scopes it
// was granted with — so connecting the calendar does not disturb a working Gmail,
// and disconnecting one does not take the other with it.
func (s *Server) calendarConfig(ctx context.Context) gcal.Config {
	cfg := s.gmailConfig(ctx)
	cfg.Scope = gcal.Scope
	cfg.RedirectURL = s.cfg.CalendarRedirectURL()
	return cfg
}

// --- connecting ------------------------------------------------------------------

// handleCalendarAuthorize starts the flow. Same shape as Gmail's, and the same
// reason for keeping the verifier server-side: the callback arrives as a top-level
// navigation from Google, and a SameSite=Lax cookie is the kind of thing that works
// until a browser tightens its defaults.
func (s *Server) handleCalendarAuthorize(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	cfg := s.calendarConfig(r.Context())

	if !cfg.Configured() {
		writeError(w, http.StatusConflict, CodeGmailNotConfigured,
			"no Google OAuth client is registered on this server")
		return
	}

	pkce, err := gcal.NewPKCE()
	if err != nil {
		s.internal(w, r, "calendar pkce", err)
		return
	}

	// Its own settings scope, not Gmail's. Two handshakes in one row would mean a
	// person who started both flows had one verifier, and whichever came back
	// second would be refused for a reason nothing on the screen could explain.
	settings, err := s.db.UserSettings(r.Context(), user.ID, "calendar")
	if err != nil {
		s.internal(w, r, "calendar settings", err)
		return
	}
	settings["pkce_verifier"] = pkce.Verifier
	settings["pkce_state"] = pkce.State
	// The attempt expires: a stored verifier that lives forever is a replay window
	// that lives forever.
	settings["pkce_expires"] = time.Now().Add(10 * time.Minute).Unix()

	if err := s.db.SetUserSettings(r.Context(), user.ID, "calendar", settings); err != nil {
		s.internal(w, r, "calendar settings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": cfg.AuthURL(pkce)})
}

// handleCalendarCallback is where Google sends the browser back.
//
// It answers with a redirect into the app rather than with JSON: the person is
// looking at a browser tab, not at a fetch response.
func (s *Server) handleCalendarCallback(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	settingsURL := s.cfg.BaseURL + "/indstillinger/integrationer"

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		// Somebody clicked "cancel" on the consent screen — or, on an Internal
		// OAuth app, Google refused an account outside the organisation with
		// `org_internal`. Both arrive here, and the page names what it was given.
		http.Redirect(w, r, settingsURL+"?calendar="+errParam, http.StatusFound)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		http.Redirect(w, r, settingsURL+"?calendar=invalid", http.StatusFound)
		return
	}

	settings, err := s.db.UserSettings(r.Context(), user.ID, "calendar")
	if err != nil {
		s.internal(w, r, "calendar settings", err)
		return
	}
	verifier, _ := settings["pkce_verifier"].(string)
	expected, _ := settings["pkce_state"].(string)
	expires, _ := settings["pkce_expires"].(float64)

	// The state has to match, or this endpoint accepts a code from anywhere.
	if verifier == "" || expected == "" || state != expected {
		http.Redirect(w, r, settingsURL+"?calendar=state", http.StatusFound)
		return
	}
	if int64(expires) < time.Now().Unix() {
		http.Redirect(w, r, settingsURL+"?calendar=expired", http.StatusFound)
		return
	}

	token, err := s.calendarConfig(r.Context()).Exchange(r.Context(), code,
		gcal.PKCE{Verifier: verifier})
	if err != nil {
		s.log.Error("calendar exchange", "err", err, "user", user.ID)
		http.Redirect(w, r, settingsURL+"?calendar=failed", http.StatusFound)
		return
	}
	if token.RefreshToken == "" {
		// Without one the connection dies in an hour. Google only withholds it when
		// the consent prompt was skipped, which access_type and prompt are meant to
		// prevent — so this means something is wrong with the client registration
		// rather than with this request.
		s.log.Error("calendar returned no refresh token", "user", user.ID)
		http.Redirect(w, r, settingsURL+"?calendar=norefresh", http.StatusFound)
		return
	}

	// The one-time values are cleared: keeping a spent verifier serves nothing and
	// is one more secret at rest.
	delete(settings, "pkce_verifier")
	delete(settings, "pkce_state")
	delete(settings, "pkce_expires")
	if err := s.db.SetUserSettings(r.Context(), user.ID, "calendar", settings); err != nil {
		s.internal(w, r, "clear calendar handshake", err)
		return
	}

	account := &store.CalendarAccount{
		UserID: user.ID, Provider: calendarProviderName,
		RefreshToken: token.RefreshToken, AccessToken: token.AccessToken,
		ExpiresAt: token.ExpiresAt,
	}
	if err := s.db.SaveCalendarAccount(r.Context(), account); err != nil {
		s.internal(w, r, "save calendar tokens", err)
		return
	}

	// The list of calendars, fetched now so the page that opens next has something
	// to tick rather than an empty panel and a button. Best effort: an account that
	// connects but cannot list itself is still connected, and the status endpoint
	// asks again when it finds nothing.
	if _, err := s.refreshCalendarList(r.Context(), account); err != nil {
		s.log.Warn("calendar list", "err", err, "user", user.ID)
	}

	s.log.Info("calendar connected", "user", user.ID, "account", account.Account)
	http.Redirect(w, r, settingsURL+"?calendar=connected", http.StatusFound)
}

// --- what the settings page reads --------------------------------------------------

type calendarStatusJSON struct {
	Connected bool             `json:"connected"`
	Account   string           `json:"account,omitempty"`
	Calendars []store.Calendar `json:"calendars"`
	// RedirectURI is what has to be registered with Google, spelled out. Derived
	// from VERDANDE_BASE_URL and matched exactly — the single most likely thing to
	// be wrong, and the error Google gives for it names neither value.
	RedirectURI string `json:"redirect_uri"`
	// HasClient says whether there is an OAuth registration to connect through at
	// all, so the page can explain rather than offer a button that answers 409.
	HasClient  bool   `json:"has_client"`
	LastSyncAt string `json:"last_sync_at,omitempty"`
	// ReadOnly is stated rather than assumed. Nothing writes a Google calendar, and
	// an interface that quietly looks editable is one somebody will try to edit.
	ReadOnly bool `json:"read_only"`
}

func (s *Server) handleGetCalendar(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	out := calendarStatusJSON{
		Calendars:   []store.Calendar{},
		RedirectURI: s.cfg.CalendarRedirectURL(),
		HasClient:   s.calendarConfig(r.Context()).Configured(),
		ReadOnly:    true,
	}

	account, err := s.db.CalendarAccountFor(r.Context(), user.ID, calendarProviderName)
	if err != nil {
		s.internal(w, r, "calendar account", err)
		return
	}
	if account == nil || account.RefreshToken == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}

	out.Connected = true
	out.Account = account.Account
	if !account.LastSyncAt.IsZero() {
		out.LastSyncAt = account.LastSyncAt.Format(time.RFC3339)
	}

	calendars, err := s.db.Calendars(r.Context(), account.ID)
	if err != nil {
		s.internal(w, r, "calendars", err)
		return
	}
	// An account connected during a Google hiccup has no calendars and no way back:
	// the person sees an empty list and nothing to press. Asking again here costs
	// one request on a page nobody opens often, and means the wrong answer lasts
	// until the next look rather than for ever. Same reasoning as the Gmail address.
	if len(calendars) == 0 {
		if fetched, err := s.refreshCalendarList(r.Context(), account); err == nil {
			calendars = fetched
			out.Account = account.Account
		} else {
			s.log.Warn("calendar list", "err", err, "user", user.ID)
		}
	}
	if calendars != nil {
		out.Calendars = calendars
	}
	writeJSON(w, http.StatusOK, out)
}

type showCalendarsRequest struct {
	// Shown is the whole set, not one flip. Same reasoning as the projects' order:
	// a person has a handful of calendars, the question is "these ones", and a list
	// that can only be built by several requests can land half applied.
	Shown []string `json:"shown"`
}

func (s *Server) handleSetCalendars(w http.ResponseWriter, r *http.Request) {
	var req showCalendarsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	account, err := s.db.CalendarAccountFor(r.Context(), user.ID, calendarProviderName)
	if err != nil {
		s.internal(w, r, "calendar account", err)
		return
	}
	if account == nil {
		// A 404 rather than a 409: everything the caller may not see answers 404,
		// and "there is no such connection" is the true answer either way.
		writeError(w, http.StatusNotFound, CodeNotFound, "no calendar is connected")
		return
	}
	if err := s.db.ShowCalendars(r.Context(), account.ID, req.Shown); err != nil {
		s.internal(w, r, "show calendars", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDisconnectCalendar forgets the connection, and the copy of the calendars
// with it. Unlike the tasks a mailbox made, none of this is the person's own work.
func (s *Server) handleDisconnectCalendar(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	account, err := s.db.CalendarAccountFor(r.Context(), user.ID, calendarProviderName)
	if err != nil {
		s.internal(w, r, "calendar account", err)
		return
	}
	if account != nil {
		if err := s.db.DeleteCalendarAccount(r.Context(), user.ID, account.ID); err != nil {
			s.internal(w, r, "disconnect calendar", err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- what the grid reads -----------------------------------------------------------

type calendarEventsJSON struct {
	Events []store.CalendarEvent `json:"events"`
	// From and To are the window verdande holds a copy of, not the window that was
	// asked for. A grid that has paged past it gets no events, and an empty answer
	// with no explanation is a calendar quietly claiming a day is clear.
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) handleCalendarEvents(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")

	fields := map[string]string{}
	if !validDay(from) {
		fields["from"] = "must be a date, as 2026-08-20"
	}
	if !validDay(to) {
		fields["to"] = "must be a date, as 2026-08-20"
	}
	if len(fields) > 0 {
		writeFieldErrors(w, fields)
		return
	}

	events, err := s.db.CalendarEvents(r.Context(), user.ID, from, to)
	if err != nil {
		s.internal(w, r, "calendar events", err)
		return
	}
	if events == nil {
		events = []store.CalendarEvent{}
	}
	windowFrom, windowTo := calendarWindow(time.Now())
	writeJSON(w, http.StatusOK, calendarEventsJSON{
		Events: events, From: windowFrom, To: windowTo,
	})
}

// handleCalendarSyncNow refreshes immediately, so somebody who has just connected
// can see it work rather than wondering whether it did.
func (s *Server) handleCalendarSyncNow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())

	// A budget of its own, well under what sits in front of this server. An unset
	// budget is not a budget of nothing: a zero here would expire the context
	// before the first call and make every sync a silent no-op.
	budget := s.cfg.CalendarSyncBudget
	if budget <= 0 {
		budget = defaultCalendarSyncBudget
	}
	ctx, cancel := context.WithTimeout(r.Context(), budget)
	defer cancel()

	count, err := s.SyncCalendars(ctx, user)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			// Not a failure: Google is slow or busy, some calendars were read, and
			// the rest arrive on the next run. Every calendar is committed as it is
			// read, so a short budget is a shorter run rather than a lost one.
			writeJSON(w, http.StatusOK, map[string]any{"events": count, "partial": true})
			return
		}
		s.upstream(w, r, CodeCalendarFailed, "sync calendar", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"events": count})
}

// SyncCalendars refreshes every calendar this person has chosen to see.
//
// Returns how many events were written. Also used by the background job, which is
// why it takes a context and a user rather than a request.
func (s *Server) SyncCalendars(ctx context.Context, user *store.User) (int, error) {
	account, err := s.db.CalendarAccountFor(ctx, user.ID, calendarProviderName)
	if err != nil {
		return 0, err
	}
	if account == nil || account.RefreshToken == "" {
		return 0, nil // not connected; nothing to do and nothing to complain about
	}

	// The same guard the mailboxes have, for the same reason: a press of "fetch
	// now" during the sweep would otherwise have two runs replacing the same window
	// at once, and the loser's delete would land after the winner's insert.
	unlock := s.lockSync("calendar:" + account.ID)
	defer unlock()
	if fresh, err := s.db.CalendarAccountFor(ctx, user.ID, calendarProviderName); err == nil && fresh != nil {
		account = fresh
	}

	access, err := s.calendarAccessToken(ctx, account)
	if err != nil {
		return 0, err
	}
	client := s.calendarClient(access)

	// The list first, so a calendar added or renamed since the last run is drawn
	// under its new name and one that has gone stops being drawn at all.
	calendars, err := s.calendarList(ctx, client, account)
	if err != nil {
		return 0, s.forgetIfRevoked(ctx, user, account, err)
	}

	from, to := calendarWindow(time.Now())
	written := 0
	for _, calendar := range calendars {
		if !calendar.Shown {
			continue
		}
		// A spent budget ends the run rather than turning into a failed fetch per
		// calendar, logged one at a time.
		if ctx.Err() != nil {
			return written, ctx.Err()
		}
		events, err := client.Events(ctx, calendar.RemoteID, from, to)
		if err != nil {
			if errors.Is(err, gcal.ErrUnauthorized) {
				return written, s.forgetIfRevoked(ctx, user, account, err)
			}
			// One calendar somebody was unshared from must not stop the others.
			s.log.Warn("calendar events", "err", err, "calendar", calendar.Name)
			continue
		}

		rows := make([]store.CalendarEvent, 0, len(events))
		for _, e := range events {
			rows = append(rows, store.CalendarEvent{
				RemoteID: e.ID, Summary: e.Summary,
				StartsAt: e.StartsAt, EndsAt: e.EndsAt,
				StartDay: e.StartDay, EndDay: e.EndDay,
				AllDay: e.AllDay, Location: e.Location, URL: e.URL,
			})
		}
		if err := s.db.ReplaceCalendarEvents(ctx, calendar.ID, from, to, rows); err != nil {
			return written, err
		}
		written += len(rows)
	}

	if err := s.db.MarkCalendarSynced(ctx, account.ID, time.Now()); err != nil {
		return written, err
	}
	return written, nil
}

// calendarAccessToken returns a usable access token, refreshing only when the one
// held has actually expired.
func (s *Server) calendarAccessToken(ctx context.Context, account *store.CalendarAccount) (string, error) {
	if account.AccessToken != "" && account.ExpiresAt.After(time.Now()) {
		return account.AccessToken, nil
	}
	token, err := s.calendarConfig(ctx).Refresh(ctx, account.RefreshToken)
	if err != nil {
		return "", err
	}
	account.AccessToken = token.AccessToken
	account.ExpiresAt = token.ExpiresAt
	if err := s.db.SaveCalendarAccount(ctx, account); err != nil {
		return "", err
	}
	return account.AccessToken, nil
}

// refreshCalendarList fetches the calendar list with a token it obtains itself.
// Used by the paths that have an account but no client yet — connecting, and the
// status endpoint correcting an empty list.
func (s *Server) refreshCalendarList(ctx context.Context, account *store.CalendarAccount) ([]store.Calendar, error) {
	access, err := s.calendarAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	return s.calendarList(ctx, s.calendarClient(access), account)
}

// calendarList writes what the provider says and returns what is stored, which is
// not the same list: the provider says what exists, and only verdande knows which
// of them somebody chose to look at.
func (s *Server) calendarList(ctx context.Context, client *gcal.Client, account *store.CalendarAccount) ([]store.Calendar, error) {
	remote, err := client.Calendars(ctx)
	if err != nil {
		return nil, err
	}

	list := make([]store.Calendar, 0, len(remote))
	for _, c := range remote {
		list = append(list, store.Calendar{
			RemoteID: c.ID, Name: c.Name, Colour: c.Colour,
			TimeZone: c.TimeZone, Primary: c.Primary, Writable: c.Owner,
		})
		// The primary calendar is the account, and its id is the address. It is the
		// only place the connected account names itself without a second scope —
		// asking for a profile scope to learn an address already in the answer would
		// be one more thing on the consent screen for nothing.
		if c.Primary && account.Account == "" {
			account.Account = c.ID
			if err := s.db.SaveCalendarAccount(ctx, account); err != nil {
				return nil, err
			}
		}
	}
	if err := s.db.ReplaceCalendars(ctx, account.ID, list); err != nil {
		return nil, err
	}
	return s.db.Calendars(ctx, account.ID)
}

// forgetIfRevoked disconnects when Google says the grant is gone, and passes every
// other failure through untouched.
//
// The distinction is the point. A revoked account polled every ten minutes for ever
// is a log line a week long and a connection the person believes they removed; a
// Google that is merely down is one that will answer next time.
func (s *Server) forgetIfRevoked(ctx context.Context, user *store.User, account *store.CalendarAccount, err error) error {
	if errors.Is(err, gcal.ErrUnauthorized) {
		s.log.Info("calendar access was revoked; disconnecting", "user", user.ID)
		_ = s.db.DeleteCalendarAccount(ctx, user.ID, account.ID)
	}
	return err
}

// calendarWindow is the span verdande keeps a copy of, measured from today.
//
// Built from the calendar date rather than by truncating the instant: Truncate
// rounds against the Unix epoch, which is midnight UTC, and in Copenhagen that is
// two in the morning — so for two hours of every summer day the window would start
// and end a day out.
func calendarWindow(now time.Time) (string, string) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return day.AddDate(0, 0, -calendarWindowBack).Format(calendarDateLayout),
		day.AddDate(0, 0, calendarWindowAhead).Format(calendarDateLayout)
}

func validDay(s string) bool {
	_, err := time.Parse(calendarDateLayout, s)
	return err == nil
}
