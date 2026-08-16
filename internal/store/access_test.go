package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// The permission matrix, stated once. Everything below checks against this.
func TestRoleCapabilities(t *testing.T) {
	cases := []struct {
		role                   Role
		view, edit, manage, ok bool
	}{
		{RoleOwner, true, true, true, true},
		{RoleEditor, true, true, false, true},
		{RoleViewer, true, false, false, true},
		// Anything that is not one of the three is not a lesser role — it is none.
		{Role(""), false, false, false, false},
		{Role("admin"), false, false, false, false},
		{Role("OWNER"), false, false, false, false},
		{Role("editor "), false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.role), func(t *testing.T) {
			if got := tc.role.Valid(); got != tc.ok {
				t.Errorf("Valid() = %v, want %v", got, tc.ok)
			}
			if got := tc.role.CanView(); got != tc.view {
				t.Errorf("CanView() = %v, want %v", got, tc.view)
			}
			if got := tc.role.CanEdit(); got != tc.edit {
				t.Errorf("CanEdit() = %v, want %v", got, tc.edit)
			}
			if got := tc.role.CanManage(); got != tc.manage {
				t.Errorf("CanManage() = %v, want %v", got, tc.manage)
			}
		})
	}
}

func TestRoleOrdering(t *testing.T) {
	if !RoleOwner.AtLeast(RoleEditor) || !RoleOwner.AtLeast(RoleViewer) {
		t.Error("owner does not outrank editor and viewer")
	}
	if !RoleEditor.AtLeast(RoleViewer) {
		t.Error("editor does not outrank viewer")
	}
	if RoleViewer.AtLeast(RoleEditor) || RoleEditor.AtLeast(RoleOwner) {
		t.Error("a lesser role satisfied a greater minimum")
	}
	if RoleOwner.AtLeast(Role("nonsense")) {
		t.Error("an unknown minimum was satisfied; it must never be")
	}
}

