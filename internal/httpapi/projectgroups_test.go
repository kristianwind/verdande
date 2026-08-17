package httpapi

import (
	"net/http"
	"testing"
)

func TestAGroupHoldsProjectsAndSurvivesBeingEmptied(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, group := ts.do(t, "POST", "/api/v1/project-groups", map[string]any{"name": "Arbejde"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create group: %d %v", resp.StatusCode, group)
	}
	groupID := group["id"].(string)
	if group["collapsed"] != false {
		t.Errorf("a new group should be open: %v", group)
	}

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Regnskab"})
	projectID := project["id"].(string)

	resp, filed := ts.do(t, "PATCH", "/api/v1/projects/"+projectID,
		map[string]any{"group_id": groupID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("file project: %d %v", resp.StatusCode, filed)
	}
	if filed["group_id"] != groupID {
		t.Errorf("group_id = %v, want %s", filed["group_id"], groupID)
	}

	// The listing is what the sidebar reads, so the filing has to survive it.
	if got := groupOf(t, ts, projectID); got != groupID {
		t.Errorf("group_id in the list = %q, want %s", got, groupID)
	}

	// An empty group is still a group. This is the whole reason it is a table and
	// not a string repeated on every project row.
	resp, _ = ts.do(t, "PATCH", "/api/v1/projects/"+projectID, map[string]any{"group_id": ""})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ungroup: status %d", resp.StatusCode)
	}
	if got := groupOf(t, ts, projectID); got != "" {
		t.Errorf("group_id = %q after ungrouping, want empty", got)
	}

	_, listed := ts.do(t, "GET", "/api/v1/project-groups", nil)
	if groups, _ := listed["groups"].([]any); len(groups) != 1 {
		t.Fatalf("the emptied group is gone: %v", listed)
	}
}

// An omitted group_id must not clear the field: that is the difference between a
// PATCH and a PUT, and renaming a project would otherwise tip it out of its group.
func TestRenamingAProjectLeavesItInItsGroup(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, group := ts.do(t, "POST", "/api/v1/project-groups", map[string]any{"name": "Arbejde"})
	groupID := group["id"].(string)
	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Regnskab"})
	projectID := project["id"].(string)
	ts.do(t, "PATCH", "/api/v1/projects/"+projectID, map[string]any{"group_id": groupID})

	_, renamed := ts.do(t, "PATCH", "/api/v1/projects/"+projectID, map[string]any{"name": "Bogholderi"})
	if renamed["group_id"] != groupID {
		t.Errorf("group_id = %v after a rename, want %s", renamed["group_id"], groupID)
	}
}

// Deleting the heading is not a request to delete the work filed under it.
func TestDeletingAGroupKeepsItsProjects(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, group := ts.do(t, "POST", "/api/v1/project-groups", map[string]any{"name": "Arbejde"})
	groupID := group["id"].(string)
	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Regnskab"})
	projectID := project["id"].(string)
	ts.do(t, "PATCH", "/api/v1/projects/"+projectID, map[string]any{"group_id": groupID})

	resp, _ := ts.do(t, "DELETE", "/api/v1/project-groups/"+groupID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete group: status %d", resp.StatusCode)
	}

	_, projects := ts.do(t, "GET", "/api/v1/projects", nil)
	found := false
	for _, raw := range projects["projects"].([]any) {
		p := raw.(map[string]any)
		if p["id"] != projectID {
			continue
		}
		found = true
		if p["group_id"] != nil && p["group_id"] != "" {
			t.Errorf("the project still points at a group that is gone: %v", p["group_id"])
		}
	}
	if !found {
		t.Error("the project went with the group it was filed under")
	}
}

// Folding is stored on the account rather than in the browser, so it has to come
// back from the server.
func TestFoldingAGroupIsRemembered(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, group := ts.do(t, "POST", "/api/v1/project-groups", map[string]any{"name": "Arbejde"})
	groupID := group["id"].(string)

	resp, folded := ts.do(t, "PATCH", "/api/v1/project-groups/"+groupID,
		map[string]any{"collapsed": true})
	if resp.StatusCode != http.StatusOK || folded["collapsed"] != true {
		t.Fatalf("fold: %d %v", resp.StatusCode, folded)
	}

	_, listed := ts.do(t, "GET", "/api/v1/project-groups", nil)
	first := listed["groups"].([]any)[0].(map[string]any)
	if first["collapsed"] != true {
		t.Errorf("collapsed = %v after re-reading, want true", first["collapsed"])
	}
}

// Groups are per person: somebody else's is not yours to see, rename or delete,
// and a group that is not yours is not found rather than forbidden.
func TestGroupsArePerPerson(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, group := ts.do(t, "POST", "/api/v1/project-groups", map[string]any{"name": "Arbejde"})
	groupID := group["id"].(string)

	other := ts.newUser(t, "anden@example.dk", "Anden")

	_, listed := other.do(t, "GET", "/api/v1/project-groups", nil)
	if groups, _ := listed["groups"].([]any); len(groups) != 0 {
		t.Errorf("somebody else's groups are visible: %v", groups)
	}
	if resp, _ := other.do(t, "PATCH", "/api/v1/project-groups/"+groupID,
		map[string]any{"name": "Mit"}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("renaming somebody else's group: status %d, want 404", resp.StatusCode)
	}
	if resp, _ := other.do(t, "DELETE", "/api/v1/project-groups/"+groupID, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("deleting somebody else's group: status %d, want 404", resp.StatusCode)
	}
}

// A project cannot be filed into a group that is not the caller's, even though
// both the project and the group exist and both requests would be well-formed.
func TestAProjectCannotJoinSomebodyElsesGroup(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	other := ts.newUser(t, "anden@example.dk", "Anden")
	_, group := other.do(t, "POST", "/api/v1/project-groups", map[string]any{"name": "Deres"})
	groupID := group["id"].(string)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Mit"})
	projectID := project["id"].(string)

	resp, _ := ts.do(t, "PATCH", "/api/v1/projects/"+projectID, map[string]any{"group_id": groupID})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404 — a group you cannot see is not one you can file into",
			resp.StatusCode)
	}
}

