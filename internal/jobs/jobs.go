// Package jobs runs the work that happens without anybody asking: reminders going
// out, nightly backups, and the trash emptying itself after thirty days.
//
// All of it lives in one goroutine per job inside the same process. There is no
// queue and no worker pool, because there is nothing here that would benefit from
// one — a homelab instance has a handful of reminders an hour, and a second moving
// part would be more likely to fail than the thing it was protecting.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kristianwind/verdande/internal/config"
	"github.com/kristianwind/verdande/internal/mail"
	"github.com/kristianwind/verdande/internal/realtime"
	"github.com/kristianwind/verdande/internal/store"
)

type Runner struct {
	cfg  *config.Config
	db   *store.DB
	log  *slog.Logger
	mail *mail.Sender
	hub  *realtime.Hub

	wg sync.WaitGroup
}

func New(cfg *config.Config, db *store.DB, log *slog.Logger, sender *mail.Sender, hub *realtime.Hub) *Runner {
	return &Runner{cfg: cfg, db: db, log: log, mail: sender, hub: hub}
}

// Start launches every background job. They stop when ctx is cancelled, and Wait
// blocks until each has finished the pass it was in the middle of — so a shutdown
// during a backup does not leave half a file behind.
func (r *Runner) Start(ctx context.Context) {
	r.every(ctx, "reminders", time.Minute, r.sendDueReminders)
	// Nightly work is checked hourly rather than scheduled at a wall-clock time:
	// a container that was asleep or restarting at 03:00 would otherwise skip a
	// day entirely, and this way it catches up on the next hour it is awake.
	r.every(ctx, "backup", time.Hour, r.nightlyBackup)
	r.every(ctx, "trash", time.Hour, r.emptyTrash)
	r.every(ctx, "sessions", 6*time.Hour, r.purgeSessions)
}

func (r *Runner) Wait() { r.wg.Wait() }

func (r *Runner) every(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// A job that panics must not take the process down with it, and
				// must not stop the other jobs either.
				func() {
					defer func() {
						if p := recover(); p != nil {
							r.log.Error("background job panicked", "job", name, "panic", p)
						}
					}()
					if err := fn(ctx); err != nil {
						r.log.Error("background job failed", "job", name, "err", err)
					}
				}()
			}
		}
	}()
}

// --- reminders ------------------------------------------------------------------

// sendDueReminders delivers everything that has come due since the last pass.
//
// Reminders are marked sent before they are delivered, not after. A reminder that
// goes out and is not recorded will go out again every minute for ever, which is
// far worse than one that is recorded and then fails to send — the second is a
// missed notification, the first is somebody's inbox filling up overnight.
func (r *Runner) sendDueReminders(ctx context.Context) error {
	due, err := r.db.DueReminders(ctx, time.Now())
	if err != nil {
		return err
	}
	for _, rem := range due {
		if err := r.db.MarkReminderSent(ctx, rem.ID); err != nil {
			r.log.Warn("mark reminder sent", "err", err, "reminder", rem.ID)
			continue
		}

		// In-app first: it is instant, it cannot fail, and for somebody with the
		// tab open it is the only one that matters.
		r.hub.PublishToUser(rem.UserID, "reminder", map[string]any{
			"task_id": rem.TaskID,
			"content": rem.TaskContent,
			"due":     rem.RemindAt.Format(time.RFC3339),
		})

		if r.mail.Configured() && rem.UserEmail != "" {
			link := r.cfg.BaseURL + "/projekt/" + rem.ProjectID
			if err := r.mail.SendReminder(ctx, rem.UserEmail, rem.UserName, rem.TaskContent, link); err != nil {
				r.log.Error("send reminder", "err", err, "reminder", rem.ID)
			}
		}
	}
	if len(due) > 0 {
		r.log.Info("reminders sent", "count", len(due))
	}
	return nil
}

// --- backups ---------------------------------------------------------------------

const (
	backupKeepDays = 14
	backupPrefix   = "verdande-"
)

