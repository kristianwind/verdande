package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kristianwind/verdande/internal/config"
	"github.com/kristianwind/verdande/internal/store"
)

// fakeCalendarGoogle stands in for the parts of Google this feature touches: the
// token endpoint, the calendar list and one calendar's events.
//
// There is no other way to exercise any of it. The real thing needs a registered
// OAuth client, an account and somebody's consent, and none of the three can be had
// inside a test — so the shapes Google returns are written down and the behaviour
// is checked against them, the way internal/imap is checked against go-imap's own
// in-process server.
type fakeCalendarGoogle struct {
	*httptest.Server
	// refreshes counts token refreshes, so a test can prove one is not made for a
	// token that has not expired.
	refreshes int
	// refuse makes every API call answer 401, which is what a revoked grant does.
	refuse bool
	events map[string][]map[string]any
}

func newFakeCalendarGoogle(t *testing.T) *fakeCalendarGoogle {
	t.Helper()
	g := &fakeCalendarGoogle{events: map[string][]map[string]any{}}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			g.refreshes++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "ya29.fresh", "refresh_token": "1//0-refresh",
				"expires_in": 3600,
			})
		case g.refuse:
			http.Error(w, `{"error":"invalid_credentials"}`, http.StatusUnauthorized)
		case strings.HasPrefix(r.URL.Path, "/users/me/calendarList"):
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"id": "kw@nolimit.dk", "summary": "kw@nolimit.dk", "primary": true,
					"accessRole": "owner", "backgroundColor": "#3f51b5",
					"timeZone": "Europe/Copenhagen"},
				{"id": "fam", "summary": "Familien", "accessRole": "reader",
					"backgroundColor": "#0b8043"},
			}})
		case strings.HasPrefix(r.URL.Path, "/calendars/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/calendars/"), "/events")
			items := g.events[id]
			if items == nil {
				items = []map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(g.Close)
	return g
}

// connectedCalendar returns a server with a calendar account already connected —
// the state the OAuth callback leaves behind, without the consent screen nothing
// can automate.
func connectedCalendar(t *testing.T, g *fakeCalendarGoogle, tune func(*config.Config)) (*testServer, *store.User) {
	t.Helper()
	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.GmailClientID = "test-client"
		cfg.GmailClientSecret = "test-secret"
		cfg.GoogleTokenURL = g.URL + "/token"
		cfg.CalendarAPIURL = g.URL
		if tune != nil {
			tune(cfg)
		}
	})
	ts.bootstrap(t)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	account := &store.CalendarAccount{
		UserID: user.ID, Provider: "google", RefreshToken: "1//0-refresh",
	}
	if err := ts.db.SaveCalendarAccount(t.Context(), account); err != nil {
		t.Fatal(err)
	}
	return ts, user
}

// The scope on the consent screen is what somebody agrees to. It has to be the
// read-only one, and the redirect has to be the calendar's own — sharing Gmail's
// would put "which token store do I write" behind a lookup.
func TestAuthorizeAsksForReadOnlyCalendar(t *testing.T) {
	g := newFakeCalendarGoogle(t)
	ts, _ := connectedCalendar(t, g, nil)

	resp, body := ts.do(t, "POST", "/api/v1/calendar/authorize", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %v", resp.StatusCode, body)
	}
	url, _ := body["url"].(string)
	for _, want := range []string{
		"scope=" + "https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fcalendar.readonly",
		"%2Foauth%2Fcalendar%2Fcallback",
		"access_type=offline",
		"prompt=consent",
	} {
		if !strings.Contains(url, want) {
			t.Errorf("the authorisation URL is missing %q:\n%s", want, url)
		}
	}
	if strings.Contains(url, "calendar.events&") || strings.Contains(url, "auth%2Fcalendar&") {
		t.Errorf("a writing scope was asked for:\n%s", url)
	}
}

// Without a registered client there is nothing to connect through, and the answer
// has to say that rather than "that already exists".
func TestAuthorizeWithoutAClientSaysSo(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/calendar/authorize", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409", resp.StatusCode)
	}
	if body["code"] != CodeGmailNotConfigured {
		t.Errorf("code = %v, want %q", body["code"], CodeGmailNotConfigured)
	}
}

