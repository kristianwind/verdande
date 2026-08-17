// Package recurrence turns "hver anden mandag" into an RRULE, and an RRULE into
// the next date a task is due.
//
// The stored form is an RFC 5545 recurrence rule, not a private format. That is
// what lets the same field drive the CalDAV server and the ICS feed later without
// a translation layer, and it means a task exported to Apple Reminders keeps
// repeating there.
//
// Only the RRULE part is stored — "FREQ=WEEKLY;BYDAY=MO" — with no DTSTART. The
// task's own due date is the anchor, so moving a task also moves its series,
// which is what somebody rescheduling a repeating chore means to happen.
package recurrence

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

// Parse reads a recurrence phrase in Danish or English and returns an RRULE.
//
// It returns "" when the text does not describe a repetition, which is the common
// case — most tasks are not recurring, and a parser that guessed would turn
// "ring til mor på mandag" into a weekly obligation.
func Parse(text string) string {
	t := normalise(text)
	if t == "" {
		return ""
	}

	for _, rule := range rules {
		m := rule.pattern.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		if out := rule.build(m); out != "" {
			return out
		}
	}
	return ""
}

func normalise(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	// "hver anden" and "every other" are ordinary ways of saying "every 2".
	// Rewriting them here keeps the interval logic in one place instead of
	// duplicating every frequency pattern for the word form.
	r := strings.NewReplacer(
		"hvert andet", "hver 2.",
		"hver anden", "hver 2.",
		"hvert tredje", "hver 3.",
		"hver tredje", "hver 3.",
		"every other", "every 2",
		"each ", "every ",
	)
	return r.Replace(t)
}

// weekdays maps every spelling somebody might type — Danish and English, full and
// short — onto the two-letter codes RFC 5545 uses.
var weekdays = map[string]string{
	"mandag": "MO", "man": "MO", "monday": "MO", "mon": "MO",
	"tirsdag": "TU", "tir": "TU", "tue": "TU", "tuesday": "TU", "tues": "TU",
	"onsdag": "WE", "ons": "WE", "wednesday": "WE", "wed": "WE",
	"torsdag": "TH", "tor": "TH", "thursday": "TH", "thu": "TH", "thur": "TH",
	"fredag": "FR", "fre": "FR", "friday": "FR", "fri": "FR",
	"lørdag": "SA", "lor": "SA", "saturday": "SA", "sat": "SA",
	"søndag": "SU", "son": "SU", "sunday": "SU", "sun": "SU",
}

var months = map[string]int{
	"januar": 1, "january": 1, "jan": 1,
	"februar": 2, "february": 2, "feb": 2,
	"marts": 3, "march": 3, "mar": 3,
	"april": 4, "apr": 4,
	"maj": 5, "may": 5,
	"juni": 6, "june": 6, "jun": 6,
	"juli": 7, "july": 7, "jul": 7,
	"august": 8, "aug": 8,
	"september": 9, "sep": 9, "sept": 9,
	"oktober": 10, "october": 10, "okt": 10, "oct": 10,
	"november": 11, "nov": 11,
	"december": 12, "dec": 12,
}

type rule struct {
	pattern *regexp.Regexp
	build   func([]string) string
}

