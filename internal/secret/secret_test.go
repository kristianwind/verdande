package secret

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAValueSurvivesTheRoundTrip(t *testing.T) {
	b := mustBox(t)
	for _, in := range []string{"1//0abc-refresh", "kodeord med æøå", strings.Repeat("x", 5000)} {
		sealed, err := b.Seal(in)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(sealed, in) {
			t.Fatal("the sealed form still contains the value")
		}
		out, err := b.Unseal(sealed)
		if err != nil {
			t.Fatal(err)
		}
		if out != in {
			t.Errorf("got %q, want %q", out, in)
		}
	}
}

// Two seals of the same value must not look alike, or the column tells anyone
// reading it which accounts share a password.
func TestSealingTwiceGivesDifferentText(t *testing.T) {
	b := mustBox(t)
	first, _ := b.Seal("same")
	second, _ := b.Seal("same")
	if first == second {
		t.Error("the same value sealed to the same text twice")
	}
}

// The column holds values written before there was a key. Reading must not fail
// on them, and sealing must not wrap an already-sealed value a second time.
func TestPlainTextIsReadAsItself(t *testing.T) {
	b := mustBox(t)
	out, err := b.Unseal("1//0abc-written-before-there-was-a-key")
	if err != nil {
		t.Fatal(err)
	}
	if out != "1//0abc-written-before-there-was-a-key" {
		t.Errorf("an unsealed value came back as %q", out)
	}

	sealed, _ := b.Seal("x")
	again, _ := b.Seal(sealed)
	if again != sealed {
		t.Error("an already-sealed value was sealed a second time")
	}
}

func TestNothingIsStillNothing(t *testing.T) {
	b := mustBox(t)
	if out, _ := b.Seal(""); out != "" {
		t.Errorf("an empty value sealed to %q", out)
	}
}

// The message somebody gets after restoring a database without its key file. It
// has to point at the key, because every other symptom points at the mailbox.
func TestTheWrongKeySaysItIsTheWrongKey(t *testing.T) {
	sealed, err := mustBox(t).Seal("hemmelig")
	if err != nil {
		t.Fatal(err)
	}
	_, err = mustBox(t).Unseal(sealed)
	if err == nil {
		t.Fatal("another key opened it")
	}
	if !strings.Contains(err.Error(), "different key") {
		t.Errorf("the error does not name the cause: %v", err)
	}
}

func TestTheKeyFileIsMadeOnceAndKeptPrivate(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open("", dir); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, KeyFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the key file is %04o, not 0600", perm)
	}

	// Opening again must reuse it. A second key here would quietly make every
	// stored token unreadable, and it would look like the mailboxes disconnecting
	// themselves overnight.
	first, _ := os.ReadFile(path)
	if _, err := Open("", dir); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Error("opening a second time replaced the key")
	}
}

func TestAKeyOthersCanReadIsRefused(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open("", dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, KeyFile)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open("", dir); err == nil {
		t.Error("a world-readable key was accepted")
	}
}

// The environment wins, so a host can keep the key off its disks altogether.
func TestTheEnvironmentBeatsTheFile(t *testing.T) {
	dir := t.TempDir()
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	if _, err := Open(key, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFile)); !os.IsNotExist(err) {
		t.Error("a key file was written even though the environment carried one")
	}

	if _, err := Open("too-short", dir); err == nil {
		t.Error("a key that is not 32 bytes was accepted")
	}
}

func mustBox(t *testing.T) *Box {
	t.Helper()
	b, err := Open("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return b
}
