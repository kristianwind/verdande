package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kristianwind/verdande/internal/store"
)

// userDate is a calendar day relative to today **in the test user's timezone**,
// which is what the API resolves dates in.
//
// Not the process's local date. CI runs in UTC, and for a Copenhagen user the two
// disagree for two hours out of every twenty-four — so a test written against
// time.Now() locally passes all day and fails on a late-evening push, which is the
// worst kind of flake to chase.
func userDate(t *testing.T, offsetDays int) string {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatalf("load timezone: %v", err)
	}
	return time.Now().In(loc).AddDate(0, 0, offsetDays).Format("2006-01-02")
}

// newUser creates an account directly in the store and returns a client signed in
// as them. Going through the invite flow for every fixture would test the invite
// flow over and over rather than the thing each test is about.
func (ts *testServer) newUser(t *testing.T, email, name string) *testServer {
	t.Helper()

	hash := mustHash(t, "et langt kodeord")
	u := &store.User{Email: email, Name: name, PasswordHash: hash}
	if err := ts.db.CreateUser(t.Context(), u, "Indbakke"); err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}

	client := &testServer{Server: ts.Server, db: ts.db, client: newJarClient(t)}
	resp, _ := client.do(t, "POST", "/api/v1/auth/login", map[string]string{
		"email": email, "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("log in as %s: status %d", email, resp.StatusCode)
	}
	return client
}

func TestCreateTaskGoesToTheInboxByDefault(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "køb kaffe"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task: %d %v", resp.StatusCode, body)
	}
	if body["content"] != "køb kaffe" {
		t.Errorf("content = %v", body["content"])
	}
	if body["priority"] != float64(4) {
		t.Errorf("priority = %v, want 4 by default", body["priority"])
	}

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := ts.db.InboxID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if body["project_id"] != inbox {
		t.Errorf("project_id = %v, want the Inbox %s", body["project_id"], inbox)
	}
}

// Quick add is the headline feature: one line in, a fully-formed task out.
func TestQuickAddParsesAndFiles(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// A project for the "#Firma" in the text to land in.
	resp, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Firma"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: %d %v", resp.StatusCode, project)
	}

	resp, body := ts.do(t, "POST", "/api/v1/tasks/quick-add", map[string]any{
		"text": "betal moms i morgen kl 10 p1 #Firma @regnskab",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("quick add: %d %v", resp.StatusCode, body)
	}

	if body["content"] != "betal moms" {
		t.Errorf("content = %v, want %q", body["content"], "betal moms")
	}
	if body["priority"] != float64(1) {
		t.Errorf("priority = %v, want 1", body["priority"])
	}
	if body["project_id"] != project["id"] {
		t.Errorf("task did not land in #Firma: %v", body["project_id"])
	}
	tomorrow := userDate(t, 1)
	if body["due_date"] != tomorrow {
		t.Errorf("due_date = %v, want %s", body["due_date"], tomorrow)
	}
	labels, _ := body["labels"].([]any)
	if len(labels) != 1 || labels[0] != "regnskab" {
		t.Errorf("labels = %v, want [regnskab]", labels)
	}
}

// An unrecognised "#name" must not throw the thought away.
func TestQuickAddWithUnknownProjectStillCreatesTheTask(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/tasks/quick-add", map[string]any{
		"text": "ring til tandlægen #FindesIkke",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("quick add: %d %v", resp.StatusCode, body)
	}
	if body["content"] != "ring til tandlægen" {
		t.Errorf("content = %v", body["content"])
	}
}

func TestQuickAddPreviewDoesNotSave(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "GET", "/api/v1/tasks/quick-add/preview?text=betal+moms+i+morgen+p1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview: %d %v", resp.StatusCode, body)
	}
	if body["content"] != "betal moms" {
		t.Errorf("content = %v", body["content"])
	}
	// Spans are what the input box highlights with.
	if spans, _ := body["spans"].([]any); len(spans) == 0 {
		t.Error("no spans returned; the input box has nothing to highlight")
	}

	_, list := ts.do(t, "GET", "/api/v1/tasks", nil)
	if tasks, _ := list["tasks"].([]any); len(tasks) != 0 {
		t.Errorf("preview created %d tasks", len(tasks))
	}
}

func TestCompleteAndReopen(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "en opgave"})
	id := task["id"].(string)

	resp, body := ts.do(t, "POST", "/api/v1/tasks/"+id+"/complete", nil)
	if resp.StatusCode != http.StatusOK || body["completed"] != true {
		t.Fatalf("complete: %d %v", resp.StatusCode, body)
	}

	// A completed task drops out of the default list.
	_, list := ts.do(t, "GET", "/api/v1/tasks", nil)
	if tasks, _ := list["tasks"].([]any); len(tasks) != 0 {
		t.Errorf("a completed task is still in the open list")
	}

	resp, body = ts.do(t, "POST", "/api/v1/tasks/"+id+"/reopen", nil)
	if resp.StatusCode != http.StatusOK || body["completed"] != false {
		t.Fatalf("reopen: %d %v", resp.StatusCode, body)
	}
}

// Completing a parent closes what is under it — a parent standing complete over
// unfinished children is a state the interface would then have to explain.
func TestCompletingAParentCompletesItsSubtasks(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, parent := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "flyt"})
	parentID := parent["id"].(string)

	_, child := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "pak køkkenet", "parent_id": parentID,
	})
	childID := child["id"].(string)

	_, grandchild := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "pak tallerkener", "parent_id": childID,
	})
	grandchildID := grandchild["id"].(string)

	ts.do(t, "POST", "/api/v1/tasks/"+parentID+"/complete", nil)

	for _, id := range []string{childID, grandchildID} {
		_, body := ts.do(t, "GET", "/api/v1/tasks/"+id, nil)
		if body["completed"] != true {
			t.Errorf("task %s was left open under a completed parent", id)
		}
	}
}

func TestDeleteIsRecoverable(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "slettes"})
	id := task["id"].(string)

	resp, _ := ts.do(t, "DELETE", "/api/v1/tasks/"+id, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	resp, _ = ts.do(t, "GET", "/api/v1/tasks/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a deleted task is still readable: %d", resp.StatusCode)
	}

	// Still in the database, restorable for thirty days.
	if err := ts.db.RestoreTask(t.Context(), id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	resp, _ = ts.do(t, "GET", "/api/v1/tasks/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("a restored task is not readable: %d", resp.StatusCode)
	}
}

