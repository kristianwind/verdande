package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CalendarAccount is one connected account belonging to one person.
type CalendarAccount struct {
	ID       string `json:"id"`
	UserID   string `json:"-"`
	Provider string `json:"provider"`
	Account  string `json:"account,omitempty"`

	RefreshToken string    `json:"-"` // never leaves the server
	AccessToken  string    `json:"-"`
	ExpiresAt    time.Time `json:"-"`

	LastSyncAt time.Time `json:"last_sync_at,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Calendar is one calendar inside an account, shown or not.
type Calendar struct {
	ID        string `json:"id"`
	AccountID string `json:"-"`
	RemoteID  string `json:"remote_id"`
	Name      string `json:"name"`
	Colour    string `json:"colour,omitempty"`
	TimeZone  string `json:"time_zone,omitempty"`
	Primary   bool   `json:"primary"`
	// Writable says the connected account could write to it. Nothing does — the
	// scope is read-only — and it is stored so the day something can, the interface
	// knows which calendars could even be offered.
	Writable  bool `json:"writable"`
	Shown     bool `json:"shown"`
	SortOrder int  `json:"-"`
}

// CalendarEvent is one occurrence, in the shape a grid asks about: days at both
// ends, inclusive.
type CalendarEvent struct {
	ID         string `json:"id"`
	CalendarID string `json:"calendar_id"`
	RemoteID   string `json:"-"`
	Summary    string `json:"summary"`
	StartsAt   string `json:"starts_at,omitempty"`
	EndsAt     string `json:"ends_at,omitempty"`
	StartDay   string `json:"start_day"`
	EndDay     string `json:"end_day"`
	AllDay     bool   `json:"all_day"`
	Location   string `json:"location,omitempty"`
	URL        string `json:"url,omitempty"`

	// CalendarName and Colour ride along with the event rather than being looked up
	// per row by the caller. Same reasoning as the assignee names on a task list:
	// the chip has to be drawn in the calendar's own colour, and a second request
	// per event to learn it is a request per event.
	CalendarName string `json:"calendar_name,omitempty"`
	Colour       string `json:"colour,omitempty"`
}

const calendarAccountColumns = `id, user_id, provider, account, refresh_token,
	access_token, expires_at, last_sync_at, created_at`

// CalendarAccountFor returns a person's connection to one provider, or nil.
func (db *DB) CalendarAccountFor(ctx context.Context, userID, provider string) (*CalendarAccount, error) {
	row := db.QueryRowContext(ctx, `SELECT `+calendarAccountColumns+`
		FROM calendar_accounts WHERE user_id = ? AND provider = ?`, userID, provider)
	a, err := db.scanCalendarAccount(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// SaveCalendarAccount writes one, sealing the two columns that must not travel
// with a backup. Called by hand rather than by the settings tables' own seal,
// which does not reach this table.
func (db *DB) SaveCalendarAccount(ctx context.Context, a *CalendarAccount) error {
	if a.ID == "" {
		a.ID = NewID()
	}
	if a.Provider == "" {
		a.Provider = "google"
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}

	refresh, err := db.sealValue(a.RefreshToken)
	if err != nil {
		return fmt.Errorf("seal refresh_token: %w", err)
	}
	access, err := db.sealValue(a.AccessToken)
	if err != nil {
		return fmt.Errorf("seal access_token: %w", err)
	}

	// The conflict is on the pair, not on the id: a reconnect builds a fresh
	// struct with no id in it, and matching on the id would insert a second row
	// that the unique index then refuses — which reads to the person as "connecting
	// failed" when what happened is that it already had.
	_, err = db.ExecContext(ctx, `
		INSERT INTO calendar_accounts (`+calendarAccountColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id, provider) DO UPDATE SET
		    account = excluded.account,
		    refresh_token = excluded.refresh_token,
		    access_token = excluded.access_token,
		    expires_at = excluded.expires_at,
		    last_sync_at = excluded.last_sync_at`,
		a.ID, a.UserID, a.Provider, a.Account, refresh, access,
		unixOrZero(a.ExpiresAt), unixOrZero(a.LastSyncAt), a.CreatedAt.Unix())
	if err != nil {
		return err
	}

	// The row that is actually there wins. On a reconnect the insert above updated
	// an existing row, so the id in hand is one nothing points at — and the
	// calendars are hung off the id, so writing them against it would attach them
	// to nothing.
	return db.QueryRowContext(ctx,
		`SELECT id FROM calendar_accounts WHERE user_id = ? AND provider = ?`,
		a.UserID, a.Provider).Scan(&a.ID)
}

