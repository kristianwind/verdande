package ics

import (
	"strings"
	"testing"
	"time"
)

func sample() Calendar {
	due := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	done := time.Date(2026, 3, 9, 14, 30, 0, 0, time.UTC)
	duration := 45

	return Calendar{
		Name:   "verdande",
		Domain: "todo.example.dk",
		Tasks: []Task{
			{
				ID: "t1", Content: "Betal moms", Description: "husk bilag",
				ProjectName: "Firma", DueDate: "2026-03-10", DueDatetime: &due,
				DurationMin: &duration, Priority: 1,
				CreatedAt: done, UpdatedAt: done,
			},
			{
				ID: "t2", Content: "Ryd op", DueDate: "2026-03-12", Priority: 4,
				CreatedAt: done, UpdatedAt: done,
			},
			{
				ID: "t3", Content: "Vand planterne", Recurrence: "FREQ=WEEKLY;BYDAY=MO",
				DueDate: "2026-03-16", Priority: 3, CreatedAt: done, UpdatedAt: done,
			},
			{
				ID: "t4", Content: "Færdig ting", Completed: true, CompletedAt: &done,
				CreatedAt: done, UpdatedAt: done,
			},
		},
	}
}

func TestRenderStructure(t *testing.T) {
	out := Render(sample())

	if !strings.HasPrefix(out, "BEGIN:VCALENDAR\r\n") {
		t.Error("the document does not begin with BEGIN:VCALENDAR")
	}
	if !strings.HasSuffix(out, "END:VCALENDAR\r\n") {
		t.Error("the document does not end with END:VCALENDAR")
	}
	if strings.Count(out, "BEGIN:VTODO") != 4 {
		t.Errorf("got %d VTODOs, want one per task", strings.Count(out, "BEGIN:VTODO"))
	}
	if a, b := strings.Count(out, "BEGIN:VTODO"), strings.Count(out, "END:VTODO"); a != b {
		t.Errorf("%d BEGIN:VTODO but %d END:VTODO", a, b)
	}

	// Every line ends CRLF — a bare LF is not iCalendar and some clients refuse it.
	for _, line := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		if strings.Contains(line, "\n") {
			t.Fatalf("a line contains a bare newline: %q", line)
		}
	}
}

// Tasks are VTODO. That is what makes them to-dos in Apple Reminders rather than
// events, and it is the same component CalDAV will serve.
func TestTasksAreTodos(t *testing.T) {
	out := Render(sample())

	if !strings.Contains(out, "SUMMARY:Betal moms") {
		t.Error("the summary is missing")
	}
	if !strings.Contains(out, "UID:t1@todo.example.dk") {
		t.Error("the UID is missing or not domain-qualified")
	}
	if !strings.Contains(out, "STATUS:NEEDS-ACTION") {
		t.Error("an open task is not marked NEEDS-ACTION")
	}
	if !strings.Contains(out, "STATUS:COMPLETED") {
		t.Error("a completed task is not marked COMPLETED")
	}
	if !strings.Contains(out, "RRULE:FREQ=WEEKLY;BYDAY=MO") {
		t.Error("the recurrence rule did not survive into the feed")
	}
	if !strings.Contains(out, "CATEGORIES:Firma") {
		t.Error("the project is not carried as a category")
	}
}

// A whole-day due date must be a DATE, not a DATE-TIME. Emitting midnight in some
// timezone would move the task by a day for anybody east or west of the server.
func TestWholeDayDueDatesAreDates(t *testing.T) {
	out := Render(sample())

	if !strings.Contains(out, "DUE;VALUE=DATE:20260312") {
		t.Error("a dateless task's due date is not a DATE value")
	}
	// The one with a clock time keeps it.
	if !strings.Contains(out, "DUE:20260310T090000Z") {
		t.Error("a timed task lost its clock time")
	}
}

// Google Calendar ignores VTODO. Timed tasks are therefore also emitted as events,
// or a feed is useless in the calendar most people actually keep.
func TestTimedTasksAlsoBecomeEvents(t *testing.T) {
	out := Render(sample())

	if strings.Count(out, "BEGIN:VEVENT") != 1 {
		t.Fatalf("got %d VEVENTs, want one for the single timed task",
			strings.Count(out, "BEGIN:VEVENT"))
	}
	if !strings.Contains(out, "DTSTART:20260310T090000Z") {
		t.Error("the event has no start")
	}
	// 45 minutes of stated duration.
	if !strings.Contains(out, "DTEND:20260310T094500Z") {
		t.Error("the event's end does not reflect the task's duration")
	}
	// The event must not share the to-do's UID, or clients treat them as one item.
	if !strings.Contains(out, "UID:t1-event@todo.example.dk") {
		t.Error("the event does not have a UID of its own")
	}
}

