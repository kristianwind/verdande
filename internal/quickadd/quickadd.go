// Package quickadd turns one line of typed text into a task.
//
//	"betal moms i morgen kl 10 p1 #Firma @regnskab"
//	→ content "betal moms", due tomorrow 10:00, priority 1, project Firma, label regnskab
//
// Danish and English are parsed together rather than behind a locale switch. People
// mix them mid-sentence — "ring til Anders on friday" is a real thing a Dane types —
// and a parser that understood only the configured language would drop half of it.
// Locale is used only to break genuine ties, of which there is currently one: the
// meaning of a bare "10" after "at".
//
// Everything the parser recognises is reported as a Span as well as a value, so the
// input box can highlight the parts it understood while they are being typed. That
// feedback is the whole point: a parser you cannot see working is one you stop
// trusting the moment it is wrong once.
package quickadd

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Kind names what a Span was recognised as.
type Kind string

const (
	KindDate     Kind = "date"
	KindTime     Kind = "time"
	KindPriority Kind = "priority"
	KindProject  Kind = "project"
	KindLabel    Kind = "label"
)

// Span is a byte range of the input that was consumed, and what it was read as.
type Span struct {
	Start int  `json:"start"`
	End   int  `json:"end"`
	Kind  Kind `json:"kind"`
}

// Result is everything one line of input turned into.
type Result struct {
	// Content is the input with every recognised span removed and whitespace
	// tidied — the task's actual title.
	Content string `json:"content"`
	// Project is the name written after '#', not an id: the caller resolves it
	// against the user's projects and decides what to do when it matches nothing.
	Project string   `json:"project,omitempty"`
	Labels  []string `json:"labels,omitempty"`
	// Priority is 1 (highest) to 4. 4 is both the default and "none stated".
	Priority int `json:"priority"`
	// DueDate is a calendar day, "YYYY-MM-DD". Empty when no date was written.
	DueDate string `json:"due_date,omitempty"`
	// DueTime is "HH:MM" in 24-hour form, set only when a clock time was written.
	// A time with no date means today, and Parse fills the date in.
	DueTime string `json:"due_time,omitempty"`
	Spans   []Span `json:"spans,omitempty"`
}

// Parse reads input as of the moment `now`, which is also the timezone dates are
// resolved in. Locale is "da" or "en" and only affects bare hour readings.
//
// Parse never fails. Anything it does not recognise stays in Content, which is the
// only behaviour that makes sense for a box people type into quickly: a task with a
// clumsy title beats an error message and a lost thought.
func Parse(input string, now time.Time, locale string) Result {
	res := Result{Priority: 4}
	var spans []Span

	// Sigils first. '#' and '@' are unambiguous, and consuming them stops a project
	// called "Fredag" from later being read as a weekday.
	res.Project, spans = extractProject(input, spans)
	res.Labels, spans = extractLabels(input, spans)
	res.Priority, spans = extractPriority(input, spans)

	// Each stage reads a copy with the earlier stages' matches blanked out, so no
	// two of them can claim the same characters. Without this, "kl 10.12" is both
	// a time and — to a date parser reading the raw line — the 10th of December.
	// mask keeps byte offsets intact, so spans still point into the original input.
	masked := mask(input, spans)

	// Time before date, so "kl 15.30" is claimed as a clock time before the date
	// parser can offer to read "15.3" as the 15th of March.
	hour, minute, hasTime, spans := extractTime(masked, spans, locale)
	date, hasDate, spans := extractDate(mask(masked, spans), spans, now)

	switch {
	case hasDate:
		res.DueDate = date.Format("2006-01-02")
	case hasTime:
		// A time on its own means today — unless that moment has already passed,
		// in which case the person means tomorrow. "kl 8" typed at nine in the
		// evening is not a request to schedule something twelve hours ago.
		day := now
		if hour < now.Hour() || (hour == now.Hour() && minute <= now.Minute()) {
			day = now.AddDate(0, 0, 1)
		}
		res.DueDate = day.Format("2006-01-02")
	}
	if hasTime {
		res.DueTime = pad2(hour) + ":" + pad2(minute)
	}

	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	res.Spans = spans
	res.Content = remove(input, spans)
	return res
}

