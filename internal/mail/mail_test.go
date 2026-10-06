package mail

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/kristianwind/verdande/internal/config"
)

// TestEveryMessageCarriesTheEditionsOwnName.
//
// These three letters are the only thing this program sends to an address its
// operator does not control, and an invitation from an urd instance that says
// "verdande" is a letter to a stranger about a program they are not being invited
// to. The name was a field with a value and no reader until 2026-10-07 — the three
// places that needed it had the word written out instead — so the assertion that
// matters is not "it says urd" but "it does not say the other one".
//
// Measured through the unconfigured path on purpose: with no SMTP host, send()
// logs the whole message at warn level and returns, so the subject and the body
// can be read as the recipient would get them without a mail server in the test.
func TestEveryMessageCarriesTheEditionsOwnName(t *testing.T) {
	for _, tc := range []struct{ name, other string }{
		{"verdande", "urd"},
		{"urd", "verdande"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			s := New(config.SMTP{}, "https://example.dk", tc.name,
				slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

			ctx := context.Background()
			if err := s.SendInvite(ctx, "ny@example.dk", "Kristian", "", "https://example.dk/i/abc", time.Hour); err != nil {
				t.Fatalf("SendInvite: %v", err)
			}
			if err := s.SendPasswordReset(ctx, "ny@example.dk", "Kristian", "https://example.dk/r/abc", time.Hour); err != nil {
				t.Fatalf("SendPasswordReset: %v", err)
			}
			if err := s.SendReminder(ctx, "ny@example.dk", "Kristian", "Ring til tandlægen", "https://example.dk/opgave/1"); err != nil {
				t.Fatalf("SendReminder: %v", err)
			}

			got := buf.String()
			if n := strings.Count(got, `msg="no SMTP host configured`); n != 3 {
				t.Fatalf("expected three logged messages, got %d — the test is reading the wrong thing", n)
			}
			if !strings.Contains(got, tc.name) {
				t.Errorf("no message mentions %q", tc.name)
			}
			if strings.Contains(got, tc.other) {
				t.Errorf("a message mentions %q on a %s instance; the name is hard-coded somewhere", tc.other, tc.name)
			}
			t.Logf("%s: %d bytes of mail across three messages", tc.name, len(got))
		})
	}
}

// TestAnInviteWithoutAProjectNamesTheProgram pins the one place the name is the
// *subject* of the sentence rather than a signature: an invitation to the instance
// itself, with no project, reads "has invited you to <program>".
func TestAnInviteWithoutAProjectNamesTheProgram(t *testing.T) {
	var buf bytes.Buffer
	s := New(config.SMTP{}, "https://example.dk", "urd",
		slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	if err := s.SendInvite(context.Background(), "ny@example.dk", "Kristian", "", "https://example.dk/i/abc", time.Hour); err != nil {
		t.Fatalf("SendInvite: %v", err)
	}
	if !strings.Contains(buf.String(), "inviteret dig til urd") {
		t.Errorf("the invite does not read \"inviteret dig til urd\":\n%s", buf.String())
	}
}
