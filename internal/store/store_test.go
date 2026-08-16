package store

import (
	"context"
	"path/filepath"
	"testing"
)

// openTest gives each test its own database file. In-memory SQLite would be faster,
// but it does not exercise WAL — and WAL is the mode verdande actually runs in.
func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrateAppliesSchema(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// Every table the data model promises must exist after a cold start.
	want := []string{
		"users", "sessions", "invites", "password_resets", "projects",
		"project_members", "sections", "tasks", "labels", "task_labels",
		"reminders", "comments", "attachments", "activity", "filters",
		"api_tokens", "tasks_fts",
	}
	for _, table := range want {
		var n int
		err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE name = ?`, table).Scan(&n)
		if err != nil {
			t.Fatalf("query sqlite_master for %s: %v", table, err)
		}
		if n == 0 {
			t.Errorf("table %s was not created", table)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	var first int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&first); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	db.Close()
	if first == 0 {
		t.Fatal("no migrations were applied")
	}

	// Re-opening must not re-run anything and fail on "table already exists".
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer db2.Close()

	var second int
	if err := db2.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&second); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if second != first {
		t.Errorf("re-opening applied %d further migrations", second-first)
	}
}

func TestPragmasAreSet(t *testing.T) {
	db := openTest(t)

	var journal string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}

	var fk int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Error("foreign_keys is off; cascading deletes would silently not happen")
	}
}

// The FTS index is kept in step with `tasks` by triggers rather than by application
// code, so a task written by any path at all is searchable. That contract is worth
// a test of its own.
func TestFTSTriggersTrackTasks(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	seedTaskFixtures(t, db)

	insert := `INSERT INTO tasks (id, project_id, content, description, created_by, created_at, updated_at)
	           VALUES ('t1', 'p1', 'Betal moms for Q3', 'husk bilag', 'u1', 0, 0)`
	if _, err := db.ExecContext(ctx, insert); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	if got := ftsCount(t, db, "moms"); got != 1 {
		t.Errorf("after insert: %d hits for 'moms', want 1", got)
	}

	if _, err := db.ExecContext(ctx,
		`UPDATE tasks SET content = 'Betal skat for Q3' WHERE id = 't1'`); err != nil {
		t.Fatalf("update task: %v", err)
	}
	if got := ftsCount(t, db, "moms"); got != 0 {
		t.Errorf("after update: %d hits for the old word 'moms', want 0", got)
	}
	if got := ftsCount(t, db, "skat"); got != 1 {
		t.Errorf("after update: %d hits for the new word 'skat', want 1", got)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM tasks WHERE id = 't1'`); err != nil {
		t.Fatalf("delete task: %v", err)
	}
	if got := ftsCount(t, db, "skat"); got != 0 {
		t.Errorf("after delete: %d hits, want 0", got)
	}
}

