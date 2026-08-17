// Package push sends Web Push messages (RFC 8030) with VAPID (RFC 8292) and
// aes128gcm payload encryption (RFC 8291).
//
// Written directly rather than pulled in as a dependency because the whole thing is
// three specs' worth of well-defined crypto and about two hundred lines, and
// because the alternative libraries carry a JWT package and a crypto stack that
// would each be larger than this file.
//
// The shape of it: derive a shared secret with the browser's public key, build a
// key and nonce from that plus both salts, encrypt the payload with AES-128-GCM,
// and authorise the request with a signed JWT naming this server.
package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Subscription struct {
	Endpoint string
	P256dh   string // the browser's public key, base64url
	Auth     string // the browser's auth secret, base64url
}

type VAPID struct {
	Public  string
	Private string
	// Subject identifies the sender to the push service, as a mailto: or https:
	// URL. Firefox rejects a request without one.
	Subject string
}

// Payload is what the service worker receives.
type Payload struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

var b64 = base64.RawURLEncoding

// ErrGone means the subscription is dead and should be deleted rather than retried.
var ErrGone = errors.New("push: the subscription no longer exists")

func IsGone(err error) bool { return errors.Is(err, ErrGone) }

// GenerateVAPIDKeys produces a P-256 keypair, base64url encoded.
func GenerateVAPIDKeys() (public, private string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	pub := elliptic.Marshal(elliptic.P256(), key.PublicKey.X, key.PublicKey.Y)
	return b64.EncodeToString(pub), b64.EncodeToString(key.D.Bytes()), nil
}

// Send delivers one message to one subscription.
func Send(ctx context.Context, sub Subscription, payload Payload, vapid VAPID) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	encrypted, err := encrypt(sub, body)
	if err != nil {
		return fmt.Errorf("push: encrypt: %w", err)
	}

	token, err := signJWT(sub.Endpoint, vapid)
	if err != nil {
		return fmt.Errorf("push: sign: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(encrypted))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", "86400")
	// Low urgency would let a service sit on a reminder for hours.
	req.Header.Set("Urgency", "normal")
	req.Header.Set("Authorization", "vapid t="+token+", k="+vapid.Public)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode >= 300:
		return fmt.Errorf("push: %s said %s", resp.Request.URL.Host, resp.Status)
	}
	return nil
}

// encrypt builds an RFC 8291 aes128gcm message.
func encrypt(sub Subscription, plaintext []byte) ([]byte, error) {
	clientPubBytes, err := b64.DecodeString(sub.P256dh)
	if err != nil {
		return nil, fmt.Errorf("client key: %w", err)
	}
	authSecret, err := b64.DecodeString(sub.Auth)
	if err != nil {
		return nil, fmt.Errorf("auth secret: %w", err)
	}

	curve := ecdh.P256()
	clientPub, err := curve.NewPublicKey(clientPubBytes)
	if err != nil {
		return nil, fmt.Errorf("client key: %w", err)
	}
	// A fresh keypair per message: reusing one would let anybody who recovered a
	// single message key read every later one.
	serverPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := serverPriv.ECDH(clientPub)
	if err != nil {
		return nil, err
	}
	serverPubBytes := serverPriv.PublicKey().Bytes()

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	// The key derivation is prescribed by RFC 8291 §3.3, down to the exact info
	// strings and their trailing NULs. Every byte here is load-bearing.
	prkKey := hkdf(authSecret, shared,
		append(append([]byte("WebPush: info\x00"), clientPubBytes...), serverPubBytes...), 32)

	cek := hkdf(salt, prkKey, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdf(salt, prkKey, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// A single record, so the padding delimiter is 0x02 ("last record") rather
	// than 0x01.
	record := append(append([]byte{}, plaintext...), 0x02)
	ciphertext := gcm.Seal(nil, nonce, record, nil)

	// Header: salt(16) | record size(4) | key id length(1) | key id.
	var out bytes.Buffer
	out.Write(salt)
	binary.Write(&out, binary.BigEndian, uint32(4096))
	out.WriteByte(byte(len(serverPubBytes)))
	out.Write(serverPubBytes)
	out.Write(ciphertext)
	return out.Bytes(), nil
}

// hkdf is HKDF-SHA256 for outputs of at most one hash block, which is all the sizes
// used here. Written out rather than imported because the full generalised form
// would be more code than this.
func hkdf(salt, ikm, info []byte, length int) []byte {
	extract := hmac.New(sha256.New, salt)
	extract.Write(ikm)
	prk := extract.Sum(nil)

	expand := hmac.New(sha256.New, prk)
	expand.Write(info)
	expand.Write([]byte{0x01})
	return expand.Sum(nil)[:length]
}

// signJWT builds the VAPID authorisation token: an ES256 JWT whose audience is the
// push service's origin.
func signJWT(endpoint string, vapid VAPID) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	subject := vapid.Subject
	if subject == "" || strings.HasSuffix(subject, "@localhost") {
		// Firefox rejects a missing or obviously bogus subject. A generic mailto
		// is better than being refused outright on an instance with no mail set up.
		subject = "mailto:verdande@example.invalid"
	}

	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		// Twelve hours. The spec caps this at 24; shorter limits how long a
		// captured token is worth anything.
		"exp": time.Now().Add(12 * time.Hour).Unix(),
		"sub": subject,
	})
	if err != nil {
		return "", err
	}
	signingInput := header + "." + b64.EncodeToString(claims)

	privBytes, err := b64.DecodeString(vapid.Private)
	if err != nil {
		return "", err
	}
	key := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{Curve: elliptic.P256()},
		D:         new(big.Int).SetBytes(privBytes),
	}
	key.PublicKey.X, key.PublicKey.Y = elliptic.P256().ScalarBaseMult(privBytes)

	sum := sha256.Sum256([]byte(signingInput))
	r, sSig, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		return "", err
	}

	// JOSE wants the two halves as fixed-width 32-byte values, not the ASN.1
	// encoding ecdsa.Sign's other form produces — a short R silently breaks
	// verification on some services and not others.
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	sSig.FillBytes(signature[32:])

	return signingInput + "." + b64.EncodeToString(signature), nil
}
