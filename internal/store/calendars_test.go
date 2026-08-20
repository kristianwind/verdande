package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func connectedAccount(t *testing.T, db *DB, userID string) *CalendarAccount {
	t.Helper()
	a := &CalendarAccount{
		UserID: userID, Provider: "google", Account: "kw@nolimit.dk",
		RefreshToken: "1//0-a-real-looking-refresh-token", AccessToken: "ya29.access",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := db.SaveCalendarAccount(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

// The same claim the mailbox tokens have to meet: what is written must not be
// readable in the file a backup copies.
func TestCalendarTokensAreNotStoredInTheClear(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()
	a := connectedAccount(t, db, userID)

	var refresh, access string
	if err := db.QueryRowContext(ctx,
		`SELECT refresh_token, access_token FROM calendar_accounts WHERE id = ?`,
		a.ID).Scan(&refresh, &access); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(refresh, "real-looking") || strings.Contains(access, "ya29") {
		t.Fatalf("a token is in the row as written: %q %q", refresh, access)
	}
	// The address stays readable, which is the reason for sealing named columns
	// rather than the whole row.
	var account string
	if err := db.QueryRowContext(ctx,
		`SELECT account FROM calendar_accounts WHERE id = ?`, a.ID).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if account != "kw@nolimit.dk" {
		t.Errorf("the address was sealed too: %q", account)
	}

	back, err := db.CalendarAccountFor(ctx, userID, "google")
	if err != nil {
		t.Fatal(err)
	}
	if back.RefreshToken != a.RefreshToken || back.AccessToken != a.AccessToken {
		t.Errorf("the tokens did not survive the round trip: %+v", back)
	}
}

// Connecting again must replace what was there, not fail. It is the same OAuth
// flow run a second time, and the person's word for it is "connect", not "add".
func TestConnectingAgainReplacesTheAccount(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()
	first := connectedAccount(t, db, userID)

	second := &CalendarAccount{
		UserID: userID, Provider: "google", Account: "kw@nolimit.dk",
		RefreshToken: "1//0-fresh", AccessToken: "ya29.fresh",
	}
	if err := db.SaveCalendarAccount(ctx, second); err != nil {
		t.Fatalf("connecting a second time failed: %v", err)
	}
	// And the id in hand is the row that is actually there. The calendars hang off
	// it, so an id nothing points at would attach them to nothing.
	if second.ID != first.ID {
		t.Errorf("the second connect came back with id %q, want the existing %q",
			second.ID, first.ID)
	}

	back, err := db.CalendarAccountFor(ctx, userID, "google")
	if err != nil {
		t.Fatal(err)
	}
	if back.RefreshToken != "1//0-fresh" {
		t.Errorf("the token came back as %q", back.RefreshToken)
	}
}

// Google says which calendars exist; only verdande knows which ones somebody wants
// to look at. A refresh that came back with the same list must not undo the choice.
func TestRefreshingTheListKeepsWhatWasChosen(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()
	a := connectedAccount(t, db, userID)

	list := []Calendar{
		{RemoteID: "primary", Name: "kw@nolimit.dk", Colour: "#3f51b5", Primary: true},
		{RemoteID: "fam", Name: "Familien", Colour: "#0b8043"},
		{RemoteID: "old", Name: "Et delt"},
	}
	if err := db.ReplaceCalendars(ctx, a.ID, list); err != nil {
		t.Fatal(err)
	}

	// Nothing is shown to begin with: an account with a dozen shared calendars in
	// it should not fill somebody's grid the moment they connect it.
	stored, err := db.Calendars(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 3 {
		t.Fatalf("got %d calendars, want 3", len(stored))
	}
	for _, c := range stored {
		if c.Shown {
			t.Errorf("%q was shown without anybody asking for it", c.Name)
		}
	}

	var famID string
	for _, c := range stored {
		if c.RemoteID == "fam" {
			famID = c.ID
		}
	}
	if err := db.ShowCalendars(ctx, a.ID, []string{famID}); err != nil {
		t.Fatal(err)
	}

	// The sweep runs again, and one calendar has been renamed and one has gone.
	if err := db.ReplaceCalendars(ctx, a.ID, []Calendar{
		{RemoteID: "primary", Name: "kw@nolimit.dk", Colour: "#3f51b5", Primary: true},
		{RemoteID: "fam", Name: "Familien og hunden", Colour: "#0b8043"},
	}); err != nil {
		t.Fatal(err)
	}

	stored, err = db.Calendars(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("got %d calendars; one that has left the account should stop being drawn",
			len(stored))
	}
	for _, c := range stored {
		switch c.RemoteID {
		case "fam":
			if !c.Shown {
				t.Error("the choice was undone by a refresh of the list")
			}
			if c.Name != "Familien og hunden" {
				t.Errorf("the new name did not arrive: %q", c.Name)
			}
			if c.ID != famID {
				t.Error("the row was rebuilt rather than updated; its id is what was chosen")
			}
		case "primary":
			if c.Shown {
				t.Error("a calendar nobody chose became shown")
			}
		}
	}
}

// A window is replaced, not merged. An event moved out of it has to disappear
// because it is absent from the answer — and the months on either side must not go
// with it.
func TestReplacingAWindowLeavesTheMonthsAroundItAlone(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()
	a := connectedAccount(t, db, userID)

	if err := db.ReplaceCalendars(ctx, a.ID, []Calendar{{RemoteID: "primary", Name: "Min"}}); err != nil {
		t.Fatal(err)
	}
	stored, _ := db.Calendars(ctx, a.ID)
	cal := stored[0]
	if err := db.ShowCalendars(ctx, a.ID, []string{cal.ID}); err != nil {
		t.Fatal(err)
	}

	july := CalendarEvent{RemoteID: "j", Summary: "Juli", StartDay: "2026-07-15", EndDay: "2026-07-15"}
	august := CalendarEvent{RemoteID: "a", Summary: "August", StartDay: "2026-08-15", EndDay: "2026-08-15"}
	if err := db.ReplaceCalendarEvents(ctx, cal.ID, "2026-07-01", "2026-07-31",
		[]CalendarEvent{july}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendarEvents(ctx, cal.ID, "2026-08-01", "2026-08-31",
		[]CalendarEvent{august}); err != nil {
		t.Fatal(err)
	}

	got, err := db.CalendarEvents(ctx, userID, "2026-07-01", "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want both months: %+v", len(got), got)
	}

	// August is synced again and the meeting has been cancelled. July stays.
	if err := db.ReplaceCalendarEvents(ctx, cal.ID, "2026-08-01", "2026-08-31", nil); err != nil {
		t.Fatal(err)
	}
	got, err = db.CalendarEvents(ctx, userID, "2026-07-01", "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Summary != "Juli" {
		t.Fatalf("got %+v; syncing August threw away July", got)
	}
}

// The grid asks "which cells does this cover", so an event that straddles the edge
// of the question has to come back.
func TestAnEventThatStraddlesTheWindowIsFound(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()
	a := connectedAccount(t, db, userID)
	if err := db.ReplaceCalendars(ctx, a.ID, []Calendar{{RemoteID: "c", Name: "Min", Colour: "#0b8043"}}); err != nil {
		t.Fatal(err)
	}
	stored, _ := db.Calendars(ctx, a.ID)
	if err := db.ShowCalendars(ctx, a.ID, []string{stored[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendarEvents(ctx, stored[0].ID, "2026-08-01", "2026-09-30",
		[]CalendarEvent{
			{RemoteID: "trip", Summary: "Aarhus", StartDay: "2026-08-28", EndDay: "2026-09-02", AllDay: true},
		}); err != nil {
		t.Fatal(err)
	}

	// A week that contains neither end of it.
	got, err := db.CalendarEvents(ctx, userID, "2026-08-31", "2026-09-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %+v; an event covering the whole week was not found", got)
	}
	// The calendar's own colour rides along, or the chip has to ask for it per row.
	if got[0].Colour != "#0b8043" || got[0].CalendarName != "Min" {
		t.Errorf("the calendar did not come with the event: %+v", got[0])
	}
}

// Somebody else's calendar is somebody else's.
func TestOnlyTheOwnersEventsComeBack(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	other := &User{Email: "andreas@example.dk", Name: "Andreas"}
	if err := db.CreateUser(ctx, other, "Indbakke"); err != nil {
		t.Fatal(err)
	}
	theirs := &CalendarAccount{UserID: other.ID, Provider: "google", RefreshToken: "r"}
	if err := db.SaveCalendarAccount(ctx, theirs); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendars(ctx, theirs.ID, []Calendar{{RemoteID: "c", Name: "Deres"}}); err != nil {
		t.Fatal(err)
	}
	stored, _ := db.Calendars(ctx, theirs.ID)
	if err := db.ShowCalendars(ctx, theirs.ID, []string{stored[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendarEvents(ctx, stored[0].ID, "2026-08-01", "2026-08-31",
		[]CalendarEvent{{RemoteID: "x", Summary: "Privat", StartDay: "2026-08-10", EndDay: "2026-08-10"}}); err != nil {
		t.Fatal(err)
	}

	got, err := db.CalendarEvents(ctx, userID, "2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("somebody else's calendar came back: %+v", got)
	}
}

// A calendar turned off stops being drawn, and stops costing the grid anything.
func TestTurningACalendarOffRemovesItsEvents(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()
	a := connectedAccount(t, db, userID)
	if err := db.ReplaceCalendars(ctx, a.ID, []Calendar{{RemoteID: "c", Name: "Min"}}); err != nil {
		t.Fatal(err)
	}
	stored, _ := db.Calendars(ctx, a.ID)
	if err := db.ShowCalendars(ctx, a.ID, []string{stored[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendarEvents(ctx, stored[0].ID, "2026-08-01", "2026-08-31",
		[]CalendarEvent{{RemoteID: "x", Summary: "Møde", StartDay: "2026-08-10", EndDay: "2026-08-10"}}); err != nil {
		t.Fatal(err)
	}

	if err := db.ShowCalendars(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM calendar_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("%d events of a calendar nobody is looking at are still in the table", count)
	}
}

// Disconnecting takes the calendars and the events with it. Unlike the tasks a
// mailbox made, none of this is the person's own work — it is a copy of somebody
// else's calendar and it means nothing without the account.
func TestDisconnectingTakesEverythingWithIt(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()
	a := connectedAccount(t, db, userID)
	if err := db.ReplaceCalendars(ctx, a.ID, []Calendar{{RemoteID: "c", Name: "Min"}}); err != nil {
		t.Fatal(err)
	}
	stored, _ := db.Calendars(ctx, a.ID)
	if err := db.ShowCalendars(ctx, a.ID, []string{stored[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceCalendarEvents(ctx, stored[0].ID, "2026-08-01", "2026-08-31",
		[]CalendarEvent{{RemoteID: "x", Summary: "Møde", StartDay: "2026-08-10", EndDay: "2026-08-10"}}); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteCalendarAccount(ctx, userID, a.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"calendar_accounts", "calendars", "calendar_events"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s still holds %d rows after disconnecting", table, count)
		}
	}
}

// The sweep finds people by the presence of an account, so it has to keep working
// now that the credential in it is ciphertext.
func TestTheSweepFindsAConnectedCalendar(t *testing.T) {
	db, userID := sealedStore(t)
	connectedAccount(t, db, userID)

	users, err := db.UsersWithCalendars(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0] != userID {
		t.Fatalf("the sweep found %v", users)
	}
}
