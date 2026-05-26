-- Reset all 12 soroban-events backfill cursors so the re-walk
-- under the rc.80 back-pressure semantics produces a complete
-- soroban_events hypertable.
--
-- Context: the 2026-05-26 fill walk dropped ~18.86M rows of ~4.66B
-- (~0.40%) across all 12 chunks while the cursor advanced past
-- the dropped ledgers (rc.79 sink dropped on buffer-full without
-- holding back the cursor). rc.80 makes PushEvent block on a full
-- buffer so the cursor cannot outrun durable writes.
--
-- The hypertable's PK includes ledger_close_time so ON CONFLICT
-- DO NOTHING makes a re-walk idempotent over rows already
-- persisted from the prior run; rows previously dropped will land
-- this time.

BEGIN;

SELECT
  sub_source,
  last_ledger AS pre_reset_last_ledger
FROM ingestion_cursors
WHERE source = 'backfill'
  AND sub_source LIKE '%soroban-events'
ORDER BY sub_source;

DELETE FROM ingestion_cursors
WHERE source = 'backfill'
  AND sub_source LIKE '%soroban-events';

SELECT count(*) AS surviving_soroban_events_backfill_cursors
FROM ingestion_cursors
WHERE source = 'backfill'
  AND sub_source LIKE '%soroban-events';

COMMIT;
