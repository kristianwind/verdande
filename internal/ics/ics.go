// Package ics writes iCalendar documents.
//
// Tasks go out as VTODO, not VEVENT. A to-do has a due date and a completion state;
// an event has a start and an end and neither of those things. Apple Reminders and
// Thunderbird read VTODO natively, and it is the same component the CalDAV server
// will serve later — so a task exported today is the task synchronised tomorrow,
// with no second representation to keep in step.
//
// Google Calendar is the awkward exception: it subscribes to ICS but ignores VTODO
// entirely. Tasks that carry a clock time are therefore also emitted as VEVENT,
// which is what makes a feed useful in the calendar most people actually keep.
package ics

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Task is the subset of a task a calendar needs. Defined here rather than importing
// the store's type, so this package stays a pure formatter with no database in it.
type Task struct {
	ID          string
	Content     string
	Description string
	ProjectName string
	DueDate     string // "YYYY-MM-DD"
	DueDatetime *time.Time
	DurationMin *int
	Priority    int
	Recurrence  string
	Completed   bool
	CompletedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Calendar struct {
	Name string
	// Domain appears in every UID. It has to be stable for the lifetime of the
	// feed: a client that sees a new UID for the same task treats it as a new item
	// and leaves the old one behind as a duplicate.
	Domain string
	Tasks  []Task
}

// Render writes the whole document.
func Render(cal Calendar) string {
	var b strings.Builder

	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	b.WriteString("PRODID:-//verdande//verdande//DA\r\n")
	b.WriteString("CALSCALE:GREGORIAN\r\n")
	b.WriteString("METHOD:PUBLISH\r\n")
	writeLine(&b, "X-WR-CALNAME:"+escape(cal.Name))
	// Tells a subscribing client how often to poll. Without it Google refetches on
	// its own schedule, which can be many hours.
	b.WriteString("X-PUBLISHED-TTL:PT1H\r\n")
	b.WriteString("REFRESH-INTERVAL;VALUE=DURATION:PT1H\r\n")

	for _, t := range cal.Tasks {
		writeTodo(&b, t, cal.Domain)
		// A dated task also goes out as an event, for clients that ignore VTODO.
		if t.DueDatetime != nil && !t.Completed {
			writeEvent(&b, t, cal.Domain)
		}
	}

	b.WriteString("END:VCALENDAR\r\n")
	return b.String()
}

func writeTodo(b *strings.Builder, t Task, domain string) {
	b.WriteString("BEGIN:VTODO\r\n")
	writeLine(b, "UID:"+t.ID+"@"+domain)
	writeLine(b, "DTSTAMP:"+utc(t.UpdatedAt))
	writeLine(b, "CREATED:"+utc(t.CreatedAt))
	writeLine(b, "SUMMARY:"+escape(t.Content))

	if t.Description != "" {
		writeLine(b, "DESCRIPTION:"+escape(t.Description))
	}
	if t.ProjectName != "" {
		writeLine(b, "CATEGORIES:"+escape(t.ProjectName))
	}

	switch {
	case t.DueDatetime != nil:
		writeLine(b, "DUE:"+utc(*t.DueDatetime))
	case t.DueDate != "":
		// A whole-day due date is a DATE, not a DATE-TIME. Emitting midnight in
		// some timezone instead would move the task a day for anybody east or
		// west of the server.
		writeLine(b, "DUE;VALUE=DATE:"+strings.ReplaceAll(t.DueDate, "-", ""))
	}

	if t.Recurrence != "" {
		writeLine(b, "RRULE:"+strings.TrimPrefix(t.Recurrence, "RRULE:"))
	}

	// RFC 5545 priority runs 1 (highest) to 9, with 0 meaning undefined. verdande's
	// P4 is "no priority stated" rather than "lowest", so it maps to 0 — otherwise
	// every unprioritised task arrives in a client marked explicitly low.
	switch t.Priority {
	case 1:
		writeLine(b, "PRIORITY:1")
	case 2:
		writeLine(b, "PRIORITY:5")
	case 3:
		writeLine(b, "PRIORITY:7")
	default:
		writeLine(b, "PRIORITY:0")
	}

	if t.Completed {
		writeLine(b, "STATUS:COMPLETED")
		writeLine(b, "PERCENT-COMPLETE:100")
		if t.CompletedAt != nil {
			writeLine(b, "COMPLETED:"+utc(*t.CompletedAt))
		}
	} else {
		writeLine(b, "STATUS:NEEDS-ACTION")
	}

	b.WriteString("END:VTODO\r\n")
}

func writeEvent(b *strings.Builder, t Task, domain string) {
	start := *t.DueDatetime
	// Without a stated duration a task is shown as half an hour. A zero-length
	// event collapses to an invisible sliver in most calendar grids.
	minutes := 30
	if t.DurationMin != nil && *t.DurationMin > 0 {
		minutes = *t.DurationMin
	}
	end := start.Add(time.Duration(minutes) * time.Minute)

	b.WriteString("BEGIN:VEVENT\r\n")
	// A distinct UID suffix: the event and the to-do describe the same task, and
	// sharing a UID would make a client treat them as two versions of one item.
	writeLine(b, "UID:"+t.ID+"-event@"+domain)
	writeLine(b, "DTSTAMP:"+utc(t.UpdatedAt))
	writeLine(b, "DTSTART:"+utc(start))
	writeLine(b, "DTEND:"+utc(end))
	writeLine(b, "SUMMARY:"+escape(t.Content))
	if t.Description != "" {
		writeLine(b, "DESCRIPTION:"+escape(t.Description))
	}
	if t.Recurrence != "" {
		writeLine(b, "RRULE:"+strings.TrimPrefix(t.Recurrence, "RRULE:"))
	}
	writeLine(b, "TRANSP:TRANSPARENT")
	b.WriteString("END:VEVENT\r\n")
}

func utc(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// escape applies RFC 5545 text escaping. A comma or semicolon in a task title is
// a value separator in iCalendar, so an unescaped one silently truncates the
// summary at that character in every client.
func escape(s string) string {
	r := strings.NewReplacer(
		"\\", "\\\\",
		";", "\\;",
		",", "\\,",
		"\n", "\\n",
		"\r", "",
	)
	return r.Replace(s)
}

// writeLine folds at 75 octets, as the spec requires, with a space beginning each
// continuation. Folding is by byte with a check for UTF-8 continuation bytes: a
// break in the middle of "ø" produces a line no parser can read.
func writeLine(b *strings.Builder, line string) {
	const limit = 73 // 75 minus CRLF

	if len(line) <= limit {
		b.WriteString(line)
		b.WriteString("\r\n")
		return
	}

	for i := 0; i < len(line); {
		end := i + limit
		if i > 0 {
			end = i + limit - 1 // the leading space on a continuation counts
		}
		if end > len(line) {
			end = len(line)
		}
		// Never split a multi-byte character.
		for end > i && end < len(line) && line[end]&0xC0 == 0x80 {
			end--
		}
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(line[i:end])
		b.WriteString("\r\n")
		i = end
	}
}

// Filename is what a browser saves the feed as.
func Filename(name string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		case r == ' ', r == '_':
			return '-'
		}
		return -1
	}, name)
	if safe == "" {
		safe = "verdande"
	}
	return fmt.Sprintf("%s.ics", safe)
}

