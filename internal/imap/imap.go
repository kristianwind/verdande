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
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
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

	// RootCAs adds a certificate authority to trust, on top of the system's. It
	// exists so a test can put a real IMAP server behind a self-signed certificate
	// and still be refused by everything else — the field can only widen trust,
	// never switch verification off, which is the difference between a seam and a
	// hole. Nil in production, where the system's roots are the answer.
	RootCAs *x509.CertPool
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
		TLSConfig: &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
			RootCAs:    a.RootCAs,
		},
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
// The second return is the highest uid the search considered. A caller that got
// through every message can move its marker to that, and then it does not matter
// whether the server put a uid in each fetch reply — which some do not, and which
// is how the same mail became a task again on every run.
func (c *Client) Since(uid uint32, limit int) ([]Message, uint32, error) {
	if _, err := c.c.Select(c.folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, 0, fmt.Errorf("open %s: %w", c.folder, err)
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
		return nil, 0, fmt.Errorf("search %s: %w", c.folder, err)
	}

	uids := found.AllUIDs()
	if len(uids) == 0 {
		return nil, uid, nil
	}
	if limit > 0 && len(uids) > limit {
		// The newest ones. A mailbox flagged over years would otherwise make the
		// first sync the largest one, which is the run most likely to be watched.
		uids = uids[len(uids)-limit:]
	}

	want := imap.UIDSetNum(uids...)
	fetch := &imap.FetchOptions{
		Envelope: true,
		UID:      true,
		// The whole message, headers included, so the MIME structure can be walked.
		// TEXT alone gives the body as bytes — boundaries and part headers and all —
		// which is what put "--=_be71cb39… Content-Transfer-Encoding: 8bit" at the
		// front of every task made from a multipart mail.
		BodySection: []*imap.FetchItemBodySection{{Peek: true}},
	}
	var highest uint32
	for _, u := range uids {
		if uint32(u) > highest {
			highest = uint32(u)
		}
	}

	msgs, err := c.c.Fetch(want, fetch).Collect()
	if err != nil {
		return nil, 0, fmt.Errorf("fetch from %s: %w", c.folder, err)
	}

	out := make([]Message, 0, len(msgs))
	for i, m := range msgs {
		if m.Envelope == nil {
			continue
		}

		// The uid comes from the search when the fetch reply does not carry one.
		// A missing uid is not cosmetic: it is the marker the next run starts from,
		// and a marker stuck at zero makes every message arrive again on every run.
		// That is what happened against a real server while the tests stayed green,
		// because the server they run against always answers with it.
		msgUID := uint32(m.UID)
		if msgUID == 0 && i < len(uids) {
			msgUID = uint32(uids[i])
		}

		out = append(out, Message{
			UID:     msgUID,
			From:    address(m.Envelope.From),
			Subject: strings.TrimSpace(m.Envelope.Subject),
			Snippet: snippet(m),
			Date:    m.Envelope.Date,
		})
	}
	return out, highest, nil
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

// snippet is the first readable text in the mail.
//
// It walks the MIME structure rather than taking the body as bytes. text/plain is
// preferred and HTML is the fallback with its tags stripped — crudely, because
// this is the line under a task's title and not a rendering engine. The mail
// itself stays where it is.
const snippetLimit = 400

func snippet(m *imapclient.FetchMessageBuffer) string {
	for _, body := range m.BodySection {
		if text := readable(body.Bytes); text != "" {
			return cut(text)
		}
	}
	return ""
}

func readable(raw []byte) string {
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		// Not something this parser recognises. The raw bytes read badly but read,
		// and a mail is not worth losing over its own headers.
		return strings.TrimSpace(string(raw))
	}
	return strings.TrimSpace(fromEntity(entity, 0))
}

// fromEntity descends into a multipart mail. The depth guard is for the mail that
// nests parts inside parts inside parts — rare, and cheaper to bound than to trust.
func fromEntity(entity *message.Entity, depth int) string {
	if depth > 4 {
		return ""
	}

	if parts := entity.MultipartReader(); parts != nil {
		var html string
		for {
			part, err := parts.NextPart()
			if err != nil {
				break
			}
			if text := fromEntity(part, depth+1); text != "" {
				kind, _, _ := part.Header.ContentType()
				if kind != "text/html" {
					return text
				}
				if html == "" {
					html = text
				}
			}
		}
		return html
	}

	body, _ := io.ReadAll(entity.Body)
	kind, _, _ := entity.Header.ContentType()
	if kind == "text/html" {
		return stripTags(string(body))
	}
	return strings.TrimSpace(string(body))
}

// stripTags removes markup without pretending to understand it. Enough for one
// line of preview, and honest about being no more than that.
func stripTags(html string) string {
	var b strings.Builder
	depth := 0
	for _, r := range html {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func cut(text string) string {
	runes := []rune(text)
	if len(runes) > snippetLimit {
		return string(runes[:snippetLimit]) + "…"
	}
	return text
}
