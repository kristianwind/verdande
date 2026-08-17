package todoist

import (
	"bytes"
	"strings"
	"testing"
)

// A real Todoist template export, with the shapes that matter: a task tree, a
// section, notes, priorities and dates.
const sampleCSV = `TYPE,CONTENT,DESCRIPTION,PRIORITY,INDENT,AUTHOR,RESPONSIBLE,DATE,DATE_LANG,TIMEZONE
task,Betal moms,husk bilag,4,1,Kristian,,2026-03-15,en,
note,Bilag ligger i Dropbox,,,,,,,,
task,Find bilag,,3,2,Kristian,,,,
task,Scan dem,,1,3,Kristian,,,,
task,Ring til revisor,,1,1,Kristian,Anders,,,
,,,,,,,,,
section,Til gennemsyn,,,,,,,,
task,Gennemgå kvartal,,2,1,Kristian,,,,
task,Underopgave,,1,2,Kristian,,,,
`

func TestParseReadsTheShape(t *testing.T) {
	rows, err := Parse(strings.NewReader(sampleCSV))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// The blank spacer row is dropped; everything else survives.
	var tasks, sections, notes int
	for _, r := range rows {
		switch r.Type {
		case TypeTask:
			tasks++
		case TypeSection:
			sections++
		case TypeNote:
			notes++
		}
	}
	if tasks != 6 {
		t.Errorf("got %d tasks, want 6", tasks)
	}
	if sections != 1 {
		t.Errorf("got %d sections, want 1", sections)
	}
	if notes != 1 {
		t.Errorf("got %d notes, want 1", notes)
	}
}

// Todoist's CSV numbers priorities the opposite way to how its interface shows
// them. Getting this backwards silently inverts the urgency of every task.
func TestPriorityIsInverted(t *testing.T) {
	cases := map[int]int{4: 1, 3: 2, 2: 3, 1: 4, 0: 4}
	for todoist, want := range cases {
		if got := PriorityToVerdande(todoist); got != want {
			t.Errorf("PriorityToVerdande(%d) = %d, want %d", todoist, got, want)
		}
	}
	for verdande, want := range map[int]int{1: 4, 2: 3, 3: 2, 4: 1} {
		if got := PriorityFromVerdande(verdande); got != want {
			t.Errorf("PriorityFromVerdande(%d) = %d, want %d", verdande, got, want)
		}
	}
	// And the two are inverses, which is what the round trip depends on.
	for v := 1; v <= 4; v++ {
		if got := PriorityToVerdande(PriorityFromVerdande(v)); got != v {
			t.Errorf("priority %d survived a round trip as %d", v, got)
		}
	}
}

func TestFromRowsBuildsTheTree(t *testing.T) {
	rows, err := Parse(strings.NewReader(sampleCSV))
	if err != nil {
		t.Fatal(err)
	}
	p := FromRows(rows, "Firma")

	if p.Name != "Firma" {
		t.Errorf("Name = %q", p.Name)
	}
	if len(p.Tasks) != 2 {
		t.Fatalf("got %d top-level tasks, want 2", len(p.Tasks))
	}

	moms := p.Tasks[0]
	if moms.Content != "Betal moms" {
		t.Fatalf("first task = %q", moms.Content)
	}
	if moms.Priority != 1 {
		t.Errorf("priority = %d, want 1 (Todoist's 4)", moms.Priority)
	}
	if moms.Date != "2026-03-15" {
		t.Errorf("date = %q", moms.Date)
	}
	if len(moms.Comments) != 1 || moms.Comments[0] != "Bilag ligger i Dropbox" {
		t.Errorf("comments = %v", moms.Comments)
	}

	// Indent 2 is a child of the indent-1 task above it; indent 3 is a child of that.
	if len(moms.Children) != 1 {
		t.Fatalf("got %d children, want 1", len(moms.Children))
	}
	if moms.Children[0].Content != "Find bilag" {
		t.Errorf("child = %q", moms.Children[0].Content)
	}
	if len(moms.Children[0].Children) != 1 {
		t.Fatalf("got %d grandchildren, want 1", len(moms.Children[0].Children))
	}
	if moms.Children[0].Children[0].Content != "Scan dem" {
		t.Errorf("grandchild = %q", moms.Children[0].Children[0].Content)
	}

	// The assignee survives.
	if p.Tasks[1].Assignee != "Anders" {
		t.Errorf("assignee = %q, want Anders", p.Tasks[1].Assignee)
	}

	// A section resets the tree, and its own tasks nest inside it.
	if len(p.Sections) != 1 {
		t.Fatalf("got %d sections", len(p.Sections))
	}
	section := p.Sections[0]
	if section.Name != "Til gennemsyn" {
		t.Errorf("section name = %q", section.Name)
	}
	if len(section.Tasks) != 1 {
		t.Fatalf("the section has %d top-level tasks, want 1", len(section.Tasks))
	}
	if len(section.Tasks[0].Children) != 1 {
		t.Errorf("the section's task has %d children, want 1", len(section.Tasks[0].Children))
	}
}

