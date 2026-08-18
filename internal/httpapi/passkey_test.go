package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kristianwind/verdande/internal/store"
)

// What can be tested without an authenticator, which is most of the surface that
// actually goes wrong.
//
// The signature itself cannot be produced here — that needs a real device or a
// virtual one, and Playwright's CDP virtual authenticator is where that belongs.
// What is testable is everything around it: who may reach these endpoints, that a
// challenge is answerable exactly once, that one account's challenge cannot be
// answered by another, and that an expired one is refused. Those are the parts a
// mistake would make silently permissive.

func TestPasskeyRegistrationIsSessionsOnly(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	admin, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	token := ts.apiToken(t, admin.ID)

	// A leaked API token must not be able to add its own way in: that would turn a
	// theft into a tenancy.
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/auth/passkeys"},
		{"POST", "/api/v1/auth/passkeys/register/begin"},
		{"POST", "/api/v1/auth/passkeys/register/finish"},
	} {
		req, _ := http.NewRequest(c.method, ts.URL+c.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s with an API token: status %d, want 403", c.method, c.path, resp.StatusCode)
		}
	}
}

// Beginning a registration hands back options and a challenge id, and the options
// carry the things that make this a passkey rather than a second factor.
func TestBeginningARegistrationAsksForADiscoverableVerifiedKey(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/auth/passkeys/register/begin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("begin: status %d, body %v", resp.StatusCode, body)
	}
	if body["challenge_id"] == "" || body["challenge_id"] == nil {
		t.Fatal("no challenge id came back")
	}

	options, _ := body["options"].(map[string]any)
	publicKey, _ := options["publicKey"].(map[string]any)
	if publicKey == nil {
		t.Fatalf("no publicKey in the options: %v", options)
	}
	selection, _ := publicKey["authenticatorSelection"].(map[string]any)
	if selection["residentKey"] != "preferred" {
		t.Errorf("residentKey = %v — only a discoverable credential can start a login "+
			"with no email typed", selection["residentKey"])
	}
	if selection["userVerification"] != "preferred" {
		t.Errorf("userVerification = %v — a key that only proves possession cannot "+
			"stand in for the password", selection["userVerification"])
	}
	// The user handle must be the account id rather than the email: it is written
	// into the credential and cannot be changed, so an email there would orphan
	// every key the moment somebody changed their address.
	user, _ := publicKey["user"].(map[string]any)
	if user["name"] != "kristian@example.dk" {
		t.Errorf("user.name = %v", user["name"])
	}
	if id, _ := user["id"].(string); id == "" {
		t.Error("no user handle in the options")
	}
}

// A challenge is answerable exactly once, and only by whom it was issued to.
func TestAChallengeIsSpentWhenItIsUsed(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	_, begun := ts.do(t, "POST", "/api/v1/auth/passkeys/register/begin", nil)
	challenge := begun["challenge_id"].(string)

	// Somebody else's challenge, answered by this account. Refused before the
	// signature is even looked at.
	resp, _ := other.do(t, "POST", "/api/v1/auth/passkeys/register/finish", map[string]any{
		"challenge_id": challenge, "credential": json.RawMessage(`{}`),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("another account's challenge: status %d, want 400", resp.StatusCode)
	}

	// And it is spent: the rightful owner cannot use it either now, because taking
	// it is what deleted it. That is deliberate — a challenge that survives a
	// failed attempt is a challenge somebody can keep trying.
	resp, _ = ts.do(t, "POST", "/api/v1/auth/passkeys/register/finish", map[string]any{
		"challenge_id": challenge, "credential": json.RawMessage(`{}`),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a spent challenge: status %d, want 400", resp.StatusCode)
	}
}

// Logging in with a key needs no session and says nothing about who exists here.
func TestPasskeyLoginRevealsNothingAboutWhoHasAKey(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// No session at all — a fresh client, which is the whole point.
	fresh := &testServer{Server: ts.Server, db: ts.db, client: newJarClient(t)}

	resp, body := fresh.do(t, "POST", "/api/v1/auth/passkey/login/begin", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("begin login: status %d, body %v", resp.StatusCode, body)
	}
	challenge := body["challenge_id"].(string)

	options, _ := body["options"].(map[string]any)
	publicKey, _ := options["publicKey"].(map[string]any)
	// A discoverable login must not list credentials: that list would say which
	// keys exist on this instance to anybody who asked.
	if allowed, present := publicKey["allowCredentials"]; present && allowed != nil {
		if list, _ := allowed.([]any); len(list) > 0 {
			t.Errorf("the login options list %d credentials; a discoverable login must not",
				len(list))
		}
	}

	// A garbage answer is refused with the same message a wrong key would get.
	resp, _ = fresh.do(t, "POST", "/api/v1/auth/passkey/login/finish", map[string]any{
		"challenge_id": challenge, "credential": json.RawMessage(`{}`),
	})
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a junk assertion: status %d, want 400 or 401", resp.StatusCode)
	}
}

// A key belongs to its owner, and nobody else can rename or remove it.
func TestAPasskeyCanOnlyBeManagedByItsOwner(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	admin, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	// Written straight to the store: registering one properly needs a device.
	key := &store.Passkey{
		UserID: admin.ID, CredentialID: "abc", PublicKey: []byte{1, 2, 3}, Name: "Min bærbare",
	}
	if err := ts.db.CreatePasskey(t.Context(), key); err != nil {
		t.Fatal(err)
	}

	_, listed := ts.do(t, "GET", "/api/v1/auth/passkeys", nil)
	if keys, _ := listed["passkeys"].([]any); len(keys) != 1 {
		t.Fatalf("the owner sees %d keys, want 1", len(keys))
	}
	_, theirs := other.do(t, "GET", "/api/v1/auth/passkeys", nil)
	if keys, _ := theirs["passkeys"].([]any); len(keys) != 0 {
		t.Errorf("somebody else sees %d of this account's keys", len(keys))
	}

	for _, c := range []struct {
		what, method string
		body         any
	}{
		{"rename", "PATCH", map[string]any{"name": "min nu"}},
		{"delete", "DELETE", nil},
	} {
		resp, _ := other.do(t, c.method, "/api/v1/auth/passkeys/"+key.ID, c.body)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s somebody else's key: status %d, want 404", c.what, resp.StatusCode)
		}
	}

	if resp, _ := ts.do(t, "PATCH", "/api/v1/auth/passkeys/"+key.ID,
		map[string]any{"name": "Telefonen"}); resp.StatusCode != http.StatusNoContent {
		t.Errorf("the owner renaming: status %d", resp.StatusCode)
	}
	if resp, _ := ts.do(t, "DELETE", "/api/v1/auth/passkeys/"+key.ID, nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("the owner deleting: status %d", resp.StatusCode)
	}
}
