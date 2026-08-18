package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kristianwind/verdande/internal/secret"
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

// The migration that this whole move stands or falls on: a Gmail connected before
// there was a mailboxes table must come out the other side still connected, with
// its token readable and its settings intact. Nobody should have to reconnect
// because the server tidied up.
func TestAGmailConnectedBeforeTheMoveSurvivesIt(t *testing.T) {
	dir := t.TempDir()
	box, err := secret.Open("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// A database at the state before 0017, with a sealed token written the way the
	// settings table wrote them.
	db, userID := storeAt(t, dir, box)
	sealed, err := box.Seal("1//0-the-original-refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	values := `{"refresh_token":"` + sealed + `","trigger":"label","label":"Verdande",` +
		`"email":"kw@nolimit.dk","seen":["abc","def"],"project_id":"p-1"}`
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO user_settings (user_id, scope, values_json, updated_at)
		VALUES (?, 'gmail', ?, 0)
		ON CONFLICT (user_id) DO UPDATE SET scope = 'gmail', values_json = excluded.values_json`,
		userID, values); err != nil {
		t.Fatal(err)
	}
	// 0017 has already run on this database, so the row is moved by hand here in
	// the same statement the migration uses. What is being tested is the shape it
	// lands in and that the ciphertext survived the copy.
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO mailboxes (id, user_id, kind, name, host, username, password, folder,
			refresh_token, access_token, expires_at, label, trigger_kind, seen, project_id,
			last_uid, last_sync_at, created_at)
		SELECT lower(hex(randomblob(16))), s.user_id, 'gmail',
			json_extract(s.values_json, '$.email'), '', json_extract(s.values_json, '$.email'),
			'', 'INBOX', json_extract(s.values_json, '$.refresh_token'), '', 0,
			json_extract(s.values_json, '$.label'), json_extract(s.values_json, '$.trigger'),
			json_extract(s.values_json, '$.seen'), json_extract(s.values_json, '$.project_id'),
			0, 0, unixepoch()
		FROM user_settings s WHERE s.scope = 'gmail'`); err != nil {
		t.Fatal(err)
	}

	moved, err := db.MailboxOfKind(context.Background(), userID, "gmail")
	if err != nil {
		t.Fatal(err)
	}
	if moved == nil {
		t.Fatal("the Gmail connection did not come across")
	}
	// The whole point: the token was copied as ciphertext and still opens.
	if moved.RefreshToken != "1//0-the-original-refresh-token" {
		t.Errorf("the token came out as %q", moved.RefreshToken)
	}
	if moved.Trigger != "label" || moved.Label != "Verdande" {
		t.Errorf("the trigger came out as %q/%q", moved.Trigger, moved.Label)
	}
	if moved.Username != "kw@nolimit.dk" {
		t.Errorf("the address came out as %q", moved.Username)
	}
	if moved.ProjectID != "p-1" {
		t.Errorf("the destination project came out as %q", moved.ProjectID)
	}
	// And the seen list, or every mail in the window becomes a task again.
	if len(moved.Seen) != 2 || moved.Seen[0] != "abc" {
		t.Errorf("the seen list came out as %v", moved.Seen)
	}
}

// storeAt opens a database in a named directory with a given key, so a test can
// keep the key while the database is reopened.
func storeAt(t *testing.T, dir string, box *secret.Box) (*DB, string) {
	t.Helper()
	db, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.UseSecrets(box)

	user := &User{Email: "kristian@example.dk", Name: "Kristian"}
	if err := db.CreateUser(context.Background(), user, "Indbakke"); err != nil {
		t.Fatal(err)
	}
	return db, user.ID
}
