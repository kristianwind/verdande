package gmail

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
		"scope":                 Scope,
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
	// Read-only, and only mail.
	if !strings.HasSuffix(Scope, "gmail.readonly") {
		t.Errorf("scope = %q; verdande never needs to send or modify", Scope)
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

func TestQuery(t *testing.T) {
	cases := []struct {
		trigger, label string
		wantContains   []string
		wantEmpty      bool
	}{
		{"starred", "", []string{"is:starred", "newer_than:30d"}, false},
		{"label", "Til handling", []string{`label:"Til handling"`, "newer_than:30d"}, false},
		{"both", "Til handling", []string{"is:starred", `label:"Til handling"`}, false},
		{"both", "", []string{"is:starred"}, false},
		// A label trigger with no label selects nothing, which is right: it would
		// otherwise match the whole mailbox.
		{"label", "", nil, true},
		{"", "", nil, true},
		{"nonsense", "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.trigger+"/"+tc.label, func(t *testing.T) {
			got := Query(tc.trigger, tc.label)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("Query = %q, want empty", got)
				}
				return
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("Query = %q, missing %q", got, want)
				}
			}
			// Without a window, connecting an account with a decade of starred
			// mail would create a decade of tasks in one sweep.
			if !strings.Contains(got, "newer_than:") {
				t.Errorf("Query = %q has no time window", got)
			}
		})
	}
}

func TestSenderName(t *testing.T) {
	cases := map[string]string{
		"Anders Jensen <anders@example.dk>":    "Anders Jensen",
		`"Jensen, Anders" <anders@example.dk>`: "Jensen, Anders",
		"anders@example.dk":                    "anders@example.dk",
		"<anders@example.dk>":                  "anders@example.dk",
		"  Anders <a@b.dk>  ":                  "Anders",
	}
	for in, want := range cases {
		if got := SenderName(in); got != want {
			t.Errorf("SenderName(%q) = %q, want %q", in, got, want)
		}
	}
}
