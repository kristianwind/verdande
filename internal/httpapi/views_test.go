package httpapi

import "testing"

// A label is how you find a task whose wording you have forgotten. Search
// covered the text only, so "regnskab" found nothing even with the task
// labelled @regnskab in front of you.
func TestSearchFindsTasksByLabel(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	ts.do(t, "POST", "/api/v1/tasks/quick-add",
		map[string]any{"text": "ring til revisoren @regnskab"})
	ts.do(t, "POST", "/api/v1/tasks/quick-add",
		map[string]any{"text": "køb kaffe"})

	_, body := ts.do(t, "GET", "/api/v1/search?q=regnskab", nil)
	tasks, _ := body["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("found %d tasks searching for a label, want 1: %v", len(tasks), body)
	}
	if got := tasks[0].(map[string]any)["content"]; got != "ring til revisoren" {
		t.Errorf("found %v", got)
	}
}

// The text search still works, and a label match does not swallow it.
func TestSearchStillFindsTasksByText(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	ts.do(t, "POST", "/api/v1/tasks/quick-add", map[string]any{"text": "køb grøn maling"})

	// Diacritic folding still applies to the text side.
	_, body := ts.do(t, "GET", "/api/v1/search?q=gron", nil)
	if tasks, _ := body["tasks"].([]any); len(tasks) != 1 {
		t.Errorf("searching \"gron\" found %d tasks, want 1", len(tasks))
	}
}

// Somebody else's label must not pull their task into your results — the label
// join has to stay inside the visibility the outer query already applies.
func TestSearchByLabelStaysWithinWhatYouCanSee(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	other := ts.newUser(t, "anden@example.dk", "Anden")
	other.do(t, "POST", "/api/v1/tasks/quick-add",
		map[string]any{"text": "deres hemmelighed @regnskab"})

	_, body := ts.do(t, "GET", "/api/v1/search?q=regnskab", nil)
	if tasks, _ := body["tasks"].([]any); len(tasks) != 0 {
		t.Errorf("found somebody else's task: %v", tasks)
	}
}
