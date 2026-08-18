// Package secret seals the values that must not travel with a backup.
//
// The reason this exists is not the database on the server — it is the copies of
// it. Backups can be downloaded through the web interface, and every one taken so
// far carried a live Gmail refresh token in plain text. Anyone who ends up with a
// copy ends up with the mailbox.
//
// So the key must not live in the database. That rules out the pattern used for
// the VAPID pair, which the instance generates and stores in the same file it
// protects: a key kept beside what it locks is not a lock. It comes from the
// environment, or from a file next to the data that the backup does not include.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// prefix marks a sealed value, so a mixed column can be read without guessing and
// so the scheme can be replaced later without having to.
const prefix = "v1:"

// KeyFile is where the key lands when the environment does not carry one. It sits
// beside the database rather than inside it, and the backup routine must never
// learn to include it.
const KeyFile = "secret.key"

// Box seals and opens values.
type Box struct {
	aead cipher.AEAD
}

// Open loads the key, generating and saving one on first use.
//
// A key given in the environment wins: that is how a host keeps the secret out of
// its disks altogether. Otherwise a file, created 0600, and read back with its
// permissions checked — a key that anybody on the box can read is a key that is
// only ceremonially secret.
func Open(env, dataDir string) (*Box, error) {
	if env != "" {
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(env))
		if err != nil || len(key) != 32 {
			return nil, errors.New("VERDANDE_SECRET_KEY must be 32 bytes of base64 — " +
				"generate one with: openssl rand -base64 32")
		}
		return newBox(key)
	}

	path := filepath.Join(dataDir, KeyFile)
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		key, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if decErr != nil || len(key) != 32 {
			// Deliberately not regenerating. A new key here would not fix anything —
			// it would silently make every stored token unreadable, which looks
			// exactly like the mailbox having disconnected itself overnight.
			return nil, fmt.Errorf("%s is not a 32-byte base64 key; move it aside only "+
				"if you accept that every connected mailbox must be reconnected", path)
		}
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%s is readable by others (%04o); chmod 600 it", path, info.Mode().Perm())
		}
		return newBox(key)

	case errors.Is(err, os.ErrNotExist):
		key := make([]byte, 32)
		if _, randErr := rand.Read(key); randErr != nil {
			return nil, randErr
		}
		encoded := base64.StdEncoding.EncodeToString(key)
		if writeErr := os.WriteFile(path, []byte(encoded+"\n"), 0o600); writeErr != nil {
			return nil, fmt.Errorf("write %s: %w", path, writeErr)
		}
		return newBox(key)

	default:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
}

func newBox(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal returns the sealed form of v. An empty string stays empty: there is nothing
// to hide, and a row full of ciphertext for "" only makes the column harder to read.
func (b *Box) Seal(v string) (string, error) {
	if v == "" || IsSealed(v) {
		return v, nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(v), nil)
	return prefix + base64.StdEncoding.EncodeToString(out), nil
}

// Unseal returns the plain value. Anything without the marker is returned as it
// is — the column holds values written before there was a key, and they are read
// as themselves until something writes them back sealed.
func (b *Box) Unseal(v string) (string, error) {
	if !IsSealed(v) {
		return v, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, prefix))
	if err != nil {
		return "", fmt.Errorf("sealed value is not base64: %w", err)
	}
	if len(raw) < b.aead.NonceSize() {
		return "", errors.New("sealed value is too short to contain a nonce")
	}
	nonce, body := raw[:b.aead.NonceSize()], raw[b.aead.NonceSize():]
	out, err := b.aead.Open(nil, nonce, body, nil)
	if err != nil {
		// The overwhelmingly likely cause, and worth saying rather than "message
		// authentication failed", which sends people looking for a corrupt row.
		return "", errors.New("cannot read a stored secret: this is a different key " +
			"than the one it was written with")
	}
	return string(out), nil
}

// IsSealed reports whether v has already been through Seal.
func IsSealed(v string) bool { return strings.HasPrefix(v, prefix) }
