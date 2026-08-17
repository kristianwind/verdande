package quickadd

import (
	"testing"
	"time"
)

// The reference moment for every test: Tuesday 10 March 2026, 10:00, Copenhagen.
// A Tuesday is deliberate — it sits mid-week, so "next friday" and "monday" resolve
// in opposite directions and a bug in either cannot hide behind the other.
func ref(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	now := time.Date(2026, 3, 10, 10, 0, 0, 0, loc)
	if now.Weekday() != time.Tuesday {
		t.Fatalf("reference date is a %s; the expectations below assume Tuesday", now.Weekday())
	}
	return now
}

// The example from the brief, end to end.
func TestParseTheBriefExample(t *testing.T) {
	got := Parse("betal moms i morgen kl 10 p1 #Firma @regnskab", ref(t), "da")

	if got.Content != "betal moms" {
		t.Errorf("Content = %q, want %q", got.Content, "betal moms")
	}
	if got.DueDate != "2026-03-11" {
		t.Errorf("DueDate = %q, want 2026-03-11", got.DueDate)
	}
	if got.DueTime != "10:00" {
		t.Errorf("DueTime = %q, want 10:00", got.DueTime)
	}
	if got.Priority != 1 {
		t.Errorf("Priority = %d, want 1", got.Priority)
	}
	if got.Project != "Firma" {
		t.Errorf("Project = %q, want Firma", got.Project)
	}
	if len(got.Labels) != 1 || got.Labels[0] != "regnskab" {
		t.Errorf("Labels = %v, want [regnskab]", got.Labels)
	}
}

func TestParseDates(t *testing.T) {
	now := ref(t)
	cases := []struct {
		in   string
		want string
	}{
		// Danish relative
		{"ring til mor i dag", "2026-03-10"},
		{"ring til mor idag", "2026-03-10"},
		{"ring til mor i morgen", "2026-03-11"},
		{"ring til mor imorgen", "2026-03-11"},
		{"ring til mor i overmorgen", "2026-03-12"},
		{"ring til mor om 3 dage", "2026-03-13"},
		{"ring til mor om en uge", "2026-03-17"},
		{"ring til mor om 2 uger", "2026-03-24"},
		{"ring til mor om en måned", "2026-04-10"},
		{"ring til mor næste uge", "2026-03-16"},
		{"ring til mor næste måned", "2026-04-10"},

		// English relative
		{"call mum today", "2026-03-10"},
		{"call mum tomorrow", "2026-03-11"},
		{"call mum day after tomorrow", "2026-03-12"},
		{"call mum in 3 days", "2026-03-13"},
		{"call mum in a week", "2026-03-17"},
		{"call mum in 2 weeks", "2026-03-24"},
		{"call mum next week", "2026-03-16"},
		{"call mum next month", "2026-04-10"},

		// Weekdays. From a Tuesday: Friday is this week, Monday is the next one,
		// and Tuesday itself is today.
		{"møde fredag", "2026-03-13"},
		{"møde på fredag", "2026-03-13"},
		{"møde mandag", "2026-03-16"},
		{"møde tirsdag", "2026-03-10"},
		{"møde søndag", "2026-03-15"},
		{"meeting friday", "2026-03-13"},
		{"meeting on friday", "2026-03-13"},

		// "next <weekday>" is next week's, counted from Monday — not merely the
		// next one to come round.
		{"møde næste fredag", "2026-03-20"},
		{"meeting next friday", "2026-03-20"},
		{"møde næste mandag", "2026-03-16"},

		// Written dates
		{"deadline 15. marts", "2026-03-15"},
		{"deadline 15 marts", "2026-03-15"},
		{"deadline den 15. marts", "2026-03-15"},
		{"deadline march 15", "2026-03-15"},
		{"deadline 1. dec", "2026-12-01"},
		{"deadline dec 1", "2026-12-01"},

		// Numeric, day-first
		{"deadline 15/3", "2026-03-15"},
		{"deadline 15/3-2026", "2026-03-15"},
		{"deadline 2026-12-24", "2026-12-24"},

		// A date that has already gone by this year means next year.
		{"deadline 1. marts", "2027-03-01"},
		{"deadline 1/3", "2027-03-01"},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := Parse(tc.in, now, "da")
			if got.DueDate != tc.want {
				t.Errorf("DueDate = %q, want %q (content %q)", got.DueDate, tc.want, got.Content)
			}
		})
	}
}

