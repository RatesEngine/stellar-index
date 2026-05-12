-- 0030 up — convert asset_supply_history_asset_ledger_idx from a
-- UNIQUE INDEX to a UNIQUE CONSTRAINT so the supply-snapshot writer's
-- `ON CONFLICT (asset_key, ledger_sequence, time) DO NOTHING` can
-- match it on Timescale hypertables.
--
-- F-1205 follow-up (codex audit-2026-05-12): the supply-snapshot
-- timer test-fire on R1 surfaced
-- `there is no unique or exclusion constraint matching the
--  ON CONFLICT specification`. The unique INDEX migration 0005
-- created works for SELECT-side queries but column-inference
-- (`ON CONFLICT (cols)`) can't find it on the hypertable in
-- PG 16 + Timescale 2.16 — Postgres's ON CONFLICT inference
-- requires the constraint to be visible via pg_constraint, and
-- UNIQUE INDEX entries are not.
--
-- Timescale REJECTS the cheaper `ADD CONSTRAINT … USING INDEX`
-- form on hypertables (`hypertables do not support adding a
-- constraint using an existing index`, verified on r1). So we
-- drop the index and ADD CONSTRAINT, which builds a new index
-- under the constraint. The table is small (one row per
-- (asset, ledger) snapshot — single-digit-MB on r1 today), so
-- the rebuild cost is bounded.
--
-- Timescale supports UNIQUE constraints on hypertables as long
-- as the columns include the partitioning column (time).
-- `(asset_key, ledger_sequence, time)` does include time, so
-- the constraint creates cleanly.
--
-- F-1261 (codex audit-2026-05-12): migration 0005 enabled
-- Timescale compression on this table, and Timescale rejects
-- index/constraint mutations on compressed hypertables with
-- "operation not supported on hypertables that have compression
-- enabled (0A000)". The defensive pattern already in tree
-- (migration 0004 against `trades`) decompresses chunks +
-- disables compression around the DDL, then restores the
-- compression settings + chunk re-compression resumes on the
-- next compression-policy run. Mirror that pattern here so
-- fresh-bootstrap migrations + future R1 schema advances both
-- succeed.

-- 1) Decompress every existing chunk and disable compression
--    so the constraint DDL is allowed. show_chunks() returns
--    no rows when there are no chunks yet (the asset_supply_history
--    hypertable is empty on a fresh integration container), so
--    this is a safe no-op in that case.
SELECT decompress_chunk(c, true)
  FROM show_chunks('asset_supply_history') c;

ALTER TABLE asset_supply_history SET (timescaledb.compress = false);

-- 2) Do the constraint swap inside a transaction so a concurrent
--    INSERT can't slip a duplicate through between DROP INDEX and
--    ADD CONSTRAINT.
BEGIN;

DROP INDEX IF EXISTS asset_supply_history_asset_ledger_idx;

ALTER TABLE asset_supply_history
    ADD CONSTRAINT asset_supply_history_asset_ledger_idx
    UNIQUE (asset_key, ledger_sequence, time);

COMMIT;

-- 3) Restore compression with the same settings migration 0005
--    set. The compression-policy from 0005 (every 7 days) still
--    runs against this hypertable, so the next policy tick will
--    re-compress the decompressed chunks automatically.
ALTER TABLE asset_supply_history SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'asset_key',
    timescaledb.compress_orderby   = 'time DESC'
);
