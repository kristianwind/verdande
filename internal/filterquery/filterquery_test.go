package filterquery

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func ctx(t *testing.T) Context {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	return Context{
		UserID:   "u1",
		Now:      time.Date(2026, 3, 10, 10, 0, 0, 0, loc),
		Location: loc,
	}
}

func TestParseProducesRunnableSQL(t *testing.T) {
	// Every expression is executed against a real SQLite schema, not merely
	// string-compared. A filter language that compiles to SQL which does not run
	// is worse than no filter language.
	db := schema(t)
	c := ctx(t)

	queries := []string{
		"today",
		"i dag",
		"overdue",
		"forsinket",
		"tomorrow",
		"no date",
		"p1",
		"p4",
		"#Firma",
		"@regnskab",
		"assigned to: me",
		"assigned to: anders@example.dk",
		"tildelt: mig",
		"assigned to: none",
		"assigned",
		"recurring",
		"completed",
		"subtask",
		"7 days",
		"14 dage",
		"2 weeks",
		"2026-12-24",
		"today & p1",
		"today | tomorrow",
		"today, tomorrow",
		"#Firma & @regnskab",
		"!@venter",
		"today & !p4",
		"(today | overdue) & p1",
		"overdue | (today & assigned to: me)",
		"#Firma & (p1 | p2) & !completed",
		"rapport",
		"7 days & #Firma & @vigtig",
	}

	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			compiled, err := Parse(q, c)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if compiled.SQL == "" {
				t.Fatal("compiled to an empty condition")
			}
			// The fragment has to slot into the real task query.
			stmt := `SELECT count(*) FROM tasks t
			         JOIN projects p ON p.id = t.project_id
			         WHERE ` + compiled.SQL
			var n int
			if err := db.QueryRow(stmt, compiled.Args...).Scan(&n); err != nil {
				t.Fatalf("query failed: %v\nSQL: %s", err, stmt)
			}
		})
	}
}

// The compiled fragment must actually select the right rows, not merely run.
func TestFiltersSelectTheRightTasks(t *testing.T) {
	db := schema(t)
	c := ctx(t)
	seed(t, db)

	cases := []struct {
		query string
		want  []string
	}{
		{"today", []string{"i-dag-p1"}},
		{"overdue", []string{"forsinket"}},
		{"tomorrow", []string{"i-morgen"}},
		// firma-p3 has no due date either, so it belongs here.
		{"no date", []string{"ingen-dato", "underopgave", "firma-p3"}},
		{"p1", []string{"i-dag-p1"}},
		{"#Firma", []string{"i-dag-p1", "firma-p3"}},
		{"@regnskab", []string{"i-dag-p1"}},
		{"assigned to: me", []string{"i-dag-p1"}},
		{"assigned to: none", []string{"forsinket", "i-morgen", "ingen-dato", "firma-p3", "underopgave"}},
		{"recurring", []string{"i-morgen"}},
		{"subtask", []string{"underopgave"}},

		// Combinations
		{"today & p1", []string{"i-dag-p1"}},
		{"today | overdue", []string{"forsinket", "i-dag-p1"}},
		{"#Firma & p1", []string{"i-dag-p1"}},
		{"#Firma & !p1", []string{"firma-p3"}},
		// u1's "regnskab" is on i-dag-p1 only, so the other Firma task survives.
		{"!@regnskab & #Firma", []string{"firma-p3"}},
		{"(today | tomorrow) & !p1", []string{"i-morgen"}},

		// A window, not a single day.
		{"7 days", []string{"i-dag-p1", "i-morgen"}},

		// Free text falls through to a content search.
		{"moms", []string{"i-dag-p1"}},
	}

	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			compiled, err := Parse(tc.query, c)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := run(t, db, compiled)
			if !sameSet(got, tc.want) {
				t.Errorf("matched %v, want %v", got, tc.want)
			}
		})
	}
}

// '&' binds tighter than '|', so "a | b & c" is "a | (b & c)".
func TestAndBindsTighterThanOr(t *testing.T) {
	db := schema(t)
	c := ctx(t)
	seed(t, db)

	loose, err := Parse("overdue | today & p1", c)
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := Parse("overdue | (today & p1)", c)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(run(t, db, loose), run(t, db, explicit)) {
		t.Error("'a | b & c' does not mean 'a | (b & c)'")
	}

	// And the other grouping really is different, or the test above proves nothing.
	other, err := Parse("(overdue | today) & p1", c)
	if err != nil {
		t.Fatal(err)
	}
	if sameSet(run(t, db, loose), run(t, db, other)) {
		t.Error("both groupings select the same rows; the precedence test is vacuous")
	}
}

func TestErrors(t *testing.T) {
	c := ctx(t)
	for _, q := range []string{
		"",
		"   ",
		"&",
		"today &",
		"| today",
		"(today",
		"today)",
		"()",
		"#",
		"@",
		"assigned to:",
	} {
		t.Run(q, func(t *testing.T) {
			if _, err := Parse(q, c); err == nil {
				t.Error("accepted an expression that is not valid")
			}
		})
	}
}

