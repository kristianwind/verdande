package store

import (
	"context"
	"testing"
)

// Saving a mailbox that is already there must update it, not fail.
//
// The row is written again on every token refresh and every time Gmail's seen
// list changes. If that write errors, the tasks a sync made are kept and the note
// of having made them is not — so the same mail becomes a task again on the next
// sweep, once every ten minutes, forever.
func TestSavingAnExistingMailboxUpdatesIt(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	m := &Mailbox{UserID: userID, Kind: "gmail", Username: "kw@nolimit.dk",
		RefreshToken: "r", Seen: []string{"a"}}
	if err := db.SaveMailbox(ctx, m); err != nil {
		t.Fatal(err)
	}

	m.Seen = []string{"a", "b"}
	m.AccessToken = "fresh"
	if err := db.SaveMailbox(ctx, m); err != nil {
		t.Fatalf("saving it a second time failed: %v", err)
	}

	back, err := db.MailboxOfKind(ctx, userID, "gmail")
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Seen) != 2 {
		t.Errorf("the seen list came back as %v", back.Seen)
	}
	if back.AccessToken != "fresh" {
		t.Errorf("the token came back as %q", back.AccessToken)
	}
}
