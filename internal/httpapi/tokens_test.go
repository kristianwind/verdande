package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestAPITokenLifecycle(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "GET", "/api/v1/tokens", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status %d, body %v", resp.StatusCode, body)
	}
	if tokens, _ := body["tokens"].([]any); len(tokens) != 0 {
		t.Errorf("a fresh account already has tokens: %v", tokens)
	}

	resp, created := ts.do(t, "POST", "/api/v1/tokens", map[string]any{
		"name": "min server", "expires_in_days": 30,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status %d, body %v", resp.StatusCode, created)
	}

	plaintext, _ := created["token"].(string)
	if !strings.HasPrefix(plaintext, "vrd_") {
		t.Fatalf("token %q does not carry the scanner-visible prefix", plaintext)
	}
	if created["expires_at"] == nil {
		t.Error("an expiry was asked for and none came back")
	}
	prefix, _ := created["prefix"].(string)
	if !strings.HasPrefix(plaintext, prefix) {
		t.Errorf("prefix %q is not the start of the token", prefix)
	}

	// The token authenticates as its owner.
	req, err := http.NewRequest("GET", ts.URL+"/api/v1/auth/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+plaintext)
	me, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("bearer request: %v", err)
	}
	defer me.Body.Close()
	if me.StatusCode != http.StatusOK {
		t.Errorf("the new token does not authenticate: status %d", me.StatusCode)
	}

	// Listing shows it, without the secret.
	_, body = ts.do(t, "GET", "/api/v1/tokens", nil)
	tokens, _ := body["tokens"].([]any)
	if len(tokens) != 1 {
		t.Fatalf("want one token listed, got %v", tokens)
	}
	listed := tokens[0].(map[string]any)
	if _, leaked := listed["token"]; leaked {
		t.Error("the listing carries the token itself")
	}
	if listed["name"] != "min server" {
		t.Errorf("name = %v", listed["name"])
	}

	id, _ := created["id"].(string)
	resp, _ = ts.do(t, "DELETE", "/api/v1/tokens/"+id, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status %d", resp.StatusCode)
	}

	// And is dead the moment it is deleted.
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+plaintext)
	me, err = (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("bearer request: %v", err)
	}
	defer me.Body.Close()
	if me.StatusCode != http.StatusUnauthorized {
		t.Errorf("a deleted token still works: status %d", me.StatusCode)
	}
}

// A token must not be able to mint another one. Otherwise a leaked token is
// permanent: revoking it leaves behind the one it issued.
func TestAPITokenCannotMintAnother(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, created := ts.do(t, "POST", "/api/v1/tokens", map[string]any{"name": "første"})
	plaintext, _ := created["token"].(string)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/tokens"},
		{"POST", "/api/v1/tokens"},
		{"DELETE", "/api/v1/tokens/" + created["id"].(string)},
	} {
		req, err := http.NewRequest(tc.method, ts.URL+tc.path, strings.NewReader(`{"name":"anden"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+plaintext)
		req.Header.Set("Content-Type", "application/json")

		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s with a bearer token: status %d, want 403",
				tc.method, tc.path, resp.StatusCode)
		}
	}
}

func TestAPITokenValidation(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	for _, tc := range []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"no name", map[string]any{"name": "  "}, "name"},
		{"negative lifetime", map[string]any{"name": "x", "expires_in_days": -1}, "expires_in_days"},
		{"absurd lifetime", map[string]any{"name": "x", "expires_in_days": 4000}, "expires_in_days"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := ts.do(t, "POST", "/api/v1/tokens", tc.body)
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, want 422", resp.StatusCode)
			}
			fields, _ := body["fields"].(map[string]any)
			if fields[tc.field] == nil {
				t.Errorf("no error on %q: %v", tc.field, body)
			}
		})
	}
}

// Somebody else's token id must delete nothing, and must not confirm it exists.
func TestAPITokenDeleteIsScopedToOwner(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, created := ts.do(t, "POST", "/api/v1/tokens", map[string]any{"name": "min"})
	id := created["id"].(string)

	other := ts.newUser(t, "anden@example.dk", "Anden")
	resp, _ := other.do(t, "DELETE", "/api/v1/tokens/"+id, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404 for a token belonging to somebody else", resp.StatusCode)
	}

	// Still there for its owner.
	_, body := ts.do(t, "GET", "/api/v1/tokens", nil)
	if tokens, _ := body["tokens"].([]any); len(tokens) != 1 {
		t.Errorf("the owner's token was affected: %v", tokens)
	}
}
