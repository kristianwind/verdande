-- Passkeys: a key on a device instead of a secret in a head.
--
-- The account already has two ways in — a password, and a TOTP code on top of it.
-- Both are shared secrets: the server keeps something that can be stolen from it,
-- and the person keeps something that can be phished out of them. A passkey is
-- neither. The private half never leaves the device, the public half is all this
-- table holds, and the signature is bound to the origin — so a convincing copy of
-- the sign-in page at a different address gets nothing.
--
-- One row per credential rather than per account, because a person has more than
-- one device and losing a laptop should not mean losing the way in. The name is
-- theirs to write: "min bærbare" is what makes a list of keys reviewable, and a
-- list nobody can read is a list nobody revokes from.
--
-- `sign_count` is the authenticator's own counter. It is stored to be compared:
-- a counter that goes backwards means the credential has been cloned, which is
-- the one thing this design cannot otherwise detect. Not every authenticator keeps
-- one — the spec allows zero — so zero means "not offered" rather than "suspicious".
CREATE TABLE passkeys (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The credential id the authenticator generated, raw bytes, base64url as
    -- stored. Unique across the instance: it is what a login is looked up by,
    -- before anybody has said who they are.
    credential_id  TEXT NOT NULL,
    public_key     BLOB NOT NULL,
    -- The AAGUID identifies the authenticator model, which is what lets a list say
    -- "iCloud Keychain" beside a key somebody named three months ago and forgot.
    aaguid         TEXT NOT NULL DEFAULT '',
    sign_count     INTEGER NOT NULL DEFAULT 0,
    -- Whether the credential lives on the device itself (a resident key). Only
    -- those can start a login with no email typed first, so the interface has to
    -- know which it has before it offers that.
    discoverable   INTEGER NOT NULL DEFAULT 0,
    -- Whether the authenticator verified the person — a PIN, a fingerprint, a
    -- face. A key that only proves possession is a first factor; one that verified
    -- its owner is both, and that is what decides whether a password is still
    -- asked for.
    user_verified  INTEGER NOT NULL DEFAULT 0,
    name           TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    last_used_at   INTEGER
);

CREATE UNIQUE INDEX idx_passkeys_credential ON passkeys (credential_id);
CREATE INDEX idx_passkeys_user ON passkeys (user_id);

-- The challenge a registration or a login is answered against.
--
-- Kept server-side rather than in a cookie: the whole point of the challenge is
-- that the server chose it and remembers choosing it. One row per attempt, deleted
-- when it is answered, and swept when it expires — a challenge that outlives its
-- ceremony is a replay waiting to be tried.
--
-- user_id is null for a login that has not said who it is yet, which is the case
-- a discoverable credential exists for.
CREATE TABLE webauthn_challenges (
    id         TEXT PRIMARY KEY,
    user_id    TEXT REFERENCES users (id) ON DELETE CASCADE,
    challenge  TEXT NOT NULL,
    -- 'register' or 'login'. A challenge issued for one must not be answerable by
    -- the other: registering is done by somebody already signed in, and logging in
    -- is not.
    purpose    TEXT NOT NULL,
    -- The library's own session state, which carries the allowed credentials and
    -- the verification requirement it will check the answer against.
    session    BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    CHECK (purpose IN ('register', 'login'))
);

CREATE INDEX idx_webauthn_challenges_expiry ON webauthn_challenges (expires_at);
