-- A note can be put away without being thrown away.
--
-- The trash already exists and is the wrong tool. It says "this was a mistake,
-- and in thirty days it is gone" — so anything worth keeping cannot go in it, and
-- an archive of twelve hundred imported notes is exactly the case where most of
-- them are worth keeping and almost none are worth looking at. Without somewhere
-- to put them, the list is the whole archive forever, and finding this week's note
-- means reading past a decade.
--
-- A column rather than a folder. Verdande already refused a second hierarchy for
-- notes — see 0018 — and archiving is not filing: it is one bit about whether a
-- note is current, and it has to be answerable next to `deleted_at`, which is the
-- other bit of the same kind.
--
-- Nullable timestamp rather than a boolean, for the same reason `deleted_at` is
-- one: "when was this put away" answers questions a flag cannot, and it costs the
-- same byte.
ALTER TABLE notes ADD COLUMN archived_at INTEGER;

-- The list asks "not deleted, not archived, newest first" on every load, and with
-- twelve hundred notes that is the query that has to be quick.
CREATE INDEX notes_current ON notes (archived_at, deleted_at, updated_at DESC)
    WHERE deleted_at IS NULL;
