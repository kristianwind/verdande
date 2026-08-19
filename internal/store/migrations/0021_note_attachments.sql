-- A note can carry files, the way a task and a group already can.
--
-- Apple Notes is full of photographs, scans and pasted screenshots, and a note
-- that arrives here as words alone is not that note — it is a summary of it. The
-- import writes the files as attachments and rewrites the links in the text, so a
-- picture that was in the middle of a paragraph is still in the middle of it.
--
-- Same table as everything else attaches to, rather than one of its own. The files
-- are content-addressed and deduplicated on disk, the upload path is written and
-- tested, and the cleanup that removes an orphan already walks this table. A
-- second table would be a second everything.

-- verdande:rebuild-tables

CREATE TABLE attachments_with_notes (
    id          TEXT PRIMARY KEY,
    task_id     TEXT REFERENCES tasks (id) ON DELETE CASCADE,
    comment_id  TEXT REFERENCES comments (id) ON DELETE CASCADE,
    group_id    TEXT REFERENCES project_groups (id) ON DELETE CASCADE,
    note_id     TEXT REFERENCES notes (id) ON DELETE CASCADE,
    filename    TEXT NOT NULL,
    mime_type   TEXT NOT NULL DEFAULT 'application/octet-stream',
    size        INTEGER NOT NULL,
    path        TEXT NOT NULL,
    uploaded_by TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL,
    -- Counted, not spelled out as pairs: with four columns the pairwise version is
    -- six clauses, and the one that is wrong looks exactly like the one that is right.
    CHECK ((task_id IS NOT NULL) + (comment_id IS NOT NULL)
         + (group_id IS NOT NULL) + (note_id IS NOT NULL) = 1)
);

INSERT INTO attachments_with_notes
    (id, task_id, comment_id, group_id, filename, mime_type, size, path, uploaded_by, created_at)
SELECT id, task_id, comment_id, group_id, filename, mime_type, size, path, uploaded_by, created_at
FROM attachments;

DROP TABLE attachments;

ALTER TABLE attachments_with_notes RENAME TO attachments;

CREATE INDEX idx_attachments_task ON attachments (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX idx_attachments_comment ON attachments (comment_id) WHERE comment_id IS NOT NULL;
CREATE INDEX idx_attachments_group ON attachments (group_id) WHERE group_id IS NOT NULL;
CREATE INDEX idx_attachments_note ON attachments (note_id) WHERE note_id IS NOT NULL;
