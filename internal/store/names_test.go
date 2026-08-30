package store

import (
	"context"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"andreas":        "Andreas",
		"Andreas":        "Andreas",
		"anne mette":     "Anne Mette",
		"  kristian  ":   "Kristian",
		"anne   mette":   "Anne Mette",
		"McDonald":       "McDonald", // an existing inner capital is kept
		"óscar":          "Óscar",    // Unicode initial
		"æblegaard":      "Æblegaard",
		"":               "",
		"   ":            "",
		"mette-marie sø": "Mette-marie Sø", // only word-initials are raised
		"iPhone listen":  "IPhone Listen",  // deliberately: the first letter is raised
	}
	for in, want := range cases {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCreateUserCapitalisesName(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	u := &User{Email: "andreas@example.dk", Name: "andreas", PasswordHash: "x"}
	if err := db.CreateUser(ctx, u, "Inbox"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	got, err := db.UserByEmail(ctx, "andreas@example.dk")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Name != "Andreas" {
		t.Errorf("stored name = %q, want %q", got.Name, "Andreas")
	}
}

func TestNormalizeUserNamesBackfill(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// An account as it looked before the app capitalised names: made properly,
	// then set back to lower case behind the store's back to stand in for an old row.
	u := &User{Email: "a@example.dk", Name: "Andreas", PasswordHash: "x"}
	if err := db.CreateUser(ctx, u, "Inbox"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE users SET name = 'andreas' WHERE id = ?`, u.ID); err != nil {
		t.Fatalf("downgrade name: %v", err)
	}

	fixed, err := db.NormalizeUserNames(ctx)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if fixed != 1 {
		t.Fatalf("fixed = %d, want 1", fixed)
	}
	got, _ := db.UserByID(ctx, u.ID)
	if got.Name != "Andreas" {
		t.Errorf("name after backfill = %q, want %q", got.Name, "Andreas")
	}

	// Idempotent: nothing left to do on a second pass.
	again, err := db.NormalizeUserNames(ctx)
	if err != nil {
		t.Fatalf("normalize again: %v", err)
	}
	if again != 0 {
		t.Errorf("second pass fixed = %d, want 0", again)
	}
}