// MarkCalendarSynced records how far a run got. Written separately from the rest
// so a sync never rewrites the credentials it is holding in memory.
func (db *DB) MarkCalendarSynced(ctx context.Context, accountID string, at time.Time) error {
	_, err := db.ExecContext(ctx,
		`UPDATE calendar_accounts SET last_sync_at = ? WHERE id = ?`, at.Unix(), accountID)
	return err
}

// DeleteCalendarAccount disconnects one. The calendars and their events go with it
// — unlike the tasks a mailbox made, none of this is the person's own work; it is
// a copy of somebody else's calendar and it has no meaning without the account.
func (db *DB) DeleteCalendarAccount(ctx context.Context, userID, id string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM calendar_accounts WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

// UsersWithCalendars returns everybody the sweep has to visit.
func (db *DB) UsersWithCalendars(ctx context.Context) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT user_id FROM calendar_accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Calendars returns what one account holds, in the order the provider listed them.
func (db *DB) Calendars(ctx context.Context, accountID string) ([]Calendar, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, account_id, remote_id, name, colour, time_zone, primary_one,
		       writable, shown, sort_order
		FROM calendars WHERE account_id = ? ORDER BY sort_order, name`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Calendar
	for rows.Next() {
		var c Calendar
		if err := rows.Scan(&c.ID, &c.AccountID, &c.RemoteID, &c.Name, &c.Colour,
			&c.TimeZone, &c.Primary, &c.Writable, &c.Shown, &c.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReplaceCalendars writes the list the provider just gave, keeping what the person
// chose.
//
// The choice survives because it is not in the list: Google says which calendars
// exist and what they are called, and only verdande knows which of them somebody
// wants to look at. Merged rather than deleted and rewritten, so ticking a calendar
// on does not come undone the next time the sweep runs.
//
// A calendar that has disappeared from the account is removed, and its events go
// with it — a calendar somebody was unshared from should stop being drawn, not
// stay behind as a frozen copy of what it held on the last good day.
func (db *DB) ReplaceCalendars(ctx context.Context, accountID string, list []Calendar) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		keep := map[string]bool{}
		for i, c := range list {
			keep[c.RemoteID] = true
			// The insert cannot carry `shown`: on conflict it would overwrite the
			// choice with the default. A new calendar starts hidden — an account
			// with a dozen shared calendars in it should not fill somebody's grid
			// the moment they connect it.
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO calendars
				    (id, account_id, remote_id, name, colour, time_zone, primary_one,
				     writable, shown, sort_order)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (account_id, remote_id) DO UPDATE SET
				    name = excluded.name,
				    colour = excluded.colour,
				    time_zone = excluded.time_zone,
				    primary_one = excluded.primary_one,
				    writable = excluded.writable,
				    sort_order = excluded.sort_order`,
				NewID(), accountID, c.RemoteID, c.Name, c.Colour, c.TimeZone,
				c.Primary, c.Writable, c.Shown, i); err != nil {
				return err
			}
		}

		rows, err := tx.QueryContext(ctx,
			`SELECT id, remote_id FROM calendars WHERE account_id = ?`, accountID)
		if err != nil {
			return err
		}
		var gone []string
		for rows.Next() {
			var id, remote string
			if err := rows.Scan(&id, &remote); err != nil {
				rows.Close()
				return err
			}
			if !keep[remote] {
				gone = append(gone, id)
			}
		}
		// Closed before the next statement runs on this transaction. An open result
		// set with a query behind it is the deadlock the pool comment in store.go
		// exists to make slow rather than fatal.
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, id := range gone {
			if _, err := tx.ExecContext(ctx, `DELETE FROM calendars WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ShowCalendars sets which of an account's calendars are drawn.
//
// The whole set in one write rather than one flip at a time, for the reason the
// projects' order is written the same way: a person has a handful of calendars, the
// question is "these ones", and a list that can only be built by several requests
// can land half applied.
func (db *DB) ShowCalendars(ctx context.Context, accountID string, shown []string) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE calendars SET shown = 0 WHERE account_id = ?`, accountID); err != nil {
			return err
		}
		for _, id := range shown {
			// Scoped in the SQL rather than checked first: an id read in one step
			// and written in the next is a window, and the account is the whole of
			// the permission here.
			if _, err := tx.ExecContext(ctx,
				`UPDATE calendars SET shown = 1 WHERE id = ? AND account_id = ?`,
				id, accountID); err != nil {
				return err
			}
		}
		// Events of a calendar nobody is looking at are dead weight in a table the
		// grid queries. They come back with the next sync if it is turned on again.
		_, err := tx.ExecContext(ctx, `
			DELETE FROM calendar_events WHERE calendar_id IN
			    (SELECT id FROM calendars WHERE account_id = ? AND shown = 0)`, accountID)
		return err
	})
}

