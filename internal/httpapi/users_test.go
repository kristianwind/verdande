package httpapi

import (
	"net/http"
	"net/url"
	"testing"
)

// The instance's membership is an administrator's list, and creating an account
// means issuing an invite — there is no other door, and no password chosen by
// somebody other than its owner.
func TestAnAdminCanInviteSomebodyToTheInstance(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/users", map[string]any{"email": "ny@example.dk"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite: %d %v", resp.StatusCode, body)
	}
	link, _ := body["link"].(string)
	if link == "" {
		t.Fatal("no link came back — with no mail server it is the only way the invite reaches anybody")
	}
	if body["emailed"] != false {
		t.Errorf("emailed = %v, want false with no mail server configured", body["emailed"])
	}

	// It shows as pending, with no project attached: this is an invite to the
	// instance, not to somewhere in it.
	_, listed := ts.do(t, "GET", "/api/v1/users", nil)
	invites, _ := listed["invites"].([]any)
	if len(invites) != 1 {
		t.Fatalf("want one pending invite, got %v", invites)
	}
	invite := invites[0].(map[string]any)
	if invite["email"] != "ny@example.dk" {
		t.Errorf("email = %v", invite["email"])
	}
	if name, ok := invite["project_name"]; ok && name != "" {
		t.Errorf("project_name = %v, want none for an instance invite", name)
	}

	// And the token in the link creates the account, through the same signup the
	// project invites use.
	token := link[len(link)-len(link)+indexAfter(link, "token="):]
	fresh := newSignedOut(t, ts)
	resp, _ = fresh.do(t, "POST", "/api/v1/auth/signup", map[string]any{
		"token": token, "name": "Ny Bruger", "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("signup with the instance invite: status %d", resp.StatusCode)
	}

	// They are on the instance, and they are not an administrator.
	_, after := ts.do(t, "GET", "/api/v1/users", nil)
	users, _ := after["users"].([]any)
	if len(users) != 2 {
		t.Fatalf("want two accounts, got %v", users)
	}
	for _, raw := range users {
		u := raw.(map[string]any)
		if u["email"] == "ny@example.dk" && u["is_admin"] != false {
			t.Errorf("a newly invited account is an administrator: %v", u)
		}
	}
	// The invite is used up rather than left live.
	if pending, _ := after["invites"].([]any); len(pending) != 0 {
		t.Errorf("the invite is still pending after being accepted: %v", pending)
	}
}

// The two refusals that make this page safe to have at all.
func TestTheLastAdministratorCannotBeRemoved(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	me, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	// Demoting yourself as the only administrator locks everybody out: there is no
	// console, and setup refuses to run once an account exists.
	resp, body := ts.do(t, "PATCH", "/api/v1/users/"+me.ID, map[string]any{"is_admin": false})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("demote the last admin: status %d, want 409", resp.StatusCode)
	}
	if body["code"] != "last_admin" {
		t.Errorf("code = %v, want last_admin — the interface has to explain this one", body["code"])
	}

	// And deleting the account you are signed in as is refused before that check
	// is even reached.
	resp, _ = ts.do(t, "DELETE", "/api/v1/users/"+me.ID, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("delete your own account: status %d, want 409", resp.StatusCode)
	}

	// Still an administrator, and still there.
	_, listed := ts.do(t, "GET", "/api/v1/users", nil)
	first := listed["users"].([]any)[0].(map[string]any)
	if first["is_admin"] != true || first["self"] != true {
		t.Errorf("the only administrator is no longer one: %v", first)
	}
}

// With a second administrator the refusal lifts — it is about the last one, not
// about administrators.
func TestASecondAdministratorCanBeDemoted(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.newUser(t, "anden@example.dk", "Anden")

	other, err := ts.db.UserByEmail(t.Context(), "anden@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	if resp, _ := ts.do(t, "PATCH", "/api/v1/users/"+other.ID,
		map[string]any{"is_admin": true}); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("promote: status %d", resp.StatusCode)
	}
	if resp, _ := ts.do(t, "PATCH", "/api/v1/users/"+other.ID,
		map[string]any{"is_admin": false}); resp.StatusCode != http.StatusNoContent {
		t.Errorf("demote a second administrator: status %d, want 204", resp.StatusCode)
	}
}

