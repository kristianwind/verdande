package httpapi

import (
	"net/http"
	"testing"
)

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

// "Waiting on others" is the other half of an assignee filter, and the half that
// is easy to get backwards: what somebody has handed over, not what they have
// been handed.
func TestDelegatedListsWhatOtherPeopleAreSittingOn(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.newUser(t, "anders@example.dk", "Anders")

	anders, err := ts.db.UserByEmail(t.Context(), "anders@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	me, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	projectID := project["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites", map[string]any{
		"email": "anders@example.dk", "role": "editor",
	})

	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "anders skriver rapporten", "project_id": projectID, "assignee_id": anders.ID,
	})
	// Mine, and unassigned: neither belongs in a view about other people.
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "jeg læser den", "project_id": projectID, "assignee_id": me.ID,
	})
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "ingen har den", "project_id": projectID,
	})

	resp, body := ts.do(t, "GET", "/api/v1/delegated", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delegated: %d %v", resp.StatusCode, body)
	}

	people, _ := body["people"].([]any)
	if len(people) != 1 {
		t.Fatalf("want one person, got %v", people)
	}
	person := people[0].(map[string]any)
	if person["name"] != "Anders" {
		t.Errorf("name = %v — the view has to carry the name, or the client has no way to ask for it",
			person["name"])
	}
	if person["avatar_color"] == nil || person["avatar_color"] == "" {
		t.Errorf("no avatar colour: %v", person)
	}

	tasks, _ := person["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("want one task, got %v", tasks)
	}
	if tasks[0].(map[string]any)["content"] != "anders skriver rapporten" {
		t.Errorf("content = %v", tasks[0])
	}
}

// The view is per person: Anders is not waiting on himself, and he cannot see
// what somebody has delegated in a project he is not in.
func TestDelegatedIsPerPersonAndRespectsAccess(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	anders := ts.newUser(t, "anders@example.dk", "Anders")

	andersUser, err := ts.db.UserByEmail(t.Context(), "anders@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	// A project Anders is not a member of.
	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Privat"})
	projectID := project["id"].(string)
	ts.do(t, "POST", "/api/v1/projects/"+projectID+"/invites", map[string]any{
		"email": "anders@example.dk", "role": "editor",
	})
	ts.do(t, "POST", "/api/v1/tasks", map[string]any{
		"content": "anders gør det", "project_id": projectID, "assignee_id": andersUser.ID,
	})

	// It is assigned to him, so it is not something he is waiting on.
	_, body := anders.do(t, "GET", "/api/v1/delegated", nil)
	if people, _ := body["people"].([]any); len(people) != 0 {
		t.Errorf("Anders is waiting on himself: %v", people)
	}

	// And somebody with no access to the project sees none of it.
	other := ts.newUser(t, "tredje@example.dk", "Tredje")
	_, third := other.do(t, "GET", "/api/v1/delegated", nil)
	if people, _ := third["people"].([]any); len(people) != 0 {
		t.Errorf("a task in a project they cannot see: %v", people)
	}
}

// /people is what puts a name on an assignee id. It must be the people you share
// work with — not the instance's user list, which is the administrator's page.
func TestPeopleIsWhoYouShareWorkWith(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	colleague := ts.newUser(t, "anders@example.dk", "Anders")
	ts.newUser(t, "fremmed@example.dk", "Fremmed")

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Delt"})
	ts.do(t, "POST", "/api/v1/projects/"+project["id"].(string)+"/invites", map[string]any{
		"email": "anders@example.dk", "role": "editor",
	})

	_, body := ts.do(t, "GET", "/api/v1/people", nil)
	names := map[string]bool{}
	for _, raw := range body["people"].([]any) {
		p := raw.(map[string]any)
		names[p["name"].(string)] = true
		if p["avatar_color"] == "" {
			t.Errorf("no avatar colour for %v — the row cannot draw a face", p["name"])
		}
	}

	if !names["Kristian"] {
		t.Error("yourself is missing, so a client cannot tell 'me' from 'somebody else' against one list")
	}
	if !names["Anders"] {
		t.Error("somebody sharing a project is missing")
	}
	if names["Fremmed"] {
		t.Error("an account with no project in common is listed — this is not the user directory")
	}

	// And it is symmetric: the person invited sees the person who invited them.
	_, theirs := colleague.do(t, "GET", "/api/v1/people", nil)
	seen := map[string]bool{}
	for _, raw := range theirs["people"].([]any) {
		seen[raw.(map[string]any)["name"].(string)] = true
	}
	if !seen["Kristian"] || !seen["Anders"] {
		t.Errorf("the member cannot see who they share with: %v", seen)
	}
	if seen["Fremmed"] {
		t.Errorf("a stranger is listed: %v", seen)
	}
}
