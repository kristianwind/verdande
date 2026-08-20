// Package gcal reads a Google Calendar. It never writes one.
//
// The scope is calendar.readonly, and that is the whole of the decision: verdande
// already is a CalDAV server, so a Google event shown here is a second calendar
// laid over the first, not a copy Google and verdande now both have to agree
// about. Writing needs `calendar.events`, and it needs an answer to what happens
// when both sides moved the same meeting — which is a synchronisation model, not a
// scope. Read first; write later and to one calendar at a time.
//
// What the API gives back and what this returns are deliberately different shapes.
// Google answers with RFC 3339 instants for a timed event and bare dates for an
// all-day one, and with an *exclusive* end date for the second. A calendar grid
// asks one question — "which cells does this event cover" — so that is what an
// Event answers, in days, worked out once here rather than in every caller.
package gcal

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/google"
)

// Scope is read-only, and only the calendar.
//
// Google also offers calendar.calendarlist.readonly and calendar.events.readonly,
// which together are strictly less than this — they leave out access rules and
// account settings, which verdande has no use for. They are the tighter pair and
// worth moving to; they are also newer, and this one has been documented and
// accepted unchanged for a decade. A consent screen that fails with `invalid_scope`
// is not a place to be adventurous on somebody else's Google account.
const Scope = "https://www.googleapis.com/auth/calendar.readonly"

const defaultAPIBase = "https://www.googleapis.com/calendar/v3"

// ErrUnauthorized means the tokens are no longer good — access was revoked, or the
// password changed. Re-exported so a caller need not know the flow lives elsewhere.
var ErrUnauthorized = google.ErrUnauthorized

type Client struct {
	accessToken string
	// base is where the Calendar API is. A field rather than a constant so a test
	// can point it at a server it controls; empty is Google. There is no other way
	// to exercise a calendar that is slow, empty, or refuses.
	base string
	http *http.Client
}

func NewClient(accessToken string) *Client {
	return &Client{accessToken: accessToken, http: &http.Client{Timeout: 30 * time.Second}}
}

// At points the client at another server. Tests only; the zero value is Google.
func (c *Client) At(base string) *Client {
	c.base = base
	return c
}

func (c *Client) api() string {
	if c.base != "" {
		return c.base
	}
	return defaultAPIBase
}

// Calendar is one entry in the person's calendar list.
type Calendar struct {
	ID string
	// Name is the summary Google holds. A calendar somebody renamed for themselves
	// carries summaryOverride instead, and that is the name they will look for.
	Name string
	// Colour is Google's own hex value for the calendar, which is what makes two
	// calendars in one grid tellable apart. Their own colour rather than one picked
	// here: it is the colour they already know the calendar by.
	Colour   string
	TimeZone string
	Primary  bool
	// Owner means the connected account can write to it. Nothing writes today —
	// it is stored so the day something does, the interface knows which calendars
	// could even be offered.
	Owner bool
}