// seedAccess builds one shared project with a member of each role, plus an outsider.
func seedAccess(t *testing.T, db *DB) {
	t.Helper()
	stmts := []string{
		`INSERT INTO users (id, email, name, password_hash, created_at, updated_at) VALUES
		 ('owner',   'owner@example.com',   'Owner',   'x', 0, 0),
		 ('editor',  'editor@example.com',  'Editor',  'x', 0, 0),
		 ('viewer',  'viewer@example.com',  'Viewer',  'x', 0, 0),
		 ('outsider','outsider@example.com','Outsider','x', 0, 0)`,

		`INSERT INTO projects (id, name, owner_id, created_at, updated_at) VALUES
		 ('shared', 'Delt projekt', 'owner', 0, 0),
		 ('private', 'Privat projekt', 'owner', 0, 0)`,

		`INSERT INTO project_members (project_id, user_id, role, added_at) VALUES
		 ('shared', 'editor', 'editor', 0),
		 ('shared', 'viewer', 'viewer', 0)`,

		`INSERT INTO tasks (id, project_id, content, created_by, created_at, updated_at) VALUES
		 ('task-shared',  'shared',  'delt opgave',   'owner', 0, 0),
		 ('task-private', 'private', 'privat opgave', 'owner', 0, 0)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestProjectRole(t *testing.T) {
	db := openTest(t)
	seedAccess(t, db)
	ctx := context.Background()

	cases := []struct {
		user, project string
		want          Role
		wantErr       bool
	}{
		// The owner is the owner without needing a project_members row.
		{"owner", "shared", RoleOwner, false},
		{"editor", "shared", RoleEditor, false},
		{"viewer", "shared", RoleViewer, false},
		{"owner", "private", RoleOwner, false},

		// An external user sees only what has been shared with them.
		{"outsider", "shared", "", true},
		{"outsider", "private", "", true},
		{"editor", "private", "", true},
		{"viewer", "private", "", true},

		// An id that does not exist is indistinguishable from one you cannot see.
		{"owner", "no-such-project", "", true},
		{"no-such-user", "shared", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.user+"/"+tc.project, func(t *testing.T) {
			got, err := ProjectRole(ctx, db, tc.project, tc.user)
			if tc.wantErr {
				if !errors.Is(err, ErrNoAccess) {
					t.Fatalf("err = %v, want ErrNoAccess", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("role = %q, want %q", got, tc.want)
			}
		})
	}
}

// The central promise of the sharing model: a viewer can read and cannot write.
func TestViewerCannotMutate(t *testing.T) {
	db := openTest(t)
	seedAccess(t, db)
	ctx := context.Background()

	if _, err := RequireProjectRole(ctx, db, "shared", "viewer", RoleViewer); err != nil {
		t.Errorf("viewer cannot view the project they were given: %v", err)
	}
	if _, err := RequireProjectRole(ctx, db, "shared", "viewer", RoleEditor); !errors.Is(err, ErrNoAccess) {
		t.Errorf("viewer passed an editor check: %v", err)
	}
	if _, err := RequireProjectRole(ctx, db, "shared", "viewer", RoleOwner); !errors.Is(err, ErrNoAccess) {
		t.Errorf("viewer passed an owner check: %v", err)
	}
}

func TestEditorCannotManageTheProject(t *testing.T) {
	db := openTest(t)
	seedAccess(t, db)
	ctx := context.Background()

	if _, err := RequireProjectRole(ctx, db, "shared", "editor", RoleEditor); err != nil {
		t.Errorf("editor cannot edit: %v", err)
	}
	// Renaming, inviting, deleting: owner only.
	if _, err := RequireProjectRole(ctx, db, "shared", "editor", RoleOwner); !errors.Is(err, ErrNoAccess) {
		t.Errorf("editor passed an owner check: %v", err)
	}
}

// An external user must see nothing but the projects actually shared with them.
func TestExternalUserSeesOnlySharedProjects(t *testing.T) {
	db := openTest(t)
	seedAccess(t, db)
	ctx := context.Background()

	for _, project := range []string{"shared", "private"} {
		if _, err := ProjectRole(ctx, db, project, "outsider"); !errors.Is(err, ErrNoAccess) {
			t.Errorf("outsider reached project %q: %v", project, err)
		}
	}

	// Once invited, and only then.
	_, err := db.Exec(`INSERT INTO project_members (project_id, user_id, role, added_at)
	                   VALUES ('shared', 'outsider', 'viewer', 0)`)
	if err != nil {
		t.Fatal(err)
	}
	if role, err := ProjectRole(ctx, db, "shared", "outsider"); err != nil || role != RoleViewer {
		t.Errorf("after being invited: role %q, err %v", role, err)
	}
	if _, err := ProjectRole(ctx, db, "private", "outsider"); !errors.Is(err, ErrNoAccess) {
		t.Error("being invited to one project granted access to another")
	}
}

// Access to a task is access to its project, and nothing else confers it.
func TestTaskAccessFollowsItsProject(t *testing.T) {
	db := openTest(t)
	seedAccess(t, db)
	ctx := context.Background()

	if role, err := TaskRole(ctx, db, "task-shared", "viewer"); err != nil || role != RoleViewer {
		t.Errorf("viewer on a task in a shared project: role %q, err %v", role, err)
	}
	if _, err := TaskRole(ctx, db, "task-private", "editor"); !errors.Is(err, ErrNoAccess) {
		t.Error("a task in an unshared project was reachable")
	}
	if _, err := TaskRole(ctx, db, "no-such-task", "owner"); !errors.Is(err, ErrNoAccess) {
		t.Error("a task that does not exist did not report as inaccessible")
	}
}

// Being assigned a task, or having created one, is not a form of membership.
// Otherwise removing somebody from a project would leave them holding the tasks.
func TestAssigneeAndAuthorGetNoStanding(t *testing.T) {
	db := openTest(t)
	seedAccess(t, db)
	ctx := context.Background()

	_, err := db.Exec(`INSERT INTO tasks (id, project_id, content, assignee_id, created_by, created_at, updated_at)
	                   VALUES ('assigned', 'private', 'til outsider', 'outsider', 'outsider', 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := TaskRole(ctx, db, "assigned", "outsider"); !errors.Is(err, ErrNoAccess) {
		t.Error("being the assignee and author granted access to the project")
	}
}

// A project in the trash is not readable until it is explicitly restored.
func TestTrashedProjectIsNotAccessible(t *testing.T) {
	db := openTest(t)
	seedAccess(t, db)
	ctx := context.Background()

	if _, err := db.Exec(`UPDATE projects SET deleted_at = 1 WHERE id = 'shared'`); err != nil {
		t.Fatal(err)
	}

	for _, user := range []string{"owner", "editor", "viewer"} {
		if _, err := ProjectRole(ctx, db, "shared", user); !errors.Is(err, ErrNoAccess) {
			t.Errorf("%s still reached a trashed project: %v", user, err)
		}
	}
	if _, err := TaskRole(ctx, db, "task-shared", "owner"); !errors.Is(err, ErrNoAccess) {
		t.Error("a task in a trashed project was still reachable")
	}
}

func TestTrashedTaskIsNotAccessible(t *testing.T) {
	db := openTest(t)
	seedAccess(t, db)
	ctx := context.Background()

	if _, err := db.Exec(`UPDATE tasks SET deleted_at = 1 WHERE id = 'task-shared'`); err != nil {
		t.Fatal(err)
	}
	if _, err := TaskRole(ctx, db, "task-shared", "owner"); !errors.Is(err, ErrNoAccess) {
		t.Error("a trashed task was still reachable")
	}
}

// A role string the database holds but this build does not understand must fail
// closed. Reading it as "some access" is how a future migration becomes a breach.
func TestUnknownStoredRoleDeniesAccess(t *testing.T) {
	db := openTest(t)
	seedAccess(t, db)
	ctx := context.Background()

	// The CHECK constraint refuses this on the way in, which is the first line of
	// defence; write it in behind the constraint's back to test the second.
	if _, err := db.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`INSERT INTO project_members (project_id, user_id, role, added_at)
	                   VALUES ('private', 'outsider', 'superuser', 0)`)
	if err != nil {
		t.Skipf("could not write an invalid role to test the fallback: %v", err)
	}

	if _, err := ProjectRole(ctx, db, "private", "outsider"); !errors.Is(err, ErrNoAccess) {
		t.Errorf("an unrecognised role granted access: %v", err)
	}
}

// Permission checks must be able to run inside the transaction that does the write.
// Checking on the connection and then writing in a transaction leaves a window in
// which a membership can be revoked between the two — so ProjectRole takes a
// Querier, which both *sql.DB and *sql.Tx satisfy.
func TestPermissionCheckWorksInsideATransaction(t *testing.T) {
	db := openTest(t)
	seedAccess(t, db)
	ctx := context.Background()

	err := db.Tx(ctx, func(tx *sql.Tx) error {
		role, err := RequireProjectRole(ctx, tx, "shared", "editor", RoleEditor)
		if err != nil {
			return err
		}
		if role != RoleEditor {
			t.Errorf("role in transaction = %q, want editor", role)
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO tasks (id, project_id, content, created_by, created_at, updated_at)
			 VALUES ('in-tx', 'shared', 'skrevet i transaktion', 'editor', 0, 0)`)
		return err
	})
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM tasks WHERE id = 'in-tx'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("the write inside the checked transaction did not land")
	}

	// And a denied check inside a transaction rolls the whole thing back.
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tasks (id, project_id, content, created_by, created_at, updated_at)
			 VALUES ('rolled-back', 'shared', 'skal rulles tilbage', 'viewer', 0, 0)`); err != nil {
			return err
		}
		_, err := RequireProjectRole(ctx, tx, "shared", "viewer", RoleEditor)
		return err
	})
	if !errors.Is(err, ErrNoAccess) {
		t.Fatalf("err = %v, want ErrNoAccess", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM tasks WHERE id = 'rolled-back'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("a write survived a transaction that failed its permission check")
	}
}