// --- reading ----------------------------------------------------------------------

// Parsed is what a client's VTODO says, reduced to the fields verdande stores.
type Parsed struct {
	UID         string
	Summary     string
	Description string
	DueDate     string
	DueDatetime *time.Time
	Priority    int
	Recurrence  string
	Completed   bool
}

// ParseVTODO reads a calendar object sent by a CalDAV client.
//
// Deliberately forgiving: this is the input side of a protocol spoken by Apple
// Reminders, Thunderbird and a long tail of phone apps, each with its own habits
// about folding, parameters and which properties it bothers to send. Anything not
// understood is ignored rather than refused, because refusing a PUT makes a client
// show a sync error and stop — losing the edit the person just made.
func ParseVTODO(body string) (Parsed, error) {
	var p Parsed
	inTodo := false

	for _, line := range unfold(body) {
		name, params, value := splitLine(line)

		switch strings.ToUpper(name) {
		case "BEGIN":
			if strings.EqualFold(value, "VTODO") {
				inTodo = true
			}
		case "END":
			if strings.EqualFold(value, "VTODO") {
				inTodo = false
			}
		}
		if !inTodo {
			continue
		}

		switch strings.ToUpper(name) {
		case "UID":
			p.UID = value
		case "SUMMARY":
			p.Summary = unescape(value)
		case "DESCRIPTION":
			p.Description = unescape(value)
		case "RRULE":
			p.Recurrence = value
		case "STATUS":
			p.Completed = strings.EqualFold(value, "COMPLETED")
		case "PERCENT-COMPLETE":
			if value == "100" {
				p.Completed = true
			}
		case "COMPLETED":
			p.Completed = true
		case "PRIORITY":
			// RFC 5545 runs 1..9 with 0 meaning undefined; verdande runs 1..4 with
			// 4 meaning none. The bands here are the inverse of what the writer does.
			switch n, _ := strconv.Atoi(value); {
			case n == 0:
				p.Priority = 4
			case n <= 2:
				p.Priority = 1
			case n <= 5:
				p.Priority = 2
			case n <= 7:
				p.Priority = 3
			default:
				p.Priority = 4
			}
		case "DUE":
			if strings.Contains(strings.ToUpper(params), "VALUE=DATE") || len(value) == 8 {
				if t, err := time.Parse("20060102", value); err == nil {
					p.DueDate = t.Format("2006-01-02")
				}
				continue
			}
			for _, layout := range []string{"20060102T150405Z", "20060102T150405"} {
				if t, err := time.Parse(layout, value); err == nil {
					utc := t.UTC()
					p.DueDate = utc.Format("2006-01-02")
					p.DueDatetime = &utc
					break
				}
			}
		}
	}

	if p.Priority == 0 {
		p.Priority = 4
	}
	if p.Summary == "" && p.UID == "" {
		return p, fmt.Errorf("ics: no VTODO found")
	}
	return p, nil
}