func TestTodayAndUpcoming(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	today := userDate(t, 0)
	yesterday := userDate(t, -1)
	inThreeDays := userDate(t, 3)

	for _, tc := range []struct{ content, due string }{
		{"forfaldt i går", yesterday},
		{"i dag", today},
		{"om tre dage", inThreeDays},
		{"ingen dato", ""},
	} {
		body := map[string]any{"content": tc.content}
		if tc.due != "" {
			body["due_date"] = tc.due
		}
		resp, out := ts.do(t, "POST", "/api/v1/tasks", body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %q: %d %v", tc.content, resp.StatusCode, out)
		}
	}

	_, todayView := ts.do(t, "GET", "/api/v1/today", nil)
	overdue, _ := todayView["overdue"].([]any)
	due, _ := todayView["today"].([]any)
	if len(overdue) != 1 {
		t.Errorf("overdue has %d tasks, want 1", len(overdue))
	}
	if len(due) != 1 {
		t.Errorf("today has %d tasks, want 1", len(due))
	}

	_, upcoming := ts.do(t, "GET", "/api/v1/upcoming", nil)
	days, _ := upcoming["days"].([]any)
	if len(days) != 7 {
		t.Fatalf("upcoming returned %d days, want 7", len(days))
	}
	// Empty days are present on purpose: the view is a calendar strip.
	var found int
	for _, d := range days {
		day := d.(map[string]any)
		tasks, _ := day["tasks"].([]any)
		found += len(tasks)
	}
	if found != 2 {
		t.Errorf("upcoming contains %d tasks over 7 days, want 2", found)
	}
}

func TestSearchFindsAcrossProjects(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "køb grøn maling"})
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "ring til Anders"})

	// Typed without the Danish letter, as somebody on the wrong keyboard would.
	resp, body := ts.do(t, "GET", "/api/v1/search?q=gron", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: %d %v", resp.StatusCode, body)
	}
	tasks, _ := body["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("search for 'gron' returned %d tasks, want 1", len(tasks))
	}
	if tasks[0].(map[string]any)["content"] != "køb grøn maling" {
		t.Errorf("wrong task: %v", tasks[0])
	}
}

func TestDragAndDropOrdering(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	ids := make([]string, 3)
	for i, name := range []string{"første", "anden", "tredje"} {
		_, body := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": name})
		ids[i] = body["id"].(string)
	}

	// Drag the third task to the top: before the first, after nothing.
	resp, body := ts.do(t, "POST", "/api/v1/tasks/"+ids[2]+"/move", map[string]any{
		"before_id": ids[0],
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move: %d %v", resp.StatusCode, body)
	}

	_, list := ts.do(t, "GET", "/api/v1/tasks", nil)
	tasks, _ := list["tasks"].([]any)
	if len(tasks) != 3 {
		t.Fatalf("got %d tasks", len(tasks))
	}
	if got := tasks[0].(map[string]any)["content"]; got != "tredje" {
		t.Errorf("first task is %v, want tredje", got)
	}
}

// Repeatedly dropping into the same gap halves it each time. Eventually the
// midpoint of two neighbours equals one of them, and the list has to be renumbered
// rather than silently stop reordering.
func TestOrderingSurvivesManyDropsIntoTheSameGap(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, a := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "a"})
	_, b := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "b"})
	topID, bottomID := a["id"].(string), b["id"].(string)

	var lastID string
	for i := 0; i < 80; i++ {
		_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "mellem"})
		id := task["id"].(string)
		resp, body := ts.do(t, "POST", "/api/v1/tasks/"+id+"/move", map[string]any{
			"after_id": topID, "before_id": bottomID,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("move %d: %d %v", i, resp.StatusCode, body)
		}
		lastID = id
	}

	// The most recent insert must still sit between the two anchors.
	_, list := ts.do(t, "GET", "/api/v1/tasks", nil)
	tasks, _ := list["tasks"].([]any)
	var posLast, posTop, posBottom = -1, -1, -1
	for i, raw := range tasks {
		switch raw.(map[string]any)["id"] {
		case lastID:
			posLast = i
		case topID:
			posTop = i
		case bottomID:
			posBottom = i
		}
	}
	if posTop < 0 || posBottom < 0 || posLast < 0 {
		t.Fatal("a task went missing")
	}
	if !(posTop < posLast && posLast < posBottom) {
		t.Errorf("ordering broke down: top=%d last=%d bottom=%d", posTop, posLast, posBottom)
	}
}

// --- permissions, through the API -------------------------------------------------

// The promise of sharing: a viewer reads and cannot write.
func TestViewerCannotMutateThroughTheAPI(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	viewer := ts.newUser(t, "viewer@example.dk", "Viewer")

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	projectID := project["id"].(string)
	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "en opgave", "project_id": projectID,
	})
	taskID := task["id"].(string)

	resp, body := ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites", map[string]any{
		"email": "viewer@example.dk", "role": "viewer",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invite: %d %v", resp.StatusCode, body)
	}

	// Reading works.
	if r, _ := viewer.do(t, "GET", "/api/v1/projects/"+projectID, nil); r.StatusCode != http.StatusOK {
		t.Fatalf("viewer cannot read the project they were given: %d", r.StatusCode)
	}
	if r, _ := viewer.do(t, "GET", "/api/v1/tasks/"+taskID, nil); r.StatusCode != http.StatusOK {
		t.Errorf("viewer cannot read a task: %d", r.StatusCode)
	}

	// Nothing else does.
	writes := []struct {
		name, method, path string
		body               any
	}{
		{"edit a task", "PATCH", "/api/v1/tasks/" + taskID, map[string]any{"content": "ændret"}},
		{"complete a task", "POST", "/api/v1/tasks/" + taskID + "/complete", nil},
		{"reopen a task", "POST", "/api/v1/tasks/" + taskID + "/reopen", nil},
		{"delete a task", "DELETE", "/api/v1/tasks/" + taskID, nil},
		{"move a task", "POST", "/api/v1/tasks/" + taskID + "/move", map[string]any{}},
		{"create a task", "POST", "/api/v1/tasks", map[string]any{"content": "ny", "project_id": projectID}},
		{"add a section", "POST", "/api/v1/projects/" + projectID + "/sections", map[string]any{"name": "Ny"}},
		{"rename the project", "PATCH", "/api/v1/projects/" + projectID, map[string]any{"name": "Kapret"}},
		{"delete the project", "DELETE", "/api/v1/projects/" + projectID, nil},
		{"invite somebody", "POST", "/api/v1/projects/" + projectID + "/invites", map[string]any{"email": "x@y.dk"}},
	}
	for _, tc := range writes {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := viewer.do(t, tc.method, tc.path, tc.body)
			if resp.StatusCode < 400 {
				t.Errorf("a viewer could %s: status %d", tc.name, resp.StatusCode)
			}
		})
	}

	// And nothing actually changed.
	_, after := ts.do(t, "GET", "/api/v1/tasks/"+taskID, nil)
	if after["content"] != "en opgave" || after["completed"] != false {
		t.Errorf("the task was modified by a viewer: %v", after)
	}
}

