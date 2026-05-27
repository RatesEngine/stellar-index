-- 0046 up — add `first_ledger` to ingestion_cursors (100% density mission).
--
-- The density-coverage projection in /v1/diagnostics/ingestion unions
-- completed portions of backfill-cursor ranges. Pre-fix it credited
-- only the [from, last_ledger] span parsed out of each backfill
-- cursor's sub_source — and the live ledgerstream cursor, which has
-- sub_source = '' and only stores `last_ledger`, contributed only its
-- head-band [backfill_top, last_ledger] via extendWithLiveTail. The
-- live cursor's actual coverage span is [first_ledger, last_ledger]
-- — the first ledger this region's live indexer ever saw — but pre-
-- fix we never persisted it. Result: even at perfect ingestion the
-- live-only band [genesis, backfill_top] could only be credited via
-- backfill, so density capped at ~98% for sources where backfill
-- and live tail didn't quite meet (project_density_100pct_goal).
--
-- The fix:
--
--   1. Add a `first_ledger` column to ingestion_cursors. Nullable
--      so the column rollout is non-blocking: existing rows survive
--      the migration without a value, and the density calc falls
--      back to sourceGenesisLedger when first_ledger IS NULL for a
--      live cursor (matches the granular-genesis policy in
--      project_density_genesis_precision).
--
--   2. Backfill the column for EXISTING backfill cursors by parsing
--      the `from` integer out of their sub_source string — every
--      backfill cursor has sub_source = "<from>-<to>:<decoder-set>"
--      (e.g. "50500000-53174999:soroswap"). split_part on '-' picks
--      out the first piece; ::integer casts. Done as part of the
--      migration so post-deploy /v1/diagnostics/ingestion immediately
--      reflects the new column without waiting for backfill workers
--      to re-write their cursors.
--
--   3. Live cursors (source='ledgerstream', sub_source='') are left
--      with first_ledger=NULL — the live indexer populates it on
--      its NEXT cursor write via UpsertCursor's INSERT branch, and
--      until then the density calc applies the sourceGenesisLedger
--      fallback. Both paths give the right answer; the NULL state
--      is a transient mid-rollout window.

BEGIN;

ALTER TABLE ingestion_cursors
    ADD COLUMN first_ledger integer;

COMMENT ON COLUMN ingestion_cursors.first_ledger IS
    'Earliest ledger this cursor''s range covers. For backfill cursors '
    'this is the `from` end of the assigned range (parsed from '
    'sub_source). For the live ledgerstream cursor it is the first '
    'ledger the live indexer ingested in this region. NULL on cursors '
    'that pre-date migration 0046; the density-coverage projection '
    'falls back to sourceGenesisLedger for NULL live cursors.';

-- Backfill the column for every existing backfill cursor. We can
-- parse the start integer out of sub_source deterministically —
-- every backfill cursor sub_source is "<int>-<int>:<decoders>" by
-- construction (internal/pipeline). split_part(..., '-', 1) picks
-- the first piece; the ::integer cast fails loudly if a malformed
-- row ever sneaks in (better than silently writing 0).
--
-- The regexp guard skips any row whose sub_source doesn't match the
-- expected shape — defensive against pre-existing rows we don't
-- want to drop. last_ledger stays untouched for those rows; density
-- math handles a NULL first_ledger gracefully (see godoc above).
UPDATE ingestion_cursors
   SET first_ledger = split_part(sub_source, '-', 1)::integer
 WHERE source = 'backfill'
   AND sub_source ~ '^[0-9]+-[0-9]+:.+$';

COMMIT;
