-- 0040 up — remove the 90-day retention policy on `oracle_updates`
-- and stop dropping raw oracle observations. Sister to migration
-- 0031 (which did the same for `trades`).
--
-- Original design (ADR-0006, migration 0003): raw oracle_updates
-- aged out at 90 days; the long-lived hourly+ CAGGs from migration
-- 0034 kept rolled-up history forever.
--
-- Revision (#14, 2026-05-22): operator wants every raw oracle
-- observation preserved indefinitely, not just the rolled-up
-- aggregates. Same reasoning as 0031:
--   1. Storage. r1's postgres data dir is on the same 1.5 TB ZFS
--      volume the trades reasoning relied on (data/postgres, 4 %
--      used at the time of 0031). Oracle volume is far smaller than
--      trades — three on-chain oracles (Reflector / RedStone / Band)
--      + Chainlink-HTTP at fanout strides land tens of rows per
--      ledger, not thousands. Forever-retention is a rounding error
--      against the trades hypertable.
--   2. Customer / audit value: per-event fidelity (exact ts, observer
--      strkey, source contract, original-feed identity) is
--      unrecoverable from a CAGG. For divergence forensics or a
--      proof-of-quote query we want the raw row.
--   3. Granular-coverage mission ([[project_full_indexing_future_scope]]):
--      the standing goal is every bit of data for every major Stellar
--      source. A 90-day cliff on oracle history is not that.
--
-- Compression on `oracle_updates` (chunks > 7d → ZSTD per 0003) is
-- unchanged, so storage growth is bounded.
--
-- ── Operator follow-up: re-backfill the oracle CAGGs over history ─
--
-- The 0034 CAGGs (oracle_prices_1m/15m/1h/4h/1d/1w/1mo) materialise
-- on the schedule their continuous policies define. Their pre-policy
-- window is empty. Once the indexer has been writing raw rows long
-- enough to cover the desired window, refresh each CAGG over the full
-- raw range so the API serves long-form oracle history:
--
--   CALL refresh_continuous_aggregate('oracle_prices_1m',  NULL, NULL);
--   CALL refresh_continuous_aggregate('oracle_prices_15m', NULL, NULL);
--   CALL refresh_continuous_aggregate('oracle_prices_1h',  NULL, NULL);
--   CALL refresh_continuous_aggregate('oracle_prices_4h',  NULL, NULL);
--   CALL refresh_continuous_aggregate('oracle_prices_1d',  NULL, NULL);
--   CALL refresh_continuous_aggregate('oracle_prices_1w',  NULL, NULL);
--   CALL refresh_continuous_aggregate('oracle_prices_1mo', NULL, NULL);
--
-- Run from psql against the ratesengine DB. Each call walks every
-- chunk older than `end_offset` from the CAGG policy; on r1 today
-- that range is small (raw retention has been 90d), so the total
-- refresh wall-clock is minutes, not hours. Future re-runs after
-- ratesengine has been preserving raw oracle history for months
-- become correspondingly longer; refresh per-grain rather than as
-- a single transaction.

BEGIN;

SELECT remove_retention_policy('oracle_updates', if_exists => true);

COMMIT;
