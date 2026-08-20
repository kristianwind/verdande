-- One row per person *and scope*, which is what the table always meant.
--
-- `user_settings` was created with `user_id` as its whole primary key, and its
-- upsert rewrote `scope` on conflict. So a person had exactly one settings row,
-- and whichever scope was written last was the only one that survived: saving an
-- AI provider deleted the Gmail connection, and connecting a calendar deleted the
-- AI provider. Nothing reported it — the write succeeded, and the loss was in the
-- row it landed on.
--
-- This was seen coming twice. Migration 0011 gave the instance's settings their
-- own table partly for this reason and wrote the warning down in as many words;
-- 0014 moved mailboxes out and said the warning "was nearly walked into a second
-- time". The third time was the calendar, which added a `calendar` scope beside
-- `ai` and `gmail` — and walked in.
--
-- The lesson worth keeping: a warning in a migration is read by whoever is editing
-- *that* migration, and nobody was. The shape is fixed here so there is nothing
-- left to warn about.
--
-- SQLite cannot change a primary key in place, so the table is rebuilt. Existing
-- rows are copied as they are: each person has at most one, and it holds whatever
-- scope won last. What the earlier writes lost is already gone and cannot be
-- recovered here — the values were overwritten, not orphaned.
CREATE TABLE user_settings_new (
    user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    scope       TEXT NOT NULL DEFAULT 'general',
    values_json TEXT NOT NULL DEFAULT '{}',
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (user_id, scope)
);

INSERT INTO user_settings_new (user_id, scope, values_json, updated_at)
SELECT user_id, scope, values_json, updated_at FROM user_settings;

DROP TABLE user_settings;

ALTER TABLE user_settings_new RENAME TO user_settings;
