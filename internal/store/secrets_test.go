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

// The sweep finds people by the presence of a mailbox, so it must keep working
// now that the credential in it is ciphertext.
func TestTheSweepStillFindsAConnectedMailbox(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	if err := db.SaveMailbox(ctx, &Mailbox{
		UserID: userID, Kind: "gmail", RefreshToken: "1//0-token",
	}); err != nil {
		t.Fatal(err)
	}
	users, err := db.UsersWithMailboxes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("the sweep found %d people with mailboxes, want 1", len(users))
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

// Three integrations, one person, and none of them may delete the others.
//
// `user_settings` had `user_id` as its whole primary key and an upsert that
// rewrote `scope` on conflict, so every person had exactly one settings row and
// the last write won the lot: saving an AI provider deleted the Gmail connection,
// and connecting a calendar deleted the AI provider. The write reported success
// each time, which is why it was reported as "the AI settings are not saved"
// rather than as "connecting the calendar deleted them".
//
// Written against the store rather than the handlers, because the shape was the
// bug: every caller was correct on its own.
func TestOnePersonCanHaveSettingsForMoreThanOneThing(t *testing.T) {
	db, userID := sealedStore(t)
	ctx := context.Background()

	want := map[string]map[string]any{
		"ai":       {"provider": "anthropic", "model": "claude-sonnet-5", "api_key": "sk-ant-xxx"},
		"gmail":    {"refresh_token": "1//0-gmail", "label": "Verdande"},
		"calendar": {"refresh_token": "1//0-calendar", "calendar_id": "primary"},
	}
	// In this order on purpose: the AI settings are written first and read last,
	// so a scope that steals the row is caught by the one written earliest.
	for _, scope := range []string{"ai", "gmail", "calendar"} {
		if err := db.SetUserSettings(ctx, userID, scope, want[scope]); err != nil {
			t.Fatalf("save %s: %v", scope, err)
		}
	}

	for _, scope := range []string{"ai", "gmail", "calendar"} {
		got, err := db.UserSettings(ctx, userID, scope)
		if err != nil {
			t.Fatalf("read %s: %v", scope, err)
		}
		for key, value := range want[scope] {
			if got[key] != value {
				t.Errorf("%s.%s is %v, want %v", scope, key, got[key], value)
			}
		}
	}

	// And a second write to one scope updates it rather than adding another row —
	// the upsert still has to upsert.
	if err := db.SetUserSettings(ctx, userID, "ai", map[string]any{"provider": "openai"}); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM user_settings WHERE user_id = ?`, userID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 3 {
		t.Errorf("the person has %d settings rows, want 3", rows)
	}
	back, err := db.UserSettings(ctx, userID, "ai")
	if err != nil {
		t.Fatal(err)
	}
	if back["provider"] != "openai" {
		t.Errorf("the second write did not land: %v", back["provider"])
	}
}

// The upgrade path, which is the one nobody runs: an existing database with a row
// in the old shape, opened by a binary that carries 0025.
//
// Every other test here starts empty and runs all the migrations in order, which
// exercises the migration's SQL but not its *situation*. The situation is a table
// that already holds somebody's settings, and the question is whether they come
// through it and whether a second scope can be written afterwards.
func TestSettingsSurviveTheUpgradeToPerScopeRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	user := &User{Email: "kristian@example.dk", Name: "Kristian"}
	if err := db.CreateUser(ctx, user, "Indbakke"); err != nil {
		t.Fatal(err)
	}

	// Wind the database back to how v0.33.3 left it: user_id as the whole primary
	// key, and 0025 not yet applied.
	for _, stmt := range []string{
		`PRAGMA foreign_keys = OFF`,
		`CREATE TABLE us_old (
			user_id     TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
			scope       TEXT NOT NULL DEFAULT 'general',
			values_json TEXT NOT NULL DEFAULT '{}',
			updated_at  INTEGER NOT NULL
		)`,
		`INSERT INTO us_old (user_id, scope, values_json, updated_at)
		 SELECT user_id, scope, values_json, updated_at FROM user_settings`,
		`DROP TABLE user_settings`,
		`ALTER TABLE us_old RENAME TO user_settings`,
		`DELETE FROM schema_migrations WHERE name = '0025_user_settings_per_scope.sql'`,
		`PRAGMA foreign_keys = ON`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("rewind (%s): %v", stmt, err)
		}
	}

	// A Gmail connection, written the way the old upsert wrote one.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO user_settings (user_id, scope, values_json, updated_at)
		VALUES (?, 'gmail', '{"label":"Verdande"}', 0)`, user.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// And now the new binary opens it.
	db, err = Open(path)
	if err != nil {
		t.Fatalf("the upgrade refused to open the database: %v", err)
	}
	defer db.Close()

	gmail, err := db.UserSettings(ctx, user.ID, "gmail")
	if err != nil {
		t.Fatal(err)
	}
	if gmail["label"] != "Verdande" {
		t.Errorf("the Gmail settings did not survive the migration: %v", gmail)
	}

	// The thing that was reported: set up the AI provider, and have it still be
	// there afterwards.
	if err := db.SetUserSettings(ctx, user.ID, "ai", map[string]any{
		"provider": "anthropic", "model": "claude-sonnet-5",
	}); err != nil {
		t.Fatalf("saving the AI provider after the upgrade: %v", err)
	}
	ai, err := db.UserSettings(ctx, user.ID, "ai")
	if err != nil {
		t.Fatal(err)
	}
	if ai["provider"] != "anthropic" {
		t.Errorf("the AI provider is %v after the upgrade, want anthropic", ai["provider"])
	}
	again, err := db.UserSettings(ctx, user.ID, "gmail")
	if err != nil {
		t.Fatal(err)
	}
	if again["label"] != "Verdande" {
		t.Errorf("saving the AI provider took the Gmail settings: %v", again)
	}
}
