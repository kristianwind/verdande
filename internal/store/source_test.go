package store

import (
	"context"
	"errors"
	"testing"
)

// One mail, one task — however many readers there are.
//
// Nothing could answer "has this already become a task?" before, so a second
// reader of the same mailbox made a second task. That is not hypothetical: a
// briefing run produced "Betal faktura fra Browns" twice in one pass, and a
// separate assistant reading the same inbox does the same thing every sweep.
func TestOneMailBecomesOneTask(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	user := &User{Email: "kristian@example.dk", Name: "Kristian", PasswordHash: "x"}
	if err := db.CreateUser(ctx, user, "Indbakke"); err != nil {
		t.Fatal(err)
	}
	inbox := newProject(t, db, user.ID)

	const thread = "gmail:thread:18f2cabc"
	first := &Task{ProjectID: inbox, Content: "Svar Mette", CreatedBy: user.ID, SourceKey: thread}
	if err := db.CreateTask(ctx, first, nil); err != nil {
		t.Fatalf("first task: %v", err)
	}

	// The same thread again — a rerun, or somebody else's assistant.
	second := &Task{ProjectID: inbox, Content: "Svar Mette", CreatedBy: user.ID, SourceKey: thread}
	err := db.CreateTask(ctx, second, nil)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second task for the same thread: err = %v, want ErrDuplicate", err)
	}

	// And the question that makes the other agent safe.
	id, err := db.TaskIDBySource(ctx, user.ID, thread)
	if err != nil {
		t.Fatal(err)
	}
	if id != first.ID {
		t.Errorf("TaskIDBySource = %q, want %q", id, first.ID)
	}
	if id, err := db.TaskIDBySource(ctx, user.ID, "gmail:thread:aldrig-set"); err != nil || id != "" {
		t.Errorf("an unseen thread answered %q, %v", id, err)
	}
}

// A task somebody typed has no source, and there can be any number of those.
// The index is partial for exactly this reason — without the WHERE clause the
// second hand-written task in the database would be refused.
func TestTasksWithoutASourceDoNotCollide(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	user := &User{Email: "kristian@example.dk", Name: "Kristian", PasswordHash: "x"}
	if err := db.CreateUser(ctx, user, "Indbakke"); err != nil {
		t.Fatal(err)
	}
	inbox := newProject(t, db, user.ID)

	for i, name := range []string{"køb kaffe", "ring til Andreas", "hent pakken"} {
		if err := db.CreateTask(ctx, &Task{ProjectID: inbox, Content: name, CreatedBy: user.ID}, nil); err != nil {
			t.Fatalf("task %d (%s): %v", i, name, err)
		}
	}
	if id, err := db.TaskIDBySource(ctx, user.ID, ""); err != nil || id != "" {
		t.Errorf("an empty source key answered %q, %v", id, err)
	}
}

// Two people on one instance, each with their own mailbox. The same thread id
// reaching both of them is two tasks, correctly — the key is scoped to whoever
// the mail arrived for.
func TestTheSameThreadForTwoPeopleIsTwoTasks(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	var ids []string
	for _, email := range []string{"kristian@example.dk", "andreas@example.dk"} {
		u := &User{Email: email, Name: email, PasswordHash: "x"}
		if err := db.CreateUser(ctx, u, "Indbakke"); err != nil {
			t.Fatal(err)
		}
		inbox := newProject(t, db, u.ID)
		task := &Task{ProjectID: inbox, Content: "Fælles tråd", CreatedBy: u.ID,
			SourceKey: "gmail:thread:delt"}
		if err := db.CreateTask(ctx, task, nil); err != nil {
			t.Fatalf("%s: %v", email, err)
		}
		ids = append(ids, task.ID)
	}
	if ids[0] == ids[1] {
		t.Error("the two people got the same task")
	}
}

// A project to hang the tasks on. The tests are about the source key, not about
// where a task lives.
func newProject(t *testing.T, db *DB, ownerID string) string {
	t.Helper()
	p := &Project{OwnerID: ownerID, Name: "Indbakke"}
	if err := db.CreateProject(context.Background(), p); err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p.ID
}
