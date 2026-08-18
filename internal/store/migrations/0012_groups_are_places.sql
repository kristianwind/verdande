-- A group becomes somewhere you can go, rather than only a heading.
--
-- Until now a project group was a label over some rows in the sidebar: a name, a
-- colour, and whether it was folded. That is enough to tidy a list and not enough
-- to be the thing people actually mean by it — "Arbejde" is a body of work with
-- context of its own: what it is, and the documents that belong to all of it
-- rather than to any one project inside it.
--
-- Two changes. A description on the group, and attachments that can hang on one.
--
-- The attachments table has to be rebuilt for the second, because its CHECK spells
-- out exactly which parent columns may be set, and SQLite cannot alter a CHECK.
--
-- Foreign keys go off for the length of it — see the marker below and the note in
-- `applyMigration`. Not because the drop would cascade: nothing references
-- `attachments`, so it takes nothing with it. It is for the check that comes with
-- the marker. With foreign keys on, the copy is validated a row at a time against
-- a half-rebuilt schema and the first bad row aborts with no idea how many others
-- there are; with them off, `PRAGMA foreign_key_check` runs once before the commit
-- and names what is actually wrong. This runs against a live database on somebody's
-- next restart, and an attachment whose task was already gone would otherwise stop
-- the server from starting with one row's worth of explanation.
--
-- The rule stays "exactly one parent": an attachment belongs to a task, a comment
-- or a group, never two. An attachment with two parents is one that gets deleted
-- once and stays visible in the other place.
--
-- verdande:rebuild-tables

ALTER TABLE project_groups ADD COLUMN description TEXT NOT NULL DEFAULT '';

CREATE TABLE attachments_rebuilt (
    id          TEXT PRIMARY KEY,
    task_id     TEXT REFERENCES tasks (id) ON DELETE CASCADE,
    comment_id  TEXT REFERENCES comments (id) ON DELETE CASCADE,
    group_id    TEXT REFERENCES project_groups (id) ON DELETE CASCADE,
    filename    TEXT NOT NULL,
    mime_type   TEXT NOT NULL DEFAULT 'application/octet-stream',
    size        INTEGER NOT NULL,
    path        TEXT NOT NULL,
    uploaded_by TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL,
    -- Exactly one parent, counted rather than spelled out as a chain of pairwise
    -- comparisons: with three columns that is three clauses, with four it is six,
    -- and the version that is wrong looks exactly like the version that is right.
    CHECK ((task_id IS NOT NULL) + (comment_id IS NOT NULL) + (group_id IS NOT NULL) = 1)
);

INSERT INTO attachments_rebuilt
    (id, task_id, comment_id, filename, mime_type, size, path, uploaded_by, created_at)
SELECT id, task_id, comment_id, filename, mime_type, size, path, uploaded_by, created_at
FROM attachments;

DROP TABLE attachments;

ALTER TABLE attachments_rebuilt RENAME TO attachments;

CREATE INDEX idx_attachments_task ON attachments (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX idx_attachments_comment ON attachments (comment_id) WHERE comment_id IS NOT NULL;
CREATE INDEX idx_attachments_group ON attachments (group_id) WHERE group_id IS NOT NULL;
