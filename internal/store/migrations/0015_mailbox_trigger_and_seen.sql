-- Room for Gmail beside the others.
--
-- Only the two columns. Moving the data is a separate migration on purpose: the
-- Gmail handlers still read user_settings, and a migration that emptied that table
-- before they were rewritten would disconnect a working mailbox. Additive first,
-- and the move when the code on the other side is ready for it.
--
-- Gmail lived in `user_settings` under scope 'gmail' because it was the only
-- integration of its kind. Now there are two, and two places to look for the same
-- question — which mailboxes does this person have — is one place too many.
--
-- `seen` is Gmail's own deduplication: a list of message ids it has already turned
-- into tasks. IMAP does not need it, because uids are monotonic and `last_uid`
-- answers the same question in one number. Gmail's query is a 30-day window rather
-- than a marker, so the list is what it has.
ALTER TABLE mailboxes ADD COLUMN seen TEXT NOT NULL DEFAULT '[]';

-- What makes a mail into a task: 'starred', or 'label' with the name in `label`.
-- Its own column rather than packed into `label` with a separator — a column that
-- holds two things joined by a colon is a column somebody parses wrongly later,
-- and a label named with a colon in it would do it on the first day.
ALTER TABLE mailboxes ADD COLUMN trigger_kind TEXT NOT NULL DEFAULT 'starred';