// Deleting an account takes its own projects and leaves everything else behind.
// The list says both numbers before anybody presses the button.
func TestTheDeleteCountsSayWhatWouldGo(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	otherUser, err := ts.db.UserByEmail(t.Context(), "anden@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	// One project of their own, with a task in it.
	_, theirs := other.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Deres"})
	other.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "deres egen", "project_id": theirs["id"].(string),
	})

	// And a task they wrote in a project somebody else owns. This one survives —
	// `created_by` is ON DELETE SET NULL — and it is counted separately, because
	// "this much disappears" and "this much stays unattributed" are two different
	// things to tell somebody who is about to press delete.
	_, mine := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Fælles"})
	sharedID := mine["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+sharedID+"/invites", map[string]any{
		"email": "anden@example.dk", "role": "editor",
	})
	other.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "skrevet i et delt projekt", "project_id": sharedID,
	})

	_, listed := ts.do(t, "GET", "/api/v1/users", nil)
	var found map[string]any
	for _, raw := range listed["users"].([]any) {
		if u := raw.(map[string]any); u["id"] == otherUser.ID {
			found = u
		}
	}
	if found == nil {
		t.Fatal("the second account is not in the list")
	}
	if found["project_count"] != float64(1) {
		t.Errorf("project_count = %v, want 1 — the Inbox must not be counted", found["project_count"])
	}
	if found["task_count"] != float64(1) {
		t.Errorf("task_count = %v, want 1 — only the task in their own project goes",
			found["task_count"])
	}
	if found["authored_elsewhere"] != float64(1) {
		t.Errorf("authored_elsewhere = %v, want 1 — the task in the shared project stays",
			found["authored_elsewhere"])
	}

	// And the delete leaves it standing, without an author.
	if resp, _ := ts.do(t, "DELETE", "/api/v1/users/"+otherUser.ID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status %d", resp.StatusCode)
	}
	_, tasks := ts.do(t, "GET", "/api/v1/tasks?project_id="+sharedID, nil)
	got, _ := tasks["tasks"].([]any)
	if len(got) != 1 {
		t.Fatalf("a task written by the deleted account should survive in a shared project, got %v", got)
	}
	surviving := got[0].(map[string]any)
	if surviving["content"] != "skrevet i et delt projekt" {
		t.Errorf("the surviving task is not the one that was written: %v", surviving["content"])
	}
	if surviving["created_by"] != "" {
		t.Errorf("created_by = %q, want empty — the author is gone, the work is not",
			surviving["created_by"])
	}
}

// The tasks table was rebuilt to change created_by's delete action, and a rebuild
// is where an index quietly stops matching its table: tasks_fts is external-content
// and keyed on tasks.rowid, which a rebuilt table hands out afresh. Search is the
// only place that would notice.
func TestSearchStillFindsTasksAfterTheTableRebuild(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Have"})
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "beskær det grønne hegn", "project_id": project["id"].(string),
	})

	for _, q := range []string{"grønne", "gronne", "hegn"} {
		_, found := ts.do(t, "GET", "/api/v1/search?q="+url.QueryEscape(q), nil)
		if got, _ := found["tasks"].([]any); len(got) != 1 {
			t.Errorf("search for %q found %d tasks, want 1", q, len(got))
		}
	}
}

// Everything here is administrators only, and not reachable with an API token
// even by an administrator: a leaked token must not be able to mint an account.
func TestTheUserPageIsAdminsOnlyAndSessionsOnly(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	for _, c := range []struct {
		what, method, path string
		body               any
	}{
		{"list", "GET", "/api/v1/users", nil},
		{"invite", "POST", "/api/v1/users", map[string]any{"email": "x@y.dk"}},
		{"promote", "PATCH", "/api/v1/users/whoever", map[string]any{"is_admin": true}},
		{"delete", "DELETE", "/api/v1/users/whoever", nil},
		{"revoke", "DELETE", "/api/v1/invites/whichever", nil},
	} {
		if resp, _ := other.do(t, c.method, c.path, c.body); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s as an ordinary user: status %d, want 403", c.what, resp.StatusCode)
		}
	}

	admin, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	token := ts.apiToken(t, admin.ID)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/users", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("an administrator's API token reached the user list: status %d", resp.StatusCode)
	}
}

