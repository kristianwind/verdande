-- Snooze: put a task out of the way without finishing or filing it. A snoozed task
-- sinks to the bottom of its list and greys out until the time it is snoozed to,
-- then comes back on its own — no job wakes it, because "snoozed" is simply
-- snoozed_until being still in the future, read against the clock each time.
--
-- Null means not snoozed, which is every task until somebody snoozes one.
ALTER TABLE tasks ADD COLUMN snoozed_until INTEGER;
