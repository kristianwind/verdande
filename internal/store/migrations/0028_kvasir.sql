-- Kvasir: what an assistant did, and what it acted on.
--
-- Two things, and they are separate on purpose: one is about the outside world,
-- and would be worth having even if Kvasir never existed.

-- 1. Where a task came from, out there.
--
-- Nothing today can answer "has this mail already become a task?". A second
-- reader of the same mailbox — another assistant, a rerun of the same sweep,
-- a person — makes a second task, and the only sign is the duplicate itself.
-- That has happened in practice, twice in one run.
--
-- Namespaced rather than a bare id: `gmail:thread:18f2c…`, `imap:<host>:<uid>`,
-- `ics:<uid>`. A Gmail thread id and an IMAP uid look nothing alike until the
-- day they collide, and a namespace costs one colon. It also means the next
-- source needs no column — which is the point of a key rather than a foreign
-- key. There is no messages table to point at, and this has to work without one.
ALTER TABLE tasks ADD COLUMN source_key TEXT NOT NULL DEFAULT '';

-- The index is the interlock, not an optimisation. Two agents reading the same
-- inbox must be unable to make two tasks, and "must be unable" is a constraint
-- rather than a convention somebody remembers to follow.
--
-- Scoped to the author: two people on one instance connect their own mailboxes,
-- and the same thread reaching both of them is two tasks, correctly. Partial
-- because `created_by` is ON DELETE SET NULL — a task outlives its author, and
-- rows that have lost theirs must not start colliding with each other.
CREATE UNIQUE INDEX tasks_once_per_source
    ON tasks (created_by, source_key)
    WHERE source_key <> '' AND created_by IS NOT NULL;

-- 2. Kvasir's ledger.
--
-- The dial that lets an assistant act on its own is only bearable because of
-- this table. Two questions have to be answerable at any moment: what did it do
-- today, and how do I undo it. Neither is answerable from the tasks themselves.
--
-- A suggestion and a completed action are the SAME ROW in different states. That
-- is the whole design: "suggest" and "act" are not two code paths, they are one
-- path that writes a different `state`. Accepting a suggestion moves it to done;
-- undoing an action moves it to undone. Nothing has to be written twice, and the
-- feed reads the same either way.
--
-- `before_json` is the undo. Not a diff and not a description — the fields as
-- they were, so putting them back needs no reasoning about what changed. It is
-- also the honest record: a task edited by hand afterwards will not match, and
-- an undo that would overwrite newer work should say so rather than win.
CREATE TABLE kvasir_actions (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- 'file', 'rewrite', 'split', 'nudge', 'prioritise'. Free text rather than a
    -- CHECK: a new capability must not need a migration to be recorded, and an
    -- unknown kind in the feed is a row nobody rendered, not a broken database.
    kind        TEXT NOT NULL,
    task_id     TEXT REFERENCES tasks (id) ON DELETE CASCADE,

    -- One sentence, written when the action was decided. Stored rather than
    -- rebuilt: it says what Kvasir believed at the time, and the project it
    -- names may since have been renamed or deleted.
    summary     TEXT NOT NULL,
    -- Why it thought so. Shown on request, because a suggestion you cannot
    -- interrogate is one you either trust blindly or ignore entirely.
    reason      TEXT NOT NULL DEFAULT '',

    before_json TEXT NOT NULL DEFAULT '{}',
    after_json  TEXT NOT NULL DEFAULT '{}',

    -- suggested → done | dismissed, and done → undone.
    state       TEXT NOT NULL CHECK (state IN ('suggested', 'done', 'dismissed', 'undone')),

    created_at  INTEGER NOT NULL,
    decided_at  INTEGER NOT NULL DEFAULT 0
);

-- The feed asks "what is waiting for me, newest first", and the corrections
-- Kvasir learns from ask "what did I get wrong lately" — both read by user and
-- order by time.
CREATE INDEX kvasir_actions_by_user ON kvasir_actions (user_id, created_at DESC);
