-- `activity.user_id` stops cascading, and the log gets an index it can be read by.
--
-- Two changes with the same cause: there is now an instance-wide audit log, and
-- the table it reads was only ever built to answer "what happened in this
-- project".
--
-- The cascade first. An audit log whose rows disappear when the account they
-- describe is deleted answers "what has happened on this server" with everything
-- except the part somebody had a reason to remove. That is the wrong answer to the
-- question, and it is the same mistake migration 0008 fixed on `tasks.created_by`:
-- the record outlives the person. SET NULL, and the reader shows "en slettet
-- konto" rather than a name.
--
-- `project_id` keeps cascading on purpose. A project purged from the trash is
-- genuinely gone — the trash is the undo, and past it the deletion was deliberate
-- and complete. Activity has its own retention window besides.
--
-- Then the index. `idx_activity_project` leads with project_id, so a query that
-- wants the newest rows across every project cannot use it and scans the table to
-- sort it. The new one is ordered the way the audit log reads, and the compound
-- (created_at, id) matches the keyset the handler pages with — two rows written in
-- the same second must not be able to hide each other across a page boundary.
--
-- verdande:rebuild-tables

CREATE TABLE activity_rebuilt (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    task_id      TEXT REFERENCES tasks (id) ON DELETE SET NULL,
    -- Nullable: the record of what was done outlives whoever did it.
    user_id      TEXT REFERENCES users (id) ON DELETE SET NULL,
    event        TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    created_at   INTEGER NOT NULL
);

INSERT INTO activity_rebuilt (id, project_id, task_id, user_id, event, payload_json, created_at)
SELECT id, project_id, task_id, user_id, event, payload_json, created_at FROM activity;

DROP TABLE activity;

ALTER TABLE activity_rebuilt RENAME TO activity;

CREATE INDEX idx_activity_project ON activity (project_id, created_at DESC);
CREATE INDEX idx_activity_task ON activity (task_id) WHERE task_id IS NOT NULL;
CREATE INDEX idx_activity_recent ON activity (created_at DESC, id DESC);
