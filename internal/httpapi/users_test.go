package httpapi

import (
	"net/http"
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

// Deleting an account takes more than its own projects, and the list says so
// before anybody presses the button.
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

	// And a task they wrote in a project somebody else owns. tasks.created_by
	// cascades, so this one goes too — and a count that missed it would understate
	// the damage in exactly the case that matters.
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
	if found["task_count"] != float64(2) {
		t.Errorf("task_count = %v, want 2 — the task in the shared project cascades too",
			found["task_count"])
	}

	// And the delete really does take it.
	if resp, _ := ts.do(t, "DELETE", "/api/v1/users/"+otherUser.ID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status %d", resp.StatusCode)
	}
	_, tasks := ts.do(t, "GET", "/api/v1/tasks?project_id="+sharedID, nil)
	if got, _ := tasks["tasks"].([]any); len(got) != 0 {
		t.Errorf("a task written by the deleted account survived in a shared project: %v", got)
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
