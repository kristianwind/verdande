package gcal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGoogle answers the two calls this package makes, out of whatever the test
// hands it. There is no other way to exercise this: the real Calendar needs an
// account, a registered client and somebody's consent, and none of the three can be
// had inside a test — so the shapes Google returns are written down here and the
// parsing is checked against them.
func fakeGoogle(t *testing.T, calendarList any, events map[string]any) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/users/me/calendarList"):
			_ = json.NewEncoder(w).Encode(calendarList)
		case strings.HasPrefix(r.URL.Path, "/calendars/"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/calendars/"), "/events")
			body, ok := events[id]
			if !ok {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(body)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return NewClient("token").At(srv.URL)
}

func TestCalendarsPrefersTheNameThePersonGaveIt(t *testing.T) {
	client := fakeGoogle(t, map[string]any{
		"items": []map[string]any{
			{"id": "primary", "summary": "kw@nolimit.dk", "backgroundColor": "#3f51b5",
				"timeZone": "Europe/Copenhagen", "primary": true, "accessRole": "owner"},
			{"id": "fam", "summary": "Family", "summaryOverride": "Familien",
				"backgroundColor": "#0b8043", "accessRole": "reader"},
			// A calendar removed from the list still arrives, flagged.
			{"id": "gone", "summary": "Væk", "deleted": true, "accessRole": "reader"},
		},
	}, nil)

	got, err := client.Calendars(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d calendars, want 2 — a deleted one is not one you can show", len(got))
	}
	if got[1].Name != "Familien" {
		t.Errorf("name = %q; summaryOverride is the name the person will look for", got[1].Name)
	}
	if !got[0].Primary || !got[0].Owner {
		t.Errorf("the primary calendar came back as %+v", got[0])
	}
	if got[1].Owner {
		t.Error("a reader was reported as an owner; nothing may offer to write to it")
	}
	if got[0].Colour != "#3f51b5" {
		t.Errorf("colour = %q; two calendars in one grid are told apart by it", got[0].Colour)
	}
}

// The three shapes an event arrives in, and the one shape a grid can read.
func TestEventsBecomeDaysAGridCanAskAbout(t *testing.T) {
	client := fakeGoogle(t, nil, map[string]any{
		"primary": map[string]any{
			"items": []map[string]any{
				{
					"id": "timed", "summary": "Bestyrelsesmøde", "htmlLink": "https://cal/1",
					"start": map[string]string{"dateTime": "2026-08-20T14:00:00+02:00"},
					"end":   map[string]string{"dateTime": "2026-08-20T15:30:00+02:00"},
				},
				{
					// All-day, one day. Google's end is the day after.
					"id": "allday", "summary": "Fridag",
					"start": map[string]string{"date": "2026-08-21"},
					"end":   map[string]string{"date": "2026-08-22"},
				},
				{
					// All-day, three days.
					"id": "trip", "summary": "Aarhus",
					"start": map[string]string{"date": "2026-08-24"},
					"end":   map[string]string{"date": "2026-08-27"},
				},
				{
					// Runs to midnight, which is the evening before as far as a
					// grid is concerned.
					"id": "late", "summary": "Nattevagt",
					"start": map[string]string{"dateTime": "2026-08-22T20:00:00+02:00"},
					"end":   map[string]string{"dateTime": "2026-08-23T00:00:00+02:00"},
				},
				{
					// A cancelled occurrence of a recurring event.
					"id": "off", "status": "cancelled", "summary": "Aflyst",
					"start": map[string]string{"date": "2026-08-25"},
					"end":   map[string]string{"date": "2026-08-26"},
				},
				{
					// Neither a date nor a dateTime: belongs on no day at all.
					"id": "nowhere", "summary": "Uden tid",
				},
			},
		},
	})

	got, err := client.Events(t.Context(), "primary", "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}

	byID := map[string]Event{}
	for _, e := range got {
		byID[e.ID] = e
	}
	if len(got) != 4 {
		t.Fatalf("got %d events, want 4: %+v", len(got), got)
	}
	if _, ok := byID["off"]; ok {
		t.Error("a cancelled occurrence was kept; it is an event that is not happening")
	}
	if _, ok := byID["nowhere"]; ok {
		t.Error("an event with no time was kept; there is no cell to draw it in")
	}

	timed := byID["timed"]
	if timed.AllDay || timed.StartDay != "2026-08-20" || timed.EndDay != "2026-08-20" {
		t.Errorf("timed = %+v", timed)
	}
	if timed.StartsAt != "2026-08-20T14:00:00+02:00" {
		t.Errorf("the instant was rewritten: %q", timed.StartsAt)
	}

	allday := byID["allday"]
	if !allday.AllDay || allday.StartDay != "2026-08-21" || allday.EndDay != "2026-08-21" {
		t.Errorf("allday = %+v; Google's end date is exclusive and has to be corrected here",
			allday)
	}
	if allday.StartsAt != "" {
		t.Errorf("an all-day event was given an instant: %q — it has a day, not a moment",
			allday.StartsAt)
	}

	if trip := byID["trip"]; trip.StartDay != "2026-08-24" || trip.EndDay != "2026-08-26" {
		t.Errorf("trip = %+v, want 24th to 26th inclusive", trip)
	}
	if late := byID["late"]; late.EndDay != "2026-08-22" {
		t.Errorf("late ends on %q; an event running to midnight ends the evening before, "+
			"or every one of them puts a chip in the following day", late.EndDay)
	}
}

// An event with no title has to say so. A blank chip in a grid reads as a
// rendering fault rather than as an empty title.
func TestAnEventWithNoTitleStillSaysSomething(t *testing.T) {
	client := fakeGoogle(t, nil, map[string]any{
		"c": map[string]any{"items": []map[string]any{{
			"id":    "x",
			"start": map[string]string{"date": "2026-08-20"},
			"end":   map[string]string{"date": "2026-08-21"},
		}}},
	})
	got, err := client.Events(t.Context(), "c", "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Summary == "" {
		t.Fatalf("got %+v", got)
	}
}

// The window is asked for with a day of slack at each end, because an event late
// in the evening in Copenhagen is the next day in UTC. The slack must not leak
// out: an event on the day before the window is not in the window.
func TestTheSlackAtTheEdgesIsThrownAwayAgain(t *testing.T) {
	client := fakeGoogle(t, nil, map[string]any{
		"c": map[string]any{"items": []map[string]any{
			{"id": "before", "summary": "For tidligt",
				"start": map[string]string{"date": "2026-08-09"},
				"end":   map[string]string{"date": "2026-08-10"}},
			{"id": "inside", "summary": "Med",
				"start": map[string]string{"date": "2026-08-10"},
				"end":   map[string]string{"date": "2026-08-11"}},
			{"id": "after", "summary": "For sent",
				"start": map[string]string{"date": "2026-08-21"},
				"end":   map[string]string{"date": "2026-08-22"}},
		}},
	})
	got, err := client.Events(t.Context(), "c", "2026-08-10", "2026-08-20")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "inside" {
		t.Fatalf("got %+v, want only the one inside the window", got)
	}
}

// A page token has to be followed, or a busy calendar silently shows its first
// page and nothing else — which is a calendar that lies about a day being clear.
func TestEveryPageIsRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{
			"items": []map[string]any{{"id": "a", "summary": "Første",
				"start": map[string]string{"date": "2026-08-11"},
				"end":   map[string]string{"date": "2026-08-12"}}},
			"nextPageToken": "more",
		}
		if r.URL.Query().Get("pageToken") == "more" {
			body = map[string]any{"items": []map[string]any{{"id": "b", "summary": "Anden",
				"start": map[string]string{"date": "2026-08-12"},
				"end":   map[string]string{"date": "2026-08-13"}}}}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	got, err := NewClient("t").At(srv.URL).Events(t.Context(), "c", "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want both pages: %+v", len(got), got)
	}
}

// A 401 is not a failure to retry; it is a connection that has ended. Telling the
// two apart is what stops a revoked account being polled every ten minutes for ever.
func TestARefusedTokenIsItsOwnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid_credentials"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := NewClient("spent").At(srv.URL).Calendars(t.Context())
	if err != ErrUnauthorized {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

// Read-only, and only the calendar. The consent screen is what somebody agrees to,
// and a scope that quietly grew is a promise quietly broken.
func TestTheScopeIsReadOnly(t *testing.T) {
	if !strings.HasSuffix(Scope, "calendar.readonly") {
		t.Errorf("scope = %q; nothing here writes a calendar", Scope)
	}
}