func TestParseTimes(t *testing.T) {
	now := ref(t)
	cases := []struct {
		in     string
		locale string
		want   string
	}{
		{"møde kl 14", "da", "14:00"},
		{"møde kl. 14", "da", "14:00"},
		{"møde kl 14:30", "da", "14:30"},
		{"møde kl 14.30", "da", "14:30"},
		{"møde klokken 16", "da", "16:00"},
		{"møde 16:30", "da", "16:30"},
		{"meeting at 14", "en", "14:00"},
		{"meeting at 10am", "en", "10:00"},
		{"meeting at 7:30 pm", "en", "19:30"},
		{"meeting 10am", "en", "10:00"},
		{"meeting 12am", "en", "00:00"},
		{"meeting 12pm", "en", "12:00"},

		// "kl 3" from a Dane is three in the morning — Danish has no am/pm habit,
		// and the 24-hour clock is what people mean. English "at 3" is not.
		{"møde kl 3", "da", "03:00"},
		{"meeting at 3", "en", "15:00"},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := Parse(tc.in, now, tc.locale)
			if got.DueTime != tc.want {
				t.Errorf("DueTime = %q, want %q", got.DueTime, tc.want)
			}
		})
	}
}

// A clock time with no day attached means the next time that clock reads it.
func TestTimeWithoutDateRollsForward(t *testing.T) {
	now := ref(t) // 10:00

	later := Parse("møde kl 14", now, "da")
	if later.DueDate != "2026-03-10" {
		t.Errorf("a time still to come today: DueDate = %q, want 2026-03-10", later.DueDate)
	}

	earlier := Parse("møde kl 8", now, "da")
	if earlier.DueDate != "2026-03-11" {
		t.Errorf("a time already past today: DueDate = %q, want 2026-03-11", earlier.DueDate)
	}
}

func TestParsePriority(t *testing.T) {
	now := ref(t)
	cases := map[string]int{
		"skriv rapport p1":  1,
		"skriv rapport p2":  2,
		"skriv rapport p3":  3,
		"skriv rapport p4":  4,
		"skriv rapport":     4,
		"skriv rapport !!!": 1,
		"skriv rapport !!":  2,
		"skriv rapport !":   3,
		// Not a priority: part of a word, or out of range.
		"skriv p5 rapport":   4,
		"skriv rapportp1":    4,
		"deploy p1beta node": 4,
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := Parse(in, now, "da").Priority; got != want {
				t.Errorf("Priority = %d, want %d", got, want)
			}
		})
	}
}

