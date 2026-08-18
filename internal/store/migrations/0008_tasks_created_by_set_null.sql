-- `tasks.created_by` stops cascading.
--
-- It was ON DELETE CASCADE, which made deleting an account delete every task that
-- account ever wrote — including in projects somebody else owns. Retiring a
-- colleague therefore removed their contributions from shared work rather than
-- leaving it behind. It is the last remaining way to lose other people's work by
-- accident, and the administrator's page saying so in plain words is a warning, not
-- a fix.
--
-- SET NULL instead: the task stays and loses its author. `created_by` becomes
-- nullable to make that expressible, which is the same shape `assignee_id` and
-- `completed_by` already have for the same reason.
--
-- SQLite cannot alter a foreign key action, so the table is rebuilt. The FTS
-- triggers come along: they are dropped first so that DROP TABLE and ALTER TABLE
-- see a schema they can parse, and recreated below. `tasks_fts` is an
-- external-content table keyed on `tasks.rowid`, and a rebuilt table hands out new
-- rowids, so the index is rebuilt at the end rather than left pointing at rows that
-- have moved.
--
-- verdande:rebuild-tables

DROP TRIGGER tasks_fts_insert;
DROP TRIGGER tasks_fts_delete;
DROP TRIGGER tasks_fts_update;

CREATE TABLE tasks_rebuilt (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    section_id      TEXT REFERENCES sections (id) ON DELETE SET NULL,
    parent_id       TEXT REFERENCES tasks (id) ON DELETE CASCADE,
    content         TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    priority        INTEGER NOT NULL DEFAULT 4,
    due_date        TEXT,
    due_datetime    INTEGER,
    due_timezone    TEXT,
    duration_min    INTEGER,
    recurrence_rule TEXT,
    assignee_id     TEXT REFERENCES users (id) ON DELETE SET NULL,
    -- Nullable, and SET NULL: an account can go without its work going with it.
    created_by      TEXT REFERENCES users (id) ON DELETE SET NULL,
    completed_at    INTEGER,
    completed_by    TEXT REFERENCES users (id) ON DELETE SET NULL,
    sort_order      REAL NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    deleted_at      INTEGER,
    fold            TEXT GENERATED ALWAYS AS (
                        replace(replace(replace(replace(replace(replace(
                            content || ' ' || description,
                            'ø', 'o'), 'Ø', 'o'),
                            'æ', 'ae'), 'Æ', 'ae'),
                            'å', 'aa'), 'Å', 'aa')
                    ) STORED,
    CHECK (priority BETWEEN 1 AND 4)
);

INSERT INTO tasks_rebuilt
    (id, project_id, section_id, parent_id, content, description, priority,
     due_date, due_datetime, due_timezone, duration_min, recurrence_rule,
     assignee_id, created_by, completed_at, completed_by, sort_order,
     created_at, updated_at, deleted_at)
SELECT
     id, project_id, section_id, parent_id, content, description, priority,
     due_date, due_datetime, due_timezone, duration_min, recurrence_rule,
     assignee_id, created_by, completed_at, completed_by, sort_order,
     created_at, updated_at, deleted_at
FROM tasks;

DROP TABLE tasks;

ALTER TABLE tasks_rebuilt RENAME TO tasks;

CREATE INDEX idx_tasks_project ON tasks (project_id, sort_order)
    WHERE deleted_at IS NULL AND completed_at IS NULL;
CREATE INDEX idx_tasks_due ON tasks (due_date)
    WHERE deleted_at IS NULL AND completed_at IS NULL AND due_date IS NOT NULL;
CREATE INDEX idx_tasks_assignee ON tasks (assignee_id)
    WHERE deleted_at IS NULL AND completed_at IS NULL AND assignee_id IS NOT NULL;
CREATE INDEX idx_tasks_parent ON tasks (parent_id) WHERE parent_id IS NOT NULL;

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

-- The rowids the index was built against are gone with the old table.
INSERT INTO tasks_fts (tasks_fts) VALUES ('rebuild');