// --- sigils -----------------------------------------------------------------

// A project or label name runs until whitespace, or is quoted when it contains
// spaces: #"Q3 rapport". Unicode letters are matched via \p{L} so "#Økonomi" works.
var (
	reProject = regexp.MustCompile(`(?:^|\s)#(?:"([^"]+)"|([\p{L}\p{N}_/-]+))`)
	reLabel   = regexp.MustCompile(`(?:^|\s)@(?:"([^"]+)"|([\p{L}\p{N}_/-]+))`)
)

func extractProject(input string, spans []Span) (string, []Span) {
	// Last one wins. Retyping "#Work #Home" is a correction, not two projects.
	all := reProject.FindAllStringSubmatchIndex(input, -1)
	if len(all) == 0 {
		return "", spans
	}
	var name string
	for _, m := range all {
		name = submatch(input, m)
		spans = append(spans, sigilSpan(input, m, KindProject))
	}
	return name, spans
}

func extractLabels(input string, spans []Span) ([]string, []Span) {
	var labels []string
	seen := map[string]bool{}
	for _, m := range reLabel.FindAllStringSubmatchIndex(input, -1) {
		name := submatch(input, m)
		if key := strings.ToLower(name); !seen[key] {
			seen[key] = true
			labels = append(labels, name)
		}
		spans = append(spans, sigilSpan(input, m, KindLabel))
	}
	return labels, spans
}

// submatch returns whichever of the two alternatives (quoted, bare) matched.
func submatch(input string, m []int) string {
	if m[2] >= 0 {
		return input[m[2]:m[3]]
	}
	return input[m[4]:m[5]]
}

// sigilSpan trims the leading whitespace the pattern had to include in order to
// anchor on a word start, so highlighting covers "#Firma" and not " #Firma".
func sigilSpan(input string, m []int, kind Kind) Span {
	start := m[0]
	for start < len(input) && (input[start] == ' ' || input[start] == '\t') {
		start++
	}
	return Span{Start: start, End: m[1], Kind: kind}
}

// --- priority ---------------------------------------------------------------

// p1..p4 as Todoist writes it. Danish users type the same thing; there is no
// competing Danish convention worth supporting.
var rePriority = regexp.MustCompile(`(?i)(?:^|\s)(p[1-4]|!{1,3})(?:\s|$)`)

func extractPriority(input string, spans []Span) (int, []Span) {
	m := rePriority.FindStringSubmatchIndex(input)
	if m == nil {
		return 4, spans
	}
	token := strings.ToLower(input[m[2]:m[3]])
	priority := 4
	switch token {
	case "p1", "!!!":
		priority = 1
	case "p2", "!!":
		priority = 2
	case "p3", "!":
		priority = 3
	case "p4":
		priority = 4
	}
	return priority, append(spans, Span{Start: m[2], End: m[3], Kind: KindPriority})
}

// --- time -------------------------------------------------------------------

var (
	// An explicit marker: "kl 10", "kl. 10:30", "klokken 16", "at 9.15".
	reTimeMarked = regexp.MustCompile(`(?i)(?:^|\s)(kl\.?|klokken|at)\s*(\d{1,2})(?:[:.](\d{2}))?\s*(am|pm)?(?:\s|$)`)
	// A 12-hour suffix carries its own marker: "10am", "7:30 pm".
	reTimeMeridiem = regexp.MustCompile(`(?i)(?:^|\s)(\d{1,2})(?::(\d{2}))?\s*(am|pm)(?:\s|$)`)
	// A bare colon time: "16:30". Only ':' — never '.', because "15.3" is a date.
	reTimeColon = regexp.MustCompile(`(?:^|\s)(\d{1,2}):(\d{2})(?:\s|$)`)
)

