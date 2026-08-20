package google

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

// The challenge has to be the SHA-256 of the verifier, base64url without padding.
// Getting this wrong means Google refuses every exchange with a message that does
// not say which end is at fault.
func TestPKCEChallengeMatchesItsVerifier(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(p.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if p.Challenge != want {
		t.Errorf("challenge = %q, want %q", p.Challenge, want)
	}

	// RFC 7636 requires 43–128 characters, and no padding.
	if n := len(p.Verifier); n < 43 || n > 128 {
		t.Errorf("verifier is %d characters, outside the 43–128 the spec allows", n)
	}
	if strings.ContainsAny(p.Verifier, "=+/") {
		t.Errorf("verifier %q contains characters that are not URL-safe", p.Verifier)
	}
	if p.State == "" {
		t.Error("no state was generated")
	}

	// Two attempts must not share anything.
	other, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if other.Verifier == p.Verifier || other.State == p.State {
		t.Error("two authorisation attempts produced the same secrets")
	}
}

func TestAuthURL(t *testing.T) {
	cfg := Config{
		ClientID:     "client-123",
		ClientSecret: "secret",
		RedirectURL:  "https://todo.example.dk/oauth/gmail/callback",
		Scope:        "https://www.googleapis.com/auth/gmail.readonly",
	}
	p, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}

	raw := cfg.AuthURL(p)
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("the URL does not parse: %v", err)
	}
	q := parsed.Query()

	checks := map[string]string{
		"client_id":             cfg.ClientID,
		"redirect_uri":          cfg.RedirectURL,
		"response_type":         "code",
		"scope":                 cfg.Scope,
		"code_challenge":        p.Challenge,
		"code_challenge_method": "S256",
		"state":                 p.State,
		// Without both of these Google returns no refresh token on a
		// re-authorisation, and the connection dies silently an hour later.
		"access_type": "offline",
		"prompt":      "consent",
	}
	for key, want := range checks {
		if got := q.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	// The verifier itself must never leave the server.
	if strings.Contains(raw, p.Verifier) {
		t.Error("the code verifier was put in the authorisation URL")
	}
}

// The scope is the one thing that differs between the two features signing in
// through this, so it has to come out of the config rather than out of a constant
// somebody could forget to change.
func TestTheScopeComesFromTheConfig(t *testing.T) {
	p, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	calendar := "https://www.googleapis.com/auth/calendar.readonly"
	raw := Config{ClientID: "a", Scope: calendar}.AuthURL(p)

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("scope"); got != calendar {
		t.Errorf("scope = %q, want %q — asking for the wrong one is a consent screen "+
			"that grants the wrong thing", got, calendar)
	}
}

func TestConfigured(t *testing.T) {
	full := Config{ClientID: "a", ClientSecret: "b", RedirectURL: "c"}
	if !full.Configured() {
		t.Error("a complete config is not reported as configured")
	}
	for _, partial := range []Config{
		{},
		{ClientID: "a"},
		{ClientID: "a", ClientSecret: "b"},
		{ClientSecret: "b", RedirectURL: "c"},
	} {
		if partial.Configured() {
			t.Errorf("%+v was reported as configured", partial)
		}
	}
}
