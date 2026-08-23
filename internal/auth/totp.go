package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTP settings: RFC 6238 defaults, which is what every authenticator app assumes
// when a QR code does not say otherwise.
const (
	totpDigits = otp.DigitsSix
	totpPeriod = 30 // seconds
	// A one-step window either side, so a code entered as it rolls over — or on a
	// phone whose clock is a few seconds out — is still accepted. Wider than this
	// starts meaningfully extending the life of a stolen code.
	totpSkew = 1
)

var (
	ErrInvalidCode   = errors.New("auth: verification code is not valid")
	ErrNoTOTPSecret  = errors.New("auth: two-factor authentication is not set up")
	recoveryCodeLen  = 10
	recoveryCodeSets = 8
)

// NewTOTPSecret creates a secret for a user and the otpauth:// URI to render as a
// QR code. Issuer and account are what the authenticator app shows in its list, so
// they have to identify this verdande instance rather than just say "verdande" —
// somebody self-hosting two of them needs to tell the entries apart.
func NewTOTPSecret(issuer, account string) (secret, uri string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: account,
		Period:      totpPeriod,
		Digits:      totpDigits,
		Algorithm:   otp.AlgorithmSHA1, // what authenticator apps actually implement
	})
	if err != nil {
		return "", "", fmt.Errorf("auth: generate totp secret: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// TOTPURI rebuilds the otpauth:// URI for an existing secret, for showing the QR
// code again before enrolment is confirmed.
func TOTPURI(secret, issuer, account string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

// VerifyTOTP checks a code against a secret at the given moment.
func VerifyTOTP(secret, code string, now time.Time) error {
	if secret == "" {
		return ErrNoTOTPSecret
	}
	// Authenticator apps display codes in groups ("123 456"); people paste what
	// they see.
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")

	ok, err := totp.ValidateCustom(code, secret, now, totp.ValidateOpts{
		Period:    totpPeriod,
		Skew:      totpSkew,
		Digits:    totpDigits,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil || !ok {
		return ErrInvalidCode
	}
	return nil
}

// TOTPStep verifies a code and returns the counter of the step it matched, so the
// caller can refuse to accept the same step twice.
//
// ValidateCustom answers only yes or no, and "yes" for a code inside the skew
// window is not enough to stop a replay: a code seen in flight stays valid for the
// rest of its period, and the fix is to record which step was spent and never
// accept that step or an earlier one again. That needs the number, so this walks
// the same window ValidateCustom would — one step either side — and reports the
// counter of the match. The comparison is constant time for the same reason the
// password check is: a code is a secret being checked against a computed value.
func TOTPStep(secret, code string, now time.Time) (int64, error) {
	if secret == "" {
		return 0, ErrNoTOTPSecret
	}
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")

	for offset := -int64(totpSkew); offset <= int64(totpSkew); offset++ {
		at := now.Add(time.Duration(offset) * totpPeriod * time.Second)
		want, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
			Period:    totpPeriod,
			Skew:      0,
			Digits:    totpDigits,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			return 0, ErrInvalidCode
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return at.Unix() / totpPeriod, nil
		}
	}
	return 0, ErrInvalidCode
}

// NewRecoveryCodes returns single-use codes for someone who has lost their phone,
// and the hashes to store. Without these, losing a device means losing the account,
// and the only fix left is editing the database by hand.
//
// The plaintext is returned once, to be shown once. Only the hashes are stored.
func NewRecoveryCodes() (codes []string, hashes []string, err error) {
	// Crockford-ish base32 without padding: unambiguous when read off a printout
	// and typed back in.
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	for i := 0; i < recoveryCodeSets; i++ {
		b := make([]byte, recoveryCodeLen)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, fmt.Errorf("auth: read random: %w", err)
		}
		var sb strings.Builder
		for j, v := range b {
			if j == recoveryCodeLen/2 {
				sb.WriteByte('-')
			}
			sb.WriteByte(alphabet[int(v)%len(alphabet)])
		}
		code := sb.String()
		codes = append(codes, code)
		hashes = append(hashes, HashToken(normalizeRecoveryCode(code)))
	}
	return codes, hashes, nil
}

// MatchRecoveryCode finds which stored hash a typed code corresponds to, or -1.
// The caller must delete that hash: a recovery code works exactly once.
func MatchRecoveryCode(hashes []string, code string) int {
	want := HashToken(normalizeRecoveryCode(code))
	for i, h := range hashes {
		if h == want {
			return i
		}
	}
	return -1
}

// normalizeRecoveryCode forgives how the code was transcribed — case, the dash,
// surrounding space — so a correct code is never rejected for its formatting.
func normalizeRecoveryCode(code string) string {
	r := strings.NewReplacer("-", "", " ", "", "\t", "")
	return strings.ToUpper(r.Replace(strings.TrimSpace(code)))
}

// DecodeTOTPSecret is used by tests and by the enrolment check to confirm a secret
// is well-formed base32 before it is written to the database.
func DecodeTOTPSecret(secret string) ([]byte, error) {
	return base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
}
