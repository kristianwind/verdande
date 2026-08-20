-- Calendars read from somewhere else: the account, the calendars in it, and the
-- events they hold.
--
-- Not in `mailboxes`, and it was the first thing considered. The two look alike
-- from a distance — a person connects a Google account over OAuth, tokens are
-- sealed, a sweep polls it — and every column past that disagrees. `trigger_kind`,
-- `seen`, `last_uid` and `project_id` all answer "what turns a message into a
-- task", and nothing here turns into a task. What is here instead is a *set*:
-- one account holds many calendars, and a person picks which of them to look at.
-- `mailboxes` has no room for a one-to-many, and giving it one would push the
-- other kind's columns onto rows that can never use them.
--
-- The decisive part is smaller and more practical. `mailboxes.kind` carries
-- `CHECK (kind IN ('gmail', 'imap'))`, and SQLite cannot alter a CHECK — adding a
-- third value means rebuilding the table with foreign keys off, which is the
-- expensive, careful thing migration 0021 had to do for a reason. Paying that to
-- store something that shares no column with the table is the wrong trade.
--
-- Hence no CHECK on `provider` here. CalDAV is the next source to read, and it
-- should cost a row, not a rebuild.

-- One connected account per person per provider. The tokens live here rather than
-- on each calendar: a refresh token copied onto ten rows is ten copies of one
-- secret, ten refreshes racing each other, and ten places to miss when access is
-- revoked.
--
-- `refresh_token` and `access_token` are written sealed; see internal/secret. The
-- store's own seal/unseal only covers the settings tables, so this table's methods
-- call them by hand — the same fact worth knowing that migration 0014 wrote down.
CREATE TABLE calendar_accounts (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider      TEXT NOT NULL DEFAULT 'google',
    -- The address the account reports, so the settings page can say which one is
    -- connected rather than only that one is.
    account       TEXT NOT NULL DEFAULT '',

    refresh_token TEXT NOT NULL DEFAULT '',
    access_token  TEXT NOT NULL DEFAULT '',
    expires_at    INTEGER NOT NULL DEFAULT 0,

    last_sync_at  INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

-- Connecting again replaces what was there, the way Gmail's does: the OAuth flow
-- is one account at a time, so a second row for the same provider would be a
-- second set of tokens for the same grant and no way to say which is live.
CREATE UNIQUE INDEX calendar_accounts_once ON calendar_accounts (user_id, provider);

-- One row per calendar the account can see, kept whether or not it is shown.
--
-- `shown` rather than deleting the ones that are not: a person who turns a calendar
-- off and on again should get the same row back, and the list is refreshed from
-- Google on every sync — so a calendar that only existed while it was ticked would
-- lose its choice every time somebody added a new one.
--
-- `colour` is Google's own hex for the calendar. Not one of verdande's ten named
-- colours, which is the rule everywhere else in this database: those are names so
-- the interface can restyle them per theme, and they are *verdande's* palette. A
-- Google calendar is told apart by the colour the person already knows it by, and
-- that colour belongs to Google.
CREATE TABLE calendars (
    id         TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES calendar_accounts (id) ON DELETE CASCADE,
    -- The id Google knows it by. Long, and an address for the primary calendar.
    remote_id  TEXT NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    colour     TEXT NOT NULL DEFAULT '',
    time_zone  TEXT NOT NULL DEFAULT '',
    primary_one INTEGER NOT NULL DEFAULT 0,
    writable   INTEGER NOT NULL DEFAULT 0,
    shown      INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX calendars_once ON calendars (account_id, remote_id);

-- The events themselves, cached.
--
-- Cached rather than fetched per view, and the reason is what a month grid does:
-- it pages. Asking Google on every arrow press means a token refresh, a round trip
-- and a spinner for a question that was answered ninety seconds ago — and a
-- calendar that is slower than the calendar it is showing is not worth having.
-- The window is refreshed by the sweep and by the "fetch now" button.
--
-- Days rather than instants, and both ends inclusive. A grid asks one question —
-- "which cells does this cover" — and `start_day <= ? AND end_day >= ?` is that
-- question. `starts_at` is kept as Google wrote it, offset and all, because it is
-- what the chip shows and reparsing it here would ask *this process* what time
-- zone it is in. An all-day event has neither: it has a day, not a moment, and
-- inventing midnight for it is how an event moves a day when a container moves
-- time zone.
CREATE TABLE calendar_events (
    id          TEXT PRIMARY KEY,
    calendar_id TEXT NOT NULL REFERENCES calendars (id) ON DELETE CASCADE,
    remote_id   TEXT NOT NULL,
    summary     TEXT NOT NULL DEFAULT '',
    starts_at   TEXT NOT NULL DEFAULT '',
    ends_at     TEXT NOT NULL DEFAULT '',
    start_day   TEXT NOT NULL,
    end_day     TEXT NOT NULL,
    all_day     INTEGER NOT NULL DEFAULT 0,
    location    TEXT NOT NULL DEFAULT '',
    url         TEXT NOT NULL DEFAULT ''
);

-- The one query there is: everything a person can see, between two days. Ordered
-- by the account so the join has somewhere to start, then by the days the grid
-- compares against.
CREATE INDEX calendar_events_by_day ON calendar_events (calendar_id, start_day, end_day);