func TestEditorCanWriteButNotManage(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	editor := ts.newUser(t, "editor@example.dk", "Editor")

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	projectID := project["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites", map[string]any{
		"email": "editor@example.dk", "role": "editor",
	})

	resp, body := editor.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "skrevet af editor", "project_id": projectID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("editor cannot create a task: %d %v", resp.StatusCode, body)
	}

	// Managing the project itself is the owner's.
	for _, tc := range []struct{ name, method, path string }{
		{"rename", "PATCH", "/api/v1/projects/" + projectID},
		{"delete", "DELETE", "/api/v1/projects/" + projectID},
		{"invite", "POST", "/api/v1/projects/" + projectID + "/invites"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := editor.do(t, tc.method, tc.path, map[string]any{"name": "x", "email": "a@b.dk"})
			if resp.StatusCode < 400 {
				t.Errorf("an editor could %s the project: %d", tc.name, resp.StatusCode)
			}
		})
	}
}

// An external user must see nothing but what has actually been shared with them.
func TestExternalUserSeesOnlySharedProjects(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	outsider := ts.newUser(t, "ekstern@example.dk", "Ekstern")

	_, shared := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	_, private := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Privat"})
	sharedID, privateID := shared["id"].(string), private["id"].(string)

	_, privateTask := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "hemmelig opgave", "project_id": privateID,
	})
	privateTaskID := privateTask["id"].(string)

	ts.do(t, "POST", "/api/v1/projects/"+sharedID+"/invites", map[string]any{
		"email": "ekstern@example.dk", "role": "editor",
	})

	// Their project list holds their own Inbox and the one shared project.
	_, list := outsider.do(t, "GET", "/api/v1/projects", nil)
	projects, _ := list["projects"].([]any)
	names := map[string]bool{}
	for _, p := range projects {
		names[p.(map[string]any)["name"].(string)] = true
	}
	if !names["Delt"] {
		t.Error("the shared project is missing from the external user's list")
	}
	if names["Privat"] {
		t.Error("an unshared project appeared in the external user's list")
	}

	// And the private one is unreachable by id, reported as absent rather than
	// forbidden — a 403 would confirm it exists.
	resp, _ := outsider.do(t, "GET", "/api/v1/projects/"+privateID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("private project: status %d, want 404", resp.StatusCode)
	}
	resp, _ = outsider.do(t, "GET", "/api/v1/tasks/"+privateTaskID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("task in a private project: status %d, want 404", resp.StatusCode)
	}

	// Nor may it be reached by filtering a list at it.
	_, tasks := outsider.do(t, "GET", "/api/v1/tasks?project_id="+privateID, nil)
	if got, _ := tasks["tasks"].([]any); len(got) != 0 {
		t.Errorf("filtering by another user's project returned %d tasks", len(got))
	}
	// Nor by searching for its contents.
	_, found := outsider.do(t, "GET", "/api/v1/search?q=hemmelig", nil)
	if got, _ := found["tasks"].([]any); len(got) != 0 {
		t.Errorf("search reached another user's tasks: %v", got)
	}
}

func TestTheInboxCannotBeDeleted(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := ts.db.InboxID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}

	resp, _ := ts.do(t, "DELETE", "/api/v1/projects/"+inbox, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("deleting the Inbox: status %d, want 409", resp.StatusCode)
	}
}

func TestRemovingAMemberUnassignsTheirTasks(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	member := ts.newUser(t, "medlem@example.dk", "Medlem")

	memberUser, err := ts.db.UserByEmail(t.Context(), "medlem@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	projectID := project["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites", map[string]any{
		"email": "medlem@example.dk", "role": "editor",
	})

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "tildelt opgave", "project_id": projectID, "assignee_id": memberUser.ID,
	})
	taskID := task["id"].(string)

	resp, _ := ts.do(t, "DELETE", "/api/v1/projects/"+projectID+"/members/"+memberUser.ID, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove member: %d", resp.StatusCode)
	}

	// The task must not still point at somebody who can no longer see it.
	_, after := ts.do(t, "GET", "/api/v1/tasks/"+taskID, nil)
	if after["assignee_id"] != nil && after["assignee_id"] != "" {
		t.Errorf("assignee_id = %v after removal, want cleared", after["assignee_id"])
	}
	// And they have lost access.
	if r, _ := member.do(t, "GET", "/api/v1/projects/"+projectID, nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("a removed member still reaches the project: %d", r.StatusCode)
	}
}

func TestSectionsBelongToTheirProject(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	outsider := ts.newUser(t, "ekstern@example.dk", "Ekstern")

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Privat"})
	projectID := project["id"].(string)

	resp, section := ts.do(t, "POST", "/api/v1/projects/"+projectID+"/sections",
		map[string]any{"name": "I gang"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create section: %d %v", resp.StatusCode, section)
	}
	sectionID := section["id"].(string)

	// A section id from somewhere else must not be editable by a stranger.
	for _, tc := range []struct{ method, path string }{
		{"PATCH", "/api/v1/sections/" + sectionID},
		{"DELETE", "/api/v1/sections/" + sectionID},
	} {
		resp, _ := outsider.do(t, tc.method, tc.path, map[string]any{"name": "kapret"})
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s by an outsider: status %d, want 404", tc.method, resp.StatusCode)
		}
	}

	// Deleting a section keeps its tasks — deleting a heading is not a request to
	// delete the work filed under it.
	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "under sektionen", "project_id": projectID, "section_id": sectionID,
	})
	taskID := task["id"].(string)

	ts.do(t, "DELETE", "/api/v1/sections/"+sectionID, nil)
	resp, after := ts.do(t, "GET", "/api/v1/tasks/"+taskID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the task went with its section: %d", resp.StatusCode)
	}
	if after["section_id"] != nil && after["section_id"] != "" {
		t.Errorf("section_id = %v, want cleared", after["section_id"])
	}
}

