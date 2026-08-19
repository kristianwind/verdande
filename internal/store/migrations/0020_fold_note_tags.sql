-- #garageristeriet and #GarageRisteriet are the same project.
--
-- They were not, because the key was stored exactly as typed and looked up the
-- same way — so a note that named a project in lower case simply did not appear
-- on it, silently and with nothing to suggest why.
--
-- Folded on the way in from now on; this brings the rows already written into
-- line. Only projects: a [[note title]] is a title, and two notes called "Møde"
-- and "møde" are two notes.
--
-- The primary key is (note_id, kind, target_id), so folding can collide with a row
-- that is already there — a note that wrote both spellings. INSERT OR IGNORE into
-- a fresh set and swap, which keeps one of each rather than failing on the pair.
CREATE TABLE note_links_folded (
    note_id     TEXT NOT NULL REFERENCES notes (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('task', 'note', 'project')),
    target_id   TEXT NOT NULL,
    PRIMARY KEY (note_id, kind, target_id)
);

INSERT OR IGNORE INTO note_links_folded (note_id, kind, target_id)
SELECT note_id, kind,
       CASE WHEN kind = 'project' THEN lower(target_id) ELSE target_id END
FROM note_links;

DROP TABLE note_links;
ALTER TABLE note_links_folded RENAME TO note_links;

CREATE INDEX note_links_backwards ON note_links (kind, target_id);