func TestParseProjectAndLabels(t *testing.T) {
	now := ref(t)

	got := Parse("køb kaffe #Hjem @indkøb @haster", now, "da")
	if got.Project != "Hjem" {
		t.Errorf("Project = %q, want Hjem", got.Project)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "indkøb" || got.Labels[1] != "haster" {
		t.Errorf("Labels = %v, want [indkøb haster]", got.Labels)
	}
	if got.Content != "køb kaffe" {
		t.Errorf("Content = %q, want %q", got.Content, "køb kaffe")
	}

	t.Run("quoted names may contain spaces", func(t *testing.T) {
		got := Parse(`send faktura #"Q3 rapport" @"skal godkendes"`, now, "da")
		if got.Project != "Q3 rapport" {
			t.Errorf("Project = %q, want %q", got.Project, "Q3 rapport")
		}
		if len(got.Labels) != 1 || got.Labels[0] != "skal godkendes" {
			t.Errorf("Labels = %v", got.Labels)
		}
		if got.Content != "send faktura" {
			t.Errorf("Content = %q", got.Content)
		}
	})

	t.Run("the last project wins", func(t *testing.T) {
		// Typing a second one is a correction, not a second project.
		got := Parse("opgave #Arbejde #Privat", now, "da")
		if got.Project != "Privat" {
			t.Errorf("Project = %q, want Privat", got.Project)
		}
		if got.Content != "opgave" {
			t.Errorf("Content = %q, want %q", got.Content, "opgave")
		}
	})

	t.Run("a repeated label is added once", func(t *testing.T) {
		got := Parse("opgave @haster @Haster", now, "da")
		if len(got.Labels) != 1 {
			t.Errorf("Labels = %v, want one entry", got.Labels)
		}
	})

	t.Run("danish letters in names", func(t *testing.T) {
		got := Parse("bogfør bilag #Økonomi @årsregnskab", now, "da")
		if got.Project != "Økonomi" {
			t.Errorf("Project = %q, want Økonomi", got.Project)
		}
		if len(got.Labels) != 1 || got.Labels[0] != "årsregnskab" {
			t.Errorf("Labels = %v, want [årsregnskab]", got.Labels)
		}
	})
}

// The parser must not claim the same characters twice. "kl 10.12" is a time; a date
// parser reading the raw line would also see the 10th of December in it.
func TestOverlappingReadingsAreResolvedOnce(t *testing.T) {
	now := ref(t)

	got := Parse("møde kl 10.12", now, "da")
	if got.DueTime != "10:12" {
		t.Errorf("DueTime = %q, want 10:12", got.DueTime)
	}
	if got.DueDate != "2026-03-10" {
		t.Errorf("DueDate = %q, want today — the time is still to come", got.DueDate)
	}
	if got.Content != "møde" {
		t.Errorf("Content = %q, want %q", got.Content, "møde")
	}
}

// A project or label named after a weekday is a name, not a date.
func TestSigilNamesAreNotReadAsDates(t *testing.T) {
	now := ref(t)

	got := Parse("planlæg #Fredagsbar", now, "da")
	if got.DueDate != "" {
		t.Errorf("DueDate = %q, want none", got.DueDate)
	}
	if got.Project != "Fredagsbar" {
		t.Errorf("Project = %q", got.Project)
	}

	exact := Parse("drinks @fredag", now, "da")
	if exact.DueDate != "" {
		t.Errorf("a label spelled like a weekday set DueDate = %q", exact.DueDate)
	}
}

// A weekday must be found wherever it sits in the line. A pattern anchored on the
// whitespace around a word eats that whitespace and then matches every other word.
func TestWeekdayIsFoundAtAnyPosition(t *testing.T) {
	now := ref(t)
	cases := []string{
		"fredag",
		"ring fredag",
		"ring til fredag",
		"ring til Anders fredag",
		"ring til Anders om det der fredag",
		"a b c d e f fredag",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			if got := Parse(in, now, "da").DueDate; got != "2026-03-13" {
				t.Errorf("DueDate = %q, want 2026-03-13", got)
			}
		})
	}
}

func TestContentKeepsWhatWasNotUnderstood(t *testing.T) {
	now := ref(t)
	cases := []struct{ in, want string }{
		{"betal moms i morgen kl 10 p1 #Firma @regnskab", "betal moms"},
		{"  ryd    op   ", "ryd op"},
		{"p1 #Firma", ""},
		{"skriv rapport om kvartalstallene", "skriv rapport om kvartalstallene"},
		{"i morgen ring til tandlægen", "ring til tandlægen"},
		{"ring #Privat til tandlægen i morgen", "ring til tandlægen"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := Parse(tc.in, now, "da").Content; got != tc.want {
				t.Errorf("Content = %q, want %q", got, tc.want)
			}
		})
	}
}

// Danish and English are parsed together: people mix them mid-sentence.
func TestLanguagesMixInOneLine(t *testing.T) {
	got := Parse("ring til Anders on friday kl 14 #Arbejde", ref(t), "da")
	if got.DueDate != "2026-03-13" {
		t.Errorf("DueDate = %q, want 2026-03-13", got.DueDate)
	}
	if got.DueTime != "14:00" {
		t.Errorf("DueTime = %q, want 14:00", got.DueTime)
	}
	if got.Content != "ring til Anders" {
		t.Errorf("Content = %q, want %q", got.Content, "ring til Anders")
	}
}