func TestActivityRecordsWhatHappened(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	projectID := project["id"].(string)
	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "en opgave", "project_id": projectID,
	})
	ts.do(t, "POST", "/api/v1/tasks/"+task["id"].(string)+"/complete", nil)

	_, body := ts.do(t, "GET", "/api/v1/projects/"+projectID+"/activity", nil)
	entries, _ := body["activity"].([]any)
	if len(entries) < 3 {
		t.Fatalf("activity has %d entries, want the create, the task and the completion", len(entries))
	}

	events := map[string]bool{}
	for _, e := range entries {
		events[e.(map[string]any)["event"].(string)] = true
	}
	for _, want := range []string{"project.created", "task.created", "task.completed"} {
		if !events[want] {
			t.Errorf("no %s in the activity log", want)
		}
	}
}

// --- recurring tasks ------------------------------------------------------------

// Ticking off a repeating task does not finish it — it moves to the next occurrence.
// That is the whole behaviour, and it is the one that would be most annoying to get
// wrong: a weekly chore that disappears is a weekly chore you stop doing.
func TestCompletingARecurringTaskAdvancesIt(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, task := ts.do(t, "POST", "/api/v1/tasks/quick-add", map[string]any{
		"text": "vand planterne hver mandag",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("quick add: %d %v", resp.StatusCode, task)
	}
	id := task["id"].(string)
	firstDue, _ := task["due_date"].(string)

	if task["recurrence_rule"] != "FREQ=WEEKLY;BYDAY=MO" {
		t.Fatalf("recurrence_rule = %v", task["recurrence_rule"])
	}
	if firstDue == "" {
		t.Fatal("a repeating task was created with no date to repeat from")
	}

	resp, done := ts.do(t, "POST", "/api/v1/tasks/"+id+"/complete", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("complete: %d %v", resp.StatusCode, done)
	}

	if done["recurred"] != true {
		t.Errorf("recurred = %v, want true", done["recurred"])
	}
	if done["completed"] != false {
		t.Error("a repeating task was closed instead of advanced")
	}

	nextDue, _ := done["due_date"].(string)
	if nextDue <= firstDue {
		t.Errorf("due_date went from %q to %q — it did not move forward", firstDue, nextDue)
	}
	// Weekly, so exactly seven days on.
	first, _ := time.Parse("2006-01-02", firstDue)
	next, _ := time.Parse("2006-01-02", nextDue)
	if days := int(next.Sub(first).Hours() / 24); days != 7 {
		t.Errorf("the task moved %d days, want 7", days)
	}

	// And it is still in the open list, not the completed one.
	_, list := ts.do(t, "GET", "/api/v1/tasks", nil)
	tasks, _ := list["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("the open list has %d tasks, want the repeating one", len(tasks))
	}
}

// The task keeps its identity across occurrences, so its sub-tasks and comments
// stay attached — a "weekly review" is one thing that recurs, not fifty-two things.
func TestARecurringTaskKeepsItsIdentity(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks/quick-add", map[string]any{
		"text": "ugentlig gennemgang hver fredag",
	})
	id := task["id"].(string)

	_, child := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "læs noter", "parent_id": id,
	})
	childID := child["id"].(string)

	ts.do(t, "POST", "/api/v1/tasks/"+id+"/complete", nil)

	resp, after := ts.do(t, "GET", "/api/v1/tasks/"+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the task disappeared: %d", resp.StatusCode)
	}
	if after["id"] != id {
		t.Error("the id changed across an occurrence")
	}
	resp, _ = ts.do(t, "GET", "/api/v1/tasks/"+childID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Error("the sub-task was lost when its parent recurred")
	}
}

// A task due at a particular hour keeps that hour when it moves.
func TestRecurrenceKeepsTheClockTime(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks/quick-add", map[string]any{
		"text": "standup hverdage kl 9",
	})
	id := task["id"].(string)
	before, _ := task["due_datetime"].(string)
	if before == "" {
		t.Fatal("no due_datetime was set")
	}

	_, done := ts.do(t, "POST", "/api/v1/tasks/"+id+"/complete", nil)
	after, _ := done["due_datetime"].(string)
	if after == "" {
		t.Fatal("the clock time was dropped when the task recurred")
	}

	parseHM := func(s string) string {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return v.Format("15:04")
	}
	if parseHM(before) != parseHM(after) {
		t.Errorf("the time changed from %s to %s", parseHM(before), parseHM(after))
	}
}

// A completion is still recorded, even though nothing was closed. "What did I get
// done this week" has to include the chores that repeat.
func TestRecurringCompletionIsRecorded(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Hjem"})
	projectID := project["id"].(string)

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "tøm opvaskemaskinen", "project_id": projectID,
		"due_date": userDate(t, 0), "recurrence_rule": "FREQ=DAILY",
	})
	ts.do(t, "POST", "/api/v1/tasks/"+task["id"].(string)+"/complete", nil)

	_, body := ts.do(t, "GET", "/api/v1/projects/"+projectID+"/activity", nil)
	entries, _ := body["activity"].([]any)

	var found bool
	for _, e := range entries {
		entry := e.(map[string]any)
		if entry["event"] != "task.completed" {
			continue
		}
		found = true
		payload, _ := entry["payload"].(map[string]any)
		if payload["recurred"] != true {
			t.Errorf("the completion was not marked as a recurrence: %v", payload)
		}
	}
	if !found {
		t.Error("no completion was recorded for a repeating task")
	}
}

// A rule the RRULE library cannot read must be refused when the task is created,
// not discovered when somebody ticks it off and it will not move.
func TestInvalidRecurrenceIsRefused(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "noget", "recurrence_rule": "FREQ=FORTNIGHTLY",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", resp.StatusCode)
	}
	fields, _ := body["fields"].(map[string]any)
	if fields["recurrence_rule"] == nil {
		t.Errorf("the error does not name the field: %v", body)
	}
}