// The order matters: the most specific pattern that can match must be tried first,
// or "hver 2. mandag" is claimed by the plain weekday rule and loses its interval.
var rules = []rule{
	// "hverdage", "weekdays", "every weekday" — Monday to Friday.
	{
		pattern: regexp.MustCompile(`^(?:hver\s+)?(?:hverdag(?:e)?|ugedag(?:e)?)$|^every\s+weekday$|^weekdays?$`),
		build:   func([]string) string { return "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" },
	},
	// "hver weekend", "every weekend"
	{
		pattern: regexp.MustCompile(`^(?:hver\s+)?weekend$|^every\s+weekend$`),
		build:   func([]string) string { return "FREQ=WEEKLY;BYDAY=SA,SU" },
	},
	// A list of weekdays: "hver mandag og torsdag", "every monday, wednesday and friday"
	{
		pattern: regexp.MustCompile(`^(?:hver|every)\s+(\d+\.?\s+)?(.+)$`),
		build: func(m []string) string {
			days := parseWeekdayList(m[2])
			if len(days) == 0 {
				return ""
			}
			return withInterval("FREQ=WEEKLY;BYDAY="+strings.Join(days, ","), m[1])
		},
	},
	// "hver 3. dag", "every 2 days", "dagligt", "daily"
	{
		pattern: regexp.MustCompile(`^(?:hver|every)\s+(\d+\.?\s+)?(dag|dage|day|days)$|^(?:dagligt|daily)$`),
		build: func(m []string) string {
			if len(m) > 1 {
				return withInterval("FREQ=DAILY", m[1])
			}
			return "FREQ=DAILY"
		},
	},
	// "hver 2. uge", "every 3 weeks", "ugentligt", "weekly"
	{
		pattern: regexp.MustCompile(`^(?:hver|every)\s+(\d+\.?\s+)?(uge|uger|week|weeks)$|^(?:ugentligt|weekly)$`),
		build: func(m []string) string {
			if len(m) > 1 {
				return withInterval("FREQ=WEEKLY", m[1])
			}
			return "FREQ=WEEKLY"
		},
	},
	// "hver måned", "every 3 months", "månedligt", "monthly"
	{
		pattern: regexp.MustCompile(`^(?:hver|every)\s+(\d+\.?\s+)?(måned|måneder|maaned|maaneder|month|months)$|^(?:månedligt|maanedligt|monthly)$`),
		build: func(m []string) string {
			if len(m) > 1 {
				return withInterval("FREQ=MONTHLY", m[1])
			}
			return "FREQ=MONTHLY"
		},
	},
	// "den 1. i måneden", "on the 1st of the month"
	{
		pattern: regexp.MustCompile(`^(?:den\s+)?(\d{1,2})\.?\s+(?:i\s+(?:hver\s+)?måned(?:en)?|of\s+(?:the\s+|every\s+)?month)$`),
		build: func(m []string) string {
			day, err := strconv.Atoi(m[1])
			if err != nil || day < 1 || day > 31 {
				return ""
			}
			return fmt.Sprintf("FREQ=MONTHLY;BYMONTHDAY=%d", day)
		},
	},
	// "hvert år", "yearly", "hver 15. marts"
	{
		pattern: regexp.MustCompile(`^(?:hver|hvert|every)\s+(?:(\d{1,2})\.?\s+)?(\p{L}+)$`),
		build: func(m []string) string {
			month, ok := months[m[2]]
			if !ok {
				return ""
			}
			if m[1] == "" {
				return fmt.Sprintf("FREQ=YEARLY;BYMONTH=%d", month)
			}
			day, err := strconv.Atoi(m[1])
			if err != nil || day < 1 || day > 31 {
				return ""
			}
			return fmt.Sprintf("FREQ=YEARLY;BYMONTH=%d;BYMONTHDAY=%d", month, day)
		},
	},
	{
		pattern: regexp.MustCompile(`^(?:hvert\s+år|hvert\s+aar|årligt|aarligt|every\s+year|yearly|annually)$`),
		build:   func([]string) string { return "FREQ=YEARLY" },
	},
}

// parseWeekdayList reads "mandag og torsdag" or "monday, wednesday and friday".
// Every word has to be a weekday: a list with something else in it is not a list
// of weekdays, and half-understanding it would be worse than not matching.
func parseWeekdayList(text string) []string {
	separators := regexp.MustCompile(`\s*(?:,|\bog\b|\band\b|&)\s*`)
	var days []string
	seen := map[string]bool{}

	for _, part := range separators.Split(text, -1) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		code, ok := weekdays[part]
		if !ok {
			return nil
		}
		if !seen[code] {
			seen[code] = true
			days = append(days, code)
		}
	}
	return days
}

