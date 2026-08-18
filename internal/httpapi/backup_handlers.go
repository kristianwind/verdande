package httpapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/jobs"
)

// The nightly backup, made visible.
//
// It has run since the beginning and nothing showed it. A backup nobody can see is
// a backup nobody knows has been failing — and the failure mode is quiet: a volume
// that went read-only, a disk that filled, a container that has not been up at
// midnight for a week. The first time anybody finds out is the day they need one.
//
// Administrators only, and behind a session as well as the admin check. A backup
// file is a complete copy of the database, so downloading one is downloading
// everybody's data; a leaked API token must not be able to.

type backupRunJSON struct {
	ID      string `json:"id"`
	Started string `json:"started_at"`
	// Finished is absent while a backup is still running, and on one that was
	// interrupted — which is the same shape from outside and honestly so: a run
	// the process died in the middle of never finished.
	Finished string `json:"finished_at,omitempty"`
	Size     int64  `json:"size_bytes"`
	Error    string `json:"error,omitempty"`
	// Present says the file is still on disk. Rotation keeps the most recent
	// fourteen, so an older row is a true record of a backup that was made and a
	// file that is no longer there — and a download link for it would 404.
	Present bool `json:"present"`
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	runs, err := s.db.ListBackupRuns(r.Context(), parseLimit(r.URL.Query().Get("limit"), 30, 100))
	if err != nil {
		s.internal(w, r, "list backups", err)
		return
	}

	out := make([]backupRunJSON, 0, len(runs))
	for _, b := range runs {
		j := backupRunJSON{
			ID: b.ID, Started: b.StartedAt.Format(time.RFC3339),
			Size: b.SizeBytes, Error: b.Error,
		}
		if b.FinishedAt != nil {
			j.Finished = b.FinishedAt.Format(time.RFC3339)
		}
		if b.Path != "" {
			if _, err := os.Stat(b.Path); err == nil {
				j.Present = true
			}
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": out})
}

// handleRunBackup takes one now.
//
// The same function the nightly job runs, without the "has one run today" check.
// Waiting until tonight to learn whether backups work at all is how somebody finds
// out on the day they need one — and it is the only way to get a snapshot from
// *before* something you are about to do.
//
// Synchronous. A backup of a homelab database is a second or two, and a button
// that returns immediately and tells you nothing is a button you press twice.
func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request) {
	if err := jobs.Backup(r.Context(), s.cfg, s.db, s.log); err != nil {
		s.internal(w, r, "run backup", err)
		return
	}
	runs, err := s.db.ListBackupRuns(r.Context(), 1)
	if err != nil || len(runs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	b := runs[0]
	j := backupRunJSON{
		ID: b.ID, Started: b.StartedAt.Format(time.RFC3339),
		Size: b.SizeBytes, Error: b.Error, Present: true,
	}
	if b.FinishedAt != nil {
		j.Finished = b.FinishedAt.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusCreated, j)
}

// handleDownloadBackup sends one backup file.
//
// The path comes from the row rather than from the request, and is then checked to
// be inside the backups directory anyway. The row is written by this process and
// should always be safe; the check is there because "should always" is how a path
// traversal gets shipped, and the thing on the other side of it is every account's
// data in one file.
func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	runs, err := s.db.ListBackupRuns(r.Context(), 100)
	if err != nil {
		s.internal(w, r, "list backups", err)
		return
	}

	id := chi.URLParam(r, "backupID")
	var path string
	for _, b := range runs {
		if b.ID == id {
			path = b.Path
			break
		}
	}
	if path == "" {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	dir, err := filepath.Abs(s.cfg.BackupsDir())
	if err != nil {
		s.internal(w, r, "backups dir", err)
		return
	}
	full, err := filepath.Abs(path)
	if err != nil || !strings.HasPrefix(full, dir+string(os.PathSeparator)) {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}
	file, err := os.Open(full)
	if err != nil {
		// Rotation keeps the most recent fourteen, so a row older than that is a
		// true record with no file behind it. Not an error — a 404 is the honest
		// answer to "send me that one".
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		s.internal(w, r, "stat backup", err)
		return
	}

	// The same treatment attachments get, and for a stronger reason: this is a
	// SQLite database, and nothing should ever be tempted to render it inline.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", filepath.Base(full)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", info.ModTime(), file)
}