func extractTime(input string, spans []Span, locale string) (int, int, bool, []Span) {
	if m := reTimeMeridiem.FindStringSubmatchIndex(input); m != nil {
		hour := atoi(input, m[2], m[3])
		minute := 0
		if m[4] >= 0 {
			minute = atoi(input, m[4], m[5])
		}
		hour = applyMeridiem(hour, strings.ToLower(input[m[6]:m[7]]))
		if valid(hour, minute) {
			return hour, minute, true, append(spans, Span{Start: m[2], End: m[7], Kind: KindTime})
		}
	}

	if m := reTimeMarked.FindStringSubmatchIndex(input); m != nil {
		hour := atoi(input, m[4], m[5])
		minute := 0
		if m[6] >= 0 {
			minute = atoi(input, m[6], m[7])
		}
		if m[8] >= 0 {
			hour = applyMeridiem(hour, strings.ToLower(input[m[8]:m[9]]))
		} else if strings.EqualFold(input[m[2]:m[3]], "at") && locale != "da" {
			// "at 8" from an English speaker means the morning; from a Dane
			// writing "kl 8" it means exactly eight. Only the English marker with
			// no minutes and no am/pm is ambiguous enough to guess at, and the
			// guess is the one a person means far more often: 1–6 is afternoon.
			if hour >= 1 && hour <= 6 && m[6] < 0 {
				hour += 12
			}
		}
		if valid(hour, minute) {
			end := m[5]
			if m[7] >= 0 {
				end = m[7]
			}
			if m[9] >= 0 {
				end = m[9]
			}
			return hour, minute, true, append(spans, Span{Start: m[2], End: end, Kind: KindTime})
		}
	}

	if m := reTimeColon.FindStringSubmatchIndex(input); m != nil {
		hour, minute := atoi(input, m[2], m[3]), atoi(input, m[4], m[5])
		if valid(hour, minute) {
			return hour, minute, true, append(spans, Span{Start: m[2], End: m[5], Kind: KindTime})
		}
	}
	return 0, 0, false, spans
}

func applyMeridiem(hour int, meridiem string) int {
	switch meridiem {
	case "am":
		if hour == 12 {
			return 0
		}
	case "pm":
		if hour != 12 {
			return hour + 12
		}
	}
	return hour
}

func valid(hour, minute int) bool { return hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59 }

// --- date -------------------------------------------------------------------

var weekdays = map[string]time.Weekday{
	"mandag": time.Monday, "monday": time.Monday, "man": time.Monday, "mon": time.Monday,
	"tirsdag": time.Tuesday, "tuesday": time.Tuesday, "tir": time.Tuesday, "tue": time.Tuesday,
	"onsdag": time.Wednesday, "wednesday": time.Wednesday, "ons": time.Wednesday, "wed": time.Wednesday,
	"torsdag": time.Thursday, "thursday": time.Thursday, "tor": time.Thursday, "thu": time.Thursday,
	"fredag": time.Friday, "friday": time.Friday, "fre": time.Friday, "fri": time.Friday,
	"lørdag": time.Saturday, "saturday": time.Saturday, "lør": time.Saturday, "sat": time.Saturday,
	"søndag": time.Sunday, "sunday": time.Sunday, "søn": time.Sunday, "sun": time.Sunday,
}

var months = map[string]time.Month{
	"januar": time.January, "january": time.January, "jan": time.January,
	"februar": time.February, "february": time.February, "feb": time.February,
	"marts": time.March, "march": time.March, "mar": time.March,
	"april": time.April, "apr": time.April,
	"maj": time.May, "may": time.May,
	"juni": time.June, "june": time.June, "jun": time.June,
	"juli": time.July, "july": time.July, "jul": time.July,
	"august": time.August, "aug": time.August,
	"september": time.September, "sep": time.September, "sept": time.September,
	"oktober": time.October, "october": time.October, "okt": time.October, "oct": time.October,
	"november": time.November, "nov": time.November,
	"december": time.December, "dec": time.December,
}