// The whole path, minus the consent screen: sync fetches the list, refreshes the
// events of what was chosen, and the grid endpoint answers with them.
func TestSyncFetchesTheChosenCalendarsAndTheGridReadsThem(t *testing.T) {
	g := newFakeCalendarGoogle(t)
	g.events["fam"] = []map[string]any{
		{"id": "e1", "summary": "Bestyrelsesmøde", "htmlLink": "https://calendar.google.com/e1",
			"start": map[string]string{"dateTime": "2026-08-20T14:00:00+02:00"},
			"end":   map[string]string{"dateTime": "2026-08-20T15:30:00+02:00"}},
	}
	g.events["kw@nolimit.dk"] = []map[string]any{
		{"id": "e2", "summary": "Skal ikke med",
			"start": map[string]string{"date": "2026-08-21"},
			"end":   map[string]string{"date": "2026-08-22"}},
	}
	ts, user := connectedCalendar(t, g, nil)

	// The list arrives on the first look at the settings page, which is what
	// somebody would be doing at this point.
	resp, body := ts.do(t, "GET", "/api/v1/calendar", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %v", resp.StatusCode, body)
	}
	if body["connected"] != true {
		t.Fatalf("not reported as connected: %v", body)
	}
	if body["read_only"] != true {
		t.Error("the connection did not say it is read-only; an interface that looks " +
			"editable is one somebody will try to edit")
	}
	// The primary calendar's id is the address, which is how the account names
	// itself without asking for a profile scope as well.
	if body["account"] != "kw@nolimit.dk" {
		t.Errorf("account = %v", body["account"])
	}

	list, _ := body["calendars"].([]any)
	if len(list) != 2 {
		t.Fatalf("got %d calendars, want 2: %v", len(list), body["calendars"])
	}
	var famID string
	for _, raw := range list {
		c := raw.(map[string]any)
		if c["shown"] == true {
			t.Errorf("%v was shown without anybody choosing it", c["name"])
		}
		if c["remote_id"] == "fam" {
			famID = c["id"].(string)
		}
	}

	// Nothing is chosen, so a sync writes nothing: an account with a dozen shared
	// calendars must not fill somebody's grid the moment it is connected.
	resp, body = ts.do(t, "POST", "/api/v1/calendar/sync", nil)
	if resp.StatusCode != http.StatusOK || body["events"] != float64(0) {
		t.Fatalf("an unchosen calendar was fetched: %d %v", resp.StatusCode, body)
	}

	resp, _ = ts.do(t, "PUT", "/api/v1/calendar/calendars",
		map[string]any{"shown": []string{famID}})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("choosing a calendar: status %d", resp.StatusCode)
	}

	resp, body = ts.do(t, "POST", "/api/v1/calendar/sync", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync: status %d, body %v", resp.StatusCode, body)
	}
	if body["events"] != float64(1) {
		t.Fatalf("events = %v, want 1 — only the chosen calendar", body["events"])
	}

	resp, body = ts.do(t, "GET",
		"/api/v1/calendar/events?from=2026-08-01&to=2026-08-31", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events: status %d, body %v", resp.StatusCode, body)
	}
	events, _ := body["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1: %v", len(events), body["events"])
	}
	event := events[0].(map[string]any)
	if event["summary"] != "Bestyrelsesmøde" || event["start_day"] != "2026-08-20" {
		t.Errorf("the event came back as %v", event)
	}
	// The calendar's colour rides along, or the chip has to ask for it per row.
	if event["colour"] != "#0b8043" {
		t.Errorf("colour = %v; two calendars in one grid are told apart by it", event["colour"])
	}
	// The window verdande holds is stated, so a grid paged past it can say so
	// rather than showing an empty month it cannot explain.
	if body["from"] == "" || body["to"] == "" {
		t.Error("the answer does not say which window it covers")
	}

	// And it is one person's calendar. Somebody else on the same instance sees
	// nothing of it.
	other := ts.newUser(t, "andreas@example.dk", "Andreas")
	_, body = other.do(t, "GET", "/api/v1/calendar/events?from=2026-08-01&to=2026-08-31", nil)
	if events, _ := body["events"].([]any); len(events) != 0 {
		t.Errorf("somebody else's calendar was visible: %v", body["events"])
	}
	_ = user
}