// Calendars lists what the connected account can see.
func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	var out []Calendar
	page := ""
	// Bounded rather than "until Google stops": a page token that never empties is
	// a loop that never ends, and the difference between a bug at Google and a
	// hung sync should not be this server hanging.
	for range 10 {
		v := url.Values{}
		v.Set("minAccessRole", "reader")
		v.Set("maxResults", "250")
		if page != "" {
			v.Set("pageToken", page)
		}

		var parsed struct {
			Items []struct {
				ID              string `json:"id"`
				Summary         string `json:"summary"`
				SummaryOverride string `json:"summaryOverride"`
				BackgroundColor string `json:"backgroundColor"`
				TimeZone        string `json:"timeZone"`
				Primary         bool   `json:"primary"`
				AccessRole      string `json:"accessRole"`
				Deleted         bool   `json:"deleted"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.get(ctx, c.api()+"/users/me/calendarList?"+v.Encode(), &parsed); err != nil {
			return nil, err
		}

		for _, item := range parsed.Items {
			if item.Deleted {
				continue
			}
			name := item.SummaryOverride
			if name == "" {
				name = item.Summary
			}
			out = append(out, Calendar{
				ID: item.ID, Name: name, Colour: item.BackgroundColor,
				TimeZone: item.TimeZone, Primary: item.Primary,
				Owner: item.AccessRole == "owner" || item.AccessRole == "writer",
			})
		}
		if parsed.NextPageToken == "" {
			break
		}
		page = parsed.NextPageToken
	}
	return out, nil
}

// Event is one occurrence, in the shape a calendar grid asks about.
type Event struct {
	ID      string
	Summary string
	// StartsAt and EndsAt are RFC 3339 for a timed event and empty for an all-day
	// one — an all-day event has no instant, only a day, and inventing midnight for
	// it is how an event moves a day every time a server changes time zone.
	StartsAt string
	EndsAt   string
	// StartDay and EndDay are the first and last day the event covers, inclusive at
	// both ends. Google's all-day end date is exclusive; that is corrected here,
	// once, rather than in every reader.
	StartDay string
	EndDay   string
	AllDay   bool
	Location string
	// URL opens the event in Google's own interface. Same reasoning as the Gmail
	// link on a task made from a mail: what verdande shows is a pointer to the
	// event, not a second copy of it that somebody could try to edit.
	URL string
}

// Events lists what falls inside a window, one calendar at a time.
//
// `singleEvents` expands a recurrence into its occurrences, which is the only form
// a grid can draw: an RRULE is an instruction, and a Tuesday cell needs the Tuesday
// it produced. It also means verdande does not have to grow a second recurrence
// engine that agrees with Google's about Danish summer time.
//
// from and to are dates, inclusive. The window is the caller's business; this
// turns it into the half-open interval Google wants.
func (c *Client) Events(ctx context.Context, calendarID, from, to string) ([]Event, error) {
	start, err := time.Parse(dateLayout, from)
	if err != nil {
		return nil, fmt.Errorf("gcal: from: %w", err)
	}
	end, err := time.Parse(dateLayout, to)
	if err != nil {
		return nil, fmt.Errorf("gcal: to: %w", err)
	}

	var out []Event
	page := ""
	for range 20 {
		v := url.Values{}
		v.Set("singleEvents", "true")
		v.Set("orderBy", "startTime")
		v.Set("maxResults", "2500")
		// UTC, and a day of slack at each end. An event at 23:30 on the last day in
		// Copenhagen is 21:30 UTC that day, but one at 00:30 on the first day is
		// 22:30 UTC the day *before* — so a window cut exactly at the requested
		// dates loses the events at both edges. The extra day is thrown away again
		// by the day fields below, which are computed from the event's own offset.
		v.Set("timeMin", start.AddDate(0, 0, -1).Format(time.RFC3339))
		v.Set("timeMax", end.AddDate(0, 0, 2).Format(time.RFC3339))
		if page != "" {
			v.Set("pageToken", page)
		}

		var parsed struct {
			Items         []rawEvent `json:"items"`
			NextPageToken string     `json:"nextPageToken"`
		}
		url := c.api() + "/calendars/" + url.PathEscape(calendarID) + "/events?" + v.Encode()
		if err := c.get(ctx, url, &parsed); err != nil {
			return nil, err
		}

		for _, item := range parsed.Items {
			// A cancelled occurrence of a recurring event still comes back, so that
			// a client keeping its own copy knows to drop it. verdande replaces the
			// window wholesale, so it only has to not add it.
			if item.Status == "cancelled" {
				continue
			}
			event, ok := item.event()
			if !ok {
				continue
			}
			if event.EndDay < from || event.StartDay > to {
				continue // the slack above, thrown away
			}
			out = append(out, event)
		}
		if parsed.NextPageToken == "" {
			break
		}
		page = parsed.NextPageToken
	}
	return out, nil
}

const dateLayout = "2006-01-02"

type rawEvent struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	Location string `json:"location"`
	HTMLLink string `json:"htmlLink"`
	Start    when   `json:"start"`
	End      when   `json:"end"`
}

type when struct {
	Date     string `json:"date"`
	DateTime string `json:"dateTime"`
}

// event turns Google's two shapes into the one a grid can read.
//
// The second return is false for an event with neither a date nor a dateTime —
// which Google does return, for an event somebody is still being asked about. It
// belongs on no day, so there is no cell to draw it in.
func (r rawEvent) event() (Event, bool) {
	e := Event{
		ID: r.ID, Summary: strings.TrimSpace(r.Summary),
		Location: r.Location, URL: r.HTMLLink,
	}
	if e.Summary == "" {
		// Google's own interface says this for an event with no title, and a blank
		// chip in a grid reads as a rendering fault rather than as an empty title.
		e.Summary = "(uden titel)"
	}

	switch {
	case r.Start.Date != "":
		e.AllDay = true
		e.StartDay = r.Start.Date
		// Google's all-day end is the day *after* the last one, so a one-day event
		// ends tomorrow. Subtracting a day is what makes "covers this cell" a
		// comparison rather than a special case at every call site.
		e.EndDay = r.Start.Date
		if day, err := time.Parse(dateLayout, r.End.Date); err == nil {
			if last := day.AddDate(0, 0, -1); !last.Before(mustDay(r.Start.Date)) {
				e.EndDay = last.Format(dateLayout)
			}
		}
	case r.Start.DateTime != "":
		e.StartsAt = r.Start.DateTime
		e.EndsAt = r.End.DateTime
		// The day is the date part of the timestamp Google returned, which carries
		// the event's own offset. Parsing it into a time.Time and formatting it
		// back would ask *this process* what day it is — and a container running in
		// UTC would then move a 23:30 meeting in Copenhagen to the day before.
		e.StartDay = dayOf(r.Start.DateTime)
		e.EndDay = dayOf(r.End.DateTime)
		if e.EndDay == "" {
			e.EndDay = e.StartDay
		}
		// An event ending at midnight ends the evening before, as far as a grid is
		// concerned. Without this, every meeting that runs to 24:00 puts a chip in
		// the following day as well.
		if e.EndDay > e.StartDay && strings.Contains(r.End.DateTime, "T00:00:00") {
			if day, err := time.Parse(dateLayout, e.EndDay); err == nil {
				e.EndDay = day.AddDate(0, 0, -1).Format(dateLayout)
			}
		}
	default:
		return Event{}, false
	}
	if e.EndDay < e.StartDay {
		e.EndDay = e.StartDay
	}
	return e, true
}

// dayOf takes the date out of an RFC 3339 timestamp without interpreting it.
func dayOf(stamp string) string {
	if len(stamp) < len(dateLayout) {
		return ""
	}
	day := stamp[:len(dateLayout)]
	if _, err := time.Parse(dateLayout, day); err != nil {
		return ""
	}
	return day
}

func mustDay(s string) time.Time {
	day, _ := time.Parse(dateLayout, s)
	return day
}

func (c *Client) get(ctx context.Context, url string, out any) error {
	return google.Get(ctx, c.http, c.accessToken, url, out)
}
