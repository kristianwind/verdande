package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// Hele vejen: ejeren deler en note med en adresse, der ikke har nogen konto, og
// den, der tager imod invitationen, har noten fra det øjeblik, kontoen findes.
func TestANoteCanBeSharedWithSomebodyWhoHasNoAccount(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	noteID := owner.createNote(t, "# Aftale om levering\nfredag kl. 9")

	resp, body := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"email": "sofie@example.dk", "role": "editor"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite by email: status %d, body %v", resp.StatusCode, body)
	}
	invited, _ := body["invited"].(map[string]any)
	if invited == nil || invited["email"] != "sofie@example.dk" {
		t.Fatalf("the response does not say who was invited: %v", body)
	}
	if invited["role"] != "editor" {
		t.Errorf("role = %v, want the one the owner chose", invited["role"])
	}
	// Uden en postserver er linket det eneste, der findes, og det skal stå i
	// svaret frem for at blive lovet i en mail, der ikke kom af sted.
	if body["emailed"] != false {
		t.Errorf("emailed = %v, want false with no mail server configured", body["emailed"])
	}
	link, _ := body["link"].(string)
	if !strings.Contains(link, "token=") {
		t.Fatalf("link = %q, want one carrying a token", link)
	}

	// Den venter i panelet under noten, så ejeren kan se, at den er sendt.
	_, panel := owner.do(t, "GET", "/api/v1/notes/"+noteID+"/shares", nil)
	pending, _ := panel["invites"].([]any)
	if len(pending) != 1 {
		t.Fatalf("want one pending invite under the note, got %v", pending)
	}

	// Og linket laver kontoen og delingen i ét.
	token := link[strings.Index(link, "token=")+len("token="):]
	fresh := newSignedOut(t, owner)
	resp, _ = fresh.do(t, "POST", "/api/v1/auth/signup", map[string]any{
		"token": token, "name": "Sofie", "password": "et langt kodeord",
	})
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("signup with the note invite: status %d", resp.StatusCode)
	}

	// Noten er hendes at læse — og som medredaktør også at rette.
	resp, note := fresh.do(t, "GET", "/api/v1/notes/"+noteID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the invited person cannot read the note: status %d", resp.StatusCode)
	}
	if note["title"] != "Aftale om levering" {
		t.Errorf("the invited person got something else: %v", note)
	}
	resp, _ = fresh.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]string{"body": "# Aftale om levering\nfredag kl. 10"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("an invited editor cannot write: status %d", resp.StatusCode)
	}

	// Og invitationen er brugt op: den står ikke længere og venter.
	_, after := owner.do(t, "GET", "/api/v1/notes/"+noteID+"/shares", nil)
	if left, _ := after["invites"].([]any); len(left) != 0 {
		t.Errorf("the invite is still pending after it was accepted: %v", left)
	}
	shares, _ := after["shares"].([]any)
	if len(shares) != 1 {
		t.Fatalf("want the accepted invite to be a share, got %v", shares)
	}
}

// En adresse, der viser sig at høre til en konto, er den konto — ikke en
// invitation til at oprette en, som personen aldrig kan komme igennem.
func TestSharingByEmailFindsAnExistingAccount(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")
	noteID := owner.createNote(t, "# Prisliste\n2026")

	resp, body := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"email": "Sofie@Example.dk"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share with an existing address: status %d, body %v", resp.StatusCode, body)
	}
	person, _ := body["user"].(map[string]any)
	if person == nil || person["id"] != sofie {
		t.Fatalf("the response does not say who it reached: %v", body)
	}
	if _, invited := body["invited"]; invited {
		t.Error("an address with an account was invited rather than shared with")
	}

	role, _, err := owner.db.NoteShareRole(t.Context(), noteID, sofie)
	if err != nil || role != "viewer" {
		t.Fatalf("share role = %v (%v), want viewer by default", role, err)
	}
}

// To links til den samme indbakke er ikke en invitation mere: det er to, der gør
// det samme, og kun det ene kan trækkes tilbage ad gangen.
func TestASecondInviteToTheSameNoteIsRefused(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	noteID := owner.createNote(t, "# Bankoplysninger\nkonto")

	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"email": "ny@example.dk"}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first invite: status %d", resp.StatusCode)
	}
	resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"email": "ny@example.dk"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second invite: status %d, want 409", resp.StatusCode)
	}

	// Trukket tilbage kan den derimod, og så kan der sendes en ny.
	_, panel := owner.do(t, "GET", "/api/v1/notes/"+noteID+"/shares", nil)
	pending, _ := panel["invites"].([]any)
	id := pending[0].(map[string]any)["id"].(string)
	if resp, _ := owner.do(t, "DELETE", "/api/v1/notes/"+noteID+"/invites/"+id, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("withdraw: status %d", resp.StatusCode)
	}
	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"email": "ny@example.dk"}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite again after withdrawing: status %d", resp.StatusCode)
	}
}

// Kun ejeren. En medlæser, der kunne invitere, ville kunne give noten videre til
// nogen, ejeren aldrig har hørt om — og til en konto, der ikke fandtes før.
func TestOnlyTheOwnerCanInviteToANote(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	other := owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")
	noteID := owner.createNote(t, "# Ferieplan\nuge 29")

	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": sofie, "role": "editor"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("share: status %d", resp.StatusCode)
	}

	resp, _ := other.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"email": "fremmed@example.dk"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an editor could invite somebody: status %d", resp.StatusCode)
	}
	if n, err := owner.db.ListNoteInvites(t.Context(), noteID); err != nil || len(n) != 0 {
		t.Fatalf("invites = %v (%v), want none", n, err)
	}
}

// En invitation til én note kan ikke bruges til at rydde en anden notes
// invitationer af vejen: note-id'et er en del af WHERE, ikke et tjek udenom.
func TestANoteInviteCannotBeWithdrawnThroughAnotherNote(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	mine := owner.createNote(t, "# Min note\n1")
	other := owner.createNote(t, "# Min anden note\n2")

	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+mine+"/shares",
		map[string]string{"email": "ny@example.dk"}); resp.StatusCode != http.StatusCreated {
		t.Fatal("invite failed")
	}
	invites, err := owner.db.ListNoteInvites(t.Context(), mine)
	if err != nil || len(invites) != 1 {
		t.Fatalf("invites = %v (%v)", invites, err)
	}

	resp, _ := owner.do(t, "DELETE", "/api/v1/notes/"+other+"/invites/"+invites[0].ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404 for another note's invite", resp.StatusCode)
	}
	if left, _ := owner.db.ListNoteInvites(t.Context(), mine); len(left) != 1 {
		t.Error("the invite was withdrawn through a note it does not belong to")
	}
}