// A token that has not expired must not be traded in for another. Refreshing on
// every run burns the quota and makes the failure mode of a bad refresh token
// arrive on every sweep rather than once an hour.
func TestAFreshTokenIsNotRefreshed(t *testing.T) {
	g := newFakeCalendarGoogle(t)
	ts, user := connectedCalendar(t, g, nil)

	account, err := ts.db.CalendarAccountFor(t.Context(), user.ID, "google")
	if err != nil {
		t.Fatal(err)
	}
	account.AccessToken = "ya29.still-good"
	account.ExpiresAt = time.Now().Add(time.Hour)
	if err := ts.db.SaveCalendarAccount(t.Context(), account); err != nil {
		t.Fatal(err)
	}

	if resp, body := ts.do(t, "POST", "/api/v1/calendar/sync", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("sync: status %d, body %v", resp.StatusCode, body)
	}
	if g.refreshes != 0 {
		t.Errorf("the token was refreshed %d times while it was still good", g.refreshes)
	}

	// And an expired one is.
	account.ExpiresAt = time.Now().Add(-time.Minute)
	if err := ts.db.SaveCalendarAccount(t.Context(), account); err != nil {
		t.Fatal(err)
	}
	if resp, body := ts.do(t, "POST", "/api/v1/calendar/sync", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("sync: status %d, body %v", resp.StatusCode, body)
	}
	if g.refreshes != 1 {
		t.Errorf("an expired token was refreshed %d times, want 1", g.refreshes)
	}
}

// Access revoked at Google's end is a connection that has ended, not a failure to
// retry. Polling it every fifteen minutes for ever is a week of log lines and a
// connection the person believes they removed.
func TestRevokedAccessDisconnects(t *testing.T) {
	g := newFakeCalendarGoogle(t)
	ts, user := connectedCalendar(t, g, nil)

	account, _ := ts.db.CalendarAccountFor(t.Context(), user.ID, "google")
	account.AccessToken = "ya29.revoked"
	account.ExpiresAt = time.Now().Add(time.Hour)
	if err := ts.db.SaveCalendarAccount(t.Context(), account); err != nil {
		t.Fatal(err)
	}
	g.refuse = true

	resp, body := ts.do(t, "POST", "/api/v1/calendar/sync", nil)
	if resp.StatusCode != StatusUpstreamRefused {
		t.Fatalf("status %d, want %d — Google saying no is not this server breaking",
			resp.StatusCode, StatusUpstreamRefused)
	}
	if body["code"] != CodeCalendarFailed {
		t.Errorf("code = %v, want %q — `internal_error` is shown as a generic sentence, "+
			"which throws the diagnosis away", body["code"], CodeCalendarFailed)
	}

	gone, err := ts.db.CalendarAccountFor(t.Context(), user.ID, "google")
	if err != nil {
		t.Fatal(err)
	}
	if gone != nil {
		t.Error("the connection is still there after Google refused the token")
	}

	// And the failure is in the error log, which is the point of having one: a
	// sync that has been failing for a week is invisible otherwise.
	_, logged := ts.do(t, "GET", "/api/v1/errors", nil)
	rows, _ := logged["errors"].([]any)
	if len(rows) == 0 {
		t.Fatal("nothing was recorded")
	}
	if row := rows[0].(map[string]any); row["what"] != "sync calendar" {
		t.Errorf("what = %v, want \"sync calendar\"", row["what"])
	}
}

// Disconnecting takes the copy with it. None of it is the person's own work, and
// a calendar left behind after the account is gone is a frozen lie about a day.
func TestDisconnectingRemovesTheEvents(t *testing.T) {
	g := newFakeCalendarGoogle(t)
	g.events["fam"] = []map[string]any{
		{"id": "e1", "summary": "Møde",
			"start": map[string]string{"date": "2026-08-20"},
			"end":   map[string]string{"date": "2026-08-21"}},
	}
	ts, user := connectedCalendar(t, g, nil)

	ts.do(t, "GET", "/api/v1/calendar", nil)
	account, _ := ts.db.CalendarAccountFor(t.Context(), user.ID, "google")
	calendars, _ := ts.db.Calendars(t.Context(), account.ID)
	var famID string
	for _, c := range calendars {
		if c.RemoteID == "fam" {
			famID = c.ID
		}
	}
	ts.do(t, "PUT", "/api/v1/calendar/calendars", map[string]any{"shown": []string{famID}})
	ts.do(t, "POST", "/api/v1/calendar/sync", nil)

	if resp, _ := ts.do(t, "DELETE", "/api/v1/calendar", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("disconnect: status %d", resp.StatusCode)
	}

	_, body := ts.do(t, "GET", "/api/v1/calendar/events?from=2026-08-01&to=2026-08-31", nil)
	if events, _ := body["events"].([]any); len(events) != 0 {
		t.Errorf("events survived the disconnect: %v", body["events"])
	}
	_, body = ts.do(t, "GET", "/api/v1/calendar", nil)
	if body["connected"] != false {
		t.Error("still reported as connected")
	}
}

