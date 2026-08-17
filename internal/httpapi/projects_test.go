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