var (
	reToday    = regexp.MustCompile(`(?i)(?:^|\s)(i\s*dag|today|idag)(?:\s|$)`)
	reTomorrow = regexp.MustCompile(`(?i)(?:^|\s)(i\s*morgen|imorgen|tomorrow|tmr)(?:\s|$)`)
	reOvermorn = regexp.MustCompile(`(?i)(?:^|\s)(i\s*overmorgen|overmorgen|day\s+after\s+tomorrow)(?:\s|$)`)
	// "om 3 dage", "in 2 weeks", "om en uge", "in a month". The plural suffixes are
	// grouped rather than written as a trailing `r?`: "måneder?" would mean
	// "månede" plus an optional r, which is not a word in any language.
	reRelative = regexp.MustCompile(`(?i)(?:^|\s)(om|in)\s+(\d+|en|et|a|an)\s*(dage?|days?|uger?|weeks?|måned(?:er)?|maaned(?:er)?|months?)(?:\s|$)`)
	reNextUnit = regexp.MustCompile(`(?i)(?:^|\s)(næste|naeste|next)\s+(uge|week|måned|maaned|month)(?:\s|$)`)
	// "næste fredag" / "next friday" — the one in the following week.
	reNextDay = regexp.MustCompile(`(?i)(?:^|\s)(næste|naeste|next)\s+(\p{L}+)(?:\s|$)`)
	// A weekday on its own, optionally with the Danish "på".
	reWeekday = regexp.MustCompile(`(?i)(?:^|\s)(?:på\s+|on\s+)?(\p{L}+)(?:\s|$)`)
	// "15. marts", "15 march", "den 3. maj"
	reDayMonth = regexp.MustCompile(`(?i)(?:^|\s)(?:den\s+)?(\d{1,2})\.?\s+(\p{L}+)(?:\s|$)`)
	// "march 15", "dec 1"
	reMonthDay = regexp.MustCompile(`(?i)(?:^|\s)(\p{L}+)\s+(\d{1,2})(?:\.|\b)(?:\s|$)`)
	// ISO first, because it is the only unambiguous numeric form.
	reISO = regexp.MustCompile(`(?:^|\s)(\d{4})-(\d{2})-(\d{2})(?:\s|$)`)
	// "15/3", "15/3-2026", "15-03-2026". Day first: that is how both Danish and
	// British English write it, and guessing month-first would silently move a
	// date by months rather than failing visibly.
	reNumeric = regexp.MustCompile(`(?:^|\s)(\d{1,2})[/.](\d{1,2})(?:[-/](\d{2,4}))?(?:\s|$)`)
)

