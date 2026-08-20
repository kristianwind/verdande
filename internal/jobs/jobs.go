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

	// SyncGmail is supplied by the HTTP layer, which owns the token refresh and
	// the task creation. Passed in as a function rather than importing it, because
	// the other direction would be a cycle: httpapi already depends on jobs being
	// startable from main.
	SyncGmail func(ctx context.Context, user *store.User) (int, error)

	// SyncMailbox reads one IMAP mailbox. Supplied by the HTTP layer for the same
	// reason as SyncGmail: it owns turning a message into a task.
	SyncMailbox func(ctx context.Context, user *store.User, m *store.Mailbox) (int, error)

	// Push delivers a notification to somebody's devices. Supplied by the HTTP
	// layer, which owns the VAPID keys and the subscription list.
	Push func(userID, title, body, projectID string)

	// SendBeacon reports this installation to the collector, if it is switched on
	// and a day has passed. Supplied by the HTTP layer for the same reason as the
	// syncs: that is where the instance settings and the version live.
	SendBeacon func(ctx context.Context) error

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
	r.every(ctx, "errors", 12*time.Hour, r.purgeErrors)
	// Ten minutes is a compromise: Gmail's push notifications would be instant but
	// need a public webhook and a Cloud Pub/Sub topic, which is a lot of Google
	// account for a to-do app. Ten minutes is fast enough for something somebody
	// starred and slow enough not to look like abuse.
	r.every(ctx, "mailboxes", 10*time.Minute, r.syncMailboxes)
	// Checked hourly, sent at most daily — the same shape as the backup, and for
	// the same reason: a container asleep at the scheduled minute would otherwise
	// skip the day. SendBeacon decides whether a day has passed.
	r.every(ctx, "beacon", time.Hour, r.sendBeacon)
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

// sendBeacon reports that this installation exists, at most once a day.
//
// Every decision about what is sent, and why this is on by default rather than
// compulsory, lives in internal/beacon. This is only the clock.
func (r *Runner) sendBeacon(ctx context.Context) error {
	if r.SendBeacon == nil {
		return nil
	}
	return r.SendBeacon(ctx)
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

		// And to the devices that asked for it. This was missing: reminders went out
		// in-app and by mail, so somebody with the tab closed and no mail configured
		// got a notification that never arrived — which is the case a reminder is
		// for. Notifications from other people's actions did push; only the timed
		// ones did not, and the interface said "on for this device" either way.
		if r.Push != nil {
			r.Push(rem.UserID, rem.TaskContent, reminderBody(rem.RemindAt), rem.ProjectID)
		}

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

// reminderBody is the line under the task's title. The time rather than the date:
// a reminder arrives when it is due, so the day is today and saying so is noise.
func reminderBody(at time.Time) string {
	return at.Format("15:04")
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
	return Backup(ctx, r.cfg, r.db, r.log)
}

// Backup takes one snapshot now, records the run and rotates the old files.
//
// Exported and free of the Runner, because the settings page can ask for one too:
// waiting until tonight to find out whether backups work at all is how somebody
// discovers on the day they need one that the volume has been read-only for a
// month. The nightly job is this function behind a "has one run today" check; the
// button is this function without it.
//
// It uses SQLite's own VACUUM INTO rather than copying the file. A plain copy of a
// live WAL-mode database can miss the most recent writes or catch a page
// mid-update; VACUUM INTO produces a consistent, already-compacted database
// without blocking writers for the duration.
func Backup(ctx context.Context, cfg *config.Config, db *store.DB, log *slog.Logger) error {
	started := time.Now()
	name := backupPrefix + started.UTC().Format("2006-01-02T150405Z") + ".db"
	path := filepath.Join(cfg.BackupsDir(), name)

	if err := os.MkdirAll(cfg.BackupsDir(), 0o755); err != nil {
		return fmt.Errorf("backups dir: %w", err)
	}

	runID, err := db.StartBackupRun(ctx, started)
	if err != nil {
		return err
	}

	if err := db.Snapshot(ctx, path); err != nil {
		// A failed attempt can leave a partial file, which would then look like a
		// backup to anybody reading the directory.
		os.Remove(path)
		if recErr := db.FinishBackupRun(ctx, runID, "", 0, err); recErr != nil {
			log.Warn("record backup failure", "err", recErr)
		}
		return fmt.Errorf("snapshot: %w", err)
	}

	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	if err := db.FinishBackupRun(ctx, runID, path, size, nil); err != nil {
		log.Warn("record backup", "err", err)
	}
	log.Info("backup written", "path", path, "bytes", size,
		"took", time.Since(started).Round(time.Millisecond).String())

	return rotateBackups(cfg, log)
}

// rotateBackups keeps the most recent fourteen and deletes the rest.
//
// Counted by file, not by age: a container that was off for a month would
// otherwise come back, find every backup older than fourteen days, and delete all
// of them — leaving none at exactly the moment they were most likely to be needed.
func rotateBackups(cfg *config.Config, log *slog.Logger) error {
	entries, err := os.ReadDir(cfg.BackupsDir())
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
		path := filepath.Join(cfg.BackupsDir(), name)
		if err := os.Remove(path); err != nil {
			log.Warn("remove old backup", "err", err, "path", path)
			continue
		}
		log.Info("old backup removed", "path", path)
	}
	return nil
}

