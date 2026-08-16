// Package auth holds password hashing, opaque tokens and TOTP — the parts of
// verdande's own user system where getting the details wrong is not recoverable
// by fixing them later.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters, at OWASP's current recommendation for interactive logins:
// 19 MiB of memory, two passes, no parallelism.
//
// Memory is the knob that matters — it is what makes a GPU attack expensive — but
// it is also charged per concurrent login, and verdande runs on somebody's homelab
// next to a game server. 19 MiB × a handful of simultaneous logins is affordable
// there in a way that the 64 MiB variant is not.
//
// These are the parameters for *new* hashes only. Every hash records the parameters
// it was made with, so raising these does not lock anyone out: existing passwords
// keep verifying against their own settings, and NeedsRehash reports which ones
// should be upgraded the next time their owner logs in and the plaintext is at hand.
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

var (
	ErrInvalidHash = errors.New("auth: password hash is not in the expected format")
	ErrMismatch    = errors.New("auth: password does not match")
)

// HashPassword returns a PHC-format string that carries the algorithm, its
// parameters and the salt alongside the digest, so it is self-describing:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	return encodeHash(password, salt, argonMemory, argonTime, argonThreads), nil
}

// VerifyPassword reports whether password produced encoded.
//
// It returns ErrMismatch for a wrong password and ErrInvalidHash for a stored value
// that cannot be read at all. Callers must not tell those two apart to the user:
// both mean "you are not logged in", and distinguishing them leaks which accounts
// exist.
func VerifyPassword(encoded, password string) error {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, uint32(len(want)))
	// Constant time: a byte-by-byte comparison that returns early leaks how much of
	// a guess was right, one request at a time.
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}

// NeedsRehash reports whether a stored hash was made with weaker parameters than
// the ones in force now. Verifying a login is the only moment the plaintext exists,
// so it is the only moment an upgrade is possible.
func NeedsRehash(encoded string) bool {
	params, _, _, err := decodeHash(encoded)
	if err != nil {
		return true
	}
	return params.memory < argonMemory || params.time < argonTime
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func encodeHash(password string, salt []byte, memory, time uint32, threads uint8) string {
	sum := argon2.IDKey([]byte(password), salt, time, memory, threads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, time, threads, b64.EncodeToString(salt), b64.EncodeToString(sum))
}

func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, ErrInvalidHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	if version != argon2.Version {
		// A hash from a different Argon2 version cannot be checked by this build.
		return argonParams{}, nil, nil, ErrInvalidHash
	}

	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	if p.memory == 0 || p.time == 0 || p.threads == 0 {
		return argonParams{}, nil, nil, ErrInvalidHash
	}

	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	sum, err := b64.DecodeString(parts[5])
	if err != nil || len(sum) == 0 {
		return argonParams{}, nil, nil, ErrInvalidHash
	}
	return p, salt, sum, nil
}