// A comma or semicolon is a value separator in iCalendar. Unescaped, it silently
// truncates the summary at that character in every client.
func TestSpecialCharactersAreEscaped(t *testing.T) {
	now := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	out := Render(Calendar{
		Name: "verdande", Domain: "example.dk",
		Tasks: []Task{{
			ID: "t1", Content: `Køb: mælk, brød; smør \ og mere`,
			Description: "linje 1\nlinje 2",
			CreatedAt:   now, UpdatedAt: now,
		}},
	})

	if !strings.Contains(out, `SUMMARY:Køb: mælk\, brød\; smør \\ og mere`) {
		t.Errorf("the summary is not escaped correctly:\n%s", out)
	}
	if !strings.Contains(out, `DESCRIPTION:linje 1\nlinje 2`) {
		t.Error("a newline in the description was not escaped")
	}
	// And the raw separators must not survive unescaped anywhere in the summary.
	for _, line := range strings.Split(out, "\r\n") {
		if !strings.HasPrefix(line, "SUMMARY:") {
			continue
		}
		body := strings.TrimPrefix(line, "SUMMARY:")
		for i := 0; i < len(body); i++ {
			if (body[i] == ',' || body[i] == ';') && (i == 0 || body[i-1] != '\\') {
				t.Errorf("unescaped %q in %q", body[i], line)
			}
		}
	}
}

// Lines fold at 75 octets, and a fold in the middle of a multi-byte character
// produces a line no parser can read.
func TestLongLinesFoldWithoutBreakingCharacters(t *testing.T) {
	now := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	long := strings.Repeat("ø", 200) // two bytes each, so folds land mid-character
	out := Render(Calendar{
		Name: "verdande", Domain: "example.dk",
		Tasks: []Task{{ID: "t1", Content: long, CreatedAt: now, UpdatedAt: now}},
	})

	for _, line := range strings.Split(out, "\r\n") {
		if len(line) > 75 {
			t.Errorf("a line is %d octets, over the 75 limit: %q", len(line), line)
		}
	}

	// Unfolding must reproduce the original text exactly.
	unfolded := strings.ReplaceAll(out, "\r\n ", "")
	if !strings.Contains(unfolded, "SUMMARY:"+long) {
		t.Error("the summary did not survive folding and unfolding")
	}
}

// P4 means "no priority stated", not "lowest". Mapping it to 9 would arrive in a
// client as an explicit low priority on every ordinary task.
func TestPriorityMapping(t *testing.T) {
	now := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	want := map[int]string{1: "PRIORITY:1", 2: "PRIORITY:5", 3: "PRIORITY:7", 4: "PRIORITY:0"}

	for priority, expected := range want {
		out := Render(Calendar{
			Name: "v", Domain: "example.dk",
			Tasks: []Task{{ID: "t", Content: "x", Priority: priority, CreatedAt: now, UpdatedAt: now}},
		})
		if !strings.Contains(out, expected) {
			t.Errorf("priority %d did not produce %q", priority, expected)
		}
	}
}

func TestEmptyCalendarIsStillValid(t *testing.T) {
	out := Render(Calendar{Name: "verdande", Domain: "example.dk"})

	if !strings.Contains(out, "BEGIN:VCALENDAR") || !strings.Contains(out, "END:VCALENDAR") {
		t.Error("an empty calendar is not a well-formed document")
	}
	if strings.Contains(out, "BEGIN:VTODO") {
		t.Error("an empty calendar contains a task")
	}
}

func TestFilename(t *testing.T) {
	cases := map[string]string{
		"verdande":      "verdande.ics",
		"Mine opgaver":  "mine-opgaver.ics",
		"Q3/2026 møder": "q32026-mder.ics",
		"":              "verdande.ics",
		"???":           "verdande.ics",
	}
	for in, want := range cases {
		if got := Filename(in); got != want {
			t.Errorf("Filename(%q) = %q, want %q", in, got, want)
		}
	}
}

