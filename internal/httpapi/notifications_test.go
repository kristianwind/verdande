package httpapi

import (
	"net/http"
	"testing"
)

// notifications reads somebody's bell.
func notifications(t *testing.T, ts *testServer) ([]any, float64) {
	t.Helper()
	resp, out := ts.do(t, "GET", "/api/v1/notifications", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list notifications: status %d", resp.StatusCode)
	}
	list, _ := out["notifications"].([]any)
	unread, _ := out["unread"].(float64)
	return list, unread
}

func firstOf(t *testing.T, list []any) map[string]any {
	t.Helper()
	if len(list) == 0 {
		t.Fatal("ingen beskeder")
	}
	n, _ := list[0].(map[string]any)
	return n
}

// At blive tildelt en opgave giver besked — og kun ved selve skiftet.
func TestBeingGivenATaskNotifiesTheAssignee(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	other := owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")

	_, project := owner.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Fælles"})
	projectID, _ := project["id"].(string)
	// Medlemskab kommer gennem en invitation; en konto, der allerede findes, bliver
	// medlem med det samme.
	if resp, _ := owner.do(t, "POST", "/api/v1/projects/"+projectID+"/invites",
		map[string]any{"email": "sofie@example.dk", "role": "editor"}); resp.StatusCode >= 300 {
		t.Fatalf("invitér: %d", resp.StatusCode)
	}

	resp, task := owner.do(t, "POST", "/api/v1/tasks",
		map[string]any{"project_id": projectID, "content": "ring til revisor"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("opret opgave: %d", resp.StatusCode)
	}
	taskID, _ := task["id"].(string)

	// Hvad hun havde, før nogen tildelte hende noget. Ikke nødvendigvis nul —
	// invitationen kan selv have sagt noget — og prøven handler om forskellen.
	before, _ := notifications(t, other)

	if resp, _ := owner.do(t, "PATCH", "/api/v1/tasks/"+taskID,
		map[string]any{"assignee_id": sofie}); resp.StatusCode != http.StatusOK {
		t.Fatalf("tildel: %d", resp.StatusCode)
	}

	list, _ := notifications(t, other)
	if len(list) != len(before)+1 {
		t.Fatalf("Sofie fik %d beskeder, want %d", len(list), len(before)+1)
	}
	n := firstOf(t, list)
	if n["kind"] != "assigned" {
		t.Errorf("kind = %v, want assigned", n["kind"])
	}
	if n["task_id"] != taskID {
		t.Errorf("beskeden pegede ikke på opgaven: %v", n["task_id"])
	}
	if n["actor_name"] != "Kristian" {
		t.Errorf("actor_name = %v", n["actor_name"])
	}

	// En rettelse af noget andet, mens den samme tildeling står i forespørgslen,
	// er ikke en ny besked. Det er dét, en fladet, der sender hele opgaven ved hver
	// ændring, gør — og så bliver klokken ubrugelig på en dag.
	if resp, _ := owner.do(t, "PATCH", "/api/v1/tasks/"+taskID,
		map[string]any{"assignee_id": sofie, "content": "ring til revisoren"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("ret: %d", resp.StatusCode)
	}
	if now, _ := notifications(t, other); len(now) != len(before)+1 {
		t.Errorf("en uændret tildeling gav besked igen: %d beskeder", len(now))
	}

	// Og den, der tildeler sig selv, får ingenting.
	me := userID(t, owner, "kristian@example.dk")
	if resp, _ := owner.do(t, "PATCH", "/api/v1/tasks/"+taskID,
		map[string]any{"assignee_id": me}); resp.StatusCode != http.StatusOK {
		t.Fatalf("tildel til sig selv: %d", resp.StatusCode)
	}
	if list, _ := notifications(t, owner); len(list) != 0 {
		t.Errorf("man fik besked om noget, man selv gjorde: %d beskeder", len(list))
	}
}

// En rettelse i en delt note giver besked — og gentagne rettelser bliver til én,
// så længe den ikke er læst.
func TestEditingASharedNoteNotifiesTheOthers(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	other := owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")

	noteID := owner.createNote(t, "# Aftale om levering\nfirst")
	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": sofie, "role": "editor"}); resp.StatusCode != http.StatusOK {
		t.Fatal("del noten")
	}

	// Selve delingen er den første besked: Sofie skal vide, at noten er der, før
	// nogen retter i den. Den tælles med i alt, hvad der følger.
	if list, unread := notifications(t, other); len(list) != 1 || unread != 1 ||
		firstOf(t, list)["kind"] != "note.shared" {
		t.Fatalf("delingen gav %d beskeder (%v ulæste): %v", len(list), unread, list)
	}

	// Ejeren skriver. Sofie skal høre det.
	if resp, _ := owner.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"body": "# Aftale om levering\nanden linje"}); resp.StatusCode != http.StatusOK {
		t.Fatal("ret noten")
	}
	list, unread := notifications(t, other)
	if unread != 2 || len(list) != 2 {
		t.Fatalf("Sofie fik %d beskeder (%v ulæste), want 2 — delingen og rettelsen",
			len(list), unread)
	}
	n := firstOf(t, list)
	if n["kind"] != "note.changed" || n["note_id"] != noteID {
		t.Errorf("besked = %v", n)
	}

	// En gemning mere, mens den er ulæst, må ikke blive til en linje mere. En note
	// gemmes ved hver tastepause, så uden det her er fem minutters skriveri tyve
	// ens linjer i klokken.
	for i := 0; i < 3; i++ {
		if resp, _ := owner.do(t, "PATCH", "/api/v1/notes/"+noteID,
			map[string]any{"body": "# Aftale om levering\nlinje nummer " + string(rune('a'+i))}); resp.StatusCode != http.StatusOK {
			t.Fatal("ret noten igen")
		}
	}
	if list, unread := notifications(t, other); len(list) != 2 || unread != 2 {
		t.Errorf("tre gemninger blev til %d beskeder, want 2 — delingen og én rettelse",
			len(list))
	}

	// Læst, og så en rettelse mere: dét er en ny ting at få at vide.
	if resp, _ := other.do(t, "POST", "/api/v1/notifications/read", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatal("markér som læst")
	}
	if resp, _ := owner.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"body": "# Aftale om levering\nefter læsningen"}); resp.StatusCode != http.StatusOK {
		t.Fatal("ret noten efter læsningen")
	}
	if list, unread := notifications(t, other); len(list) != 3 || unread != 1 {
		t.Errorf("efter læsningen gav en rettelse %d beskeder (%v ulæste), want 3 og 1", len(list), unread)
	}

	// Ejeren har ikke fået besked om sine egne rettelser undervejs.
	if list, _ := notifications(t, owner); len(list) != 0 {
		t.Errorf("ejeren fik %d beskeder om sine egne rettelser", len(list))
	}

	// Og den anden vej: Sofie skriver, ejeren hører det.
	if resp, _ := other.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"body": "# Aftale om levering\nfra Sofie"}); resp.StatusCode != http.StatusOK {
		t.Fatal("Sofie retter")
	}
	list, _ = notifications(t, owner)
	if len(list) != 1 {
		t.Fatalf("ejeren fik %d beskeder, da noten blev rettet af en anden, want 1", len(list))
	}
	if firstOf(t, list)["actor_name"] != "Sofie" {
		t.Errorf("beskeden nævnte ikke hvem: %v", list[0])
	}

	// En ændring, der ikke er tekst, er ikke en rettelse at give besked om.
	if resp, _ := owner.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"pinned": true}); resp.StatusCode != http.StatusOK {
		t.Fatal("stjernemarkér")
	}
	if list, _ := notifications(t, other); len(list) != 3 {
		t.Errorf("en stjernemarkering gav besked: %d beskeder, want 3", len(list))
	}
}

// En note, ingen deler, er ingen at give besked.
func TestEditingAPrivateNoteNotifiesNobody(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	other := owner.newUser(t, "sofie@example.dk", "Sofie")

	noteID := owner.createNote(t, "# Min egen\nintet")
	if resp, _ := owner.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"body": "# Min egen\nnoget"}); resp.StatusCode != http.StatusOK {
		t.Fatal("ret noten")
	}
	if list, _ := notifications(t, other); len(list) != 0 {
		t.Errorf("Sofie hørte om en note, hun ikke kan se: %d beskeder", len(list))
	}
	if list, _ := notifications(t, owner); len(list) != 0 {
		t.Errorf("ejeren fik besked om sin egen note: %d beskeder", len(list))
	}
}
