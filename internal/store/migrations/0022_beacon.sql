-- One row per installation that has ever reported in.
--
-- This table only exists on the instance that is acting as the collector. Every
-- other instance has it too, empty, because the schema is the same everywhere —
-- and because "am I the collector" is a setting, not a build.
--
-- What is deliberately NOT here is the point of the whole feature: there is no
-- column for an IP address, a hostname, a domain, an account, a count of tasks,
-- or anything else. The two things stored are the two things sent, and the table
-- is the promise written down where it cannot drift from the prose — a column
-- added here later is a promise broken, and it should be as awkward to add as
-- possible.
--
-- `instance_id` is the primary key rather than a rowid alias, because the same
-- installation reporting daily must update its row rather than grow a new one.
-- What is wanted is "how many installations", not "how many pings".
CREATE TABLE beacon_installs (
    instance_id TEXT PRIMARY KEY,
    version     TEXT NOT NULL DEFAULT '',
    first_seen  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL
);

-- The count is always "seen in the last N days", so last_seen is what every query
-- sorts and filters on.
CREATE INDEX beacon_by_last_seen ON beacon_installs (last_seen DESC);
