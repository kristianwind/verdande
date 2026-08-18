package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAMailboxIsStoredSealedAndComesBackWhole(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	const password = "abcd-efgh-ijkl-mnop"
	m := &Mailbox{
		UserID:   userID,
		Kind:     "imap",
		Name:     "iCloud",
		Host:     "imap.mail.me.com:993",
		Username: "kw@icloud.com",
		Password: password,
	}
	if err := db.SaveMailbox(ctx, m); err != nil {
		t.Fatal(err)
	}

	var stored string
	if err := db.QueryRowContext(ctx,
		`SELECT password FROM mailboxes WHERE id = ?`, m.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, password) {
		t.Fatalf("the password is in the row as typed: %s", stored)
	}

	back, err := db.Mailbox(ctx, userID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back == nil {
		t.Fatal("the mailbox was not found")
	}
	if back.Password != password {
		t.Errorf("got password %q", back.Password)
	}
	if back.Folder != "INBOX" {
		t.Errorf("the folder defaulted to %q, want INBOX", back.Folder)
	}
}

// The question that prompted the table: two people on one instance, each with
// their own mailbox, neither seeing the other's.
func TestAMailboxBelongsToOnePerson(t *testing.T) {
	db, kristian := sealedStore(t)
	ctx := context.Background()

	andreas := &User{Email: "andreas@example.dk", Name: "Andreas"}
	if err := db.CreateUser(ctx, andreas, "Indbakke"); err != nil {
		t.Fatal(err)
	}

	mine := &Mailbox{UserID: kristian, Kind: "imap", Host: "imap.mail.me.com:993", Username: "kw"}
	theirs := &Mailbox{UserID: andreas.ID, Kind: "imap", Host: "imap.fastmail.com:993", Username: "andreas"}
	for _, m := range []*Mailbox{mine, theirs} {
		if err := db.SaveMailbox(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	list, err := db.Mailboxes(ctx, kristian)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Username != "kw" {
		t.Fatalf("got %d mailboxes for Kristian, want only his own", len(list))
	}

	// And the id alone is not enough to reach somebody else's.
	other, err := db.Mailbox(ctx, kristian, theirs.ID)
	if err != nil {
		t.Fatal(err)
	}
	if other != nil {
		t.Error("one person read another's mailbox by its id")
	}
}

// Several mailboxes each is the ordinary case — a work Gmail and a private
// iCloud — and is the whole reason this is a table rather than a setting.
func TestOnePersonCanHaveSeveral(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	for _, host := range []string{"imap.mail.me.com:993", "imap.fastmail.com:993"} {
		if err := db.SaveMailbox(ctx, &Mailbox{
			UserID: userID, Kind: "imap", Host: host, Username: "kw",
		}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := db.Mailboxes(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d mailboxes, want 2", len(list))
	}
}

// The same mailbox twice would make two tasks out of every mail.
func TestTheSameMailboxTwiceIsRefused(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	first := &Mailbox{UserID: userID, Kind: "imap", Host: "imap.mail.me.com:993", Username: "kw"}
	if err := db.SaveMailbox(ctx, first); err != nil {
		t.Fatal(err)
	}
	again := &Mailbox{UserID: userID, Kind: "imap", Host: "imap.mail.me.com:993", Username: "kw"}
	if err := db.SaveMailbox(ctx, again); err == nil {
		t.Error("the same host and username was accepted a second time")
	}
}

func TestReadingProgressIsRemembered(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	m := &Mailbox{UserID: userID, Kind: "imap", Host: "imap.mail.me.com:993", Username: "kw"}
	if err := db.SaveMailbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Truncate(time.Second)
	if err := db.MarkMailboxRead(ctx, m.ID, 4711, at); err != nil {
		t.Fatal(err)
	}

	back, err := db.Mailbox(ctx, userID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if back.LastUID != 4711 {
		t.Errorf("last uid is %d", back.LastUID)
	}
	if !back.LastSyncAt.Equal(at) {
		t.Errorf("last sync is %v, want %v", back.LastSyncAt, at)
	}
	// The credentials it was holding must not have been rewritten as a side effect.
	if back.Host != "imap.mail.me.com:993" {
		t.Errorf("the host changed to %q", back.Host)
	}
}

// Disconnecting takes the mailbox, not the work it produced.
func TestDisconnectingKeepsTheTasks(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	m := &Mailbox{UserID: userID, Kind: "imap", Host: "imap.mail.me.com:993", Username: "kw"}
	if err := db.SaveMailbox(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteMailbox(ctx, userID, m.ID); err != nil {
		t.Fatal(err)
	}
	list, err := db.Mailboxes(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("the mailbox is still connected")
	}
}