// A non-repeating task must still complete normally.
func TestCompletingAPlainTaskStillCloses(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "engangsopgave"})
	_, done := ts.do(t, "POST", "/api/v1/tasks/"+task["id"].(string)+"/complete", nil)

	if done["completed"] != true {
		t.Error("a plain task was not completed")
	}
	if done["recurred"] == true {
		t.Error("a plain task reported as recurring")
	}
}

// --- saved filters ----------------------------------------------------------------

func TestSavedFilters(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Firma"})
	projectID := project["id"].(string)

	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "betal moms", "project_id": projectID,
		"priority": 1, "due_date": userDate(t, 0), "labels": []string{"regnskab"},
	})
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "ryd op", "project_id": projectID, "priority": 4,
	})
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "forsinket ting", "due_date": userDate(t, -3), "priority": 2,
	})

	resp, filter := ts.do(t, "POST", "/api/v1/filters", map[string]any{
		"name": "Haster i dag", "query": "today & p1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create filter: %d %v", resp.StatusCode, filter)
	}

	resp, result := ts.do(t, "GET", "/api/v1/filters/"+filter["id"].(string)+"/tasks", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("run filter: %d %v", resp.StatusCode, result)
	}
	tasks, _ := result["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("the filter matched %d tasks, want 1", len(tasks))
	}
	if tasks[0].(map[string]any)["content"] != "betal moms" {
		t.Errorf("matched the wrong task: %v", tasks[0])
	}
}

func TestFilterPreviewRunsWithoutSaving(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "forsinket", "due_date": userDate(t, -2),
	})
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "ingen dato"})

	resp, body := ts.do(t, "GET", "/api/v1/filters/preview?query=overdue", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview: %d %v", resp.StatusCode, body)
	}
	tasks, _ := body["tasks"].([]any)
	if len(tasks) != 1 {
		t.Errorf("overdue matched %d tasks, want 1", len(tasks))
	}

	_, list := ts.do(t, "GET", "/api/v1/filters", nil)
	if saved, _ := list["filters"].([]any); len(saved) != 0 {
		t.Errorf("the preview saved %d filters", len(saved))
	}
}

// A filter that cannot be compiled must be refused when it is saved, not when it
// is next opened and silently returns nothing.
func TestInvalidFilterIsRefusedOnSave(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/filters", map[string]any{
		"name": "Ødelagt", "query": "today & (p1",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", resp.StatusCode)
	}
	fields, _ := body["fields"].(map[string]any)
	if fields["query"] == nil {
		t.Errorf("the error does not name the query field: %v", body)
	}
}

// A filter belongs to one person and must not reach anybody else's tasks.
func TestFiltersAreScopedToTheirOwner(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "min private opgave", "priority": 1, "due_date": userDate(t, 0),
	})
	_, filter := ts.do(t, "POST", "/api/v1/filters", map[string]any{
		"name": "Mit filter", "query": "today & p1",
	})
	filterID := filter["id"].(string)

	// Somebody else cannot run it.
	resp, _ := other.do(t, "GET", "/api/v1/filters/"+filterID+"/tasks", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("another user ran the filter: status %d", resp.StatusCode)
	}

	// And writing the same expression themselves finds none of the owner's tasks.
	_, result := other.do(t, "GET", "/api/v1/filters/preview?query=today+%26+p1", nil)
	if tasks, _ := result["tasks"].([]any); len(tasks) != 0 {
		t.Errorf("an identical filter reached another user's tasks: %v", tasks)
	}
}

func TestLabelsCRUD(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// Labels appear by being used, without being created first.
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "med etiket", "labels": []string{"venter"},
	})
	_, list := ts.do(t, "GET", "/api/v1/labels", nil)
	labels, _ := list["labels"].([]any)
	if len(labels) != 1 {
		t.Fatalf("got %d labels, want the one created by using it", len(labels))
	}
	if labels[0].(map[string]any)["task_count"] != float64(1) {
		t.Errorf("task_count = %v, want 1", labels[0].(map[string]any)["task_count"])
	}

	resp, created := ts.do(t, "POST", "/api/v1/labels", map[string]any{"name": "haster"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create label: %d %v", resp.StatusCode, created)
	}
	// The same name twice is a conflict, not a second label.
	resp, _ = ts.do(t, "POST", "/api/v1/labels", map[string]any{"name": "HASTER"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate label: status %d, want 409", resp.StatusCode)
	}

	resp, _ = ts.do(t, "DELETE", "/api/v1/labels/"+created["id"].(string), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete label: status %d", resp.StatusCode)
	}
}

// Deleting a label is tidying, not a request to delete the work filed under it.
func TestDeletingALabelKeepsItsTasks(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "vigtig opgave", "labels": []string{"venter"},
	})
	taskID := task["id"].(string)

	_, list := ts.do(t, "GET", "/api/v1/labels", nil)
	labels, _ := list["labels"].([]any)
	labelID := labels[0].(map[string]any)["id"].(string)

	ts.do(t, "DELETE", "/api/v1/labels/"+labelID, nil)

	resp, after := ts.do(t, "GET", "/api/v1/tasks/"+taskID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the task went with its label: %d", resp.StatusCode)
	}
	if got, _ := after["labels"].([]any); len(got) != 0 {
		t.Errorf("labels = %v, want empty", got)
	}
}

// --- reminders, feeds and templates -----------------------------------------------

func TestReminders(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "møde", "due_date": userDate(t, 0), "due_time": "14:00",
	})
	taskID := task["id"].(string)

	// Relative: ten minutes before whenever the task is due.
	offset := -10
	resp, body := ts.do(t, "POST", "/api/v1/tasks/"+taskID+"/reminders",
		map[string]any{"offset_min": offset})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create relative reminder: %d %v", resp.StatusCode, body)
	}

	// Absolute.
	resp, _ = ts.do(t, "POST", "/api/v1/tasks/"+taskID+"/reminders",
		map[string]any{"remind_at": time.Now().Add(time.Hour).Format(time.RFC3339)})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create absolute reminder: %d", resp.StatusCode)
	}

	// Both at once is not a reminder with two times; it is a mistake.
	resp, _ = ts.do(t, "POST", "/api/v1/tasks/"+taskID+"/reminders", map[string]any{
		"offset_min": offset, "remind_at": time.Now().Format(time.RFC3339),
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("a reminder with both a time and an offset: status %d, want 422", resp.StatusCode)
	}

	_, list := ts.do(t, "GET", "/api/v1/tasks/"+taskID+"/reminders", nil)
	reminders, _ := list["reminders"].([]any)
	if len(reminders) != 2 {
		t.Fatalf("got %d reminders, want 2", len(reminders))
	}
}

