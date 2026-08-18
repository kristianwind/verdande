package httpapi

import (
	"fmt"
	"net/http"
	"testing"
)

// The point of the instance-wide log is that it crosses projects the caller is not
// a member of. The per-project endpoint cannot answer this question, and an
// administrator asking "what has happened on this server" was previously told to
// go and look in each project they happen to belong to.
func TestTheAuditLogCrossesProjectsTheAdminIsNotInAllOfThem(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	// A project the administrator is not a member of.
	_, theirs := other.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Deres eget"})
	theirID := theirs["id"].(string)
	other.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "noget i deres eget", "project_id": theirID,
	})

	// The administrator cannot read that project's own history at all.
	if resp, _ := ts.do(t, "GET", "/api/v1/projects/"+theirID+"/activity", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a project the admin is not in: status %d, want 404", resp.StatusCode)
	}

	// The instance log has it.
	resp, body := ts.do(t, "GET", "/api/v1/activity", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit log: status %d", resp.StatusCode)
	}
	entries, _ := body["activity"].([]any)
	var found map[string]any
	for _, raw := range entries {
		e := raw.(map[string]any)
		if e["project_id"] == theirID && e["event"] == "task.created" {
			found = e
		}
	}
	if found == nil {
		t.Fatalf("nothing from a project the admin is not in: %v", entries)
	}
	if found["project_name"] != "Deres eget" {
		t.Errorf("project_name = %v — a row that does not name its project says nothing",
			found["project_name"])
	}
	if found["user_name"] != "Anden" {
		t.Errorf("user_name = %v", found["user_name"])
	}
}

// Administrators only, and sessions only. An audit log readable with a bearer token
// would let a stolen token read the record of the theft — and of everything else on
// the instance.
func TestTheAuditLogIsAdminsOnlyAndSessionsOnly(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	for _, path := range []string{"/api/v1/activity", "/api/v1/activity/events"} {
		if resp, _ := other.do(t, "GET", path, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s as an ordinary user: status %d, want 403", path, resp.StatusCode)
		}
	}

	admin, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	token := ts.apiToken(t, admin.ID)
	for _, path := range []string{"/api/v1/activity", "/api/v1/activity/events"} {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s with an admin's API token: status %d, want 403", path, resp.StatusCode)
		}
	}
}

// The cursor is keyed on (created_at, id) because the log is written in whole
// seconds. A page boundary that landed inside a busy second would, with a
// timestamp-only cursor, either repeat those rows or skip them — and an audit log
// that skips rows is worse than no audit log.
func TestTheAuditLogPagesWithoutRepeatingOrSkipping(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Meget"})
	projectID := project["id"].(string)
	// Written as fast as the loop goes, so they share timestamps.
	const written = 25
	for i := range written {
		ts.do(t, "POST", "/api/v1/tasks", map[string]any{
			"content": fmt.Sprintf("opgave %d", i), "project_id": projectID,
		})
	}

	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 20; page++ {
		path := "/api/v1/activity?limit=4"
		if cursor != "" {
			path += "&before=" + cursor
		}
		resp, body := ts.do(t, "GET", path, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("page %d: status %d", page, resp.StatusCode)
		}
		entries, _ := body["activity"].([]any)
		for _, raw := range entries {
			id := raw.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatalf("row %s came back on two pages", id)
			}
			seen[id] = true
		}
		next, _ := body["next_cursor"].(string)
		if next == "" {
			break
		}
		if next == cursor {
			t.Fatalf("the cursor did not move: %s", next)
		}
		cursor = next
	}

	// Everything that was written is in the walk exactly once: the task creations,
	// plus the project creation.
	if len(seen) != written+1 {
		t.Errorf("the walk saw %d rows, want %d — a page boundary lost some",
			len(seen), written+1)
	}
}

