package httpapi

import (
	"net/http"
	"testing"
)

// A note in a project is the project's; a note in no project is its author's.
// Both answers come from the project's own roles rather than from a rule notes
// invented for themselves, which is the point of filing them there.
func TestANoteIsNotReadableByJustAnybody(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/notes", map[string]any{
		"body": "Noget privat om #Firma",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatal("no id came back")
	}

	// The tags were read out of the text on the way in, without being asked for.
	links, _ := body["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("the note recorded %v", body["links"])
	}

	// Somebody else gets the same answer as for a note that does not exist, so an
	// id cannot be probed for existence.
	other := ts.newUser(t, "anden@example.dk", "Anden")
	resp, _ = other.do(t, "GET", "/api/v1/notes/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a stranger got %d for somebody else's note, want 404", resp.StatusCode)
	}
}

// Searching must not become a way to read what you cannot open. The filter is
// applied after the index, and this is what says so.
func TestSearchDoesNotLeakNotesYouCannotRead(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	if resp, body := ts.do(t, "POST", "/api/v1/notes", map[string]any{
		"body": "Hemmeligt kodeord til hjemmesiden",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %v", resp.StatusCode, body)
	}

	_, mine := ts.do(t, "GET", "/api/v1/notes?q=hemmeligt", nil)
	if got := len(mine["notes"].([]any)); got != 1 {
		t.Fatalf("the author found %d of their own notes", got)
	}

	other := ts.newUser(t, "anden@example.dk", "Anden")
	_, theirs := other.do(t, "GET", "/api/v1/notes?q=hemmeligt", nil)
	if got := len(theirs["notes"].([]any)); got != 0 {
		t.Errorf("a stranger's search returned %d notes", got)
	}
}

// The backwards question, which is what makes notes worth linking at all.
func TestANoteIsFoundFromTheTaskItMentions(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, task := ts.do(t, "POST", "/api/v1/tasks/quick-add", map[string]any{
		"text": "ring til Anders",
	})
	taskID, _ := task["id"].(string)
	if taskID == "" {
		t.Fatalf("no task id: %v", task)
	}

	if resp, _ := ts.do(t, "POST", "/api/v1/notes", map[string]any{
		"body": "Aftalt at han ringer først, se /opgave/" + taskID,
	}); resp.StatusCode != http.StatusCreated {
		t.Fatal("the note was not written")
	}

	_, found := ts.do(t, "GET", "/api/v1/notes/linking/task/"+taskID, nil)
	notes, _ := found["notes"].([]any)
	if len(notes) != 1 {
		t.Fatalf("%d notes point at the task, want 1", len(notes))
	}
}

// Editing the text moves the links with it. A link that outlives its mention
// claims a connection that is not there.
func TestChangingANoteChangesWhatItPointsAt(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, note := ts.do(t, "POST", "/api/v1/notes", map[string]any{"body": "Om #Firma"})
	id, _ := note["id"].(string)

	_, before := ts.do(t, "GET", "/api/v1/notes/linking/project/Firma", nil)
	if len(before["notes"].([]any)) != 1 {
		t.Fatal("the first save recorded nothing")
	}

	if resp, _ := ts.do(t, "PATCH", "/api/v1/notes/"+id, map[string]any{
		"body": "Om #Regnskab i stedet",
	}); resp.StatusCode != http.StatusOK {
		t.Fatal("the note was not changed")
	}

	_, after := ts.do(t, "GET", "/api/v1/notes/linking/project/Firma", nil)
	if got := len(after["notes"].([]any)); got != 0 {
		t.Errorf("%d notes still point at Firma", got)
	}
}