// A reminder that has come due is found by the sweep; one in the future is not,
// and neither is one on a task that has already been done.
func TestDueRemindersAreFoundOnce(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "husk"})
	taskID := task["id"].(string)

	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)
	ts.do(t, "POST", "/api/v1/tasks/"+taskID+"/reminders",
		map[string]any{"remind_at": past.Format(time.RFC3339)})
	ts.do(t, "POST", "/api/v1/tasks/"+taskID+"/reminders",
		map[string]any{"remind_at": future.Format(time.RFC3339)})

	due, err := ts.db.DueReminders(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("got %d due reminders, want only the one in the past", len(due))
	}
	if due[0].TaskContent != "husk" {
		t.Errorf("the reminder does not carry the task's text: %+v", due[0])
	}

	// Marking it sent takes it out of the sweep, so it cannot go out twice.
	if err := ts.db.MarkReminderSent(t.Context(), due[0].ID); err != nil {
		t.Fatal(err)
	}
	again, err := ts.db.DueReminders(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("a sent reminder came round again: %+v", again)
	}
}

// A reminder for something already done is a small betrayal of the whole feature.
func TestCompletedTasksDoNotRemind(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "gjort"})
	taskID := task["id"].(string)
	ts.do(t, "POST", "/api/v1/tasks/"+taskID+"/reminders",
		map[string]any{"remind_at": time.Now().Add(-time.Minute).Format(time.RFC3339)})

	ts.do(t, "POST", "/api/v1/tasks/"+taskID+"/complete", nil)

	due, err := ts.db.DueReminders(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Errorf("a completed task still reminded: %+v", due)
	}
}

func TestICSFeed(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "møde med revisor", "due_date": userDate(t, 1), "due_time": "10:00",
	})
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "uden dato"})

	resp, body := ts.do(t, "GET", "/api/v1/feed", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get feed: %d %v", resp.StatusCode, body)
	}
	feedURL, _ := body["url"].(string)
	if feedURL == "" {
		t.Fatal("no feed URL was returned")
	}

	// The feed itself is fetched without a session, as a calendar client would.
	path := feedURL[strings.Index(feedURL, "/ics/"):]
	req, err := http.NewRequest("GET", ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Body.Close()

	if raw.StatusCode != http.StatusOK {
		t.Fatalf("fetching the feed without a session: %d", raw.StatusCode)
	}
	if ct := raw.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("Content-Type = %q, want text/calendar", ct)
	}

	content, _ := io.ReadAll(raw.Body)
	feed := string(content)
	if !strings.Contains(feed, "BEGIN:VCALENDAR") {
		t.Error("the feed is not an iCalendar document")
	}
	if !strings.Contains(feed, "SUMMARY:møde med revisor") {
		t.Error("the dated task is missing from the feed")
	}
	if strings.Contains(feed, "uden dato") {
		t.Error("a task with no date appeared in a calendar")
	}

	// An invented token is a missing feed, not a prompt for credentials nobody
	// can answer.
	bad, err := (&http.Client{}).Get(ts.URL + "/ics/opfundet.ics")
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown feed token: status %d, want 404", bad.StatusCode)
	}
}

// Rotating the token is what somebody does when a feed URL has leaked, so the old
// one has to stop working immediately.
func TestRotatingTheFeedTokenBreaksTheOldURL(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, first := ts.do(t, "GET", "/api/v1/feed", nil)
	oldURL := first["url"].(string)

	_, second := ts.do(t, "POST", "/api/v1/feed/rotate", nil)
	newURL := second["url"].(string)

	if oldURL == newURL {
		t.Fatal("rotating produced the same URL")
	}

	oldPath := oldURL[strings.Index(oldURL, "/ics/"):]
	resp, err := (&http.Client{}).Get(ts.URL + oldPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the old feed URL still works: status %d", resp.StatusCode)
	}
}

func TestProjectTemplates(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Onboarding"})
	projectID := project["id"].(string)

	_, section := ts.do(t, "POST", "/api/v1/projects/"+projectID+"/sections",
		map[string]any{"name": "Første uge"})
	sectionID := section["id"].(string)

	_, parent := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "opret konti", "project_id": projectID, "section_id": sectionID,
		"priority": 1, "due_date": userDate(t, 0), "labels": []string{"it"},
	})
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "udlever laptop", "project_id": projectID, "parent_id": parent["id"].(string),
	})
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "opfølgning", "project_id": projectID, "due_date": userDate(t, 14),
	})

	resp, tpl := ts.do(t, "POST", "/api/v1/templates", map[string]any{
		"project_id": projectID, "name": "Onboarding-skabelon",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("save template: %d %v", resp.StatusCode, tpl)
	}
	if tpl["task_count"] != float64(3) {
		t.Errorf("task_count = %v, want 3", tpl["task_count"])
	}

	// Used a month later, the dates land relative to the new start.
	start := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	resp, created := ts.do(t, "POST", "/api/v1/templates/"+tpl["id"].(string)+"/use",
		map[string]any{"name": "Onboarding: Mette", "start_date": start})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("use template: %d %v", resp.StatusCode, created)
	}
	if created["name"] != "Onboarding: Mette" {
		t.Errorf("name = %v", created["name"])
	}

	newID := created["id"].(string)
	_, list := ts.do(t, "GET", "/api/v1/tasks?project_id="+newID, nil)
	tasks, _ := list["tasks"].([]any)
	if len(tasks) != 3 {
		t.Fatalf("the new project has %d tasks, want 3", len(tasks))
	}

	// Sections came across, sub-tasks kept their parent, and the spacing between
	// the dates survived even though the dates themselves did not.
	_, sections := ts.do(t, "GET", "/api/v1/projects/"+newID+"/sections", nil)
	if got, _ := sections["sections"].([]any); len(got) != 1 {
		t.Errorf("got %d sections, want 1", len(got))
	}

	var withParent, dated int
	var earliest, latest string
	for _, raw := range tasks {
		task := raw.(map[string]any)
		if task["parent_id"] != nil && task["parent_id"] != "" {
			withParent++
		}
		if due, _ := task["due_date"].(string); due != "" {
			dated++
			if earliest == "" || due < earliest {
				earliest = due
			}
			if due > latest {
				latest = due
			}
		}
	}
	if withParent != 1 {
		t.Errorf("%d tasks have a parent, want 1", withParent)
	}
	if dated != 2 {
		t.Errorf("%d tasks have a date, want 2", dated)
	}
	if earliest != start {
		t.Errorf("the first task is due %q, want the stated start %q", earliest, start)
	}
	first, _ := time.Parse("2006-01-02", earliest)
	last, _ := time.Parse("2006-01-02", latest)
	if gap := int(last.Sub(first).Hours() / 24); gap != 14 {
		t.Errorf("the tasks are %d days apart, want the original 14", gap)
	}
}

