-- 0069 up — enable real-time aggregation on source_volume_1h.
--
-- Migration 0068 created the CAGG with TimescaleDB's default
-- materialized_only=true. With that, a query only sees buckets the
-- refresh policy has already materialized — and an hourly bucket
-- isn't materialized until the hour CLOSES (+ the 5-min end_offset).
-- So the in-progress hour was invisible: the source activity chart
-- showed "no data" for the current hour until ~5 min past the top of
-- the next hour (observed on /dexes/sdex, 2026-06-19).
--
-- materialized_only=false turns on real-time aggregation: a query
-- UNIONs the materialized history with a live aggregate of the raw
-- trades ABOVE the materialization watermark (only the current
-- partial hour — bounded + cheap, NOT a full-window rescan). The
-- read query's read-time XLM/USD multiply works identically on the
-- live-computed rows (same columns).
--
-- Idempotent: r1 already had this applied via a live ALTER on
-- 2026-06-19; re-running is a no-op.

BEGIN;

ALTER MATERIALIZED VIEW source_volume_1h SET (timescaledb.materialized_only = false);

COMMIT;
