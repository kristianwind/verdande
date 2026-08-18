-- Mailboxes: one row per connected mailbox, not one setting per person.
--
-- Gmail lived in `user_settings` under scope 'gmail'. That table has `user_id` as
-- its whole primary key, so it holds exactly one scope per person and its upsert
-- rewrites the scope name on conflict — adding IMAP beside Gmail there would have
-- deleted the Gmail connection without saying so. Migration 0011 warns about this
-- in as many words, and the warning was nearly walked into a second time.
--
-- The shape is wrong for the thing anyway. A person has mailboxes, plural: a work
-- Gmail and a private iCloud is the ordinary case, not the exotic one. A row each
-- says that. It also answers the question that prompted this — every mailbox
-- belongs to a user, so two people on the same instance connect their own and see
-- only their own. The one piece that stays instance-wide is the Google OAuth
-- client, which is a single registration everybody signs in through, and that is
-- correct: it is the app, not the account.
--
-- `kind` rather than two tables. What differs is how the credential is obtained —
-- OAuth for Gmail, a password for IMAP — and what differs is a nullable column,
-- not a schema. What is the same is everything the sync loop touches: which
-- mailbox, how far it has read, when it last ran.
--
-- `password` and `refresh_token` are written sealed; see internal/secret. The
-- store's own seal/unseal only covers the settings tables, so this table's methods
-- call them by hand — a fact worth knowing before adding a third secret column.
--
-- `last_uid` is IMAP's place-marker and means nothing for Gmail, which keeps its
-- own by query. Zero is "nothing read yet" for both.
CREATE TABLE mailboxes (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('gmail', 'imap')),
    name          TEXT NOT NULL DEFAULT '',

    -- IMAP
    host          TEXT NOT NULL DEFAULT '',
    username      TEXT NOT NULL DEFAULT '',
    password      TEXT NOT NULL DEFAULT '',
    folder        TEXT NOT NULL DEFAULT 'INBOX',

    -- Gmail
    refresh_token TEXT NOT NULL DEFAULT '',
    access_token  TEXT NOT NULL DEFAULT '',
    expires_at    INTEGER NOT NULL DEFAULT 0,
    label         TEXT NOT NULL DEFAULT '',

    last_uid      INTEGER NOT NULL DEFAULT 0,
    last_sync_at  INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

-- The sweep asks "whose mailboxes are due", so it reads by user.
CREATE INDEX mailboxes_by_user ON mailboxes (user_id);

-- The same mailbox twice is a mistake, not a choice: two rows would make two
-- tasks out of every mail. Gmail's uniqueness is the account, IMAP's is the host
-- and the username together, and `kind` keeps the two from colliding.
CREATE UNIQUE INDEX mailboxes_once ON mailboxes (user_id, kind, host, username);