// A range that is not a date has to be refused, not guessed at. An empty `from`
// compared as a string matches everything before it, which is a grid quietly
// answering a question nobody asked.
func TestEventsRefuseARangeThatIsNotADate(t *testing.T) {
	g := newFakeCalendarGoogle(t)
	ts, _ := connectedCalendar(t, g, nil)

	for _, query := range []string{
		"", "?from=2026-08-01", "?from=&to=2026-08-31",
		"?from=i+morgen&to=2026-08-31", "?from=2026-08-01&to=2026-13-40",
	} {
		resp, body := ts.do(t, "GET", "/api/v1/calendar/events"+query, nil)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%q: status %d, want 422 (%v)", query, resp.StatusCode, body)
		}
	}
}

// A person with no calendar connected still opens the view. It has to answer, and
// answer emptily, rather than fail — the grid is also verdande's own tasks, and
// they must not disappear because Google is not set up.
func TestTheGridAnswersWithNoCalendarConnected(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "GET", "/api/v1/calendar/events?from=2026-08-01&to=2026-08-31", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %v", resp.StatusCode, body)
	}
	if events, ok := body["events"].([]any); !ok || len(events) != 0 {
		t.Errorf("events = %v, want an empty list rather than null", body["events"])
	}

	resp, body = ts.do(t, "GET", "/api/v1/calendar", nil)
	if resp.StatusCode != http.StatusOK || body["connected"] != false {
		t.Fatalf("status %d, body %v", resp.StatusCode, body)
	}
	if body["has_client"] != false {
		t.Error("an instance with no OAuth registration claimed to have one; the page " +
			"would offer a button that answers 409")
	}
	// Spelled out even when nothing is connected: it is what has to be registered
	// with Google, and it is the single most likely thing to be wrong.
	if uri, _ := body["redirect_uri"].(string); !strings.HasSuffix(uri, "/oauth/calendar/callback") {
		t.Errorf("redirect_uri = %q", uri)
	}
}

// A slow Google must not hold the request open until something in front of this
// server gives up on it and answers with its own HTML.
func TestASlowCalendarDoesNotHangTheRequest(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-blocked:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()

	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.GmailClientID = "test-client"
		cfg.GmailClientSecret = "test-secret"
		cfg.GoogleTokenURL = slow.URL + "/token"
		cfg.CalendarAPIURL = slow.URL
		// A second rather than the real twenty-five: what is being tested is that a
		// budget is applied at all, and spending the production one on every run
		// would put half a minute into the suite to learn the same thing.
		cfg.CalendarSyncBudget = time.Second
	})
	ts.bootstrap(t)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.db.SaveCalendarAccount(t.Context(), &store.CalendarAccount{
		UserID: user.ID, Provider: "google", RefreshToken: "r",
		AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	// Its own client, because the shared one gives up after ten seconds — which is
	// shorter than the budget being tested and would prove nothing either way.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/calendar/sync", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, c := range ts.client.Jar.Cookies(mustParse(t, ts.URL)) {
		req.AddCookie(c)
	}

	started := time.Now()
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("the request never came back (%v after %s); the sync has no budget of its "+
			"own, and whatever sits in front of this server answers for it",
			err, time.Since(started).Round(time.Second))
	}
	defer resp.Body.Close()

	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("the sync took %s; the budget is not being applied", took.Round(time.Second))
	}
	if resp.StatusCode >= 500 {
		t.Errorf("status %d — a slow Google is not this server breaking", resp.StatusCode)
	}
}
