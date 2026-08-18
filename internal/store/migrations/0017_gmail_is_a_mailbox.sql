-- Gmail's rows move in beside the ones read over IMAP.
--
-- Held back until now on purpose: this empties the settings row, and running it
-- while the handlers still read that row would have disconnected a working
-- mailbox. internal/httpapi now reads the mailboxes table, so the move is safe.
--
-- The values move across still sealed. Both tables seal with the same key and
-- store the same string, so copying the ciphertext verbatim is not a shortcut
-- around the encryption — it is the encryption, untouched. That also means this
-- migration does not need the key, which a migration has no business holding.
--
-- `username` is the address the account reports, and it is what makes the unique
-- index meaningful. It may be empty on rows that never got that far; two such rows
-- cannot exist for one person anyway, because the settings table could only hold
-- one.
INSERT INTO mailboxes (
    id, user_id, kind, name, host, username, password, folder,
    refresh_token, access_token, expires_at, label, trigger_kind, seen, project_id,
    last_uid, last_sync_at, created_at
)
SELECT
    lower(hex(randomblob(16))),
    s.user_id,
    'gmail',
    COALESCE(json_extract(s.values_json, '$.email'), 'Gmail'),
    '',
    COALESCE(json_extract(s.values_json, '$.email'), ''),
    '',
    'INBOX',
    COALESCE(json_extract(s.values_json, '$.refresh_token'), ''),
    COALESCE(json_extract(s.values_json, '$.access_token'), ''),
    COALESCE(json_extract(s.values_json, '$.expires_at'), 0),
    COALESCE(json_extract(s.values_json, '$.label'), ''),
    COALESCE(json_extract(s.values_json, '$.trigger'), 'starred'),
    COALESCE(json_extract(s.values_json, '$.seen'), '[]'),
    COALESCE(json_extract(s.values_json, '$.project_id'), ''),
    0,
    0,
    unixepoch()
FROM user_settings s
WHERE s.scope = 'gmail'
  AND COALESCE(json_extract(s.values_json, '$.refresh_token'), '') <> '';

-- Emptied, not deleted: the row may hold nothing else today, but the table is
-- shared and a DELETE here would be a habit worth not forming.
UPDATE user_settings SET values_json = '{}' WHERE scope = 'gmail';
