package httpapi

import (
	"net/http"
	"testing"
)

// Restoring has been possible since the trash existed. Finding what to restore
// has not: the endpoint takes an id, and the id is exactly what you no longer
// have once the project has gone from the interface.
func TestTheTrashCanBeListedAndRestored(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, created := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Ferie"})
	id := created["id"].(string)
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "book fly", "project_id": id})

	if resp, _ := ts.do(t, "DELETE", "/api/v1/projects/"+id, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status %d", resp.StatusCode)
	}

	// Gone from the ordinary listing.
	_, live := ts.do(t, "GET", "/api/v1/projects", nil)
	for _, raw := range live["projects"].([]any) {
		if raw.(map[string]any)["id"] == id {
			t.Fatal("a deleted project is still in the project list")
		}
	}

	// And findable in the trash, with enough to decide whether to bring it back.
	_, trash := ts.do(t, "GET", "/api/v1/trash/projects", nil)
	list, _ := trash["projects"].([]any)
	if len(list) != 1 {
		t.Fatalf("want one project in the trash, got %v", list)
	}
	entry := list[0].(map[string]any)
	if entry["name"] != "Ferie" {
		t.Errorf("name = %v", entry["name"])
	}
	if entry["task_count"] != float64(1) {
		t.Errorf("task_count = %v, want 1 — the count is what says whether it matters",
			entry["task_count"])
	}
	if entry["purge_after"] == nil || entry["deleted_at"] == nil {
		t.Errorf("no deadline was reported: %v", entry)
	}

	if resp, _ := ts.do(t, "POST", "/api/v1/trash/projects/"+id+"/restore", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("restore: status %d", resp.StatusCode)
	}

	// Back, with its task.
	_, tasks := ts.do(t, "GET", "/api/v1/tasks?project_id="+id, nil)
	if got, _ := tasks["tasks"].([]any); len(got) != 1 {
		t.Errorf("the task did not come back with the project: %v", got)
	}
	_, trash = ts.do(t, "GET", "/api/v1/trash/projects", nil)
	if list, _ := trash["projects"].([]any); len(list) != 0 {
		t.Errorf("still in the trash after restoring: %v", list)
	}
}

// Somebody else's deleted project is not in your trash, and you cannot restore it.
func TestTheTrashIsPerOwner(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, created := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Privat"})
	id := created["id"].(string)
	ts.do(t, "DELETE", "/api/v1/projects/"+id, nil)

	other := ts.newUser(t, "anden@example.dk", "Anden")

	_, trash := other.do(t, "GET", "/api/v1/trash/projects", nil)
	if list, _ := trash["projects"].([]any); len(list) != 0 {
		t.Errorf("somebody else's deleted project is visible: %v", list)
	}
	resp, _ := other.do(t, "POST", "/api/v1/trash/projects/"+id+"/restore", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}
}

func TestReorderProjectsWritesTheGivenOrder(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	var ids []string
	for _, name := range []string{"Alfa", "Bravo", "Charlie"} {
		_, body := ts.do(t, "POST", "/api/v1/projects", map[string]string{"name": name})
		ids = append(ids, body["id"].(string))
	}

	// Back to front.
	reversed := []string{ids[2], ids[1], ids[0]}
	resp, _ := ts.do(t, "POST", "/api/v1/projects/reorder", map[string]any{"ids": reversed})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder: status %d", resp.StatusCode)
	}

	_, list := ts.do(t, "GET", "/api/v1/projects", nil)
	projects, _ := list["projects"].([]any)

	var got []string
	for _, raw := range projects {
		p := raw.(map[string]any)
		// The Inbox sorts ahead of everything by its own rule; it is not part of
		// what was reordered.
		if p["is_inbox"] == true {
			continue
		}
		got = append(got, p["id"].(string))
	}
	if len(got) != 3 || got[0] != reversed[0] || got[1] != reversed[1] || got[2] != reversed[2] {
		t.Errorf("order = %v, want %v", got, reversed)
	}
}

// sort_order is a column on the project, not a per-viewer preference, so
// reordering must not rearrange a project somebody else owns.
func TestReorderProjectsLeavesSomebodyElsesAlone(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	other := ts.newUser(t, "anden@example.dk", "Anden")
	_, theirs := other.do(t, "POST", "/api/v1/projects", map[string]string{"name": "Deres"})
	theirID := theirs["id"].(string)

	_, before := other.do(t, "GET", "/api/v1/projects", nil)
	var wanted float64
	for _, raw := range before["projects"].([]any) {
		if p := raw.(map[string]any); p["id"] == theirID {
			wanted = p["sort_order"].(float64)
		}
	}

	// Ask, as somebody else, to put their project first.
	resp, _ := ts.do(t, "POST", "/api/v1/projects/reorder", map[string]any{
		"ids": []string{theirID},
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder: status %d", resp.StatusCode)
	}

	_, after := other.do(t, "GET", "/api/v1/projects", nil)
	for _, raw := range after["projects"].([]any) {
		if p := raw.(map[string]any); p["id"] == theirID {
			if p["sort_order"] != wanted {
				t.Errorf("sort_order changed from %v to %v — somebody else moved it",
					wanted, p["sort_order"])
			}
		}
	}
}

// The member list errored on every project — the ORDER BY named an expression,
// which SQLite refuses across a UNION — so the share panel had never shown
// anybody, not even the owner.
func TestListMembersWorksAtAll(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]string{"name": "Firma"})

	resp, body := ts.do(t, "GET", "/api/v1/projects/"+project["id"].(string)+"/members", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %v", resp.StatusCode, body)
	}
	members, _ := body["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("a project with no invites lists %d members, want its owner", len(members))
	}
	if m := members[0].(map[string]any); m["role"] != "owner" {
		t.Errorf("role = %v, want owner", m["role"])
	}
}

// And the Inbox, which is the one every task drawer asks about.
func TestListMembersWorksForTheInbox(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, list := ts.do(t, "GET", "/api/v1/projects", nil)
	var inboxID string
	for _, raw := range list["projects"].([]any) {
		if p := raw.(map[string]any); p["is_inbox"] == true {
			inboxID = p["id"].(string)
		}
	}

	resp, body := ts.do(t, "GET", "/api/v1/projects/"+inboxID+"/members", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, body %v", resp.StatusCode, body)
	}
}
