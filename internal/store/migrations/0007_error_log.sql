-- Server errors, kept where they can outlive the container that produced them.
--
-- verdande already logs the real cause of every 500 to stdout. On a Rune that is
-- the container's log, and every restart starts a new one — so the panel can
-- report "HTTP 5xx, twice, at 11:49" long after the line explaining it has gone.
-- An operator is then told that something broke and given no way to find out what.
--
-- Bounded by the sweeper rather than by size: this is a diagnostic, and one that
-- grows without limit is a disk that fills for a reason nobody expected.
CREATE TABLE error_log (
    id         TEXT PRIMARY KEY,
    at         INTEGER NOT NULL,
    method     TEXT NOT NULL,
    path       TEXT NOT NULL,
    status     INTEGER NOT NULL,
    -- what: the operation the handler was attempting, in the words it uses in the
    -- log — "list projects", "move task". The detail a stack trace would give and
    -- a status code cannot.
    what       TEXT NOT NULL,
    message    TEXT NOT NULL,
    -- Whose request it was, so a fault that only one account hits is visible as
    -- one. NULL for anything that failed before authentication.
    user_id    TEXT REFERENCES users (id) ON DELETE SET NULL,
    request_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_error_log_at ON error_log (at DESC);
