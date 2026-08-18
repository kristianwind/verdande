package jobs

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/kristianwind/verdande/internal/config"
	"github.com/kristianwind/verdande/internal/mail"
	"github.com/kristianwind/verdande/internal/realtime"
	"github.com/kristianwind/verdande/internal/store"
)

func newRunner(t *testing.T) (*Runner, *store.DB, *config.Config) {
	t.Helper()

	dir := t.TempDir()
	cfg := &config.Config{
		BaseURL:        "http://localhost:8080",
		DataDir:        dir,
		TrashRetention: 30 * 24 * time.Hour,
	}
	for _, d := range []string{cfg.FilesDir(), cfg.BackupsDir()} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	db, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, db, log, mail.New(cfg.SMTP, cfg.BaseURL, log), realtime.NewHub(log)), db, cfg
}

func seedUser(t *testing.T, db *store.DB) *store.User {
	t.Helper()
	u := &store.User{Email: "kristian@example.dk", Name: "Kristian", PasswordHash: "x"}
	if err := db.CreateUser(t.Context(), u, "Indbakke"); err != nil {
		t.Fatal(err)
	}
	return u
}

// A backup has to be a database somebody can actually open and read — not merely a
// file of the right size. This is the one piece of the system where a silent
// failure is only discovered at the worst possible moment.
func TestBackupProducesAReadableDatabase(t *testing.T) {
	runner, db, cfg := newRunner(t)
	user := seedUser(t, db)

	inbox, err := db.InboxID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := &store.Task{ProjectID: inbox, Content: "noget der skal overleve", CreatedBy: user.ID}
	if err := db.CreateTask(t.Context(), task, nil); err != nil {
		t.Fatal(err)
	}

	if err := runner.RunBackupNow(t.Context()); err != nil {
		t.Fatalf("backup: %v", err)
	}

	backups := backupFiles(t, cfg)
	if len(backups) != 1 {
		t.Fatalf("got %d backup files, want 1", len(backups))
	}

	// Open the copy as a database of its own and read the row back out.
	copyDB, err := sql.Open("sqlite", filepath.Join(cfg.BackupsDir(), backups[0]))
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()

	var content string
	err = copyDB.QueryRow(`SELECT content FROM tasks WHERE id = ?`, task.ID).Scan(&content)
	if err != nil {
		t.Fatalf("reading the backup: %v", err)
	}
	if content != "noget der skal overleve" {
		t.Errorf("content = %q", content)
	}

	// And the run was recorded, so the panel can say when it last worked.
	runs, err := db.ListBackupRuns(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Error != "" || runs[0].FinishedAt == nil {
		t.Errorf("the run was not recorded as a success: %+v", runs)
	}
	if runs[0].SizeBytes == 0 {
		t.Error("the recorded size is zero")
	}
}

// Writes made after a backup must not appear in it — otherwise the "snapshot" is
// really a live handle and restoring from it would be unpredictable.
func TestABackupIsAPointInTime(t *testing.T) {
	runner, db, cfg := newRunner(t)
	user := seedUser(t, db)
	inbox, _ := db.InboxID(t.Context(), user.ID)

	before := &store.Task{ProjectID: inbox, Content: "før", CreatedBy: user.ID}
	if err := db.CreateTask(t.Context(), before, nil); err != nil {
		t.Fatal(err)
	}
	if err := runner.RunBackupNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := &store.Task{ProjectID: inbox, Content: "efter", CreatedBy: user.ID}
	if err := db.CreateTask(t.Context(), after, nil); err != nil {
		t.Fatal(err)
	}

	copyDB, err := sql.Open("sqlite", filepath.Join(cfg.BackupsDir(), backupFiles(t, cfg)[0]))
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()

	var n int
	if err := copyDB.QueryRow(`SELECT count(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("the backup holds %d tasks, want only the one that existed when it was taken", n)
	}
}

// Fourteen are kept, counted by file rather than by age: a container that was off
// for a month must not come back and delete every backup it has for being old.
func TestBackupsRotateByCountNotAge(t *testing.T) {
	runner, _, cfg := newRunner(t)

	// Twenty files, all timestamped a month ago — older than any age-based cutoff.
	old := time.Now().AddDate(0, -1, 0)
	for i := 0; i < 20; i++ {
		name := backupPrefix + old.Add(time.Duration(i)*time.Hour).UTC().Format("2006-01-02T150405Z") + ".db"
		if err := os.WriteFile(filepath.Join(cfg.BackupsDir(), name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := rotateBackups(cfg, runner.log); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	kept := backupFiles(t, cfg)
	if len(kept) != backupKeepDays {
		t.Fatalf("kept %d backups, want %d", len(kept), backupKeepDays)
	}
	// The ones kept are the most recent, not an arbitrary fourteen.
	for i := 1; i < len(kept); i++ {
		if kept[i-1] > kept[i] {
			t.Fatal("the remaining backups are not sorted by time")
		}
	}
	if kept[0] <= backupPrefix+old.UTC().Format("2006-01-02T150405Z") {
		t.Error("an older backup was kept over a newer one")
	}
}

// A file left behind by a failed attempt would look like a backup to anybody
// reading the directory.
func TestAFailedBackupLeavesNoFile(t *testing.T) {
	runner, db, cfg := newRunner(t)

	// Point the snapshot at a path that cannot be written.
	blocked := filepath.Join(cfg.BackupsDir(), "nope")
	if err := os.MkdirAll(blocked, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := db.Snapshot(t.Context(), filepath.Join(blocked, "sub", "x.db")); err == nil {
		t.Skip("this filesystem allowed the write; the failure path cannot be exercised here")
	}

	if err := runner.RunBackupNow(t.Context()); err != nil {
		t.Fatalf("an ordinary backup should still work: %v", err)
	}
	if got := len(backupFiles(t, cfg)); got != 1 {
		t.Errorf("got %d backup files, want 1", got)
	}
}

// Snapshotting onto an existing file would silently produce a corrupt or partial
// result, so it is refused.
func TestSnapshotRefusesToOverwrite(t *testing.T) {
	_, db, cfg := newRunner(t)

	path := filepath.Join(cfg.BackupsDir(), "taken.db")
	if err := os.WriteFile(path, []byte("allerede her"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := db.Snapshot(t.Context(), path); err == nil {
		t.Error("Snapshot overwrote an existing file")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "allerede her" {
		t.Error("the existing file was modified")
	}
}

// The nightly job is checked hourly but must run at most once a day, or every
// hourly tick would take a full copy.
func TestTheNightlyBackupRunsAtMostOncePerDay(t *testing.T) {
	runner, _, cfg := newRunner(t)
	ctx := context.Background()

	if err := runner.nightlyBackup(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if got := len(backupFiles(t, cfg)); got != 1 {
		t.Fatalf("got %d backups after the first run, want 1", got)
	}

	// An hour later — the next tick — nothing further should happen.
	if err := runner.nightlyBackup(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := len(backupFiles(t, cfg)); got != 1 {
		t.Errorf("got %d backups after a second tick, want still 1", got)
	}
}

// The runner must stop when its context is cancelled, and Wait must return —
// otherwise a shutdown hangs until Docker loses patience and kills the process.
func TestJobsStopOnShutdown(t *testing.T) {
	runner, _, _ := newRunner(t)

	ctx, cancel := context.WithCancel(context.Background())
	runner.Start(ctx)
	cancel()

	done := make(chan struct{})
	go func() {
		runner.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the background jobs did not stop within five seconds of shutdown")
	}
}

func backupFiles(t *testing.T, cfg *config.Config) []string {
	t.Helper()
	entries, err := os.ReadDir(cfg.BackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".db" &&
			len(e.Name()) > len(backupPrefix) && e.Name()[:len(backupPrefix)] == backupPrefix {
			out = append(out, e.Name())
		}
	}
	return out
}
