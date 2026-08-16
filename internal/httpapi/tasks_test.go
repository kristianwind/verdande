package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/kristianwind/verdande/internal/store"
)

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
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
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

	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	inThreeDays := time.Now().AddDate(0, 0, 3).Format("2006-01-02")

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
