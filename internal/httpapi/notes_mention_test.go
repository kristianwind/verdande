package httpapi

import (
	"net/http"
	"testing"
)

// At nævne nogen i en note er at ville have dem til at læse den: omtalen deler
// noten, og den, der bliver nævnt, får besked om det.
func TestMentioningSomebodySharesTheNote(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	other := owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")

	noteID := owner.createNote(t, "# Aftale om levering\nikke noget endnu")
	if resp, _ := other.do(t, "GET", "/api/v1/notes/"+noteID, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("noten var læselig før omtalen: %d", resp.StatusCode)
	}

	if resp, _ := owner.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"body": "# Aftale om levering\n@Sofie kan du kigge på det?"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("ret noten: %d", resp.StatusCode)
	}

	// Delt, som læser.
	role, has, err := owner.db.NoteShareRole(t.Context(), noteID, sofie)
	if err != nil || !has || role != "viewer" {
		t.Fatalf("deling = %v (findes: %v, fejl: %v), want viewer", role, has, err)
	}
	if resp, _ := other.do(t, "GET", "/api/v1/notes/"+noteID, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("den omtalte kan ikke læse noten: %d", resp.StatusCode)
	}

	// Og sagt til — med omtalen som beskeden, ikke "rettede en note". Det er én
	// handling, og den ene sætning siger mere end den anden.
	list, unread := notifications(t, other)
	if len(list) != 1 || unread != 1 {
		t.Fatalf("Sofie fik %d beskeder (%v ulæste), want 1", len(list), unread)
	}
	if n := firstOf(t, list); n["kind"] != "note.mention" || n["note_id"] != noteID {
		t.Errorf("besked = %v", n)
	}
}

// En gemning mere med det samme navn i er ikke en omtale mere. En note gemmes ved
// hver tastepause, så uden det bliver fem minutters skriveri til tyve beskeder.
func TestTheSameMentionOnlyCountsOnce(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	other := owner.newUser(t, "sofie@example.dk", "Sofie")

	noteID := owner.createNote(t, "# Aftale\n@Sofie kigger")
	// Omtalen stod i noten fra begyndelsen: den tæller ved oprettelsen.
	if list, _ := notifications(t, other); len(list) != 1 {
		t.Fatalf("en omtale i en ny note gav %d beskeder, want 1", len(list))
	}

	for i := 0; i < 3; i++ {
		if resp, _ := owner.do(t, "PATCH", "/api/v1/notes/"+noteID,
			map[string]any{"body": "# Aftale\n@Sofie kigger på linje " + string(rune('a'+i))}); resp.StatusCode != http.StatusOK {
			t.Fatal("ret noten")
		}
	}
	// Tre gemninger mere: én besked om rettelserne, slået sammen, og ikke tre
	// omtaler mere.
	list, _ := notifications(t, other)
	if len(list) != 2 {
		t.Fatalf("tre gemninger med det samme navn gav %d beskeder, want 2", len(list))
	}
	if firstOf(t, list)["kind"] != "note.changed" {
		t.Errorf("den nyeste besked = %v, want note.changed", firstOf(t, list))
	}
}

// En medredaktør må skrive i noten, men ikke give den videre. Ellers kunne den,
// ejeren har delt med, skrive et navn og dele ejerens note med en fremmed.
func TestAnEditorsMentionDoesNotShareTheNote(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	editor := owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")
	peterClient := owner.newUser(t, "peter@example.dk", "Peter")
	peter := userID(t, owner, "peter@example.dk")

	noteID := owner.createNote(t, "# Aftale\nintet")
	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": sofie, "role": "editor"}); resp.StatusCode != http.StatusOK {
		t.Fatal("del noten")
	}

	if resp, _ := editor.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"body": "# Aftale\n@Peter ved det"}); resp.StatusCode != http.StatusOK {
		t.Fatal("medredaktøren retter")
	}

	if _, has, err := owner.db.NoteShareRole(t.Context(), noteID, peter); err != nil || has {
		t.Errorf("en medredaktørs omtale delte ejerens note (findes: %v, fejl: %v)", has, err)
	}
	// Og Peter får ikke besked om en note, han ikke kan åbne.
	if list, _ := notifications(t, peterClient); len(list) != 0 {
		t.Errorf("Peter fik %d beskeder om en note, han ikke kan se", len(list))
	}
	// Ejeren hører derimod, at der er skrevet i noten — det er stadig en rettelse.
	if list, _ := notifications(t, owner); len(list) == 0 {
		t.Error("ejeren hørte ikke, at medredaktøren skrev i noten")
	}
}

// Rollen står. En omtale skal give adgang til den, der ikke har nogen — ikke
// sætte en medredaktør ned til læser, fordi ejeren skrev deres navn.
func TestAMentionDoesNotLowerAnExistingRole(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")

	noteID := owner.createNote(t, "# Aftale\nintet")
	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": sofie, "role": "editor"}); resp.StatusCode != http.StatusOK {
		t.Fatal("del noten")
	}
	if resp, _ := owner.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"body": "# Aftale\n@Sofie retter selv"}); resp.StatusCode != http.StatusOK {
		t.Fatal("ret noten")
	}

	role, _, err := owner.db.NoteShareRole(t.Context(), noteID, sofie)
	if err != nil || role != "editor" {
		t.Errorf("rolle = %v (%v), want editor uændret", role, err)
	}
}