// nightlyBackup snapshots the database once a day.
//
// It uses SQLite's own VACUUM INTO rather than copying the file. A plain copy of a
// live WAL-mode database can miss the most recent writes or catch a page mid-update;
// VACUUM INTO produces a consistent, already-compacted database without blocking
// writers for the duration.
//
// Attachments are deliberately not copied. They are content-addressed files that
// only ever get added, so duplicating gigabytes of them every night to hold
// fourteen identical copies would fill the volume it is meant to protect. The
// README says to back up the whole /data directory; this covers the one part that
// cannot simply be copied.
func (r *Runner) nightlyBackup(ctx context.Context) error {
	last, err := r.db.LastBackupAt(ctx)
	if err != nil {
		return err
	}
	if time.Since(last) < 23*time.Hour {
		return nil
	}

	started := time.Now()
	name := backupPrefix + started.UTC().Format("2006-01-02T150405Z") + ".db"
	path := filepath.Join(r.cfg.BackupsDir(), name)

	runID, err := r.db.StartBackupRun(ctx, started)
	if err != nil {
		return err
	}

	if err := r.db.Snapshot(ctx, path); err != nil {
		// A failed attempt can leave a partial file, which would then look like a
		// backup to anybody reading the directory.
		os.Remove(path)
		if recErr := r.db.FinishBackupRun(ctx, runID, "", 0, err); recErr != nil {
			r.log.Warn("record backup failure", "err", recErr)
		}
		return fmt.Errorf("snapshot: %w", err)
	}

	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	if err := r.db.FinishBackupRun(ctx, runID, path, size, nil); err != nil {
		r.log.Warn("record backup", "err", err)
	}
	r.log.Info("backup written", "path", path, "bytes", size,
		"took", time.Since(started).Round(time.Millisecond).String())

	return r.rotateBackups()
}

// rotateBackups keeps the most recent fourteen and deletes the rest.
//
// Counted by file, not by age: a container that was off for a month would
// otherwise come back, find every backup older than fourteen days, and delete all
// of them — leaving none at exactly the moment they were most likely to be needed.
func (r *Runner) rotateBackups() error {
	entries, err := os.ReadDir(r.cfg.BackupsDir())
	if err != nil {
		return err
	}

	var backups []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), backupPrefix) && strings.HasSuffix(e.Name(), ".db") {
			backups = append(backups, e.Name())
		}
	}
	// The filenames are timestamps, so sorting them sorts by age.
	sort.Sort(sort.Reverse(sort.StringSlice(backups)))

	for _, name := range backups[min(len(backups), backupKeepDays):] {
		path := filepath.Join(r.cfg.BackupsDir(), name)
		if err := os.Remove(path); err != nil {
			r.log.Warn("remove old backup", "err", err, "path", path)
			continue
		}
		r.log.Info("old backup removed", "path", path)
	}
	return nil
}

// --- housekeeping -----------------------------------------------------------------

func (r *Runner) emptyTrash(ctx context.Context) error {
	n, err := r.db.PurgeTrash(ctx, r.cfg.TrashRetention)
	if err != nil {
		return err
	}
	if n > 0 {
		r.log.Info("trash emptied", "rows", n)
	}
	return nil
}

func (r *Runner) purgeSessions(ctx context.Context) error {
	n, err := r.db.PurgeExpiredSessions(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		r.log.Debug("expired sessions removed", "count", n)
	}
	return nil
}

// RunBackupNow is what the "back up now" button calls, and what the tests use.
func (r *Runner) RunBackupNow(ctx context.Context) error {
	started := time.Now()
	name := backupPrefix + started.UTC().Format("2006-01-02T150405Z") + ".db"
	path := filepath.Join(r.cfg.BackupsDir(), name)

	runID, err := r.db.StartBackupRun(ctx, started)
	if err != nil {
		return err
	}
	if err := r.db.Snapshot(ctx, path); err != nil {
		os.Remove(path)
		_ = r.db.FinishBackupRun(ctx, runID, "", 0, err)
		return err
	}
	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	if err := r.db.FinishBackupRun(ctx, runID, path, size, nil); err != nil {
		return err
	}
	return r.rotateBackups()
}