// The requirement from the brief, stated directly: a Todoist CSV in, and out again,
// has to be the same thing. This is the test that stops an import from quietly
// losing a sub-task, a comment or a priority.
func TestRoundTripIsLossless(t *testing.T) {
	rows, err := Parse(strings.NewReader(sampleCSV))
	if err != nil {
		t.Fatal(err)
	}
	project := FromRows(rows, "Firma")

	var out bytes.Buffer
	if err := Write(&out, ToRows(project)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read what was written and rebuild. The two projects must be identical.
	reparsed, err := Parse(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatalf("re-parsing our own output: %v", err)
	}
	again := FromRows(reparsed, "Firma")

	assertSameProject(t, project, again)
}

// And a second pass must produce byte-identical output, or "export" is not a
// repeatable operation and diffing two exports is meaningless.
func TestExportIsStable(t *testing.T) {
	rows, err := Parse(strings.NewReader(sampleCSV))
	if err != nil {
		t.Fatal(err)
	}
	project := FromRows(rows, "Firma")

	var first, second bytes.Buffer
	if err := Write(&first, ToRows(project)); err != nil {
		t.Fatal(err)
	}
	reparsed, err := Parse(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(&second, ToRows(FromRows(reparsed, "Firma"))); err != nil {
		t.Fatal(err)
	}

	if first.String() != second.String() {
		t.Errorf("two exports of the same project differ:\n--- first ---\n%s\n--- second ---\n%s",
			first.String(), second.String())
	}
}

// What is written has to be readable by Todoist, which means the exact header and
// no priority or indent on rows that are not tasks.
func TestWrittenFileMatchesTodoistsFormat(t *testing.T) {
	var out bytes.Buffer
	err := Write(&out, ToRows(Project{
		Name:  "Firma",
		Tasks: []Task{{Content: "En opgave", Priority: 1}},
		Sections: []Section{{
			Name:  "En sektion",
			Tasks: []Task{{Content: "I sektionen", Priority: 4, Comments: []string{"en note"}}},
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")

	want := "TYPE,CONTENT,DESCRIPTION,PRIORITY,INDENT,AUTHOR,RESPONSIBLE,DATE,DATE_LANG,TIMEZONE"
	if strings.TrimSpace(lines[0]) != want {
		t.Errorf("header:\n got %q\nwant %q", lines[0], want)
	}

	for _, line := range lines[1:] {
		fields := strings.Split(line, ",")
		if len(fields) < 5 {
			continue
		}
		// Only task rows carry a priority and an indent.
		if fields[0] != TypeTask && (fields[3] != "" || fields[4] != "") {
			t.Errorf("a %q row carries a priority or indent: %q", fields[0], line)
		}
		if fields[0] == TypeTask && fields[4] == "" {
			t.Errorf("a task row has no indent: %q", line)
		}
	}
}

// Files arrive having been opened in a spreadsheet, which is where BOMs, semicolons
// and reordered columns come from. Refusing those means refusing most real exports.
func TestParseSurvivesRealWorldFiles(t *testing.T) {
	t.Run("utf-8 BOM", func(t *testing.T) {
		withBOM := "\xEF\xBB\xBF" + sampleCSV
		rows, err := Parse(strings.NewReader(withBOM))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(rows) == 0 {
			t.Fatal("no rows were read")
		}
		if rows[0].Content != "Betal moms" {
			t.Errorf("first row = %+v; the BOM was probably left in the header", rows[0])
		}
	})

	t.Run("reordered columns", func(t *testing.T) {
		reordered := "CONTENT,TYPE,INDENT,PRIORITY,DESCRIPTION,AUTHOR,RESPONSIBLE,DATE,DATE_LANG,TIMEZONE\n" +
			"Betal moms,task,1,4,,Kristian,,2026-03-15,en,\n"
		rows, err := Parse(strings.NewReader(reordered))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d rows", len(rows))
		}
		if rows[0].Content != "Betal moms" || rows[0].Type != TypeTask || rows[0].Priority != 4 {
			t.Errorf("columns were read by position, not by name: %+v", rows[0])
		}
	})

	t.Run("short rows", func(t *testing.T) {
		short := "TYPE,CONTENT,DESCRIPTION,PRIORITY,INDENT\ntask,Kort række\n"
		rows, err := Parse(strings.NewReader(short))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(rows) != 1 || rows[0].Content != "Kort række" {
			t.Errorf("rows = %+v", rows)
		}
	})

	t.Run("danish characters and commas in content", func(t *testing.T) {
		var out bytes.Buffer
		original := Project{Tasks: []Task{
			{Content: `Køb mælk, brød og smør`, Description: "på vej hjem", Priority: 2},
		}}
		if err := Write(&out, ToRows(original)); err != nil {
			t.Fatal(err)
		}
		rows, err := Parse(bytes.NewReader(out.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		back := FromRows(rows, "")
		if back.Tasks[0].Content != `Køb mælk, brød og smør` {
			t.Errorf("content = %q; the comma was probably not quoted", back.Tasks[0].Content)
		}
	})
}

func TestParseRejectsWhatIsNotAnExport(t *testing.T) {
	for _, in := range []string{
		"",
		"navn;pris\nkaffe;40\n",
		"just some text\n",
	} {
		t.Run(in, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(in)); err == nil {
				t.Error("a file that is not a Todoist export was accepted")
			}
		})
	}
}

// A hand-edited file can jump from indent 1 to indent 3. Dropping those tasks would
// lose work silently; attaching them one level deeper than what is open does not.
func TestBrokenIndentationDoesNotLoseTasks(t *testing.T) {
	broken := `TYPE,CONTENT,DESCRIPTION,PRIORITY,INDENT,AUTHOR,RESPONSIBLE,DATE,DATE_LANG,TIMEZONE
task,Forælder,,1,1,,,,,
task,Springer til tre,,1,3,,,,,
task,Tilbage til en,,1,1,,,,,
`
	rows, err := Parse(strings.NewReader(broken))
	if err != nil {
		t.Fatal(err)
	}
	p := FromRows(rows, "x")

	if got := countTasks(p); got != 3 {
		t.Errorf("%d tasks survived, want all 3", got)
	}
	if len(p.Tasks) != 2 {
		t.Errorf("got %d top-level tasks, want 2", len(p.Tasks))
	}
}

func TestParseDate(t *testing.T) {
	cases := []struct {
		in       string
		wantDate string
		wantOK   bool
	}{
		{"2026-03-15", "2026-03-15", true},
		{"2026-03-15 10:00", "2026-03-15", true},
		{"15-03-2026", "2026-03-15", true},
		{"15/03/2026", "2026-03-15", true},
		// Todoist stores what was typed, so most dates are natural language and
		// belong to the caller's own parser.
		{"every Monday", "", false},
		{"tomorrow", "", false},
		{"i morgen", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			date, remaining, ok := ParseDate(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if date != tc.wantDate {
				t.Errorf("date = %q, want %q", date, tc.wantDate)
			}
			if !ok && tc.in != "" && remaining != tc.in {
				t.Errorf("remaining = %q, want the original text back", remaining)
			}
		})
	}
}

// --- helpers ---------------------------------------------------------------------

func assertSameProject(t *testing.T, want, got Project) {
	t.Helper()

	if len(want.Tasks) != len(got.Tasks) {
		t.Fatalf("top-level tasks: %d vs %d", len(want.Tasks), len(got.Tasks))
	}
	for i := range want.Tasks {
		assertSameTask(t, "task["+want.Tasks[i].Content+"]", want.Tasks[i], got.Tasks[i])
	}

	if len(want.Sections) != len(got.Sections) {
		t.Fatalf("sections: %d vs %d", len(want.Sections), len(got.Sections))
	}
	for i := range want.Sections {
		if want.Sections[i].Name != got.Sections[i].Name {
			t.Errorf("section name: %q vs %q", want.Sections[i].Name, got.Sections[i].Name)
		}
		if len(want.Sections[i].Tasks) != len(got.Sections[i].Tasks) {
			t.Fatalf("section %q tasks: %d vs %d", want.Sections[i].Name,
				len(want.Sections[i].Tasks), len(got.Sections[i].Tasks))
		}
		for j := range want.Sections[i].Tasks {
			assertSameTask(t, "section "+want.Sections[i].Name,
				want.Sections[i].Tasks[j], got.Sections[i].Tasks[j])
		}
	}
}

func assertSameTask(t *testing.T, where string, want, got Task) {
	t.Helper()

	if want.Content != got.Content {
		t.Errorf("%s content: %q vs %q", where, want.Content, got.Content)
	}
	if want.Description != got.Description {
		t.Errorf("%s description: %q vs %q", where, want.Description, got.Description)
	}
	if want.Priority != got.Priority {
		t.Errorf("%s priority: %d vs %d", where, want.Priority, got.Priority)
	}
	if want.Date != got.Date {
		t.Errorf("%s date: %q vs %q", where, want.Date, got.Date)
	}
	if want.Assignee != got.Assignee {
		t.Errorf("%s assignee: %q vs %q", where, want.Assignee, got.Assignee)
	}
	if len(want.Comments) != len(got.Comments) {
		t.Errorf("%s comments: %v vs %v", where, want.Comments, got.Comments)
	} else {
		for i := range want.Comments {
			if want.Comments[i] != got.Comments[i] {
				t.Errorf("%s comment %d: %q vs %q", where, i, want.Comments[i], got.Comments[i])
			}
		}
	}
	if len(want.Children) != len(got.Children) {
		t.Fatalf("%s children: %d vs %d", where, len(want.Children), len(got.Children))
	}
	for i := range want.Children {
		assertSameTask(t, where+" > "+want.Children[i].Content, want.Children[i], got.Children[i])
	}
}

func countTasks(p Project) int {
	var count func([]Task) int
	count = func(tasks []Task) int {
		n := 0
		for _, t := range tasks {
			n += 1 + count(t.Children)
		}
		return n
	}
	total := count(p.Tasks)
	for _, s := range p.Sections {
		total += count(s.Tasks)
	}
	return total
}
