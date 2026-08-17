// Package gmail implements the OAuth2 authorisation-code flow with PKCE against
// Google, and the small slice of the Gmail API verdande needs.
//
// The connection is one-way on purpose: a starred or labelled message becomes a
// task, and nothing verdande does afterwards touches the mailbox. Unstarring the
// message does not delete the task, completing the task does not unstar the
// message. Two-way would mean deciding what to do when both sides changed, and the
// only honest answer to that is a synchronisation model far bigger than the feature.
//
// Scope is gmail.readonly. verdande never needs to send, modify or delete, and
// asking for less is what makes the consent screen something a person can agree to.
package gmail

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
	authEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenEndpoint = "https://oauth2.googleapis.com/token"
	apiBase       = "https://gmail.googleapis.com/gmail/v1"

	// Read-only, and only mail. Not profile, not contacts, not send.
	Scope = "https://www.googleapis.com/auth/gmail.readonly"
)

// Config is the OAuth client the operator registered in Google Cloud. It belongs to
// the instance rather than to a person: one registration, any number of users.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func (c Config) Configured() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != ""
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
	v.Set("scope", Scope)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint,
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
		return Token{}, fmt.Errorf("gmail: reading token response: %w", err)
	}
	if parsed.Error != "" {
		return Token{}, fmt.Errorf("gmail: %s: %s", parsed.Error, parsed.ErrorDescription)
	}
	if resp.StatusCode != http.StatusOK || parsed.AccessToken == "" {
		return Token{}, fmt.Errorf("gmail: token exchange failed with %s", resp.Status)
	}

	return Token{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		// A minute of slack, so a token that is about to expire is refreshed
		// rather than used and rejected.
		ExpiresAt: time.Now().Add(time.Duration(parsed.ExpiresIn-60) * time.Second),
	}, nil
}

// --- the API ---------------------------------------------------------------------

type Client struct {
	accessToken string
	http        *http.Client
}

func NewClient(accessToken string) *Client {
	return &Client{accessToken: accessToken, http: &http.Client{Timeout: 30 * time.Second}}
}

// Message is the part of a Gmail message a task is made from.
type Message struct {
	ID       string
	ThreadID string
	From     string
	Subject  string
	Snippet  string
	// Link opens the message in Gmail's own interface, which is the whole point:
	// the task is a pointer back to the mail, not a copy of it.
	Link string
}

// Profile identifies the connected mailbox, so the settings page can show which
// account is connected rather than only that one is.
func (c *Client) Profile(ctx context.Context) (string, error) {
	var parsed struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := c.get(ctx, apiBase+"/users/me/profile", &parsed); err != nil {
		return "", err
	}
	return parsed.EmailAddress, nil
}

// List finds messages matching a Gmail search query.
//
// Gmail's own query language is used rather than a filter built here — "is:starred",
// "label:Til-handling" — because it is what the person already knows from the
// search box, and it is what their existing filters are written in.
func (c *Client) List(ctx context.Context, query string, max int) ([]string, error) {
	if max <= 0 || max > 50 {
		max = 25
	}
	v := url.Values{}
	v.Set("q", query)
	v.Set("maxResults", fmt.Sprint(max))

	var parsed struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := c.get(ctx, apiBase+"/users/me/messages?"+v.Encode(), &parsed); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(parsed.Messages))
	for _, m := range parsed.Messages {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// Get fetches the headers of one message. Metadata format only: verdande needs the
// sender and the subject, and asking for the full body would download attachments
// it has no use for.
func (c *Client) Get(ctx context.Context, id string) (Message, error) {
	v := url.Values{}
	v.Set("format", "metadata")
	v.Add("metadataHeaders", "From")
	v.Add("metadataHeaders", "Subject")

	var parsed struct {
		ID       string `json:"id"`
		ThreadID string `json:"threadId"`
		Snippet  string `json:"snippet"`
		Payload  struct {
			Headers []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"headers"`
		} `json:"payload"`
	}
	if err := c.get(ctx, apiBase+"/users/me/messages/"+url.PathEscape(id)+"?"+v.Encode(), &parsed); err != nil {
		return Message{}, err
	}

	msg := Message{
		ID: parsed.ID, ThreadID: parsed.ThreadID, Snippet: parsed.Snippet,
		Link: "https://mail.google.com/mail/u/0/#inbox/" + parsed.ThreadID,
	}
	for _, h := range parsed.Payload.Headers {
		switch strings.ToLower(h.Name) {
		case "from":
			msg.From = h.Value
		case "subject":
			msg.Subject = h.Value
		}
	}
	return msg, nil
}

func (c *Client) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gmail: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ErrUnauthorized means the tokens are no longer good — the person revoked access,
// or changed their password. The caller disconnects rather than retrying.
var ErrUnauthorized = fmt.Errorf("gmail: authorisation was refused")

// SenderName pulls "Anders Jensen" out of "Anders Jensen <anders@example.dk>",
// falling back to the address when there is no display name.
func SenderName(from string) string {
	from = strings.TrimSpace(from)
	if i := strings.Index(from, "<"); i > 0 {
		// Space first, then quotes. A display name arrives as `"Jensen, Anders" `
		// — with a trailing space before the angle bracket — so trimming quotes
		// first stops at the space and leaves the closing quote behind.
		name := strings.Trim(strings.TrimSpace(from[:i]), `"`)
		if name != "" {
			return name
		}
	}
	return strings.Trim(strings.Trim(from, "<>"), `"`)
}

// Query builds the Gmail search for the configured trigger.
func Query(trigger, label string) string {
	var terms []string
	switch trigger {
	case "starred":
		terms = append(terms, "is:starred")
	case "label":
		if label != "" {
			terms = append(terms, `label:"`+label+`"`)
		}
	case "both":
		if label != "" {
			// Gmail's OR needs the braces form to group correctly.
			terms = append(terms, `{is:starred label:"`+label+`"}`)
		} else {
			terms = append(terms, "is:starred")
		}
	}
	if len(terms) == 0 {
		return ""
	}
	// Only recent mail. Without this, connecting an account with a decade of
	// starred messages would create a decade of tasks in one sweep.
	terms = append(terms, "newer_than:30d")
	return strings.Join(terms, " ")
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b)[:n], nil
}
