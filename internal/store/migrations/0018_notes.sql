-- Notes.
--
-- Markdown in a column, not rich text and not a format of our own. Export was a
-- requirement before a line was written, and if the stored form *is* the exported
-- form there is no conversion left to lose anything in — a note stays readable by
-- any editor on the day this program is gone. It is also what makes full-text
-- search and anything an AI does over notes cheap: both read text.
--
-- Under a project rather than in a folder tree of its own. Projects, groups and
-- labels already exist and are already shared, already have roles, already have a
-- trash. A second hierarchy to file things in is one too many, and a note that
-- belongs to a project inherits every one of those answers instead of needing its
-- own. A note with no project is loose, the way a task in the inbox is.
--
-- `deleted_at` and `created_by` follow tasks exactly, including SET NULL: an
-- account can be removed without taking somebody else's notes with it.
CREATE TABLE notes (
    id          TEXT PRIMARY KEY,
    project_id  TEXT REFERENCES projects (id) ON DELETE CASCADE,
    title       TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL DEFAULT '',
    created_by  TEXT REFERENCES users (id) ON DELETE SET NULL,
    pinned      INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    deleted_at  INTEGER,

    -- The same Danish transliteration as tasks carry, and for the same reason:
    -- unicode61 folds diacritics, so "grøn" is findable as "gron", but it does
    -- nothing for æ→ae or å→aa, which is how Danes actually type when the keyboard
    -- is not theirs.
    fold        TEXT GENERATED ALWAYS AS (
                    replace(replace(replace(replace(replace(replace(
                        title || ' ' || body,
                        'ø', 'o'), 'Ø', 'o'),
                        'æ', 'ae'), 'Æ', 'ae'),
                        'å', 'aa'), 'Å', 'aa')
                ) STORED
);

CREATE INDEX notes_by_project ON notes (project_id, deleted_at, updated_at DESC);

-- What a note points at: a task, another note, a project.
--
-- Derived from the body every time it is saved, never edited directly. The text
-- stays the truth; this is an index over it — which is what makes the question
-- worth asking backwards. "What refers to this task" must not mean searching every
-- note's prose, or the panel that shows it would be too slow to leave open.
--
-- `target_id` is deliberately not a foreign key. A link to something that has been
-- deleted is a fact about the note, and it should read as a dead link rather than
-- vanish or block the delete.
CREATE TABLE note_links (
    note_id     TEXT NOT NULL REFERENCES notes (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('task', 'note', 'project')),
    target_id   TEXT NOT NULL,
    PRIMARY KEY (note_id, kind, target_id)
);

CREATE INDEX note_links_backwards ON note_links (kind, target_id);

CREATE VIRTUAL TABLE notes_fts USING fts5 (
    title,
    body,
    fold,
    id UNINDEXED,
    project_id UNINDEXED,
    content = 'notes',
    content_rowid = 'rowid',
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TRIGGER notes_fts_insert AFTER INSERT ON notes BEGIN
    INSERT INTO notes_fts (rowid, title, body, fold, id, project_id)
    VALUES (new.rowid, new.title, new.body, new.fold, new.id, new.project_id);
END;

CREATE TRIGGER notes_fts_delete AFTER DELETE ON notes BEGIN
    INSERT INTO notes_fts (notes_fts, rowid, title, body, fold, id, project_id)
    VALUES ('delete', old.rowid, old.title, old.body, old.fold, old.id, old.project_id);
END;

CREATE TRIGGER notes_fts_update AFTER UPDATE ON notes BEGIN
    INSERT INTO notes_fts (notes_fts, rowid, title, body, fold, id, project_id)
    VALUES ('delete', old.rowid, old.title, old.body, old.fold, old.id, old.project_id);
    INSERT INTO notes_fts (rowid, title, body, fold, id, project_id)
    VALUES (new.rowid, new.title, new.body, new.fold, new.id, new.project_id);
END;
