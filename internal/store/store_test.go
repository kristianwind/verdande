package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
	"time"
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

// Two people saving at the same moment must both succeed.
//
// This is a regression test for a specific and badly-behaved failure: with the
// default deferred BEGIN, a write transaction starts as a reader and asks for the
// write lock partway through. SQLite refuses that upgrade with SQLITE_BUSY at once
// and does *not* apply busy_timeout to it — so the contention surfaced as a 500
// two milliseconds after the request arrived, while every single-threaded test
// passed. `_txlock=immediate` is what makes the timeout actually cover writers.
func TestConcurrentWritersDoNotHitSQLiteBusy(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	seedTaskFixtures(t, db)

	const writers = 8
	errs := make(chan error, writers)

	start := make(chan struct{})
	for i := range writers {
		go func(i int) {
			<-start // all of them ask for the write lock at once
			errs <- db.CreateTask(ctx, &Task{
				ProjectID: "p1",
				Content:   fmt.Sprintf("opgave %d", i),
				CreatedBy: "u1",
			}, nil)
		}(i)
	}
	close(start)

	for range writers {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent write failed: %v", err)
		}
	}

	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM tasks WHERE project_id = 'p1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != writers {
		t.Errorf("wrote %d tasks, want %d", n, writers)
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

// The rebuild in 0008 is the one migration that does not just add: it copies the
// tasks table, drops the original and renames the copy back. Every test above this
// one starts from an empty database, where a rebuild that loses rows looks exactly
// like a rebuild that works — so this one carries data across it.
//
// What can go wrong, in the order it would: the drop cascades and takes the child
// rows with it; the copy misses a column; the FTS index is left keyed on rowids the
// rebuilt table no longer hands out; and finally the thing the migration is for,
// which is that deleting an account stops deleting other people's work.
func TestTheTasksRebuildCarriesTheDataAcross(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")

	// A database at 0007: the schema as it shipped in v0.10.2.
	sqlDB, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sqlDB.Close()
	if _, err := sqlDB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	for _, entry := range entries {
		name := filepath.Base(entry)
		if name >= "0008" {
			break
		}
		body, err := migrationFS.ReadFile(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := sqlDB.ExecContext(ctx,
			`INSERT INTO schema_migrations (name, applied_at) VALUES (?, unixepoch())`, name); err != nil {
			t.Fatal(err)
		}
	}

	// Two people, one project each, and a task in each project written by the
	// other — which is the shape the migration exists for. Plus a label, a comment
	// and a sub-task, so the child tables have something to lose.
	now := time.Now().Unix()
	for _, stmt := range []string{
		`INSERT INTO users (id, email, name, password_hash, avatar_color, is_admin, created_at, updated_at)
		 VALUES ('u1', 'en@example.dk', 'En', 'x', 'graphite', 1, ` + fmt.Sprint(now) + `, ` + fmt.Sprint(now) + `),
		        ('u2', 'to@example.dk', 'To', 'x', 'graphite', 0, ` + fmt.Sprint(now) + `, ` + fmt.Sprint(now) + `)`,
		`INSERT INTO projects (id, owner_id, name, color, created_at, updated_at)
		 VALUES ('p1', 'u1', 'Ens', 'graphite', ` + fmt.Sprint(now) + `, ` + fmt.Sprint(now) + `),
		        ('p2', 'u2', 'Tos', 'graphite', ` + fmt.Sprint(now) + `, ` + fmt.Sprint(now) + `)`,
		`INSERT INTO tasks (id, project_id, content, description, created_by, priority, sort_order, created_at, updated_at)
		 VALUES ('t1', 'p1', 'grøn hæk', 'i ens eget', 'u1', 2, 1.0, ` + fmt.Sprint(now) + `, ` + fmt.Sprint(now) + `),
		        ('t2', 'p2', 'skrevet af en anden', '', 'u1', 4, 2.0, ` + fmt.Sprint(now) + `, ` + fmt.Sprint(now) + `)`,
		`INSERT INTO tasks (id, project_id, parent_id, content, created_by, sort_order, created_at, updated_at)
		 VALUES ('t3', 'p1', 't1', 'undertask', 'u1', 3.0, ` + fmt.Sprint(now) + `, ` + fmt.Sprint(now) + `)`,
		`INSERT INTO labels (id, user_id, name, created_at) VALUES ('l1', 'u1', 'have', ` + fmt.Sprint(now) + `)`,
		`INSERT INTO task_labels (task_id, label_id) VALUES ('t1', 'l1')`,
		`INSERT INTO comments (id, task_id, user_id, body, created_at, updated_at)
		 VALUES ('c1', 't1', 'u1', 'en kommentar', ` + fmt.Sprint(now) + `, ` + fmt.Sprint(now) + `)`,
		// 0009 rebuilds activity and 0012 rebuilds attachments, so both need rows
		// here: a rebuild that drops them looks exactly like one that works.
		`INSERT INTO activity (id, project_id, task_id, user_id, event, payload_json, created_at)
		 VALUES ('a1', 'p1', 't1', 'u1', 'task.created', '{}', ` + fmt.Sprint(now) + `),
		        ('a2', 'p2', NULL, 'u1', 'project.created', '{}', ` + fmt.Sprint(now) + `)`,
		`INSERT INTO attachments (id, task_id, filename, mime_type, size, path, uploaded_by, created_at)
		 VALUES ('f1', 't1', 'tegning.pdf', 'application/pdf', 1024, 'ab/cd/abcd', 'u1', ` + fmt.Sprint(now) + `)`,
		`INSERT INTO project_groups (id, owner_id, name, color, collapsed, sort_order, created_at, updated_at)
		 VALUES ('g1', 'u1', 'Arbejde', 'graphite', 0, 1024, ` + fmt.Sprint(now) + `, ` + fmt.Sprint(now) + `)`,
	} {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}
	sqlDB.Close()

	// Now the upgrade, through the real runner.
	db, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer db.Close()

	var tasks, labels, comments int
	if err := db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM task_labels`).Scan(&labels); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM comments`).Scan(&comments); err != nil {
		t.Fatal(err)
	}
	if tasks != 3 || labels != 1 || comments != 1 {
		t.Fatalf("after the rebuild: %d tasks, %d labels, %d comments — want 3, 1, 1. "+
			"A drop that cascaded would look exactly like this", tasks, labels, comments)
	}

	// 0009 and 0012 rebuild activity and attachments by the same machinery. They
	// run against a live database on the next restart, and the thing that goes
	// wrong in a rebuild is rows quietly not arriving.
	var activity, attachments, groups int
	if err := db.QueryRow(`SELECT count(*) FROM activity`).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM attachments`).Scan(&attachments); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM project_groups`).Scan(&groups); err != nil {
		t.Fatal(err)
	}
	if activity != 2 || attachments != 1 || groups != 1 {
		t.Fatalf("after 0009 and 0012: %d activity rows, %d attachments, %d groups — want 2, 1, 1",
			activity, attachments, groups)
	}

	// The attachment kept its parent and its bytes. A rebuild that lost the task_id
	// leaves a row nothing can reach, which is worse than losing it outright.
	var fileParent, filename string
	if err := db.QueryRow(
		`SELECT task_id, filename FROM attachments WHERE id = 'f1'`).Scan(&fileParent, &filename); err != nil {
		t.Fatal(err)
	}
	if fileParent != "t1" || filename != "tegning.pdf" {
		t.Errorf("the attachment came across as %q on %q", filename, fileParent)
	}

	// And the new columns arrived with their defaults rather than as NULL, which is
	// what an ALTER that forgot NOT NULL DEFAULT would give.
	var groupAbout, sidebar string
	if err := db.QueryRow(`SELECT description FROM project_groups WHERE id = 'g1'`).Scan(&groupAbout); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT sidebar_collapsed FROM users WHERE id = 'u1'`).Scan(&sidebar); err != nil {
		t.Fatal(err)
	}
	if groupAbout != "" || sidebar != "[]" {
		t.Errorf("new columns on existing rows: description %q, sidebar_collapsed %q", groupAbout, sidebar)
	}

	// Every column came across, including the generated one and the sub-task's
	// self-reference.
	var content, description, parent string
	var priority int
	if err := db.QueryRow(
		`SELECT content, description, priority FROM tasks WHERE id = 't1'`).
		Scan(&content, &description, &priority); err != nil {
		t.Fatal(err)
	}
	if content != "grøn hæk" || description != "i ens eget" || priority != 2 {
		t.Errorf("t1 came across as %q/%q/%d", content, description, priority)
	}
	if err := db.QueryRow(`SELECT parent_id FROM tasks WHERE id = 't3'`).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if parent != "t1" {
		t.Errorf("the sub-task lost its parent: %q", parent)
	}

	// The FTS index was rebuilt against the new rowids. Searching the fold column
	// is the sharpest check: it is generated, so a copy that skipped it would still
	// have the row and not the index entry.
	for _, q := range []string{"grøn", "gron", "haek"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM tasks_fts WHERE tasks_fts MATCH ?`, q).Scan(&n); err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if n != 1 {
			t.Errorf("search for %q found %d rows, want 1 — the index is keyed on rowids "+
				"the rebuilt table no longer hands out", q, n)
		}
	}

	// And the point of the whole thing.
	if err := db.DeleteUser(ctx, "u1"); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var survived int
	if err := db.QueryRow(`SELECT count(*) FROM tasks WHERE id = 't2'`).Scan(&survived); err != nil {
		t.Fatal(err)
	}
	if survived != 1 {
		t.Fatal("a task written in somebody else's project went with the account")
	}
	var author sql.NullString
	if err := db.QueryRow(`SELECT created_by FROM tasks WHERE id = 't2'`).Scan(&author); err != nil {
		t.Fatal(err)
	}
	if author.Valid {
		t.Errorf("created_by = %q, want null", author.String)
	}
	// Their own project still goes, and everything in it with it.
	var own int
	if err := db.QueryRow(`SELECT count(*) FROM tasks WHERE project_id = 'p1'`).Scan(&own); err != nil {
		t.Fatal(err)
	}
	if own != 0 {
		t.Errorf("%d tasks survived in the deleted account's own project", own)
	}
}
