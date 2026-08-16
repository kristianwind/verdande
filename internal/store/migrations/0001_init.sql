-- verdande — initial schema.
--
-- Conventions used throughout:
--   * ids are UUIDv7 stored as TEXT: time-sortable, so they index well and give a
--     stable creation order without a second column.
--   * timestamps are INTEGER unix seconds, UTC. The one exception is tasks.due_date,
--     which is a calendar date ('YYYY-MM-DD') and deliberately has no timezone —
--     "due Tuesday" means Tuesday wherever you happen to be standing.
--   * sort_order is REAL, not INTEGER, so drag-and-drop can insert between two
--     neighbours by averaging them instead of renumbering the whole list.
--   * soft delete is `deleted_at`; the 30-day trash sweeper is what makes it final.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL,
    name          TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    totp_secret   TEXT,
    totp_enabled  INTEGER NOT NULL DEFAULT 0,
    avatar_color  TEXT NOT NULL DEFAULT '#8a8f98',
    timezone      TEXT NOT NULL DEFAULT 'Europe/Copenhagen',
    locale        TEXT NOT NULL DEFAULT 'da',
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- Email identifies a person for login and invites, so it is unique case-insensitively.
-- Addresses are also stored already lowercased; this index is the backstop.
CREATE UNIQUE INDEX idx_users_email ON users (lower(email));

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    ua           TEXT NOT NULL DEFAULT '',
    ip           TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
);

CREATE INDEX idx_sessions_user ON sessions (user_id);
CREATE INDEX idx_sessions_expires ON sessions (expires_at);

-- Invites carry a hashed token, never the token itself: the plaintext lives only in
-- the emailed link. A leaked database therefore cannot be used to accept invites.
CREATE TABLE invites (
    id          TEXT PRIMARY KEY,
    email       TEXT NOT NULL,
    project_id  TEXT REFERENCES projects (id) ON DELETE CASCADE,
    role        TEXT NOT NULL DEFAULT 'editor',
    token_hash  TEXT NOT NULL UNIQUE,
    created_by  TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    accepted_at INTEGER,
    CHECK (role IN ('owner', 'editor', 'viewer'))
);

CREATE INDEX idx_invites_email ON invites (lower(email));

-- Single-use tokens for "forgot my password". Same hashed-token rule as invites.
CREATE TABLE password_resets (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER
);

CREATE TABLE projects (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    color      TEXT NOT NULL DEFAULT 'graphite',
    icon       TEXT,
    view_mode  TEXT NOT NULL DEFAULT 'list',
    owner_id   TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    is_inbox   INTEGER NOT NULL DEFAULT 0,
    archived   INTEGER NOT NULL DEFAULT 0,
    sort_order REAL NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted_at INTEGER,
    CHECK (view_mode IN ('list', 'board', 'calendar'))
);

CREATE INDEX idx_projects_owner ON projects (owner_id) WHERE deleted_at IS NULL;

-- Exactly one Inbox per user, enforced here rather than in application code.
CREATE UNIQUE INDEX idx_projects_one_inbox ON projects (owner_id) WHERE is_inbox = 1;

CREATE TABLE project_members (
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       TEXT NOT NULL,
    added_at   INTEGER NOT NULL,
    PRIMARY KEY (project_id, user_id),
    CHECK (role IN ('owner', 'editor', 'viewer'))
);

CREATE INDEX idx_project_members_user ON project_members (user_id);

CREATE TABLE sections (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    sort_order REAL NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted_at INTEGER
);

CREATE INDEX idx_sections_project ON sections (project_id) WHERE deleted_at IS NULL;

CREATE TABLE tasks (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    section_id      TEXT REFERENCES sections (id) ON DELETE SET NULL,
    parent_id       TEXT REFERENCES tasks (id) ON DELETE CASCADE,
    content         TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    priority        INTEGER NOT NULL DEFAULT 4,
    -- due_date is the calendar day ('YYYY-MM-DD'); due_datetime pins it to a moment
    -- when the task has a clock time. A timed task sets both, so day-based queries
    -- never have to do timezone maths.
    due_date        TEXT,
    due_datetime    INTEGER,
    due_timezone    TEXT,
    duration_min    INTEGER,
    recurrence_rule TEXT,
    assignee_id     TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_by      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    completed_at    INTEGER,
    completed_by    TEXT REFERENCES users (id) ON DELETE SET NULL,
    sort_order      REAL NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    deleted_at      INTEGER,
    -- A Danish transliteration of the searchable text, kept beside the original so
    -- full-text search can match either spelling. See tasks_fts below for why.
    fold            TEXT GENERATED ALWAYS AS (
                        replace(replace(replace(replace(replace(replace(
                            content || ' ' || description,
                            'ø', 'o'), 'Ø', 'o'),
                            'æ', 'ae'), 'Æ', 'ae'),
                            'å', 'aa'), 'Å', 'aa')
                    ) STORED,
    CHECK (priority BETWEEN 1 AND 4)
);

-- The three access patterns that carry the app: a project's open tasks, the Today
-- and Upcoming views, and "what is assigned to me".
CREATE INDEX idx_tasks_project ON tasks (project_id, sort_order)
    WHERE deleted_at IS NULL AND completed_at IS NULL;
