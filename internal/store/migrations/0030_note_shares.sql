-- Sharing a note with a person, rather than only by filing it in a shared project.
--
-- Until now a note was shared exactly one way: put it in a project, and everyone
-- who could read the project could read the note. That is the right tool when the
-- note belongs with a body of work, and the wrong one when it is just "let Sofie
-- see this" — it makes you stand up a project to hand over a single page.
--
-- A note_shares row is that direct grant: this note, this person, this role. It
-- sits alongside the project path, not instead of it — a note can be loose, filed,
-- shared with people, or any combination, and access is the most that any of those
-- allow. Only the note's owner (its created_by) writes these rows.
--
-- role is 'viewer' or 'editor', the same words a project membership uses, so there
-- is one idea of what a role means in this program and not two.
CREATE TABLE note_shares (
    note_id    TEXT NOT NULL REFERENCES notes (id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'viewer',
    created_by TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,

    -- One share per person per note: sharing again changes the role, it does not
    -- add a second row that the first would then quietly disagree with.
    PRIMARY KEY (note_id, user_id)
);

-- "Which notes are shared with me" is asked on every visit to the notes page, to
-- build the Delt med mig group and to widen what the list may show. It is a lookup
-- by user_id, so that is what carries the index.
CREATE INDEX note_shares_by_user ON note_shares (user_id);