// ReplaceCalendarEvents swaps one calendar's events inside a window.
//
// Replaced rather than merged, and no sync token. Google offers incremental sync,
// and it would save bytes verdande is not short of while costing the one thing it
// cannot afford: a token that has to be right, forever, for the grid not to be
// quietly wrong. A window that is deleted and rewritten cannot drift — an event
// somebody moved out of the window disappears because it is not in the answer, not
// because verdande remembered to delete it.
//
// Bounded by the window at both ends, so a sync of September does not throw away
// August.
func (db *DB) ReplaceCalendarEvents(ctx context.Context, calendarID, from, to string, events []CalendarEvent) error {
	return db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM calendar_events
			WHERE calendar_id = ? AND end_day >= ? AND start_day <= ?`,
			calendarID, from, to); err != nil {
			return err
		}
		for _, e := range events {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO calendar_events
				    (id, calendar_id, remote_id, summary, starts_at, ends_at,
				     start_day, end_day, all_day, location, url)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				NewID(), calendarID, e.RemoteID, e.Summary, e.StartsAt, e.EndsAt,
				e.StartDay, e.EndDay, e.AllDay, e.Location, e.URL); err != nil {
				return err
			}
		}
		return nil
	})
}

// CalendarEvents returns everything one person can see between two days,
// inclusive at both ends.
//
// Scoped to the caller in the join rather than checked afterwards: the events of
// somebody else's calendar are somebody else's, and a filter applied after the read
// is a filter that can be forgotten.
func (db *DB) CalendarEvents(ctx context.Context, userID, from, to string) ([]CalendarEvent, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT e.id, e.calendar_id, e.remote_id, e.summary, e.starts_at, e.ends_at,
		       e.start_day, e.end_day, e.all_day, e.location, e.url, c.name, c.colour
		FROM calendar_events e
		JOIN calendars c ON c.id = e.calendar_id
		JOIN calendar_accounts a ON a.id = c.account_id
		WHERE a.user_id = ? AND c.shown = 1 AND e.end_day >= ? AND e.start_day <= ?
		ORDER BY e.start_day, e.all_day DESC, e.starts_at`, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CalendarEvent
	for rows.Next() {
		var e CalendarEvent
		if err := rows.Scan(&e.ID, &e.CalendarID, &e.RemoteID, &e.Summary, &e.StartsAt,
			&e.EndsAt, &e.StartDay, &e.EndDay, &e.AllDay, &e.Location, &e.URL,
			&e.CalendarName, &e.Colour); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (db *DB) scanCalendarAccount(row scanner) (CalendarAccount, error) {
	var a CalendarAccount
	var expires, lastSync, created int64
	err := row.Scan(&a.ID, &a.UserID, &a.Provider, &a.Account, &a.RefreshToken,
		&a.AccessToken, &expires, &lastSync, &created)
	if err != nil {
		return a, err
	}
	for _, field := range []*string{&a.RefreshToken, &a.AccessToken} {
		plain, err := db.unsealValue(*field)
		if err != nil {
			return a, err
		}
		*field = plain
	}
	a.ExpiresAt = timeOrZero(expires)
	a.LastSyncAt = timeOrZero(lastSync)
	a.CreatedAt = time.Unix(created, 0)
	return a, nil
}