// unfold rejoins continuation lines, which the spec folds at 75 octets by beginning
// the next line with a space or tab.
func unfold(body string) []string {
	raw := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")

	var out []string
	for _, line := range raw {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && len(out) > 0 {
			out[len(out)-1] += line[1:]
			continue
		}
		out = append(out, line)
	}
	return out
}

// splitLine separates NAME;PARAMS:VALUE. The parameters can themselves contain a
// colon inside quotes, so the split is on the first colon outside quotes.
func splitLine(line string) (name, params, value string) {
	inQuotes := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuotes = !inQuotes
		case ':':
			if inQuotes {
				continue
			}
			left := line[:i]
			value = line[i+1:]
			if semi := strings.Index(left, ";"); semi >= 0 {
				return left[:semi], left[semi+1:], value
			}
			return left, "", value
		}
	}
	return line, "", ""
}

func unescape(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\N`, "\n", `\,`, ",", `\;`, ";", `\\`, `\`)
	return r.Replace(s)
}

// --- reading a whole calendar ------------------------------------------------------

// Event is one VEVENT, reduced to what a grid needs — the same shape the Google
// side already stores, so the two arrive in the database identically.
//
// Days at both ends, inclusive, because that is the question a grid asks: which
// cells does this cover. StartsAt keeps the stamp as the file wrote it, offset and
// all: it is what the chip shows, and reparsing it here would ask *this process*
// what time zone it is in.
type Event struct {
	UID      string
	Summary  string
	Location string
	URL      string
	AllDay   bool
	StartsAt string // RFC3339, empty for an all-day event
	EndsAt   string
	StartDay string // YYYY-MM-DD
	EndDay   string
}

// Subscribed is what an .ics file somebody subscribed to says.
//
// Its own name rather than `Calendar`: that one is the *writing* side — what
// verdande publishes as a feed — and the two are different documents that happen
// to share a format. One name for both would be a promise that a round trip
// through this package is lossless, which it is not and does not need to be.
type Subscribed struct {
	Name     string
	TimeZone string
	Events   []Event
}

// ParseCalendar reads an .ics file that somebody subscribed to.
//
// Forgiving in the same way ParseVTODO is, and for a sharper reason: this file was
// written by somebody else's software and is fetched on a schedule with nobody
// watching. One property this does not understand must not cost the other four
// hundred events in the file — so anything unrecognised is skipped, and an event
// without the two things a grid cannot do without (a uid and a start) is dropped
// rather than stored half-made.
//
// # What it deliberately does not do
//
// **Recurrence is not expanded.** RRULE in a subscribed calendar means "and then
// every Tuesday until 2031", and expanding it here would mean writing a year of
// rows per rule and re-deciding what happens when the file changes. A recurring
// event is stored as its first occurrence, which is honest — it is what the file
// says on its own terms — and the day it matters, the answer is an expander shared
// with `internal/recurrence`, not a second one written here.
//
// **Time zones are not resolved.** A DTSTART with TZID=Europe/Copenhagen is stored
// with the offset the file carries or, when it carries none, as a local stamp. The
// alternative is shipping a tz database and deciding what to do when the file
// names a zone this build has never heard of. The day that matters, it is one
// place.
func ParseCalendar(body string) Subscribed {
	var cal Subscribed
	var current *Event
	depth := 0

	for _, line := range unfold(body) {
		name, params, value := splitLine(line)
		upper := strings.ToUpper(name)

		switch upper {
		case "BEGIN":
			if strings.EqualFold(value, "VEVENT") {
				current = &Event{}
			}
			depth++
			continue
		case "END":
			depth--
			if strings.EqualFold(value, "VEVENT") && current != nil {
				if current.UID != "" && current.StartDay != "" {
					cal.Events = append(cal.Events, *current)
				}
				current = nil
			}
			continue
		}

		if current == nil {
			// Calendar-level properties. X-WR-CALNAME is what every publisher uses
			// to say what the calendar is called; there is no standard property for
			// it, which is why a non-standard one is read here.
			switch upper {
			case "X-WR-CALNAME":
				cal.Name = unescape(value)
			case "X-WR-TIMEZONE":
				cal.TimeZone = value
			}
			continue
		}

		switch upper {
		case "UID":
			current.UID = value
		case "SUMMARY":
			current.Summary = unescape(value)
		case "LOCATION":
			current.Location = unescape(value)
		case "URL":
			current.URL = value
		case "DTSTART":
			current.AllDay = isDateOnly(params, value)
			current.StartDay, current.StartsAt = readStamp(params, value)
		case "DTEND":
			day, at := readStamp(params, value)
			current.EndsAt = at
			// DTEND is exclusive for an all-day event: a one-day event ends on the
			// next morning. Stored inclusive, or every all-day event would cover one
			// cell too many — and a public holiday would eat the day after it.
			if current.AllDay {
				day = shiftDay(day, -1)
			}
			current.EndDay = day
		}
	}

	// A missing DTEND means the event ends when it starts. Filled after the loop
	// rather than inside it, because DTEND may arrive before DTSTART in a file
	// nobody promised would be in order.
	for i := range cal.Events {
		if cal.Events[i].EndDay == "" || cal.Events[i].EndDay < cal.Events[i].StartDay {
			cal.Events[i].EndDay = cal.Events[i].StartDay
		}
	}
	return cal
}

// isDateOnly reports whether a stamp is a day rather than a moment. VALUE=DATE
// says so outright; an eight-character value says it by being one.
func isDateOnly(params, value string) bool {
	if strings.Contains(strings.ToUpper(params), "VALUE=DATE") {
		return true
	}
	return len(value) == 8 && !strings.ContainsAny(value, "T")
}

// readStamp returns the day and, for a timed event, the moment as RFC3339.
//
// Three shapes exist in the wild: 20260821 (a day), 20260821T140000Z (UTC), and
// 20260821T140000 with a TZID parameter (a local time in a named zone). The third
// is kept as written, without a zone: resolving it needs a tz database, and a
// stamp that says 14:00 is closer to the truth than one this process has moved.
func readStamp(params, value string) (day, at string) {
	value = strings.TrimSpace(value)
	if len(value) < 8 {
		return "", ""
	}
	day = value[0:4] + "-" + value[4:6] + "-" + value[6:8]
	if len(value) < 15 || value[8] != 'T' {
		return day, ""
	}

	clock := value[9:11] + ":" + value[11:13] + ":" + value[13:15]
	switch {
	case strings.HasSuffix(value, "Z"):
		return day, day + "T" + clock + "Z"
	default:
		// A local stamp, with or without a TZID. Written without an offset, which
		// is what the interface already treats as "the clock the file said".
		return day, day + "T" + clock
	}
}

// shiftDay moves a YYYY-MM-DD by whole days.
func shiftDay(day string, by int) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, by).Format("2006-01-02")
}