func extractDate(input string, spans []Span, now time.Time) (time.Time, bool, []Span) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// Ordered most-specific first: an explicit date beats a relative phrase, and a
	// two-word phrase must be tried before either of its words matches alone.
	if m := reISO.FindStringSubmatchIndex(input); m != nil {
		y, mo, d := atoi(input, m[2], m[3]), atoi(input, m[4], m[5]), atoi(input, m[6], m[7])
		if date, ok := makeDate(y, time.Month(mo), d, now.Location()); ok {
			return date, true, append(spans, Span{Start: m[2], End: m[7], Kind: KindDate})
		}
	}

	if m := reNumeric.FindStringSubmatchIndex(input); m != nil {
		day, mo := atoi(input, m[2], m[3]), atoi(input, m[4], m[5])
		year := today.Year()
		explicitYear := m[6] >= 0
		if explicitYear {
			year = atoi(input, m[6], m[7])
			if year < 100 {
				year += 2000
			}
		}
		if date, ok := makeDate(year, time.Month(mo), day, now.Location()); ok {
			if !explicitYear && date.Before(today) {
				date = date.AddDate(1, 0, 0)
			}
			end := m[5]
			if explicitYear {
				end = m[7]
			}
			return date, true, append(spans, Span{Start: m[2], End: end, Kind: KindDate})
		}
	}

	if m := reOvermorn.FindStringSubmatchIndex(input); m != nil {
		return today.AddDate(0, 0, 2), true, append(spans, Span{Start: m[2], End: m[3], Kind: KindDate})
	}
	if m := reTomorrow.FindStringSubmatchIndex(input); m != nil {
		return today.AddDate(0, 0, 1), true, append(spans, Span{Start: m[2], End: m[3], Kind: KindDate})
	}
	if m := reToday.FindStringSubmatchIndex(input); m != nil {
		return today, true, append(spans, Span{Start: m[2], End: m[3], Kind: KindDate})
	}

	if m := reRelative.FindStringSubmatchIndex(input); m != nil {
		n := 1
		if word := strings.ToLower(input[m[4]:m[5]]); !isWordOne(word) {
			n = atoi(input, m[4], m[5])
		}
		unit := strings.ToLower(input[m[6]:m[7]])
		var date time.Time
		switch {
		case strings.HasPrefix(unit, "dag"), strings.HasPrefix(unit, "day"):
			date = today.AddDate(0, 0, n)
		case strings.HasPrefix(unit, "uge"), strings.HasPrefix(unit, "week"):
			date = today.AddDate(0, 0, 7*n)
		default:
			date = today.AddDate(0, n, 0)
		}
		return date, true, append(spans, Span{Start: m[2], End: m[7], Kind: KindDate})
	}

	if m := reNextUnit.FindStringSubmatchIndex(input); m != nil {
		unit := strings.ToLower(input[m[4]:m[5]])
		var date time.Time
		if strings.HasPrefix(unit, "uge") || strings.HasPrefix(unit, "week") {
			date = startOfNextWeek(today)
		} else {
			date = today.AddDate(0, 1, 0)
		}
		return date, true, append(spans, Span{Start: m[2], End: m[5], Kind: KindDate})
	}

	if m := reNextDay.FindStringSubmatchIndex(input); m != nil {
		if wd, ok := weekdays[strings.ToLower(input[m[4]:m[5]])]; ok {
			// "next friday" is the Friday of next week, counted from Monday —
			// not "the next Friday to occur", which on a Thursday would be
			// tomorrow and surprise everybody.
			date := startOfNextWeek(today).AddDate(0, 0, int(isoIndex(wd)))
			return date, true, append(spans, Span{Start: m[2], End: m[5], Kind: KindDate})
		}
	}

	if date, ok, s := matchDayMonth(input, spans, today); ok {
		return date, true, s
	}

	// A bare weekday is tried last: it is the loosest pattern here, so it only gets
	// a say once nothing more specific in the line has claimed a date.
	//
	// This walks words rather than running a regex over the line. A pattern that
	// anchors on the whitespace around a word consumes that whitespace, so in a run
	// of plain words it matches every other one — and "ring til Anders fredag"
	// would find no weekday at all, depending only on how many words came first.
	ws := words(input)
	for i, w := range ws {
		wd, ok := weekdays[strings.ToLower(w.text)]
		if !ok {
			continue
		}
		start := w.start
		// "på fredag" and "on friday" are one phrase; leaving the preposition
		// behind would strand it in the task title as "ring til Anders on".
		if i > 0 {
			switch strings.ToLower(ws[i-1].text) {
			case "på", "paa", "on":
				start = ws[i-1].start
			}
		}
		ahead := (int(wd) - int(today.Weekday()) + 7) % 7
		return today.AddDate(0, 0, ahead), true, append(spans, Span{Start: start, End: w.end, Kind: KindDate})
	}

	return time.Time{}, false, spans
}

type word struct {
	text       string
	start, end int
}