// --- gmail ---------------------------------------------------------------------------

// syncMailboxes polls every connected mailbox, Gmail and IMAP alike.
//
// One sweep rather than two, now that both live in the same table. Nobody is
// waiting, so no budget of its own — the request handler has one because a person
// is watching a spinner. A host that has gone quiet costs this run its own dial
// timeout and nothing else: the loop moves on, per mailbox and per person, so one
// bad password does not stop everybody else's mail.
func (r *Runner) syncMailboxes(ctx context.Context) error {
	if r.SyncMailbox == nil {
		return nil
	}
	userIDs, err := r.db.UsersWithMailboxes(ctx)
	if err != nil {
		return err
	}
	for _, id := range userIDs {
		user, err := r.db.UserByID(ctx, id)
		if err != nil || user == nil {
			continue
		}
		boxes, err := r.db.Mailboxes(ctx, id)
		if err != nil {
			r.log.Warn("list mailboxes", "err", err, "user", id)
			continue
		}
		for i := range boxes {
			var created int
			var err error
			// Gmail is fetched over HTTP with a token, IMAP over a connection with a
			// password. Which one is a property of the row, not of the loop.
			if boxes[i].Kind == "gmail" {
				if r.SyncGmail == nil {
					continue
				}
				created, err = r.SyncGmail(ctx, user)
			} else {
				created, err = r.SyncMailbox(ctx, user, &boxes[i])
			}
			if err != nil {
				r.log.Warn("mailbox sync", "err", err, "user", id,
					"kind", boxes[i].Kind, "mailbox", boxes[i].ID)
				continue
			}
			if created > 0 {
				r.log.Info("tasks created from a mailbox", "user", id,
					"kind", boxes[i].Kind, "count", created)
			}
		}
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

// purgeErrors keeps the diagnostic bounded. Thirty days is long enough to answer
// "what happened last week" and short enough that a fault loop cannot fill the
// volume the tasks live on.
func (r *Runner) purgeErrors(ctx context.Context) error {
	n, err := r.db.PurgeOldErrors(ctx, 30*24*time.Hour)
	if err != nil {
		return err
	}
	if n > 0 {
		r.log.Debug("old error rows removed", "count", n)
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

// RunBackupNow is the Runner's way in, for the tests that already hold one.
//
// It was a second copy of Backup — written when the button that would call it did
// not exist yet — and the two had already drifted: this one never created the
// backups directory, and logged nothing. One implementation, so a fix to the
// nightly path is a fix to the button as well.
func (r *Runner) RunBackupNow(ctx context.Context) error {
	return Backup(ctx, r.cfg, r.db, r.log)
}
