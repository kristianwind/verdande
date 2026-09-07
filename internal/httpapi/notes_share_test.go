package httpapi

import (
	"net/http"
	"testing"
)

// createNote makes a note as the current client and returns its id.
func (ts *testServer) createNote(t *testing.T, body string) string {
	t.Helper()
	resp, out := ts.do(t, "POST", "/api/v1/notes", map[string]any{"body": body})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create note: status %d", resp.StatusCode)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatal("create note returned no id")
	}
	return id
}

func userID(t *testing.T, ts *testServer, email string) string {
	t.Helper()
	u, err := ts.db.UserByEmail(t.Context(), email)
	if err != nil {
		t.Fatalf("look up %s: %v", email, err)
	}
	return u.ID
}

// A note shared with a person shows up in their list, marked as theirs-from-
// somebody, and a viewer can read it but not change it.
func TestSharedNoteReachesTheOtherPerson(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	other := owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")

	noteID := owner.createNote(t, "# Ferieplan\nuge 29")

	// Before sharing, Sofie cannot see it at all.
	if resp, _ := other.do(t, "GET", "/api/v1/notes/"+noteID, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("an unshared note was reachable: %d", resp.StatusCode)
	}

	// Share it as a viewer.
	resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": sofie, "role": "viewer"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share: status %d", resp.StatusCode)
	}

	// Now it is in Sofie's list, flagged as shared with her and carrying its owner.
	_, list := other.do(t, "GET", "/api/v1/notes", nil)
	notes, _ := list["notes"].([]any)
	var found map[string]any
	for _, raw := range notes {
		n := raw.(map[string]any)
		if n["id"] == noteID {
			found = n
		}
	}
	if found == nil {
		t.Fatal("the shared note is not in the recipient's list")
	}
	if found["shared_with_me"] != true {
		t.Errorf("the note is not marked shared_with_me: %v", found)
	}
	ownerObj, _ := found["owner"].(map[string]any)
	if ownerObj == nil || ownerObj["name"] != "Kristian" {
		t.Errorf("the owner is not carried on the shared note: %v", found["owner"])
	}

	// A viewer reads it.
	if resp, _ := other.do(t, "GET", "/api/v1/notes/"+noteID, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("a viewer could not read the shared note: %d", resp.StatusCode)
	}
	// But cannot change it.
	if resp, _ := other.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"body": "hijacked"}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a viewer changed a shared note: %d", resp.StatusCode)
	}
}

// An editor may change the text, but only the owner may delete the note or hand it
// to somebody else.
func TestSharedEditorCanWriteButNotGiveAway(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	other := owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")

	noteID := owner.createNote(t, "# Delt\noprindelig")
	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": sofie, "role": "editor"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("share as editor failed")
	}

	// The editor writes.
	if resp, _ := other.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"body": "# Delt\nrettet"}); resp.StatusCode != http.StatusOK {
		t.Errorf("an editor could not change a shared note: %d", resp.StatusCode)
	}

	// The editor cannot re-share it: giving access is the owner's alone.
	if resp, _ := other.do(t, "GET", "/api/v1/notes/"+noteID+"/shares", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an editor could read the share list: %d", resp.StatusCode)
	}
	// The editor cannot delete it.
	if resp, _ := other.do(t, "DELETE", "/api/v1/notes/"+noteID, nil); resp.StatusCode == http.StatusNoContent {
		t.Error("an editor deleted the owner's note")
	}
	// The note still exists for the owner.
	if resp, _ := owner.do(t, "GET", "/api/v1/notes/"+noteID, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("the note is gone after the editor's delete attempt: %d", resp.StatusCode)
	}
}

// Unsharing takes access away again, and only the owner can do it.
func TestUnshareRemovesAccess(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	other := owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")

	noteID := owner.createNote(t, "# Hemmelig")
	owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": sofie, "role": "viewer"})

	// A non-owner cannot unshare.
	if resp, _ := other.do(t, "DELETE", "/api/v1/notes/"+noteID+"/shares/"+sofie, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a non-owner unshared a note: %d", resp.StatusCode)
	}

	// The owner can.
	if resp, _ := owner.do(t, "DELETE", "/api/v1/notes/"+noteID+"/shares/"+sofie, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("owner unshare: %d", resp.StatusCode)
	}
	// And access is gone.
	if resp, _ := other.do(t, "GET", "/api/v1/notes/"+noteID, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("the note was still reachable after unsharing: %d", resp.StatusCode)
	}
}

// The share list is owner-only and offers the instance's other people as
// candidates, minus those already on the note.
func TestShareListShowsCandidatesAndIsOwnerOnly(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	owner.newUser(t, "sofie@example.dk", "Sofie")
	owner.newUser(t, "anders@example.dk", "Anders")
	sofie := userID(t, owner, "sofie@example.dk")

	noteID := owner.createNote(t, "# Plan")
	owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": sofie, "role": "viewer"})

	_, out := owner.do(t, "GET", "/api/v1/notes/"+noteID+"/shares", nil)
	shares, _ := out["shares"].([]any)
	if len(shares) != 1 {
		t.Fatalf("shares = %d, want 1", len(shares))
	}
	cands, _ := out["candidates"].([]any)
	// Anders is a candidate; Sofie is already on the note and so is not.
	names := map[string]bool{}
	for _, c := range cands {
		names[c.(map[string]any)["name"].(string)] = true
	}
	if !names["Anders"] {
		t.Errorf("Anders is not offered as a candidate: %v", names)
	}
	if names["Sofie"] {
		t.Error("somebody already on the note is offered as a candidate")
	}
	if names["Kristian"] {
		t.Error("the owner is offered as a candidate to share with themselves")
	}
}

