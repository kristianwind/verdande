-- Calendar feeds, project templates and reminder delivery.

-- A per-user token for the read-only calendar feed.
--
-- Calendar clients cannot log in — Apple Calendar and Google both fetch a URL with
-- no credentials and no way to prompt for any — so the URL itself is the
-- capability. That is why the token is separate from the session and from the API
-- token: it can be rotated on its own when a feed URL ends up somewhere it should
-- not, without signing the person out of everything else.
ALTER TABLE users ADD COLUMN ics_token TEXT;

CREATE UNIQUE INDEX idx_users_ics_token ON users (ics_token) WHERE ics_token IS NOT NULL;

-- A project saved as a shape to reuse: its sections and tasks, without dates,
-- assignees or anything else that belonged to the run it was captured from.
--
-- The body is JSON rather than a set of tables. A template is written once and read
-- whole; normalising it would buy queries nobody makes, and cost a migration every
-- time a task gains a field.
CREATE TABLE project_templates (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    color       TEXT NOT NULL DEFAULT 'graphite',
    body_json   TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE INDEX idx_templates_user ON project_templates (user_id);

-- Reminders already exist as a table; what was missing is the ability to find the
-- ones that are due. A relative reminder ("10 minutes before") has no absolute time
-- of its own, so the scheduler resolves it against the task's due_datetime — and
-- this index is what keeps that sweep from being a full scan every minute.
CREATE INDEX idx_reminders_task ON reminders (task_id) WHERE sent_at IS NULL;

-- Where a nightly backup got to. One row per run, so the panel can show when the
-- last one succeeded and an operator can tell "no backups configured" apart from
-- "backups have been failing for a week".
CREATE TABLE backup_runs (
    id          TEXT PRIMARY KEY,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER,
    path        TEXT,
    size_bytes  INTEGER,
    error       TEXT
);

CREATE INDEX idx_backup_runs_started ON backup_runs (started_at DESC);
