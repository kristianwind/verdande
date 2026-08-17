-- Notifications, Web Push, and the mail-to-task address.

-- What a person needs to be told about: an assignment, a comment on something they
-- are in, an invite accepted. Stored rather than only pushed, so the bell icon has
-- a history and a notification survives the browser being shut.
CREATE TABLE notifications (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    actor_id     TEXT REFERENCES users (id) ON DELETE SET NULL,
    project_id   TEXT REFERENCES projects (id) ON DELETE CASCADE,
    task_id      TEXT REFERENCES tasks (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    title        TEXT NOT NULL,
    body         TEXT NOT NULL DEFAULT '',
    read_at      INTEGER,
    created_at   INTEGER NOT NULL
);

CREATE INDEX idx_notifications_user ON notifications (user_id, created_at DESC);
CREATE INDEX idx_notifications_unread ON notifications (user_id) WHERE read_at IS NULL;

-- Web Push (RFC 8030) subscriptions. One row per browser per device: the same
-- person signed in on a laptop and a phone has two, and revoking one must not
-- silence the other.
CREATE TABLE push_subscriptions (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    endpoint     TEXT NOT NULL UNIQUE,
    p256dh       TEXT NOT NULL,
    auth         TEXT NOT NULL,
    user_agent   TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    -- A push service returns 404 or 410 when a subscription is dead. Counting the
    -- failures lets a flaky network be forgiven while a genuinely gone endpoint is
    -- eventually dropped.
    failures     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_push_user ON push_subscriptions (user_id);

-- The VAPID keypair identifying this instance to push services. One row, ever.
-- Generated on first use rather than configured: it is not a secret an operator
-- should have to produce, and rotating it only costs re-subscribing.
CREATE TABLE instance_keys (
    id          TEXT PRIMARY KEY,
    public_key  TEXT NOT NULL,
    private_key TEXT NOT NULL,
    created_at  INTEGER NOT NULL
);

-- The per-user address that turns an email into a task: todo+<token>@domain.
ALTER TABLE users ADD COLUMN mail_token TEXT;

CREATE UNIQUE INDEX idx_users_mail_token ON users (mail_token) WHERE mail_token IS NOT NULL;

-- Settings that belong to a person rather than to the instance: which AI provider
-- they use and with whose key, what their Gmail connection is. Kept as JSON for
-- the same reason templates are — written whole, read whole, and it would
-- otherwise be a migration every time an integration gains an option.
CREATE TABLE user_settings (
    user_id     TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    scope       TEXT NOT NULL DEFAULT 'general',
    values_json TEXT NOT NULL DEFAULT '{}',
    updated_at  INTEGER NOT NULL
);
