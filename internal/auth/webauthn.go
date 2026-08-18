package auth

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/go-webauthn/webauthn/webauthn"
)

// Passkeys, wrapped so the rest of the app never touches the protocol types.
//
// A password and a TOTP code are both shared secrets: the server keeps something
// that can be stolen from it, and the person keeps something that can be talked
// out of them. A passkey is neither. The private half never leaves the device, and
// the signature is bound to the origin that asked for it — so a convincing copy of
// the sign-in page at another address gets a signature it cannot use.
//
// That binding is also the one thing that can be got wrong here, and it is
// invisible when it is: the RP ID has to be the registrable domain of the address
// people actually visit. Get it wrong and registration works, login works on your
// machine, and everybody behind a different hostname is quietly locked out.

// WebAuthn is the relying party — this instance, as an authenticator sees it.
type WebAuthn struct{ *webauthn.WebAuthn }

// NewWebAuthn derives the relying party from the instance's own base URL.
//
// Derived rather than configured, because a second setting that has to agree with
// VERDANDE_BASE_URL is a second setting that will one day disagree with it — and
// the failure is a login that cannot be completed, reported as "the button does
// nothing".
func NewWebAuthn(baseURL, displayName string) (*WebAuthn, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("auth: %q is not a usable base URL for passkeys", baseURL)
	}

	// The RP ID is the host without the port. A port in it is rejected by every
	// browser, and localhost:5173 during development is exactly where somebody
	// would first put one.
	rpID := parsed.Hostname()

	// An IP address is not a domain, and no browser will accept one as an RP ID —
	// `SecurityError: This is an invalid domain`, thrown in the browser, long after
	// the server has happily issued a challenge. Refused here instead, so the
	// endpoints answer 503 and the interface never offers a button that cannot
	// work. `localhost` is the exception every browser makes, and it is what
	// development runs on.
	if ip := net.ParseIP(rpID); ip != nil {
		return nil, fmt.Errorf(
			"auth: passkeys need a domain name, and %q is an IP address — "+
				"set VERDANDE_BASE_URL to the address people actually visit", rpID)
	}

	w, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: displayName,
		// The origin is the full scheme://host:port, which is what the browser
		// sends and what the signature is checked against.
		RPOrigins: []string{strings.TrimSuffix(parsed.Scheme+"://"+parsed.Host, "/")},
	})
	if err != nil {
		return nil, fmt.Errorf("auth: passkeys: %w", err)
	}
	return &WebAuthn{w}, nil
}

// A PasskeyUser is what the library needs to know about an account.
//
// The handle is the account id rather than anything a person chose. It is written
// into the credential on the device and cannot be changed afterwards, so it must
// not be an email address: changing your email would orphan every key you own.
type PasskeyUser struct {
	ID          string
	Email       string
	Name        string
	Credentials []webauthn.Credential
}

func (u *PasskeyUser) WebAuthnID() []byte                         { return []byte(u.ID) }
func (u *PasskeyUser) WebAuthnName() string                       { return u.Email }
func (u *PasskeyUser) WebAuthnDisplayName() string                { return u.Name }
func (u *PasskeyUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

// EncodeCredentialID renders a credential id for storage and for a URL. Raw bytes
// go in a BLOB well enough, but the id is also compared against what the browser
// sends back as base64url, and one encoding on both sides is one thing to be
// wrong about.
func EncodeCredentialID(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

func DecodeCredentialID(encoded string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(encoded)
}