func TestGroupsReorderAsAWholeList(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	ids := make([]string, 3)
	for i, name := range []string{"En", "To", "Tre"} {
		_, g := ts.do(t, "POST", "/api/v1/project-groups", map[string]any{"name": name})
		ids[i] = g["id"].(string)
	}

	reversed := []string{ids[2], ids[1], ids[0]}
	resp, _ := ts.do(t, "POST", "/api/v1/project-groups/reorder", map[string]any{"ids": reversed})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reorder: status %d", resp.StatusCode)
	}

	_, listed := ts.do(t, "GET", "/api/v1/project-groups", nil)
	got := listed["groups"].([]any)
	for i, want := range reversed {
		if got[i].(map[string]any)["id"] != want {
			t.Fatalf("order = %v, want %v", got, reversed)
		}
	}
}

// A name is required, the same as a project's. An unnamed heading is a heading
// nobody can tell apart from the next one.
func TestAGroupNeedsAName(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/project-groups", map[string]any{"name": "   "})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422 — body %v", resp.StatusCode, body)
	}
}

// groupOf reads a project's group back out of the listing the sidebar uses.
func groupOf(t *testing.T, ts *testServer, projectID string) string {
	t.Helper()
	_, body := ts.do(t, "GET", "/api/v1/projects", nil)
	for _, raw := range body["projects"].([]any) {
		p := raw.(map[string]any)
		if p["id"] == projectID {
			id, _ := p["group_id"].(string)
			return id
		}
	}
	t.Fatalf("project %s is not in the listing", projectID)
	return ""
}

// Colour is a name from a closed set, on both a project and a group.
//
// Refused rather than stored: an unknown name would be written happily and then
// painted as the default, which looks like a colour that did not save.
func TestAColourMustBeOneFromThePalette(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, group := ts.do(t, "POST", "/api/v1/project-groups", map[string]any{"name": "Arbejde"})
	groupID := group["id"].(string)
	if group["color"] != "graphite" {
		t.Errorf("a new group's colour = %v, want graphite", group["color"])
	}

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Regnskab"})
	projectID := project["id"].(string)

	for _, c := range []struct {
		what, method, path string
	}{
		{"group", "PATCH", "/api/v1/project-groups/" + groupID},
		{"project", "PATCH", "/api/v1/projects/" + projectID},
	} {
		resp, body := ts.do(t, c.method, c.path, map[string]any{"color": "teal"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s, a real colour: %d %v", c.what, resp.StatusCode, body)
		}
		if body["color"] != "teal" {
			t.Errorf("%s colour = %v, want teal", c.what, body["color"])
		}

		resp, body = ts.do(t, c.method, c.path, map[string]any{"color": "#ff0000"})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s, a hex value: status %d, want 422 — body %v", c.what, resp.StatusCode, body)
		}
	}

	// A rename does not disturb the colour, and a recolour does not disturb the
	// name: both are PATCH fields on the same row, and the whole point of a PATCH
	// is that the one you did not send is the one you did not mean.
	_, renamed := ts.do(t, "PATCH", "/api/v1/project-groups/"+groupID,
		map[string]any{"name": "Kontoret"})
	if renamed["color"] != "teal" {
		t.Errorf("colour = %v after a rename, want teal", renamed["color"])
	}
	if renamed["name"] != "Kontoret" {
		t.Errorf("name = %v", renamed["name"])
	}
}
