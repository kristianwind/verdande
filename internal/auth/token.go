package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// tokenBytes is 32 bytes — 256 bits — of randomness. These tokens sit in emailed
// links and in scripts' config files, where they may live for a long time and be
// guessed at without rate limiting, so they are sized to be unguessable rather than
// convenient.
const tokenBytes = 32

// NewToken returns a URL-safe random token. This is the only time the plaintext
// exists: store HashToken(t), put t in the link, and never write it down anywhere
// else. A database dump then contains nothing an attacker can present.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken is the lookup key for a token.
//
// SHA-256 with no salt, deliberately — unlike a password, this has to be *found* by
// its value in a WHERE clause, which a per-row salt makes impossible. That is safe
// here only because the input is 256 bits of uniform randomness: there is no
// dictionary to run against it and no work factor worth adding.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// apiTokenPrefix marks verdande's own tokens in logs and config files, and lets
// secret scanners recognise one that has been committed by accident.
const apiTokenPrefix = "vrd_"

// NewAPIToken returns a personal API token and the short prefix shown in the UI so
// somebody can tell two of their tokens apart without being shown either in full.
func NewAPIToken() (token, prefix string, err error) {
	raw, err := NewToken()
	if err != nil {
		return "", "", err
	}
	token = apiTokenPrefix + raw
	return token, token[:len(apiTokenPrefix)+6], nil
}

// IsAPIToken reports whether a credential looks like one of verdande's API tokens
// rather than a session id, so the two are never checked against the wrong table.
func IsAPIToken(s string) bool { return strings.HasPrefix(s, apiTokenPrefix) }