// words splits on anything that is not a letter or digit, keeping byte offsets.
func words(s string) []word {
	var out []word
	start := -1
	for i, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, word{text: s[start:i], start: start, end: i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, word{text: s[start:], start: start, end: len(s)})
	}
	return out
}

// mask blanks out the byte ranges already claimed, leaving the string the same
// length so every offset computed against it still indexes the original input.
func mask(s string, spans []Span) string {
	if len(spans) == 0 {
		return s
	}
	b := []byte(s)
	for _, sp := range spans {
		for i := sp.Start; i < sp.End && i < len(b); i++ {
			b[i] = ' '
		}
	}
	return string(b)
}

// matchDayMonth handles the two written forms, "15. marts" and "march 15".
func matchDayMonth(input string, spans []Span, today time.Time) (time.Time, bool, []Span) {
	try := func(m []int, dayGroup, monthGroup int) (time.Time, bool, []Span) {
		if m == nil {
			return time.Time{}, false, spans
		}
		mo, ok := months[strings.ToLower(input[m[monthGroup*2]:m[monthGroup*2+1]])]
		if !ok {
			return time.Time{}, false, spans
		}
		day := atoi(input, m[dayGroup*2], m[dayGroup*2+1])
		date, ok := makeDate(today.Year(), mo, day, today.Location())
		if !ok {
			return time.Time{}, false, spans
		}
		// A date already gone by means next year: nobody schedules backwards.
		if date.Before(today) {
			date = date.AddDate(1, 0, 0)
		}
		// The span runs from the first group to the last, which excludes the
		// optional "den " prefix and the trailing separator the pattern matched.
		return date, true, append(spans, Span{Start: m[2], End: m[5], Kind: KindDate})
	}

	if date, ok, s := try(reDayMonth.FindStringSubmatchIndex(input), 1, 2); ok {
		return date, ok, s
	}
	return try(reMonthDay.FindStringSubmatchIndex(input), 2, 1)
}

// startOfNextWeek returns the coming Monday. On a Monday that is seven days out,
// not today — "next week" never means "right now".
func startOfNextWeek(today time.Time) time.Time {
	return today.AddDate(0, 0, 8-int(isoIndex(today.Weekday()))-1)
}

// isoIndex maps a weekday to its Monday-first position, 0..6. Go's own numbering
// starts on Sunday, which puts the weekend in the wrong place for week arithmetic.
func isoIndex(wd time.Weekday) int {
	if wd == time.Sunday {
		return 6
	}
	return int(wd) - 1
}

// makeDate rejects anything that would silently roll over — 31 February becoming
// 3 March is a wrong date, not a date.
func makeDate(year int, month time.Month, day int, loc *time.Location) (time.Time, bool) {
	if month < time.January || month > time.December || day < 1 || day > 31 || year < 1970 || year > 2999 {
		return time.Time{}, false
	}
	date := time.Date(year, month, day, 0, 0, 0, 0, loc)
	if date.Day() != day || date.Month() != month {
		return time.Time{}, false
	}
	return date, true
}

func isWordOne(w string) bool {
	switch w {
	case "en", "et", "a", "an":
		return true
	}
	return false
}

// --- assembling the leftover ------------------------------------------------

// remove cuts every recognised span out of the input and tidies what is left:
// runs of whitespace collapse to one, and stray separators left dangling by a
// removal are dropped.
func remove(input string, spans []Span) string {
	if len(spans) == 0 {
		return strings.Join(strings.Fields(input), " ")
	}
	var b strings.Builder
	prev := 0
	for _, s := range spans {
		if s.Start < prev {
			// Overlapping spans: keep the first, skip what is already gone.
			if s.End > prev {
				prev = s.End
			}
			continue
		}
		b.WriteString(input[prev:s.Start])
		b.WriteByte(' ')
		prev = s.End
	}
	b.WriteString(input[prev:])
	return strings.Join(strings.Fields(b.String()), " ")
}

func atoi(s string, start, end int) int {
	n, _ := strconv.Atoi(s[start:end])
	return n
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
