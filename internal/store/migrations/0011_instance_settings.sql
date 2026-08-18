-- Settings that belong to the instance rather than to a person.
--
-- The first of them is the Gmail OAuth client. It was configuration only —
-- `VERDANDE_GMAIL_CLIENT_ID` and `_SECRET` — which on a Rune means editing the
-- manifest and recreating the container to change a value you paste once and
-- never touch again. The registration itself cannot be avoided (Google issues no
-- Gmail access to an unregistered client, and `gmail.readonly` is a restricted
-- scope, so a client id shipped inside a public image would be both extractable
-- and unusable), but having to redeploy to enter it can.
--
-- Its own table rather than `user_settings`, for two reasons. An OAuth client is
-- the instance's, not one person's — everybody connecting their mailbox uses the
-- same registration. And `user_settings` has `user_id` as its whole primary key,
-- so it can hold exactly one scope per person and its upsert rewrites the scope
-- name on conflict; adding a second scope there would silently overwrite the AI
-- provider's key.
--
-- JSON for the same reason templates are: written whole, read whole, and it would
-- otherwise be a migration every time a setting is added.
CREATE TABLE instance_settings (
    scope       TEXT PRIMARY KEY,
    values_json TEXT NOT NULL DEFAULT '{}',
    updated_at  INTEGER NOT NULL
);
