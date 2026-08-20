// Package google implements the OAuth2 authorisation-code flow with PKCE against
// Google.
//
// It was written inside internal/gmail, because Gmail was the only thing that
// needed it. Calendar needs the same flow — the same endpoints, the same PKCE, the
// same refresh — and the only thing that differs between them is one string: the
// scope. A second copy would be a second place for "access_type=offline" to be
// forgotten, which is the mistake that makes a connection die silently an hour
// after it was made.
//
// What is *not* here is the API calls. Those differ completely, so gmail and gcal
// keep their own clients and share only the way in.
package google

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	authEndpoint         = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultTokenEndpoint = "https://oauth2.googleapis.com/token"
)

// Config is the OAuth client the operator registered in Google Cloud, plus the
// scope one feature is asking for.
//
// The client belongs to the instance rather than to a person: one registration,
// any number of users. The scope belongs to the feature: Gmail and Calendar sign
// in through the same registration and ask for different things, and Google issues
// a refresh token per authorisation — so connecting one does not disturb the other.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scope        string

	// TokenURL is where Google is. A field rather than a constant so a test can
	// point it at a server it controls: there is no other way to exercise what
	// happens when the token endpoint is slow or refuses. Empty is Google, so
	// nothing outside a test has to know it exists.
	TokenURL string
}

func (c Config) Configured() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != ""
}

func (c Config) tokenURL() string {
	if c.TokenURL != "" {
		return c.TokenURL
	}
	return defaultTokenEndpoint
}

// PKCE is the proof-key pair for one authorisation attempt.
//
// PKCE on a server-side client is not strictly required — the client secret already
// authenticates the token exchange — but it costs nothing and closes the window in
// which an authorisation code intercepted from the redirect could be replayed
// before verdande gets to it.
type PKCE struct {
	Verifier  string
	Challenge string
	State     string
}

func NewPKCE() (PKCE, error) {
	verifier, err := randomString(64)
	if err != nil {
		return PKCE{}, err
	}
	state, err := randomString(32)
	if err != nil {
		return PKCE{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		State:     state,
	}, nil
}

// AuthURL is where the browser is sent to ask the person for consent.
func (c Config) AuthURL(p PKCE) string {
	v := url.Values{}
	v.Set("client_id", c.ClientID)
	v.Set("redirect_uri", c.RedirectURL)
	v.Set("response_type", "code")
	v.Set("scope", c.Scope)
	v.Set("state", p.State)
	v.Set("code_challenge", p.Challenge)
	v.Set("code_challenge_method", "S256")
	// Without both of these Google returns no refresh token on a re-authorisation,
	// and the connection silently stops working an hour later.
	v.Set("access_type", "offline")
	v.Set("prompt", "consent")
	return authEndpoint + "?" + v.Encode()
}

type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// Exchange trades the authorisation code for tokens.
func (c Config) Exchange(ctx context.Context, code string, p PKCE) (Token, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("redirect_uri", c.RedirectURL)
	form.Set("grant_type", "authorization_code")
	form.Set("code_verifier", p.Verifier)

	return c.token(ctx, form)
}

// Refresh gets a fresh access token. Google does not return a new refresh token
// here, so the caller keeps the one it has.
func (c Config) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	form := url.Values{}
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("grant_type", "refresh_token")

	token, err := c.token(ctx, form)
	if err != nil {
		return Token{}, err
	}
	token.RefreshToken = refreshToken
	return token, nil
}

func (c Config) token(ctx context.Context, form url.Values) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL(),
		strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return Token{}, err
	}
	defer resp.Body.Close()

	var parsed struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		ExpiresIn        int    `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Token{}, fmt.Errorf("google: reading token response: %w", err)
	}
	if parsed.Error != "" {
		return Token{}, fmt.Errorf("google: %s: %s", parsed.Error, parsed.ErrorDescription)
	}
	if resp.StatusCode != http.StatusOK || parsed.AccessToken == "" {
		return Token{}, fmt.Errorf("google: token exchange failed with %s", resp.Status)
	}

	return Token{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		// A minute of slack, so a token that is about to expire is refreshed
		// rather than used and rejected.
		ExpiresAt: time.Now().Add(time.Duration(parsed.ExpiresIn-60) * time.Second),
	}, nil
}

// ErrUnauthorized means the tokens are no longer good — the person revoked access,
// or changed their password. The caller disconnects rather than retrying.
var ErrUnauthorized = fmt.Errorf("google: authorisation was refused")

// Get performs an authorised GET and decodes the JSON body into out.
//
// Shared by the two API clients because the interesting part is the same in both:
// a 401 is not a failure to retry, it is a connection that has ended, and telling
// those apart is what stops a revoked account being polled every ten minutes for
// ever.
func Get(ctx context.Context, client *http.Client, accessToken, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("google: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n], nil
}