// Spans drive the live highlighting in the input box, so they have to point at the
// right bytes of the original string — not of some lowercased or rewritten copy.
func TestSpansPointIntoTheOriginalInput(t *testing.T) {
	in := "betal moms i morgen kl 10 p1 #Firma @regnskab"
	got := Parse(in, ref(t), "da")

	// A span covers the whole phrase that was understood, marker included — the
	// highlight should sit under "kl 10", not under a bare "10" with a stray "kl"
	// left unhighlighted beside it.
	want := map[Kind]string{
		KindDate:     "i morgen",
		KindTime:     "kl 10",
		KindPriority: "p1",
		KindProject:  "#Firma",
		KindLabel:    "@regnskab",
	}
	seen := map[Kind]bool{}
	for _, s := range got.Spans {
		if s.Start < 0 || s.End > len(in) || s.Start >= s.End {
			t.Fatalf("span %+v is not a valid range of a %d-byte input", s, len(in))
		}
		text := in[s.Start:s.End]
		if w, ok := want[s.Kind]; ok && text != w {
			t.Errorf("span %s covers %q, want %q", s.Kind, text, w)
		}
		seen[s.Kind] = true
	}
	for kind := range want {
		if !seen[kind] {
			t.Errorf("no span of kind %s", kind)
		}
	}

	// Spans are handed to the UI in the order they appear.
	for i := 1; i < len(got.Spans); i++ {
		if got.Spans[i-1].Start > got.Spans[i].Start {
			t.Errorf("spans are not sorted: %+v", got.Spans)
		}
	}

	// And they must not overlap, or highlighting would paint over itself.
	for i := 1; i < len(got.Spans); i++ {
		if got.Spans[i].Start < got.Spans[i-1].End {
			t.Errorf("spans %+v and %+v overlap", got.Spans[i-1], got.Spans[i])
		}
	}
}

// Nothing typed into a quick-add box may produce an invalid date or a panic.
func TestNonsenseInputIsHarmless(t *testing.T) {
	now := ref(t)
	inputs := []string{
		"", "   ", "#", "@", "#\"", "p", "p9",
		"31. februar", "32/13", "0/0", "2026-13-45", "99:99", "kl 25:00",
		"@@@@", "####", "i morgen i morgen i morgen",
		"om dage", "in weeks", "næste", "next",
		"1/1/1/1/1", "----", "kl", "at",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			got := Parse(in, now, "da")
			if got.Priority < 1 || got.Priority > 4 {
				t.Errorf("Priority = %d, out of range", got.Priority)
			}
			if got.DueDate != "" {
				if _, err := time.Parse("2006-01-02", got.DueDate); err != nil {
					t.Errorf("DueDate = %q is not a date: %v", got.DueDate, err)
				}
			}
			if got.DueTime != "" {
				if _, err := time.Parse("15:04", got.DueTime); err != nil {
					t.Errorf("DueTime = %q is not a time: %v", got.DueTime, err)
				}
			}
		})
	}
}

// An impossible calendar date must be left in the title rather than rolled over
// into a different, plausible-looking one.
func TestImpossibleDatesAreNotSilentlyMoved(t *testing.T) {
	now := ref(t)
	for _, in := range []string{"deadline 31. februar", "deadline 30/2", "deadline 2026-02-30"} {
		t.Run(in, func(t *testing.T) {
			if got := Parse(in, now, "da").DueDate; got != "" {
				t.Errorf("DueDate = %q, want none — 30/31 February is not a date", got)
			}
		})
	}
}

// --- recurrence ---------------------------------------------------------------

func TestParseRecurrence(t *testing.T) {
	now := ref(t) // Tuesday 10 March 2026

	cases := []struct {
		in      string
		content string
		rule    string
		wantDue string
	}{
		{"vand planterne hver mandag", "vand planterne", "FREQ=WEEKLY;BYDAY=MO", "2026-03-16"},
		{"tag skraldet ud hver tirsdag", "tag skraldet ud", "FREQ=WEEKLY;BYDAY=TU", "2026-03-17"},
		{"løb hver dag", "løb", "FREQ=DAILY", "2026-03-11"},
		{"status hver 2. uge", "status", "FREQ=WEEKLY;INTERVAL=2", "2026-03-24"},
		{"betal husleje den 1. i måneden", "betal husleje", "FREQ=MONTHLY;BYMONTHDAY=1", "2026-04-01"},
		{"standup hverdage", "standup", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", "2026-03-11"},
		{"review every 2 weeks", "review", "FREQ=WEEKLY;INTERVAL=2", "2026-03-24"},
		{"backup weekly", "backup", "FREQ=WEEKLY", "2026-03-17"},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := Parse(tc.in, now, "da")
			if got.Recurrence != tc.rule {
				t.Errorf("Recurrence = %q, want %q", got.Recurrence, tc.rule)
			}
			if got.Content != tc.content {
				t.Errorf("Content = %q, want %q", got.Content, tc.content)
			}
			if got.DueDate != tc.wantDue {
				t.Errorf("DueDate = %q, want %q", got.DueDate, tc.wantDue)
			}
		})
	}
}

