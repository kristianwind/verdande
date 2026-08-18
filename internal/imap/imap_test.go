package imap

import (
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// A host without a port is the mistake everyone makes once, and the error a TLS
// dial gives for it says nothing useful about which of the two fields is wrong.
func TestAHostWithoutAPortSaysSo(t *testing.T) {
	_, err := Dial(Account{Host: "imap.mail.me.com", Username: "kw", Password: "x"})
	if err == nil {
		t.Fatal("a host with no port was accepted")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Errorf("the error does not mention the port: %v", err)
	}
}

func TestASenderIsRenderedForReading(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []imap.Address
		want string
	}{
		{"nobody", nil, ""},
		{"bare address", []imap.Address{{Mailbox: "kw", Host: "nolimit.dk"}}, "kw@nolimit.dk"},
		{"with a name", []imap.Address{{Name: "Kristian Wind", Mailbox: "kw", Host: "nolimit.dk"}},
			"Kristian Wind <kw@nolimit.dk>"},
		// The first sender wins. A mail from two people is rare enough that naming
		// one is better than a task titled with a list.
		{"two senders", []imap.Address{
			{Mailbox: "first", Host: "example.dk"},
			{Mailbox: "second", Host: "example.dk"},
		}, "first@example.dk"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := address(c.in); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The snippet is what shows under a task's title, so its only real duty is to end.
func TestASnippetIsTrimmedAndBounded(t *testing.T) {
	long := strings.Repeat("a", snippetLimit*2)
	m := &imapclient.FetchMessageBuffer{
		BodySection: []imapclient.FetchBodySectionBuffer{
			{Bytes: []byte("   ")},
			{Bytes: []byte("  " + long + "  ")},
		},
	}
	got := snippet(m)
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
		t.Errorf("the snippet is not trimmed: %q", got[:20])
	}
	// An empty part must not win over the one that has the text.
	if !strings.HasPrefix(got, "aaa") {
		t.Errorf("an empty body part was preferred: %q", got)
	}
	if len([]rune(got)) > snippetLimit+1 {
		t.Errorf("the snippet runs to %d characters", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("a cut snippet does not say that it was cut")
	}
}

func TestAnEmptyMailboxIsNotAnError(t *testing.T) {
	if got := snippet(&imapclient.FetchMessageBuffer{}); got != "" {
		t.Errorf("a mail with no body parts gave %q", got)
	}
}
