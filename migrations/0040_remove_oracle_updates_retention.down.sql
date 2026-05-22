-- 0040 down — reinstate the 90-day retention policy on
-- `oracle_updates` (mirrors the 0003 original).
--
-- Reinstating retention does NOT delete already-preserved rows that
-- accumulated while 0040 was applied — the policy only schedules
-- future drops. An operator wanting to recover storage immediately
-- has to run drop_chunks() by hand.

BEGIN;

SELECT add_retention_policy('oracle_updates', INTERVAL '90 days');

COMMIT;
