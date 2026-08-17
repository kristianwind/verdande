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
