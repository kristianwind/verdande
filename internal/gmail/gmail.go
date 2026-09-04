// Package gmail is the small slice of the Gmail API verdande needs, and the scope
// it asks for.
//
// The one thing verdande writes back is the star. A starred message becomes a
// task, and once it has, the star comes off — exactly as the IMAP side takes the
// flag off a mail it has imported, and for the same reason: the star meant "turn
// this into a task", the task now exists, so the star is no longer true. Nothing
// else is touched. Completing the task does not reach back to the mail; the mail
// is a pointer the task was made from, not a copy the two have to be kept in step.
//
// That one write is why the scope is gmail.modify rather than gmail.readonly.
// Removing the STARRED label is a modify, and Gmail offers no narrower scope that
// can do it — so the consent screen asks for more than it used to, and the trade
// is deliberate: a starred inbox that empties itself as the tasks are made, rather
// than one that fills with stars nothing ever clears. Still not send, still not
// delete — modify is labels, and the only label verdande removes is the one that
// triggered the task.
//
// The OAuth flow itself moved to internal/google when Calendar needed the same one.
// The names below are aliases so that every call site — and this package's own
// tests — still read as Gmail's, which is what they are: one registration, one
// flow, one scope each.
package gmail

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/google"
)

// Read mail and change its labels, and only mail. Not profile, not contacts, not
// send, not delete. The single write is removing the STARRED label from a message
// that has become a task; see the package comment for why that costs modify.
const Scope = "https://www.googleapis.com/auth/gmail.modify"

const defaultAPIBase = "https://gmail.googleapis.com/gmail/v1"

// The OAuth flow, which is Google's rather than Gmail's.
type (
	Config = google.Config
	PKCE   = google.PKCE
	Token  = google.Token
)

var (
	NewPKCE         = google.NewPKCE
	ErrUnauthorized = google.ErrUnauthorized
)

// --- the API ---------------------------------------------------------------------

type Client struct {
	accessToken string
	// base is where the Gmail API is. A field rather than a constant so a test can
	// point it at a server it controls — and "the sync comes back before whatever
	// sits in front of this server gives up on it" is exactly the kind of promise
	// worth a test and impossible to make against the real thing. Empty is Google.
	base string
	http *http.Client
}

func NewClient(accessToken string) *Client {
	return &Client{accessToken: accessToken, http: &http.Client{Timeout: 30 * time.Second}}
}

// At points the client at another server. Tests only; the zero value is Google.
func (c *Client) At(base string) *Client {
	c.base = base
	return c
}

func (c *Client) api() string {
	if c.base != "" {
		return c.base
	}
	return defaultAPIBase
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
	if err := c.get(ctx, c.api()+"/users/me/profile", &parsed); err != nil {
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
	if err := c.get(ctx, c.api()+"/users/me/messages?"+v.Encode(), &parsed); err != nil {
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
	if err := c.get(ctx, c.api()+"/users/me/messages/"+url.PathEscape(id)+"?"+v.Encode(), &parsed); err != nil {
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
	return google.Get(ctx, c.http, c.accessToken, url, out)
}

// Unstar takes the star off the given messages — one call for all of them, the way
// the IMAP side unflags a batch, so a sweep that made ten tasks is one write and
// not ten.
//
// STARRED is a system label whose id is its name, so no lookup is needed; a custom
// label would need one, and verdande removes none. batchModify answers 204 with no
// body on success. An empty list is a no-op, not a call: batchModify with no ids is
// an error on Google's side and nothing worth making a round trip to hear.
func (c *Client) Unstar(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	body := struct {
		IDs            []string `json:"ids"`
		RemoveLabelIDs []string `json:"removeLabelIds"`
	}{IDs: ids, RemoveLabelIDs: []string{"STARRED"}}
	return google.Post(ctx, c.http, c.accessToken, c.api()+"/users/me/messages/batchModify", body, nil)
}

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