// A pending invite can be withdrawn, which is the only way to stop a link that
// went to the wrong address: the token is stored as a hash and cannot be looked up.
func TestAPendingInviteCanBeWithdrawn(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, created := ts.do(t, "POST", "/api/v1/users", map[string]any{"email": "forkert@example.dk"})
	link := created["link"].(string)

	_, listed := ts.do(t, "GET", "/api/v1/users", nil)
	inviteID := listed["invites"].([]any)[0].(map[string]any)["id"].(string)

	if resp, _ := ts.do(t, "DELETE", "/api/v1/invites/"+inviteID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("withdraw: status %d", resp.StatusCode)
	}

	// The link no longer works.
	token := link[indexAfter(link, "token="):]
	fresh := newSignedOut(t, ts)
	resp, _ := fresh.do(t, "POST", "/api/v1/auth/signup", map[string]any{
		"token": token, "name": "Forkert", "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a withdrawn invite still signs somebody up: status %d", resp.StatusCode)
	}
}

// An address that already has an account is a conflict rather than a second
// invite: the signup would fail at the end, after somebody had chosen a password.
func TestInvitingAnAddressThatAlreadyHasAnAccount(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/users",
		map[string]any{"email": "kristian@example.dk"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409 — body %v", resp.StatusCode, body)
	}
}

// newSignedOut is a client against the same server with an empty cookie jar.
func newSignedOut(t *testing.T, ts *testServer) *testServer {
	t.Helper()
	return &testServer{Server: ts.Server, db: ts.db, client: newJarClient(t)}
}

// indexAfter returns the offset just past `needle` in `s`.
func indexAfter(s, needle string) int {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return i + len(needle)
		}
	}
	return 0
}

// A 500 has to leave something behind that a restart cannot take.
//
// The panel's watcher already reports that one happened. What it cannot report is
// what it was: that lives in the container's log, and a Rune replaces the
// container on every restart, so the explanation is usually gone before anybody
// looks.
func TestAServerErrorIsKeptWhereARestartCannotTakeIt(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// Nothing has gone wrong yet.
	_, empty := ts.do(t, "GET", "/api/v1/errors", nil)
	if rows, _ := empty["errors"].([]any); len(rows) != 0 {
		t.Fatalf("a fresh instance has errors already: %v", rows)
	}

	// Break something the handler cannot survive: the table it reads is gone, so
	// the query fails and the handler answers 500. Dropping it is a blunt way to
	// provoke that, and a faithful one — it is the same code path a disk error or
	// a corrupt page would take.
	if _, err := ts.db.ExecContext(t.Context(), `DROP TABLE project_groups`); err != nil {
		t.Fatal(err)
	}
	resp, _ := ts.do(t, "GET", "/api/v1/project-groups", nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 — the fault was not provoked", resp.StatusCode)
	}

	_, body := ts.do(t, "GET", "/api/v1/errors", nil)
	rows, _ := body["errors"].([]any)
	if len(rows) != 1 {
		t.Fatalf("want one recorded error, got %v", rows)
	}
	row := rows[0].(map[string]any)
	if row["what"] != "list project groups" {
		t.Errorf("what = %v — the operation is the part a status code cannot give", row["what"])
	}
	if row["path"] != "/api/v1/project-groups" || row["method"] != "GET" {
		t.Errorf("path/method = %v %v", row["method"], row["path"])
	}
	if row["status"] != float64(500) {
		t.Errorf("status = %v", row["status"])
	}
	if row["message"] == "" {
		t.Error("no message: the row says something broke and not what")
	}
	if row["user_name"] != "Kristian" {
		t.Errorf("user_name = %v — a fault only one account hits is a different problem",
			row["user_name"])
	}
	if row["request_id"] == "" {
		t.Error("no request id, so the row cannot be tied back to the log line")
	}
}

// The list is an administrator's: it carries paths, accounts and the server's own
// error strings.
func TestTheErrorLogIsAdminsOnly(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	if resp, _ := other.do(t, "GET", "/api/v1/errors", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("status %d, want 403", resp.StatusCode)
	}
}