// "hver mandag" contains a weekday. If the date parser ran first it would claim
// Monday as a one-off date and leave "hver" stranded in the title.
func TestRecurrenceBeatsAPlainWeekday(t *testing.T) {
	now := ref(t)

	repeating := Parse("møde hver fredag", now, "da")
	if repeating.Recurrence != "FREQ=WEEKLY;BYDAY=FR" {
		t.Errorf("Recurrence = %q", repeating.Recurrence)
	}
	if repeating.Content != "møde" {
		t.Errorf("Content = %q, want %q", repeating.Content, "møde")
	}

	// And a plain weekday is still a one-off.
	once := Parse("møde fredag", now, "da")
	if once.Recurrence != "" {
		t.Errorf("a one-off got a recurrence rule: %q", once.Recurrence)
	}
	if once.DueDate != "2026-03-13" {
		t.Errorf("DueDate = %q, want 2026-03-13", once.DueDate)
	}
}

// The repetition phrase sits in a line with everything else in it.
func TestRecurrenceAlongsideEverythingElse(t *testing.T) {
	got := Parse("send rapport hver mandag kl 9 p1 #Arbejde @fast", ref(t), "da")

	if got.Recurrence != "FREQ=WEEKLY;BYDAY=MO" {
		t.Errorf("Recurrence = %q", got.Recurrence)
	}
	if got.Content != "send rapport" {
		t.Errorf("Content = %q, want %q", got.Content, "send rapport")
	}
	if got.DueTime != "09:00" {
		t.Errorf("DueTime = %q, want 09:00", got.DueTime)
	}
	if got.Priority != 1 {
		t.Errorf("Priority = %d, want 1", got.Priority)
	}
	if got.Project != "Arbejde" {
		t.Errorf("Project = %q, want Arbejde", got.Project)
	}
	if len(got.Labels) != 1 || got.Labels[0] != "fast" {
		t.Errorf("Labels = %v", got.Labels)
	}
	// An explicit clock time with a repetition still anchors on the next occurrence.
	if got.DueDate != "2026-03-16" {
		t.Errorf("DueDate = %q, want 2026-03-16", got.DueDate)
	}
}

// An explicit date wins over the rule's own first occurrence: "hver mandag fra på
// fredag" is unusual, but "hver måned den 20/3" is somebody stating a start.
func TestExplicitDateAnchorsTheSeries(t *testing.T) {
	got := Parse("aflæs måler hver måned 20/3", ref(t), "da")

	if got.Recurrence != "FREQ=MONTHLY" {
		t.Errorf("Recurrence = %q", got.Recurrence)
	}
	if got.DueDate != "2026-03-20" {
		t.Errorf("DueDate = %q, want the stated date 2026-03-20", got.DueDate)
	}
}

func TestRecurrenceTextIsHumanReadable(t *testing.T) {
	got := Parse("vand planterne hver mandag", ref(t), "da")
	if got.RecurrenceText == "" {
		t.Fatal("no readable description was produced")
	}
	if got.RecurrenceText == got.Recurrence {
		t.Errorf("the description is just the raw rule: %q", got.RecurrenceText)
	}
}

// A repetition must be reported as a span so the input box can highlight it.
func TestRecurrenceIsHighlighted(t *testing.T) {
	in := "vand planterne hver mandag"
	got := Parse(in, ref(t), "da")

	var found bool
	for _, s := range got.Spans {
		if s.Kind != KindRepeat {
			continue
		}
		found = true
		if text := in[s.Start:s.End]; text != "hver mandag" {
			t.Errorf("the repeat span covers %q, want %q", text, "hver mandag")
		}
	}
	if !found {
		t.Error("no span of kind repeat")
	}
}