func TestTemplatesAreScopedToTheirOwner(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Privat"})
	_, tpl := ts.do(t, "POST", "/api/v1/templates",
		map[string]any{"project_id": project["id"].(string)})
	tplID := tpl["id"].(string)

	resp, _ := other.do(t, "POST", "/api/v1/templates/"+tplID+"/use", map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("another user used the template: status %d", resp.StatusCode)
	}
	_, list := other.do(t, "GET", "/api/v1/templates", nil)
	if got, _ := list["templates"].([]any); len(got) != 0 {
		t.Errorf("another user's template list shows %d entries", len(got))
	}
}

// --- comments, attachments and import/export -----------------------------------------

func TestCommentsAndAttachments(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "med kommentarer"})
	taskID := task["id"].(string)

	resp, comment := ts.do(t, "POST", "/api/v1/tasks/"+taskID+"/comments",
		map[string]any{"body": "første kommentar"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create comment: %d %v", resp.StatusCode, comment)
	}
	if comment["user_name"] != "Kristian" {
		t.Errorf("user_name = %v", comment["user_name"])
	}

	// A file attached directly to the task.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "bilag.pdf")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("%PDF-1.4 ikke en rigtig pdf"))
	writer.Close()

	req, err := http.NewRequest("POST", ts.URL+"/api/v1/tasks/"+taskID+"/attachments", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	upload, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer upload.Body.Close()
	if upload.StatusCode != http.StatusCreated {
		t.Fatalf("upload: status %d", upload.StatusCode)
	}

	var attachment map[string]any
	raw, _ := io.ReadAll(upload.Body)
	json.Unmarshal(raw, &attachment)
	if attachment["filename"] != "bilag.pdf" {
		t.Errorf("filename = %v", attachment["filename"])
	}

	_, list := ts.do(t, "GET", "/api/v1/tasks/"+taskID+"/comments", nil)
	if got, _ := list["comments"].([]any); len(got) != 1 {
		t.Errorf("got %d comments, want 1", len(got))
	}
	if got, _ := list["attachments"].([]any); len(got) != 1 {
		t.Errorf("got %d attachments on the task, want 1", len(got))
	}

	// The file downloads, and never as something a browser would render.
	dl, dlBody := ts.do(t, "GET", "/api/v1/attachments/"+attachment["id"].(string), nil)
	_ = dlBody
	if dl.StatusCode != http.StatusOK {
		t.Fatalf("download: status %d", dl.StatusCode)
	}
	if ct := dl.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q; an uploaded file must never be served inline", ct)
	}
	if cd := dl.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}
}

// A viewer can read a discussion but not join it, and cannot upload.
func TestViewersCannotComment(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	viewer := ts.newUser(t, "viewer@example.dk", "Viewer")

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	projectID := project["id"].(string)
	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "opgave", "project_id": projectID,
	})
	taskID := task["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites",
		map[string]any{"email": "viewer@example.dk", "role": "viewer"})
	ts.do(t, "POST", "/api/v1/tasks/"+taskID+"/comments", map[string]any{"body": "ejerens ord"})

	if r, body := viewer.do(t, "GET", "/api/v1/tasks/"+taskID+"/comments", nil); r.StatusCode != http.StatusOK {
		t.Errorf("a viewer cannot read comments: %d %v", r.StatusCode, body)
	}
	if r, _ := viewer.do(t, "POST", "/api/v1/tasks/"+taskID+"/comments",
		map[string]any{"body": "smuglet ind"}); r.StatusCode < 400 {
		t.Errorf("a viewer could comment: status %d", r.StatusCode)
	}
}

// Somebody else's comment is theirs. A project role does not confer the right to
// rewrite what another person said.
func TestOnlyTheAuthorCanEditAComment(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	editor := ts.newUser(t, "editor@example.dk", "Editor")

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	projectID := project["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites",
		map[string]any{"email": "editor@example.dk", "role": "editor"})
	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "opgave", "project_id": projectID,
	})
	taskID := task["id"].(string)
	_, comment := ts.do(t, "POST", "/api/v1/tasks/"+taskID+"/comments",
		map[string]any{"body": "ejerens ord"})
	commentID := comment["id"].(string)

	resp, _ := editor.do(t, "PATCH", "/api/v1/comments/"+commentID,
		map[string]any{"body": "omskrevet"})
	if resp.StatusCode < 400 {
		t.Errorf("an editor rewrote somebody else's comment: status %d", resp.StatusCode)
	}

	_, list := ts.do(t, "GET", "/api/v1/tasks/"+taskID+"/comments", nil)
	comments, _ := list["comments"].([]any)
	if comments[0].(map[string]any)["body"] != "ejerens ord" {
		t.Error("the comment was changed")
	}
}