// Danish is full of letters a diacritic-folding tokenizer will not touch, because
// they are not accented forms of anything. Someone typing "gron" on a keyboard they
// have not switched, or "aarhus" out of habit, still has to find their own tasks.
func TestFTSFindsDanishSpellingVariants(t *testing.T) {
	db := openTest(t)
	seedTaskFixtures(t, db)

	_, err := db.Exec(`INSERT INTO tasks (id, project_id, content, created_by, created_at, updated_at)
	                   VALUES ('t1', 'p1', 'Køb grøn maling til væggen i Århus', 'u1', 0, 0)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Every one of these is the same task, spelled the way somebody might type it.
	queries := []string{
		"grøn", "gron", "GRØN", "Gron", // ø → o
		"væggen", "vaeggen", // æ → ae
		"århus", "aarhus", "arhus", // å → aa, and → a via remove_diacritics
		"køb", "kob",
		"grø", "gro", // prefix matching, both spellings
	}
	for _, q := range queries {
		if got := ftsCount(t, db, q); got != 1 {
			t.Errorf("query %q: %d hits, want 1", q, got)
		}
	}

	if got := ftsCount(t, db, "blå"); got != 0 {
		t.Errorf("query for an absent word returned %d hits, want 0", got)
	}
}

// The search box takes free text, and free text contains the characters FTS5 uses
// as operators. None of them may reach the query planner as syntax.
func TestFTSHandlesQuerySyntaxInUserInput(t *testing.T) {
	db := openTest(t)
	seedTaskFixtures(t, db)

	_, err := db.Exec(`INSERT INTO tasks (id, project_id, content, created_by, created_at, updated_at)
	                   VALUES ('t1', 'p1', 'Budget: Q3 - "final" version (AND review)', 'u1', 0, 0)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Each of these would be a syntax error, or would silently mean something else,
	// if it were handed to MATCH unquoted.
	for _, q := range []string{
		`budget: q3`,
		`"final"`,
		`- final`,
		`AND`,
		`review)`,
		`q3 NEAR final`,
		`^budget`,
		`*`,
	} {
		t.Run(q, func(t *testing.T) {
			expr := MatchExpr(q)
			if expr == "" {
				return // nothing searchable in the input, e.g. "*"
			}
			var n int
			err := db.QueryRow(`SELECT count(*) FROM tasks_fts WHERE tasks_fts MATCH ?`, expr).Scan(&n)
			if err != nil {
				t.Fatalf("query %q produced invalid FTS5 %q: %v", q, expr, err)
			}
		})
	}
}

func TestMatchExprIsEmptyForUnsearchableInput(t *testing.T) {
	for _, q := range []string{"", "   ", "***", "-", `""`, "!@#$%"} {
		if got := MatchExpr(q); got != "" {
			t.Errorf("MatchExpr(%q) = %q, want empty", q, got)
		}
	}
}

func TestFoldDanish(t *testing.T) {
	cases := map[string]string{
		"grøn":   "gron",
		"Ærlig":  "aerlig",
		"Århus":  "aarhus",
		"blåbær": "blaabaer",
		"plain":  "plain",
		"":       "",
	}
	for in, want := range cases {
		if got := FoldDanish(in); got != want {
			t.Errorf("FoldDanish(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSoftDeleteKeepsRowRecoverable(t *testing.T) {
	db := openTest(t)
	seedTaskFixtures(t, db)

	_, err := db.Exec(`INSERT INTO tasks (id, project_id, content, created_by, created_at, updated_at)
	                   VALUES ('t1', 'p1', 'slettet', 'u1', 0, 0)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.Exec(`UPDATE tasks SET deleted_at = 1 WHERE id = 't1'`); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	var content string
	if err := db.QueryRow(`SELECT content FROM tasks WHERE id = 't1'`).Scan(&content); err != nil {
		t.Fatalf("soft-deleted row should still be readable: %v", err)
	}
	if content != "slettet" {
		t.Errorf("content = %q after soft delete", content)
	}
}

func TestConstraints(t *testing.T) {
	db := openTest(t)
	seedTaskFixtures(t, db)

	cases := []struct {
		name string
		stmt string
	}{{
		name: "priority outside 1-4",
		stmt: `INSERT INTO tasks (id, project_id, content, priority, created_by, created_at, updated_at)
		       VALUES ('bad', 'p1', 'x', 9, 'u1', 0, 0)`,
	}, {
		name: "unknown project view_mode",
		stmt: `INSERT INTO projects (id, name, view_mode, owner_id, created_at, updated_at)
		       VALUES ('bad', 'x', 'gantt', 'u1', 0, 0)`,
	}, {
		name: "unknown member role",
		stmt: `INSERT INTO project_members (project_id, user_id, role, added_at)
		       VALUES ('p1', 'u1', 'admin', 0)`,
	}, {
		name: "reminder that is neither absolute nor relative",
		stmt: `INSERT INTO reminders (id, task_id, user_id, created_at)
		       VALUES ('bad', 't-none', 'u1', 0)`,
	}, {
		name: "attachment on both a task and a comment",
		stmt: `INSERT INTO attachments (id, task_id, comment_id, filename, size, path, uploaded_by, created_at)
		       VALUES ('bad', 't-none', 'c-none', 'f', 1, 'p', 'u1', 0)`,
	}, {
		name: "second inbox for one user",
		stmt: `INSERT INTO projects (id, name, owner_id, is_inbox, created_at, updated_at)
		       VALUES ('inbox2', 'Inbox', 'u1', 1, 0, 0)`,
	}, {
		name: "duplicate label name for one user",
		stmt: `INSERT INTO labels (id, user_id, name, created_at) VALUES ('l2', 'u1', 'REGNSKAB', 0)`,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(tc.stmt); err == nil {
				t.Error("statement was accepted; the constraint is not doing its job")
			}
		})
	}
}

func TestEmailUniquenessIsCaseInsensitive(t *testing.T) {
	db := openTest(t)
	seedTaskFixtures(t, db)

	_, err := db.Exec(`INSERT INTO users (id, email, name, password_hash, created_at, updated_at)
	                   VALUES ('u2', 'KRISTIAN@example.com', 'Dup', 'x', 0, 0)`)
	if err == nil {
		t.Error("a second user differing only in case was accepted")
	}
}

func TestCascadeDeleteRemovesChildren(t *testing.T) {
	db := openTest(t)
	seedTaskFixtures(t, db)

	_, err := db.Exec(`INSERT INTO tasks (id, project_id, content, created_by, created_at, updated_at)
	                   VALUES ('parent', 'p1', 'parent', 'u1', 0, 0)`)
	if err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	_, err = db.Exec(`INSERT INTO tasks (id, project_id, parent_id, content, created_by, created_at, updated_at)
	                  VALUES ('child', 'p1', 'parent', 'child', 'u1', 0, 0)`)
	if err != nil {
		t.Fatalf("insert child: %v", err)
	}

	if _, err := db.Exec(`DELETE FROM tasks WHERE id = 'parent'`); err != nil {
		t.Fatalf("delete parent: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM tasks WHERE id = 'child'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("subtask outlived its parent")
	}
}

func seedTaskFixtures(t *testing.T, db *DB) {
	t.Helper()
	stmts := []string{
		`INSERT INTO users (id, email, name, password_hash, created_at, updated_at)
		 VALUES ('u1', 'kristian@example.com', 'Kristian', 'x', 0, 0)`,
		`INSERT INTO projects (id, name, owner_id, is_inbox, created_at, updated_at)
		 VALUES ('p1', 'Inbox', 'u1', 1, 0, 0)`,
		`INSERT INTO labels (id, user_id, name, created_at) VALUES ('l1', 'u1', 'regnskab', 0)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// ftsCount searches the way the application does — through MatchExpr — so the tests
// exercise the query builder and the index together rather than a hand-written
// expression the app never uses.
func ftsCount(t *testing.T, db *DB, query string) int {
	t.Helper()
	expr := MatchExpr(query)
	if expr == "" {
		return 0
	}
	var n int
	err := db.QueryRow(`SELECT count(*) FROM tasks_fts WHERE tasks_fts MATCH ?`, expr).Scan(&n)
	if err != nil {
		t.Fatalf("fts query %q (expr %q): %v", query, expr, err)
	}
	return n
}