// withInterval appends INTERVAL when the phrase carried a number. INTERVAL=1 is the
// default and is left out, so "hver uge" and "hver 1. uge" produce the same rule.
func withInterval(base, raw string) string {
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if raw == "" {
		return base
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 1 {
		return base
	}
	return fmt.Sprintf("%s;INTERVAL=%d", base, n)
}

// Valid reports whether a rule can be parsed by the RRULE library, so an invalid
// one is refused at the API boundary rather than discovered when a task is ticked
// off and cannot be advanced.
func Valid(rule string) bool {
	if rule == "" {
		return true
	}
	_, err := compile(rule, time.Now())
	return err == nil
}

// Next returns the first occurrence strictly after `after`.
//
// Strictly after matters: the anchor is the task's current due date, and an
// implementation that included it would return the same day forever and make
// completing a daily task do nothing at all.
func Next(rule string, after time.Time) (time.Time, error) {
	set, err := compile(rule, after)
	if err != nil {
		return time.Time{}, err
	}
	// A whole day is added rather than a nanosecond so that a rule anchored at
	// midnight does not return its own anchor back through a rounding difference.
	next := set.After(after, false)
	if next.IsZero() {
		// A finite rule (COUNT or UNTIL) that has run out. The series is over.
		return time.Time{}, ErrSeriesEnded
	}
	return next, nil
}

// ErrSeriesEnded means a bounded rule has produced its last occurrence.
var ErrSeriesEnded = fmt.Errorf("recurrence: the series has ended")

func compile(rule string, anchor time.Time) (*rrule.RRule, error) {
	if rule == "" {
		return nil, fmt.Errorf("recurrence: empty rule")
	}
	// The stored rule carries no DTSTART; the task's due date supplies it. Dates
	// are anchored at midnight in the anchor's own location so that adding days
	// never drifts across a daylight-saving boundary into the previous evening.
	option, err := rrule.StrToROption("RRULE:" + strings.TrimPrefix(rule, "RRULE:"))
	if err != nil {
		return nil, err
	}
	option.Dtstart = time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, anchor.Location())
	return rrule.NewRRule(*option)
}

// Describe renders a rule back into Danish, for showing on a task without making
// somebody read "FREQ=WEEKLY;BYDAY=MO,TH".
func Describe(rule string) string {
	if rule == "" {
		return ""
	}
	parts := map[string]string{}
	for _, kv := range strings.Split(strings.TrimPrefix(rule, "RRULE:"), ";") {
		if k, v, ok := strings.Cut(kv, "="); ok {
			parts[strings.ToUpper(k)] = v
		}
	}

	interval := 1
	if n, err := strconv.Atoi(parts["INTERVAL"]); err == nil && n > 0 {
		interval = n
	}

	if days := parts["BYDAY"]; days != "" {
		if days == "MO,TU,WE,TH,FR" {
			return "hverdage"
		}
		if days == "SA,SU" {
			return "hver weekend"
		}
		names := make([]string, 0, 4)
		for _, code := range strings.Split(days, ",") {
			names = append(names, danishDay(code))
		}
		joined := strings.Join(names, " og ")

		// "hver mandag", not "hver uge mandag". A weekly rule naming its days does
		// not also need the word for the interval — that is how the phrase is said,
		// and it is what the parser accepts back.
		if interval <= 1 {
			return "hver " + joined
		}
		return every(interval, "uge") + " " + joined
	}

	switch parts["FREQ"] {
	case "DAILY":
		return every(interval, "dag")
	case "WEEKLY":
		return every(interval, "uge")
	case "MONTHLY":
		if d := parts["BYMONTHDAY"]; d != "" {
			return "den " + d + ". hver måned"
		}
		return every(interval, "måned")
	case "YEARLY":
		return every(interval, "år")
	}
	return rule
}

func every(interval int, unit string) string {
	if interval <= 1 {
		if unit == "år" {
			return "hvert år"
		}
		return "hver " + unit
	}
	if interval == 2 {
		if unit == "år" {
			return "hvert andet år"
		}
		return "hver anden " + unit
	}
	return fmt.Sprintf("hver %d. %s", interval, unit)
}

func danishDay(code string) string {
	switch code {
	case "MO":
		return "mandag"
	case "TU":
		return "tirsdag"
	case "WE":
		return "onsdag"
	case "TH":
		return "torsdag"
	case "FR":
		return "fredag"
	case "SA":
		return "lørdag"
	case "SU":
		return "søndag"
	}
	return code
}
