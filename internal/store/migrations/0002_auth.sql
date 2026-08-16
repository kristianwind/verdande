-- Sessions gain a two-step state, and TOTP gains a way back in.

-- A session created by a correct password but not yet by a correct TOTP code.
-- Such a session authenticates exactly one thing — completing the TOTP step — and
-- nothing else. Modelling the half-finished login as a session rather than as a
-- separate token means it expires, is listed, and can be revoked by the same code
-- that handles every other session.
ALTER TABLE sessions ADD COLUMN pending_totp INTEGER NOT NULL DEFAULT 0;

-- Single-use codes for someone who has lost their authenticator.
--
-- Without these, losing a phone means losing the account, and the only remaining
-- repair is editing SQLite by hand — which is not a recovery story you can put in
-- front of somebody who invited their partner to a shared shopping list.
CREATE TABLE totp_recovery_codes (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    used_at    INTEGER
);

CREATE INDEX idx_recovery_codes_user ON totp_recovery_codes (user_id) WHERE used_at IS NULL;
