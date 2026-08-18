package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kristianwind/verdande/internal/secret"
)

// The whole point of the exercise: what is written must not be readable in the
// file that a backup copies.
func TestATokenIsNotStoredInTheClear(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	const token = "1//0-a-real-looking-refresh-token"
	if err := db.SetUserSettings(ctx, userID, "gmail", map[string]any{
		"refresh_token": token,
		"label":         "Verdande",
	}); err != nil {
		t.Fatal(err)
	}

	var raw string
	if err := db.QueryRowContext(ctx,
		`SELECT values_json FROM user_settings WHERE user_id = ?`, userID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, token) {
		t.Fatalf("the token is in the row as written: %s", raw)
	}
	// The rest stays readable, which is the reason for naming the secret fields
	// rather than sealing the whole blob.
	if !strings.Contains(raw, "Verdande") {
		t.Errorf("the label was sealed too: %s", raw)
	}

	back, err := db.UserSettings(ctx, userID, "gmail")
	if err != nil {
		t.Fatal(err)
	}
	if back["refresh_token"] != token {
		t.Errorf("got %q back", back["refresh_token"])
	}
}

// The migration that matters. A row written before there was a key must still be
// readable, and must put itself away sealed the next time it is written — nobody
// should have to reconnect a mailbox because the server learned to encrypt.
func TestATokenWrittenBeforeTheKeyStillWorks(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	const token = "1//0-written-when-there-was-no-key"
	plain, _ := json.Marshal(map[string]any{"refresh_token": token})
	if _, err := db.ExecContext(ctx, `
		INSERT INTO user_settings (user_id, scope, values_json, updated_at)
		VALUES (?, 'gmail', ?, 0)`, userID, string(plain)); err != nil {
		t.Fatal(err)
	}

	values, err := db.UserSettings(ctx, userID, "gmail")
	if err != nil {
		t.Fatalf("a row from before the key could not be read: %v", err)
	}
	if values["refresh_token"] != token {
		t.Fatalf("got %q, want the plain token back", values["refresh_token"])
	}

	// Written back — and now sealed, without anything having asked for it.
	if err := db.SetUserSettings(ctx, userID, "gmail", values); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRowContext(ctx,
		`SELECT values_json FROM user_settings WHERE user_id = ?`, userID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, token) {
		t.Error("the row is still in the clear after being written back")
	}
}

// UsersWithGmail finds people by the presence of the key, not its value, so it
// must keep working now that the value is ciphertext.
func TestTheSweepStillFindsAConnectedMailbox(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	if err := db.SetUserSettings(ctx, userID, "gmail",
		map[string]any{"refresh_token": "1//0-token"}); err != nil {
		t.Fatal(err)
	}
	users, err := db.UsersWithGmail(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("the sweep found %d connected mailboxes, want 1", len(users))
	}
}

func sealedStore(t *testing.T) (*DB, string) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	box, err := secret.Open("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db.UseSecrets(box)

	user := &User{Email: "kristian@example.dk", Name: "Kristian"}
	if err := db.CreateUser(context.Background(), user, "Indbakke"); err != nil {
		t.Fatal(err)
	}
	return db, user.ID
}