// En abonnementskalender, som den ser ud fra nogen andens software.
//
// Alle fire former, der findes i naturen, i én fil: en heldagsbegivenhed, en med
// et UTC-stempel, en med TZID og lokal tid, og en over flere dage. Plus en, der
// mangler det, et gitter ikke kan undvære, og en foldet linje — begge dele
// kommer, og de kommer uden varsel.
func TestParseCalendarReadsTheFourShapesThatExist(t *testing.T) {
	body := "BEGIN:VCALENDAR\r\n" +
		"VERSION:2.0\r\n" +
		"X-WR-CALNAME:Helligdage\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:heldag@example.dk\r\n" +
		"DTSTART;VALUE=DATE:20260824\r\n" +
		"DTEND;VALUE=DATE:20260825\r\n" +
		"SUMMARY:Grundlovsdag\r\n" +
		"END:VEVENT\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:utc@example.dk\r\n" +
		"DTSTART:20260825T120000Z\r\n" +
		"DTEND:20260825T133000Z\r\n" +
		`SUMMARY:Møde med en meget lang titel\, der er foldet over to lin` + "\r\n" + " jer\r\n" +
		"END:VEVENT\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:lokal@example.dk\r\n" +
		"DTSTART;TZID=Europe/Copenhagen:20260826T090000\r\n" +
		"DTEND;TZID=Europe/Copenhagen:20260826T100000\r\n" +
		"SUMMARY:Tandlæge\r\n" +
		"END:VEVENT\r\n" +
		"BEGIN:VEVENT\r\n" +
		"UID:flerdage@example.dk\r\n" +
		"DTSTART;VALUE=DATE:20260901\r\n" +
		"DTEND;VALUE=DATE:20260904\r\n" +
		"SUMMARY:Ferie\r\n" +
		"END:VEVENT\r\n" +
		"BEGIN:VEVENT\r\n" +
		"SUMMARY:Uden uid og uden start\r\n" +
		"END:VEVENT\r\n" +
		"END:VCALENDAR\r\n"

	cal := ParseCalendar(body)

	if cal.Name != "Helligdage" {
		t.Errorf("kalenderens navn = %q", cal.Name)
	}
	if len(cal.Events) != 4 {
		t.Fatalf("%d begivenheder, want 4 — den femte mangler både uid og start", len(cal.Events))
	}

	// Heldag. DTEND er eksklusiv i filen: en endagsbegivenhed slutter næste morgen.
	// Gemmes inklusivt, ellers dækker hver eneste heldagsbegivenhed én celle for
	// meget — og en helligdag æder dagen efter sig.
	one := cal.Events[0]
	if !one.AllDay || one.StartDay != "2026-08-24" || one.EndDay != "2026-08-24" {
		t.Errorf("heldag: %+v", one)
	}
	if one.StartsAt != "" {
		t.Errorf("en heldagsbegivenhed fik et klokkeslæt: %q", one.StartsAt)
	}

	// UTC, og den foldede linje samlet igen.
	two := cal.Events[1]
	if two.StartsAt != "2026-08-25T12:00:00Z" || two.EndsAt != "2026-08-25T13:30:00Z" {
		t.Errorf("utc: %+v", two)
	}
	if two.Summary != "Møde med en meget lang titel, der er foldet over to linjer" {
		t.Errorf("den foldede linje blev ikke samlet: %q", two.Summary)
	}

	// Lokal tid med TZID: klokkeslættet står, som filen skrev det. At flytte det
	// her ville spørge *denne* proces, hvilken tidszone den er i.
	three := cal.Events[2]
	if three.StartsAt != "2026-08-26T09:00:00" {
		t.Errorf("lokal: %q", three.StartsAt)
	}

	// Flere dage, begge ender inklusive.
	four := cal.Events[3]
	if four.StartDay != "2026-09-01" || four.EndDay != "2026-09-03" {
		t.Errorf("ferie: %s til %s", four.StartDay, four.EndDay)
	}
}

// En fil, der ikke er en kalender, må ikke koste noget. Den hentes på et skema
// uden nogen, der kigger med, og en fejl her ville være en sweep, der stopper.
func TestParseCalendarSurvivesRubbish(t *testing.T) {
	for _, body := range []string{
		"",
		"ikke en kalender overhovedet",
		"BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n",
		"BEGIN:VEVENT\r\nDTSTART:x\r\nEND:VEVENT\r\n",
		"BEGIN:VEVENT\r\nUID:a\r\nDTSTART:2026\r\nEND:VEVENT\r\n",
	} {
		if got := ParseCalendar(body); len(got.Events) != 0 {
			t.Errorf("%q gav %d begivenheder", body, len(got.Events))
		}
	}
}
