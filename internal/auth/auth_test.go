package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestPasswordRoundTrip(t *testing.T) {
	const password = "kaffe & rugbrød, tak!"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := VerifyPassword(hash, password); err != nil {
		t.Errorf("verify with the right password: %v", err)
	}
	if err := VerifyPassword(hash, password+"x"); err != ErrMismatch {
		t.Errorf("verify with a wrong password: %v, want ErrMismatch", err)
	}
}

// The salt is per-hash, so the same password never produces the same stored value.
// Without that, identical hashes in a leaked table tell an attacker which accounts
// share a password.
func TestSamePasswordHashesDifferently(t *testing.T) {
	a, err := HashPassword("hemmeligt")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("hemmeligt")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two hashes of one password are identical; the salt is not random")
	}
	// Both must still verify.
	for _, h := range []string{a, b} {
		if err := VerifyPassword(h, "hemmeligt"); err != nil {
			t.Errorf("verify: %v", err)
		}
	}
}

func TestPasswordHashFormat(t *testing.T) {
	hash, err := HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash = %q; the parameters must be recorded in the string", hash)
	}
	if n := len(strings.Split(hash, "$")); n != 6 {
		t.Errorf("hash has %d $-separated fields, want 6", n)
	}
}

// A stored hash that has been truncated, edited or produced by something else must
// fail closed. The one thing it must never do is verify.
func TestMalformedHashNeverVerifies(t *testing.T) {
	good, err := HashPassword("hemmeligt")
	if err != nil {
		t.Fatal(err)
	}

	broken := []string{
		"",
		"not a hash",
		"$argon2id$v=19$m=19456,t=2,p=1$onlyfourfields",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$aGFzaA",  // wrong variant
		"$argon2id$v=13$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$aGFzaA", // wrong version
		"$argon2id$v=19$m=0,t=0,p=0$c2FsdHNhbHRzYWx0$aGFzaA",     // zero parameters
		"$argon2id$v=19$m=19456,t=2,p=1$!!!not-base64!!!$aGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0$", // empty digest
		good[:len(good)-1],                                 // digest truncated by one character
		good[:len(good)-8],
	}
	for _, h := range broken {
		t.Run(h, func(t *testing.T) {
			if err := VerifyPassword(h, "hemmeligt"); err == nil {
				t.Error("a malformed hash verified")
			}
		})
	}
}

func TestNeedsRehash(t *testing.T) {
	current, err := HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	if NeedsRehash(current) {
		t.Error("a hash made with the current parameters wants rehashing")
	}

	// A hash from when the parameters were weaker should be upgraded on next login.
	weak := encodeHash("x", []byte("0123456789abcdef"), 8*1024, 1, 1)
	if !NeedsRehash(weak) {
		t.Error("a hash with weaker parameters was not flagged for rehashing")
	}
	// It must still verify in the meantime, or the upgrade locks people out.
	if err := VerifyPassword(weak, "x"); err != nil {
		t.Errorf("an old-parameter hash no longer verifies: %v", err)
	}

	if !NeedsRehash("garbage") {
		t.Error("an unreadable hash should be replaced")
	}
}

func TestTokensAreUniqueAndOpaque(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatal("NewToken returned a duplicate")
		}
		seen[tok] = true

		if len(tok) < 40 {
			t.Fatalf("token %q is shorter than 256 bits of randomness", tok)
		}
		// URL-safe: these end up in emailed links.
		if strings.ContainsAny(tok, "+/= ") {
			t.Fatalf("token %q contains characters that need escaping in a URL", tok)
		}
	}
}

func TestHashTokenIsStableAndDistinct(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}

	if HashToken(a) != HashToken(a) {
		t.Error("HashToken is not deterministic; a stored token could never be found again")
	}
	if HashToken(a) == HashToken(b) {
		t.Error("two different tokens hash the same")
	}
	if strings.Contains(HashToken(a), a) {
		t.Error("the hash contains the token it is meant to protect")
	}
}

func TestAPITokenIsRecognisable(t *testing.T) {
	tok, prefix, err := NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if !IsAPIToken(tok) {
		t.Errorf("token %q is not recognised as an API token", tok)
	}
	if !strings.HasPrefix(tok, prefix) {
		t.Errorf("prefix %q does not match token %q", prefix, tok)
	}
	if IsAPIToken("some-session-id") {
		t.Error("a session id was mistaken for an API token")
	}
}

func TestTOTPRoundTrip(t *testing.T) {
	secret, uri, err := NewTOTPSecret("verdande", "kristian@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := DecodeTOTPSecret(secret); err != nil {
		t.Errorf("secret %q is not valid base32: %v", secret, err)
	}
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Errorf("uri = %q", uri)
	}

	now := time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)
	code := codeAt(t, secret, now)

	if err := VerifyTOTP(secret, code, now); err != nil {
		t.Errorf("the current code was rejected: %v", err)
	}
	// Apps show the code in two groups; people paste it that way.
	if err := VerifyTOTP(secret, code[:3]+" "+code[3:], now); err != nil {
		t.Errorf("a code with a space in it was rejected: %v", err)
	}
	if err := VerifyTOTP(secret, "000000", now); err == nil {
		t.Error("a wrong code was accepted")
	}
	if err := VerifyTOTP("", code, now); err != ErrNoTOTPSecret {
		t.Errorf("verifying against an empty secret: %v, want ErrNoTOTPSecret", err)
	}
}

// One step of tolerance either way covers a code entered as it rolls over and a
// phone whose clock drifts. Two steps out must fail, or a stolen code lives too long.
func TestTOTPWindow(t *testing.T) {
	secret, _, err := NewTOTPSecret("verdande", "k@example.com")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 10, 10, 0, 0, 0, time.UTC)
	code := codeAt(t, secret, now)

	for _, delta := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		if err := VerifyTOTP(secret, code, now.Add(delta)); err != nil {
			t.Errorf("code rejected at %v offset: %v", delta, err)
		}
	}
	for _, delta := range []time.Duration{-90 * time.Second, 90 * time.Second} {
		if err := VerifyTOTP(secret, code, now.Add(delta)); err == nil {
			t.Errorf("code still accepted %v away from when it was issued", delta)
		}
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, hashes, err := NewRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != len(hashes) || len(codes) == 0 {
		t.Fatalf("got %d codes and %d hashes", len(codes), len(hashes))
	}

	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Error("duplicate recovery code")
		}
		seen[c] = true
	}

	// Every code must match its own entry, and nothing else.
	for i, c := range codes {
		if got := MatchRecoveryCode(hashes, c); got != i {
			t.Errorf("code %d matched index %d", i, got)
		}
	}
	if got := MatchRecoveryCode(hashes, "AAAAA-AAAAA"); got != -1 {
		t.Errorf("an invented code matched index %d", got)
	}

	// Transcription must be forgiven: read off a printout, typed back in.
	c := codes[0]
	for _, variant := range []string{
		strings.ToLower(c),
		strings.ReplaceAll(c, "-", ""),
		"  " + c + "  ",
		strings.ToLower(strings.ReplaceAll(c, "-", " ")),
	} {
		if got := MatchRecoveryCode(hashes, variant); got != 0 {
			t.Errorf("variant %q matched index %d, want 0", variant, got)
		}
	}
}

// codeAt produces the code an authenticator app would display at that moment,
// using the same settings VerifyTOTP checks against.
func codeAt(t *testing.T, secret string, now time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, now, totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      totpSkew,
		Digits:    totpDigits,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	return code
}