func TestImportTodoistCSV(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	csv := "TYPE,CONTENT,DESCRIPTION,PRIORITY,INDENT,AUTHOR,RESPONSIBLE,DATE,DATE_LANG,TIMEZONE\n" +
		"task,Betal moms,husk bilag,4,1,Kristian,,2026-03-15,en,\n" +
		"note,En kommentar,,,,,,,,\n" +
		"task,Find bilag,,1,2,Kristian,,,,\n" +
		"section,Til gennemsyn,,,,,,,,\n" +
		"task,Gennemgå,,3,1,Kristian,,,,\n"

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("name", "Fra Todoist")
	part, err := writer.CreateFormFile("file", "firma.csv")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte(csv))
	writer.Close()

	req, err := http.NewRequest("POST", ts.URL+"/api/v1/import/todoist", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("import: status %d, %s", resp.StatusCode, raw)
	}
	var result map[string]any
	raw, _ := io.ReadAll(resp.Body)
	json.Unmarshal(raw, &result)

	if result["tasks"] != float64(3) {
		t.Errorf("imported %v tasks, want 3", result["tasks"])
	}
	if result["sections"] != float64(1) {
		t.Errorf("imported %v sections, want 1", result["sections"])
	}
	if result["comments"] != float64(1) {
		t.Errorf("imported %v comments, want 1", result["comments"])
	}

	projectID := result["project_id"].(string)
	_, list := ts.do(t, "GET", "/api/v1/tasks?project_id="+projectID, nil)
	tasks, _ := list["tasks"].([]any)

	var found bool
	for _, raw := range tasks {
		task := raw.(map[string]any)
		if task["content"] != "Betal moms" {
			continue
		}
		found = true
		// Todoist's 4 is verdande's 1. Getting this backwards would invert the
		// urgency of every imported task.
		if task["priority"] != float64(1) {
			t.Errorf("priority = %v, want 1", task["priority"])
		}
		if task["due_date"] != "2026-03-15" {
			t.Errorf("due_date = %v", task["due_date"])
		}
	}
	if !found {
		t.Error("the imported task was not found")
	}
}

// The whole point of import and export together: what goes in comes back out.
func TestTodoistImportExportRoundTrip(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	original := "TYPE,CONTENT,DESCRIPTION,PRIORITY,INDENT,AUTHOR,RESPONSIBLE,DATE,DATE_LANG,TIMEZONE\n" +
		"task,Betal moms,husk bilag,4,1,Kristian,,,,\n" +
		"task,Find bilag,,2,2,Kristian,,,,\n" +
		"task,Ring til revisor,,1,1,Kristian,,,,\n"

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("name", "Rundtur")
	part, _ := writer.CreateFormFile("file", "firma.csv")
	part.Write([]byte(original))
	writer.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/import/todoist", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var result map[string]any
	json.Unmarshal(raw, &result)
	projectID, _ := result["project_id"].(string)
	if projectID == "" {
		t.Fatalf("import failed: %s", raw)
	}

	// Export it again and compare the shape.
	out, exported := ts.do(t, "GET", "/api/v1/export/projects/"+projectID+".csv", nil)
	_ = exported
	if out.StatusCode != http.StatusOK {
		t.Fatalf("export: status %d", out.StatusCode)
	}

	// The response body was consumed by `do`; fetch it as text instead.
	req2, _ := http.NewRequest("GET", ts.URL+"/api/v1/export/projects/"+projectID+".csv", nil)
	req2.Header.Set("Sec-Fetch-Site", "same-origin")
	csvResp, err := ts.client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer csvResp.Body.Close()
	csvBytes, _ := io.ReadAll(csvResp.Body)

	roundTripped := string(csvBytes)
	for _, want := range []string{"Betal moms", "Find bilag", "Ring til revisor", "husk bilag"} {
		if !strings.Contains(roundTripped, want) {
			t.Errorf("%q is missing from the export:\n%s", want, roundTripped)
		}
	}
	// The priorities have to come back as Todoist's numbering, not verdande's.
	if !strings.Contains(roundTripped, "task,Betal moms,husk bilag,4,1") {
		t.Errorf("the exported row does not match Todoist's format:\n%s", roundTripped)
	}
	// And the sub-task comes back exactly as it went in: Todoist priority 2 became
	// verdande's 3 on the way in and has to become 2 again on the way out.
	if !strings.Contains(roundTripped, "task,Find bilag,,2,2") {
		t.Errorf("the sub-task lost its indent or priority:\n%s", roundTripped)
	}
}

func TestAccountExport(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Firma"})
	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "en opgave", "project_id": project["id"].(string), "labels": []string{"vigtig"},
	})
	ts.do(t, "POST", "/api/v1/tasks/"+task["id"].(string)+"/comments",
		map[string]any{"body": "en kommentar"})

	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/export/account", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: status %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)

	var export map[string]any
	if err := json.Unmarshal(raw, &export); err != nil {
		t.Fatalf("the export is not valid JSON: %v", err)
	}
	if export["version"] != float64(1) {
		t.Errorf("version = %v", export["version"])
	}

	// A password hash must never be in an export somebody emails to themselves.
	if strings.Contains(string(raw), "$argon2id$") {
		t.Error("the account export contains a password hash")
	}

	projects, _ := export["projects"].([]any)
	if len(projects) < 2 {
		t.Fatalf("got %d projects, want the Inbox and Firma", len(projects))
	}

	var foundComment bool
	for _, p := range projects {
		tasks, _ := p.(map[string]any)["tasks"].([]any)
		for _, raw := range tasks {
			comments, _ := raw.(map[string]any)["comments"].([]any)
			if len(comments) > 0 {
				foundComment = true
			}
		}
	}
	if !foundComment {
		t.Error("comments are missing from the account export")
	}
	if labels, _ := export["labels"].([]any); len(labels) == 0 {
		t.Error("labels are missing from the account export")
	}
}

func TestNotifications(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	projectID := project["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites",
		map[string]any{"email": "anden@example.dk", "role": "editor"})
	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "opgave", "project_id": projectID,
	})
	ts.do(t, "POST", "/api/v1/tasks/"+task["id"].(string)+"/comments",
		map[string]any{"body": "sig noget"})

	// The other member hears about it.
	_, list := other.do(t, "GET", "/api/v1/notifications", nil)
	notifications, _ := list["notifications"].([]any)
	if len(notifications) == 0 {
		t.Fatal("the other member was not notified about the comment")
	}
	if list["unread"] == float64(0) {
		t.Error("the notification is not counted as unread")
	}

	// The author is not told what they just wrote.
	_, own := ts.do(t, "GET", "/api/v1/notifications", nil)
	if got, _ := own["notifications"].([]any); len(got) != 0 {
		t.Errorf("the author was notified about their own comment: %v", got)
	}

	other.do(t, "POST", "/api/v1/notifications/read", nil)
	_, after := other.do(t, "GET", "/api/v1/notifications", nil)
	if after["unread"] != float64(0) {
		t.Errorf("unread = %v after marking everything read", after["unread"])
	}
}
