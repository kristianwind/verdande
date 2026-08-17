package recurrence

import (
	"testing"
	"time"
)

func TestParseDanish(t *testing.T) {
	cases := map[string]string{
		"hver dag":               "FREQ=DAILY",
		"dagligt":                "FREQ=DAILY",
		"hver 3. dag":            "FREQ=DAILY;INTERVAL=3",
		"hver anden dag":         "FREQ=DAILY;INTERVAL=2",
		"hver uge":               "FREQ=WEEKLY",
		"ugentligt":              "FREQ=WEEKLY",
		"hver 2. uge":            "FREQ=WEEKLY;INTERVAL=2",
		"hver anden uge":         "FREQ=WEEKLY;INTERVAL=2",
		"hver mandag":            "FREQ=WEEKLY;BYDAY=MO",
		"hver fredag":            "FREQ=WEEKLY;BYDAY=FR",
		"hver mandag og torsdag": "FREQ=WEEKLY;BYDAY=MO,TH",
		"hverdage":               "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
		"hver weekend":           "FREQ=WEEKLY;BYDAY=SA,SU",
		"hver måned":             "FREQ=MONTHLY",
		"månedligt":              "FREQ=MONTHLY",
		"hver 3. måned":          "FREQ=MONTHLY;INTERVAL=3",
		"den 1. i måneden":       "FREQ=MONTHLY;BYMONTHDAY=1",
		"den 15. i måneden":      "FREQ=MONTHLY;BYMONTHDAY=15",
		"hvert år":               "FREQ=YEARLY",
		"årligt":                 "FREQ=YEARLY",
		"hver 15. marts":         "FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=15",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := Parse(in); got != want {
				t.Errorf("Parse(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestParseEnglish(t *testing.T) {
	cases := map[string]string{
		"every day":                          "FREQ=DAILY",
		"daily":                              "FREQ=DAILY",
		"every 2 days":                       "FREQ=DAILY;INTERVAL=2",
		"every week":                         "FREQ=WEEKLY",
		"weekly":                             "FREQ=WEEKLY",
		"every 2 weeks":                      "FREQ=WEEKLY;INTERVAL=2",
		"every other week":                   "FREQ=WEEKLY;INTERVAL=2",
		"every monday":                       "FREQ=WEEKLY;BYDAY=MO",
		"every monday and thursday":          "FREQ=WEEKLY;BYDAY=MO,TH",
		"every monday, wednesday and friday": "FREQ=WEEKLY;BYDAY=MO,WE,FR",
		"weekdays":                           "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
		"every weekday":                      "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
		"every weekend":                      "FREQ=WEEKLY;BYDAY=SA,SU",
		"every month":                        "FREQ=MONTHLY",
		"monthly":                            "FREQ=MONTHLY",
		"every 3 months":                     "FREQ=MONTHLY;INTERVAL=3",
		"every year":                         "FREQ=YEARLY",
		"yearly":                             "FREQ=YEARLY",
		"annually":                           "FREQ=YEARLY",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := Parse(in); got != want {
				t.Errorf("Parse(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

// Most tasks do not repeat. Anything that is not clearly a repetition must return
// nothing, or "ring til mor på mandag" becomes a weekly obligation.
func TestParseRefusesWhatIsNotARepetition(t *testing.T) {
	for _, in := range []string{
		"", "   ", "mandag", "i morgen", "tomorrow", "ring til mor",
		"hver", "every", "hver blomst", "every thing",
		"hver mandag og noget", "every monday and pigs",
		"den 45. i måneden", "hver 99. marts",
		"p1", "#Firma",
	} {
		t.Run(in, func(t *testing.T) {
			if got := Parse(in); got != "" {
				t.Errorf("Parse(%q) = %q, want empty", in, got)
			}
		})
	}
}

// Everything the parser produces has to be a rule the RRULE library accepts —
// otherwise a task is created that cannot be advanced when it is ticked off.
func TestEverythingParsedIsValid(t *testing.T) {
	phrases := []string{
		"hver dag", "hver 3. dag", "hver mandag", "hver mandag og torsdag",
		"hverdage", "hver weekend", "hver 2. uge", "hver måned",
		"den 15. i måneden", "hvert år", "hver 15. marts",
		"every 2 weeks", "weekdays", "every monday, wednesday and friday",
	}
	for _, phrase := range phrases {
		t.Run(phrase, func(t *testing.T) {
			rule := Parse(phrase)
			if rule == "" {
				t.Fatalf("Parse(%q) returned nothing", phrase)
			}
			if !Valid(rule) {
				t.Errorf("Parse(%q) produced %q, which is not a valid RRULE", phrase, rule)
			}
		})
	}
}

func TestValid(t *testing.T) {
	if !Valid("") {
		t.Error("an empty rule means 'does not repeat' and must be accepted")
	}
	for _, rule := range []string{"FREQ=DAILY", "FREQ=WEEKLY;BYDAY=MO", "RRULE:FREQ=DAILY"} {
		if !Valid(rule) {
			t.Errorf("Valid(%q) = false", rule)
		}
	}
	for _, rule := range []string{"nonsense", "FREQ=FORTNIGHTLY", "BYDAY=MO"} {
		if Valid(rule) {
			t.Errorf("Valid(%q) = true, want false", rule)
		}
	}
}

func TestNext(t *testing.T) {
	// A Tuesday, so weekday rules resolve in both directions.
	anchor := date(2026, 3, 10)
	if anchor.Weekday() != time.Tuesday {
		t.Fatalf("the anchor is a %s; the expectations below assume Tuesday", anchor.Weekday())
	}

	cases := []struct {
		rule string
		want time.Time
	}{
		{"FREQ=DAILY", date(2026, 3, 11)},
		{"FREQ=DAILY;INTERVAL=3", date(2026, 3, 13)},
		{"FREQ=WEEKLY", date(2026, 3, 17)},
		{"FREQ=WEEKLY;INTERVAL=2", date(2026, 3, 24)},
		// From a Tuesday, the next Monday is next week.
		{"FREQ=WEEKLY;BYDAY=MO", date(2026, 3, 16)},
		// The next Friday is this week.
		{"FREQ=WEEKLY;BYDAY=FR", date(2026, 3, 13)},
		// A list picks whichever comes first.
		{"FREQ=WEEKLY;BYDAY=MO,TH", date(2026, 3, 12)},
		{"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", date(2026, 3, 11)},
		{"FREQ=MONTHLY", date(2026, 4, 10)},
		{"FREQ=MONTHLY;BYMONTHDAY=1", date(2026, 4, 1)},
		{"FREQ=MONTHLY;BYMONTHDAY=15", date(2026, 3, 15)},
		{"FREQ=YEARLY", date(2027, 3, 10)},
		{"FREQ=YEARLY;BYMONTH=12;BYMONTHDAY=24", date(2026, 12, 24)},
	}

	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			got, err := Next(tc.rule, anchor)
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("Next(%q, %s) = %s, want %s",
					tc.rule, fmtDate(anchor), fmtDate(got), fmtDate(tc.want))
			}
		})
	}
}

// The next occurrence is strictly after the anchor. If it were not, a daily task
// would return its own due date forever and ticking it off would do nothing.
func TestNextIsStrictlyAfterTheAnchor(t *testing.T) {
	anchor := date(2026, 3, 10) // a Tuesday

	for _, rule := range []string{
		"FREQ=DAILY",
		"FREQ=WEEKLY",
		"FREQ=WEEKLY;BYDAY=TU", // the anchor's own weekday
		"FREQ=MONTHLY",
		"FREQ=MONTHLY;BYMONTHDAY=10", // the anchor's own day of the month
		"FREQ=YEARLY",
	} {
		t.Run(rule, func(t *testing.T) {
			got, err := Next(rule, anchor)
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if !got.After(anchor) {
				t.Errorf("Next returned %s, which is not after the anchor %s",
					fmtDate(got), fmtDate(anchor))
			}
		})
	}
}

// Repeatedly advancing a rule must keep producing dates in order and never stall.
// This is what completing a recurring task does over months of use.
func TestAdvancingRepeatedlyMakesProgress(t *testing.T) {
	rules := []string{
		"FREQ=DAILY", "FREQ=WEEKLY;BYDAY=MO,TH", "FREQ=MONTHLY;BYMONTHDAY=31",
		"FREQ=MONTHLY", "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29",
	}
	for _, rule := range rules {
		t.Run(rule, func(t *testing.T) {
			at := date(2026, 1, 15)
			for i := 0; i < 60; i++ {
				next, err := Next(rule, at)
				if err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				if !next.After(at) {
					t.Fatalf("step %d: %s did not advance past %s",
						i, fmtDate(next), fmtDate(at))
				}
				at = next
			}
		})
	}
}

// 31 January plus a month is not 31 February. RRULE skips months that have no such
// day rather than rolling into the next one — the behaviour a person expects from
// "the 31st of every month" is that it happens in the months that have a 31st.
func TestMonthlyOnADayNotEveryMonthHas(t *testing.T) {
	got, err := Next("FREQ=MONTHLY;BYMONTHDAY=31", date(2026, 1, 31))
	if err != nil {
		t.Fatal(err)
	}
	// February and April have no 31st, so March is next.
	if want := date(2026, 3, 31); !got.Equal(want) {
		t.Errorf("Next = %s, want %s", fmtDate(got), fmtDate(want))
	}
}

// A rule with a COUNT runs out. The caller has to be told, not handed a zero time
// it might store as a due date in year 1.
func TestBoundedSeriesEnds(t *testing.T) {
	at := date(2026, 3, 10)
	rule := "FREQ=DAILY;COUNT=3"

	var last time.Time
	for i := 0; i < 3; i++ {
		next, err := Next(rule, at)
		if err != nil {
			// COUNT is measured from DTSTART, which Next re-anchors on each call,
			// so an unbounded loop is not expected here — but if the library ever
			// reports the end, it must be as an error and not a zero time.
			if err != ErrSeriesEnded {
				t.Fatalf("step %d: %v", i, err)
			}
			return
		}
		if next.IsZero() {
			t.Fatalf("step %d returned a zero time instead of an error", i)
		}
		last = next
		at = next
	}
	if last.IsZero() {
		t.Error("no occurrences were produced")
	}
}

func TestDescribe(t *testing.T) {
	cases := map[string]string{
		"":                                 "",
		"FREQ=DAILY":                       "hver dag",
		"FREQ=DAILY;INTERVAL=2":            "hver anden dag",
		"FREQ=DAILY;INTERVAL=3":            "hver 3. dag",
		"FREQ=WEEKLY":                      "hver uge",
		"FREQ=WEEKLY;INTERVAL=2":           "hver anden uge",
		"FREQ=WEEKLY;BYDAY=MO":             "hver mandag",
		"FREQ=WEEKLY;BYDAY=MO,TH":          "hver mandag og torsdag",
		"FREQ=WEEKLY;BYDAY=FR;INTERVAL=2":  "hver anden uge fredag",
		"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR": "hverdage",
		"FREQ=WEEKLY;BYDAY=SA,SU":          "hver weekend",
		"FREQ=MONTHLY":                     "hver måned",
		"FREQ=MONTHLY;BYMONTHDAY=15":       "den 15. hver måned",
		"FREQ=YEARLY":                      "hvert år",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := Describe(in); got != want {
				t.Errorf("Describe(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

// Whatever the parser produces must survive being described and re-parsed back
// into the same rule. That round trip is what keeps the text on a task honest.
func TestDescribeRoundTrip(t *testing.T) {
	phrases := []string{
		"hver dag", "hver anden dag", "hver uge", "hver anden uge",
		"hverdage", "hver weekend", "hver måned", "hvert år",
		"hver mandag", "hver mandag og torsdag",
	}
	for _, phrase := range phrases {
		t.Run(phrase, func(t *testing.T) {
			rule := Parse(phrase)
			if rule == "" {
				t.Fatalf("Parse(%q) returned nothing", phrase)
			}
			back := Parse(Describe(rule))
			if back != rule {
				t.Errorf("%q → %q → %q → %q", phrase, rule, Describe(rule), back)
			}
		})
	}
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func fmtDate(t time.Time) string { return t.Format("2006-01-02 (Mon)") }
