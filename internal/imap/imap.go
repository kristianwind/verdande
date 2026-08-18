// Package imap reads a mailbox over IMAP and hands back the messages that should
// become tasks.
//
// This exists next to internal/gmail rather than inside it because the two agree
// on almost nothing. Gmail is an HTTP API with OAuth, labels and stars; IMAP is a
// connection with a password, folders and flags. What they do share is the shape
// of the answer — a handful of messages, each with a sender, a subject and enough
// of a body to read — and that shape is Message here, deliberately identical to
// what the Gmail client returns, so the caller can be told where to look and not
// how to look.
//
// iCloud, Fastmail and any ordinary host speak this. Office 365 no longer accepts
// a password here at all — it wants OAuth of its own — so it is not a host you can
// reach with this package, and pretending otherwise would only fail later and
// less clearly.
package imap

import (
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Message is one mail, reduced to what a task needs.
type Message struct {
	UID     uint32
	From    string
	Subject string
	Snippet string
	Date    time.Time
}

// Account is one mailbox to read.
type Account struct {
	Host     string // "imap.mail.me.com:993"
	Username string
	Password string // an app-specific password on every host worth naming
	Folder   string // "INBOX" when empty
}

// Dial opens a TLS connection and logs in. The caller closes it.
//
// TLS is not optional and there is no flag to make it so: this carries a password
// that unlocks a person's whole mailbox, and a switch for turning that protection
// off is a switch that will one day be found already flipped.
func Dial(a Account) (*Client, error) {
	host, _, err := net.SplitHostPort(a.Host)
	if err != nil {
		return nil, fmt.Errorf("host must include a port, like imap.mail.me.com:993: %w", err)
	}

	c, err := imapclient.DialTLS(a.Host, &imapclient.Options{
		TLSConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
	})
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", a.Host, err)
	}

	if err := c.Login(a.Username, a.Password).Wait(); err != nil {
		c.Close()
		// Deliberately not repeating the server's own words here. A bad password is
		// the overwhelmingly likely cause, and hosts answer it with everything from
		// "AUTHENTICATIONFAILED" to a sentence about web login being required.
		return nil, fmt.Errorf("%s refused the login for %s: %w", a.Host, a.Username, err)
	}

	folder := a.Folder
	if folder == "" {
		folder = "INBOX"
	}
	return &Client{c: c, folder: folder}, nil
}

// Client is a logged-in connection to one mailbox.
type Client struct {
	c      *imapclient.Client
	folder string
}

// Close ends the session politely, then hangs up regardless.
func (c *Client) Close() error {
	if err := c.c.Logout().Wait(); err != nil {
		return c.c.Close()
	}
	return c.c.Close()
}

// Since returns the flagged messages in the folder newer than uid.
//
// Flagged, not unread: unread is a state the person is still using, and a sync
// that consumed it would be taking something away. Flagging a mail is a deliberate
// act with no other meaning, which is exactly what is wanted — the same reason the
// Gmail side reads stars.
func (c *Client) Since(uid uint32, limit int) ([]Message, error) {
	if _, err := c.c.Select(c.folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("open %s: %w", c.folder, err)
	}

	// From uid+1 to the end. A mailbox with nothing new answers with no uids
	// rather than an error, and that is not a failure worth reporting upwards.
	set := imap.UIDSetNum()
	set.AddRange(imap.UID(uid+1), 0)

	criteria := &imap.SearchCriteria{
		UID:  []imap.UIDSet{set},
		Flag: []imap.Flag{imap.FlagFlagged},
	}
	found, err := c.c.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("search %s: %w", c.folder, err)
	}

	uids := found.AllUIDs()
	if len(uids) == 0 {
		return nil, nil
	}
	if limit > 0 && len(uids) > limit {
		// The newest ones. A mailbox flagged over years would otherwise make the
		// first sync the largest one, which is the run most likely to be watched.
		uids = uids[len(uids)-limit:]
	}

	want := imap.UIDSetNum(uids...)
	fetch := &imap.FetchOptions{
		Envelope:    true,
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{{Specifier: imap.PartSpecifierText, Peek: true}},
	}
	msgs, err := c.c.Fetch(want, fetch).Collect()
	if err != nil {
		return nil, fmt.Errorf("fetch from %s: %w", c.folder, err)
	}

	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Envelope == nil {
			continue
		}
		out = append(out, Message{
			UID:     uint32(m.UID),
			From:    address(m.Envelope.From),
			Subject: strings.TrimSpace(m.Envelope.Subject),
			Snippet: snippet(m),
			Date:    m.Envelope.Date,
		})
	}
	return out, nil
}

// address renders the first sender as "Name <addr>", or just the address.
func address(list []imap.Address) string {
	if len(list) == 0 {
		return ""
	}
	a := list[0]
	addr := a.Addr()
	if a.Name == "" {
		return addr
	}
	return a.Name + " <" + addr + ">"
}

// snippet is the first readable line or so of the body.
//
// No MIME walk and no HTML stripping yet: this is what shows under a task's title,
// and a wrong-but-short line is a smaller problem than a parser that has to be
// right about every mail ever sent. The full mail stays where it is.
const snippetLimit = 400

func snippet(m *imapclient.FetchMessageBuffer) string {
	for _, body := range m.BodySection {
		text := strings.TrimSpace(string(body.Bytes))
		if text == "" {
			continue
		}
		if len(text) > snippetLimit {
			text = text[:snippetLimit] + "…"
		}
		return text
	}
	return ""
}