// The filters, and the list the event filter is drawn from.
func TestTheAuditLogFiltersAndListsItsEvents(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")
	otherUser, err := ts.db.UserByEmail(t.Context(), "anden@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	_, mine := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Mit"})
	mineID := mine["id"].(string)
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "min", "project_id": mineID})

	_, theirs := other.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Deres"})
	theirID := theirs["id"].(string)
	other.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "deres", "project_id": theirID})

	_, byUser := ts.do(t, "GET", "/api/v1/activity?user_id="+otherUser.ID, nil)
	for _, raw := range byUser["activity"].([]any) {
		if got := raw.(map[string]any)["user_id"]; got != otherUser.ID {
			t.Errorf("user_id filter returned a row by %v", got)
		}
	}
	if len(byUser["activity"].([]any)) == 0 {
		t.Error("the user filter returned nothing")
	}

	_, byProject := ts.do(t, "GET", "/api/v1/activity?project_id="+mineID, nil)
	for _, raw := range byProject["activity"].([]any) {
		if got := raw.(map[string]any)["project_id"]; got != mineID {
			t.Errorf("project_id filter returned a row from %v", got)
		}
	}

	_, byEvent := ts.do(t, "GET", "/api/v1/activity?event=project.created", nil)
	rows := byEvent["activity"].([]any)
	if len(rows) != 2 {
		t.Errorf("event filter found %d project.created rows, want 2", len(rows))
	}
	for _, raw := range rows {
		if got := raw.(map[string]any)["event"]; got != "project.created" {
			t.Errorf("event filter returned %v", got)
		}
	}

	// And the vocabulary the filter is built from.
	_, events := ts.do(t, "GET", "/api/v1/activity/events", nil)
	counts := map[string]float64{}
	for _, raw := range events["events"].([]any) {
		e := raw.(map[string]any)
		counts[e["event"].(string)] = e["count"].(float64)
	}
	if counts["project.created"] != 2 || counts["task.created"] != 2 {
		t.Errorf("event counts = %v", counts)
	}

	// A malformed cursor is a 400, not a silent first page: paging that quietly
	// restarts is how an audit walk reads the same rows forever.
	if resp, _ := ts.do(t, "GET", "/api/v1/activity?before=igår", nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a malformed cursor: status %d, want 400", resp.StatusCode)
	}
}

// A deleted account leaves its trail behind. This is the reason migration 0009
// exists: an audit log that lost its rows when somebody left would be missing
// exactly the part worth keeping.
func TestTheAuditLogOutlivesTheAccountItDescribes(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")
	otherUser, err := ts.db.UserByEmail(t.Context(), "anden@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	// In a project that outlives them, so the row is not carried off by the
	// project cascade instead.
	_, mine := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Fælles"})
	sharedID := mine["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+sharedID+"/invites", map[string]any{
		"email": "anden@example.dk", "role": "editor",
	})
	other.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "deres bidrag", "project_id": sharedID,
	})

	_, before := ts.do(t, "GET", "/api/v1/activity?user_id="+otherUser.ID+"&event=task.created", nil)
	if len(before["activity"].([]any)) != 1 {
		t.Fatalf("expected one row before the delete: %v", before["activity"])
	}

	if resp, _ := ts.do(t, "DELETE", "/api/v1/users/"+otherUser.ID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status %d", resp.StatusCode)
	}

	_, after := ts.do(t, "GET", "/api/v1/activity?project_id="+sharedID+"&event=task.created", nil)
	rows := after["activity"].([]any)
	if len(rows) != 1 {
		t.Fatalf("the row went with the account: %v", rows)
	}
	row := rows[0].(map[string]any)
	if _, ok := row["user_id"]; ok {
		t.Errorf("user_id is still there after the account went: %v", row["user_id"])
	}
	if _, ok := row["user_name"]; ok {
		t.Errorf("user_name is still there after the account went: %v", row["user_name"])
	}

	// And the project's own history still shows it, rather than dropping the row
	// on an inner join to a user that is gone.
	_, project := ts.do(t, "GET", "/api/v1/projects/"+sharedID+"/activity", nil)
	var kept int
	for _, raw := range project["activity"].([]any) {
		if raw.(map[string]any)["event"] == "task.created" {
			kept++
		}
	}
	if kept != 1 {
		t.Errorf("the project history dropped the authorless row: %d", kept)
	}
}