// Sharing refuses a made-up person and the owner themselves.
func TestShareRejectsUnknownAndSelf(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	me := userID(t, owner, "kristian@example.dk")
	noteID := owner.createNote(t, "# Note")

	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": "does-not-exist", "role": "viewer"}); resp.StatusCode != http.StatusNotFound {
		t.Errorf("sharing with an unknown id: %d, want 404", resp.StatusCode)
	}
	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": me, "role": "viewer"}); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("sharing with oneself: %d, want 422", resp.StatusCode)
	}
	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": me, "role": "owner"}); resp.StatusCode == http.StatusOK {
		t.Error("a note was shared at the owner role")
	}
}

// De noter, en delt note peger på, følger med — og holder op igen.
//
// Det var meldt som en fejl den anden vej rundt: linkene i en delt note førte
// ingen steder hen, fordi det, de pegede på, ikke var delt. Prøven går hele vejen
// rundt om svaret på det — dybden, teksten der ændrer sig bagefter, det der ikke
// må følge med, og vejen tilbage.
func TestSharedNoteTakesItsLinksWithIt(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	other := owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")

	// Prisliste peger videre på Leveringstider, så kæden er to led lang.
	deep := owner.createNote(t, "# Leveringstider\nfire dage")
	linked := owner.createNote(t, "# Prisliste\nog [[Leveringstider]]")
	apart := owner.createNote(t, "# Bankoplysninger\nikke til deling")
	noteID := owner.createNote(t, "# Aftale om levering\nse [[Prisliste]]")

	reachable := func(id string) bool {
		resp, _ := other.do(t, "GET", "/api/v1/notes/"+id, nil)
		return resp.StatusCode == http.StatusOK
	}

	resp, out := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": sofie, "role": "viewer"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share: status %d", resp.StatusCode)
	}
	// Svaret siger, hvad der fulgte med — hele kæden, ikke kun første led. En
	// deling, der stille tog to noter mere med, ville være rigtig og alligevel ikke
	// til at overskue.
	follows, _ := out["follows"].([]any)
	if len(follows) != 2 {
		t.Fatalf("delingen nævnte %d noter, der fulgte med, want 2: %v", len(follows), out["follows"])
	}

	if !reachable(linked) {
		t.Error("noten, den delte note peger på, fulgte ikke med")
	}
	if !reachable(deep) {
		t.Error("kæden stoppede efter ét led")
	}
	if reachable(apart) {
		t.Error("en note, ingen peger på, blev delt")
	}

	// Skrevet bagefter tæller også. Et link i dag i en note delt i går skal føre
	// samme sted hen for begge.
	senere := owner.createNote(t, "# Rabatter\nti procent")
	if reachable(senere) {
		t.Fatal("en helt ny note var delt, før noget pegede på den")
	}
	if resp, _ := owner.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"body": "# Aftale om levering\nse [[Prisliste]] og [[Rabatter]]"}); resp.StatusCode != http.StatusOK {
		t.Fatal("kunne ikke skrive videre i den delte note")
	}
	if !reachable(senere) {
		t.Error("et link skrevet efter delingen fulgte ikke med")
	}

	// Og vejen tilbage: tages delingen tilbage, følger det med, den tog med sig.
	if resp, _ := owner.do(t, "DELETE", "/api/v1/notes/"+noteID+"/shares/"+sofie, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unshare: status %d", resp.StatusCode)
	}
	if reachable(linked) || reachable(deep) || reachable(senere) {
		t.Error("det, der fulgte med delingen, blev stående, da den blev taget tilbage")
	}
}

// En medredaktør kan ikke dele ejerens noter med sig selv ved at skrive et link.
//
// Det er den ene vej, "linkene følger med" kunne blive til en rettighedsfejl: et
// link peger på en titel, og enhver, der må rette i en delt note, kan skrive en
// titel. Derfor regnes delingerne kun om, når ejeren selv har gemt.
func TestAnEditorCannotShareTheOwnersNotesWithThemselves(t *testing.T) {
	owner := newTestServer(t)
	owner.bootstrap(t)
	other := owner.newUser(t, "sofie@example.dk", "Sofie")
	sofie := userID(t, owner, "sofie@example.dk")

	secret := owner.createNote(t, "# Bankoplysninger\nreg og konto")
	noteID := owner.createNote(t, "# Fælles\nintet link her")
	if resp, _ := owner.do(t, "POST", "/api/v1/notes/"+noteID+"/shares",
		map[string]string{"user_id": sofie, "role": "editor"}); resp.StatusCode != http.StatusOK {
		t.Fatal("share as editor failed")
	}

	// Hun skriver linket selv, i den note hun har fået lov at rette i.
	if resp, _ := other.do(t, "PATCH", "/api/v1/notes/"+noteID,
		map[string]any{"body": "# Fælles\nse [[Bankoplysninger]]"}); resp.StatusCode != http.StatusOK {
		t.Fatal("medredaktøren kunne ikke skrive")
	}
	if resp, _ := other.do(t, "GET", "/api/v1/notes/"+secret, nil); resp.StatusCode == http.StatusOK {
		t.Error("en medredaktør delte ejerens note med sig selv ved at skrive et link")
	}
}