// The filter box takes free text from a person. None of it may reach the database
// as SQL — every value has to arrive as a bound parameter.
func TestNoUserTextReachesTheSQL(t *testing.T) {
	db := schema(t)
	c := ctx(t)
	seed(t, db)

	hostile := []string{
		`#'; DROP TABLE tasks; --`,
		`@' OR 1=1 --`,
		`assigned to: ' OR '1'='1`,
		`'; DELETE FROM projects; --`,
		`x' UNION SELECT password_hash FROM users --`,
	}

	for _, q := range hostile {
		t.Run(q, func(t *testing.T) {
			compiled, err := Parse(q, c)
			if err != nil {
				return // refusing it outright is also a correct answer
			}
			// The property is that the typed text appears only among the bound
			// arguments — never in the statement. Scanning the SQL for words like
			// DELETE would instead flag the column name `deleted_at`, which proves
			// nothing about injection.
			for _, needle := range []string{"DROP TABLE", "DELETE FROM", "UNION SELECT", "OR 1=1", "'1'='1"} {
				if strings.Contains(strings.ToUpper(compiled.SQL), needle) {
					t.Fatalf("%q was built into the SQL: %s", needle, compiled.SQL)
				}
			}
			// And it still executes, so the expression is not merely inert text —
			// it reaches the database as a real query with the hostile part bound.
			run(t, db, compiled)
		})
	}

	// And everything is still there.
	var tasks, projects int
	if err := db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&tasks); err != nil {
		t.Fatalf("the tasks table did not survive: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM projects`).Scan(&projects); err != nil {
		t.Fatalf("the projects table did not survive: %v", err)
	}
	if tasks == 0 || projects == 0 {
		t.Error("rows were deleted by a filter expression")
	}
}

// Labels are personal: the same word on another person's task is a different label.
func TestLabelsAreScopedToTheAskingUser(t *testing.T) {
	db := schema(t)
	seed(t, db)

	// Both users have a label called "regnskab", on different tasks. Each must see
	// only their own — that is what "labels are personal" has to mean in practice.
	mine, err := Parse("@regnskab", ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, db, mine); !sameSet(got, []string{"i-dag-p1"}) {
		t.Errorf("u1 matched %v, want only their own labelled task", got)
	}

	other := ctx(t)
	other.UserID = "u2"
	theirs, err := Parse("@regnskab", other)
	if err != nil {
		t.Fatal(err)
	}
	if got := run(t, db, theirs); !sameSet(got, []string{"firma-p3"}) {
		t.Errorf("u2 matched %v, want only their own labelled task", got)
	}
}

// --- helpers ------------------------------------------------------------------

func schema(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "f.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	stmts := []string{
		`CREATE TABLE users (id TEXT PRIMARY KEY, email TEXT, name TEXT, password_hash TEXT)`,
		`CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT, deleted_at INTEGER)`,
		`CREATE TABLE labels (id TEXT PRIMARY KEY, user_id TEXT, name TEXT)`,
		`CREATE TABLE task_labels (task_id TEXT, label_id TEXT)`,
		`CREATE TABLE tasks (
			id TEXT PRIMARY KEY, project_id TEXT, parent_id TEXT, content TEXT,
			description TEXT DEFAULT '', priority INTEGER DEFAULT 4,
			due_date TEXT, recurrence_rule TEXT, assignee_id TEXT,
			completed_at INTEGER, deleted_at INTEGER)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

func seed(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`INSERT INTO users (id, email, name) VALUES
		 ('u1','kristian@example.dk','Kristian'), ('u2','anders@example.dk','Anders')`,
		`INSERT INTO projects (id, name) VALUES ('p-firma','Firma'), ('p-inbox','Indbakke')`,
		`INSERT INTO labels (id, user_id, name) VALUES
		 ('l-regnskab','u1','regnskab'), ('l-andens','u2','regnskab')`,

		// 10 March 2026 is "today" for these tests.
		`INSERT INTO tasks (id, project_id, content, priority, due_date, assignee_id) VALUES
		 ('i-dag-p1','p-firma','betal moms',1,'2026-03-10','u1')`,
		`INSERT INTO tasks (id, project_id, content, priority, due_date) VALUES
		 ('forsinket','p-inbox','var forsinket',3,'2026-03-01')`,
		`INSERT INTO tasks (id, project_id, content, priority, due_date, recurrence_rule) VALUES
		 ('i-morgen','p-inbox','i morgen',3,'2026-03-11','FREQ=DAILY')`,
		`INSERT INTO tasks (id, project_id, content, priority) VALUES
		 ('ingen-dato','p-inbox','ingen dato',4)`,
		`INSERT INTO tasks (id, project_id, content, priority) VALUES
		 ('firma-p3','p-firma','noget andet',3)`,
		`INSERT INTO tasks (id, project_id, parent_id, content, priority) VALUES
		 ('underopgave','p-inbox','ingen-dato','en underopgave',4)`,

		`INSERT INTO task_labels (task_id, label_id) VALUES ('i-dag-p1','l-regnskab')`,
		// The other user's identically-named label, on a task of theirs.
		`INSERT INTO task_labels (task_id, label_id) VALUES ('firma-p3','l-andens')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func run(t *testing.T, db *sql.DB, c Compiled) []string {
	t.Helper()
	rows, err := db.Query(`SELECT t.id FROM tasks t
	                       JOIN projects p ON p.id = t.project_id
	                       WHERE `+c.SQL+` ORDER BY t.id`, c.Args...)
	if err != nil {
		t.Fatalf("query: %v\nSQL: %s", err, c.SQL)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