CREATE INDEX idx_tasks_due ON tasks (due_date)
    WHERE deleted_at IS NULL AND completed_at IS NULL AND due_date IS NOT NULL;
CREATE INDEX idx_tasks_assignee ON tasks (assignee_id)
    WHERE deleted_at IS NULL AND completed_at IS NULL AND assignee_id IS NOT NULL;
CREATE INDEX idx_tasks_parent ON tasks (parent_id) WHERE parent_id IS NOT NULL;

CREATE TABLE labels (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    color      TEXT NOT NULL DEFAULT 'graphite',
    sort_order REAL NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX idx_labels_user_name ON labels (user_id, lower(name));

CREATE TABLE task_labels (
    task_id  TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    label_id TEXT NOT NULL REFERENCES labels (id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, label_id)
);

CREATE INDEX idx_task_labels_label ON task_labels (label_id);

-- A reminder is either absolute (remind_at) or relative to the task's due time
-- (offset_min, negative = before). Exactly one of the two.
CREATE TABLE reminders (
    id         TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    remind_at  INTEGER,
    offset_min INTEGER,
    sent_at    INTEGER,
    created_at INTEGER NOT NULL,
    CHECK ((remind_at IS NULL) <> (offset_min IS NULL))
);

CREATE INDEX idx_reminders_pending ON reminders (remind_at) WHERE sent_at IS NULL;

CREATE TABLE comments (
    id         TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted_at INTEGER
);

CREATE INDEX idx_comments_task ON comments (task_id) WHERE deleted_at IS NULL;

-- An attachment hangs on a task or on a comment, never both and never neither.
CREATE TABLE attachments (
    id          TEXT PRIMARY KEY,
    task_id     TEXT REFERENCES tasks (id) ON DELETE CASCADE,
    comment_id  TEXT REFERENCES comments (id) ON DELETE CASCADE,
    filename    TEXT NOT NULL,
    mime_type   TEXT NOT NULL DEFAULT 'application/octet-stream',
    size        INTEGER NOT NULL,
    path        TEXT NOT NULL,
    uploaded_by TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL,
    CHECK ((task_id IS NULL) <> (comment_id IS NULL))
);

CREATE INDEX idx_attachments_task ON attachments (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX idx_attachments_comment ON attachments (comment_id) WHERE comment_id IS NOT NULL;

CREATE TABLE activity (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    task_id      TEXT REFERENCES tasks (id) ON DELETE SET NULL,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    event        TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    created_at   INTEGER NOT NULL
);

CREATE INDEX idx_activity_project ON activity (project_id, created_at DESC);
CREATE INDEX idx_activity_task ON activity (task_id) WHERE task_id IS NOT NULL;

CREATE TABLE filters (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    query      TEXT NOT NULL,
    color      TEXT NOT NULL DEFAULT 'graphite',
    sort_order REAL NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX idx_filters_user ON filters (user_id);

-- Personal API tokens for scripts and integrations. Hashed, like every other token.
CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    prefix       TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    expires_at   INTEGER
);

CREATE INDEX idx_api_tokens_user ON api_tokens (user_id);

-- Full-text search over tasks. External-content FTS5: the index stores no copy of
-- the text, it points back into `tasks` by rowid, and the triggers below keep the
-- two in step. `UNINDEXED` columns are carried for filtering without being tokenized.
--
-- Three indexed columns, not two. FTS5's `remove_diacritics` strips combining marks,
-- and in Unicode "ø" and "æ" are not marked letters — they are letters in their own
-- right. Diacritic folding alone therefore leaves a Danish user unable to find
-- "grøn" by typing "gron". The generated `fold` column on `tasks` carries a
-- transliterated copy, and searching all three columns covers every spelling:
--
--     "grøn"  → content hit        "gron"   → fold hit
--     "århus" → content hit        "aarhus" → fold hit
--     "arhus" → content hit, because remove_diacritics folds the indexed å to a
--
CREATE VIRTUAL TABLE tasks_fts USING fts5 (
    content,
    description,
    fold,
    id UNINDEXED,
    project_id UNINDEXED,
    content = 'tasks',
    content_rowid = 'rowid',
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TRIGGER tasks_fts_insert AFTER INSERT ON tasks BEGIN
    INSERT INTO tasks_fts (rowid, content, description, fold, id, project_id)
    VALUES (new.rowid, new.content, new.description, new.fold, new.id, new.project_id);
END;

CREATE TRIGGER tasks_fts_delete AFTER DELETE ON tasks BEGIN
    INSERT INTO tasks_fts (tasks_fts, rowid, content, description, fold, id, project_id)
    VALUES ('delete', old.rowid, old.content, old.description, old.fold, old.id, old.project_id);
END;

CREATE TRIGGER tasks_fts_update AFTER UPDATE ON tasks BEGIN
    INSERT INTO tasks_fts (tasks_fts, rowid, content, description, fold, id, project_id)
    VALUES ('delete', old.rowid, old.content, old.description, old.fold, old.id, old.project_id);
    INSERT INTO tasks_fts (rowid, content, description, fold, id, project_id)
    VALUES (new.rowid, new.content, new.description, new.fold, new.id, new.project_id);
END;
