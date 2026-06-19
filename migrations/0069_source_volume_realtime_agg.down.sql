-- 0069 down — revert source_volume_1h to materialized-only.
--
-- Restores TimescaleDB's default (no real-time tail). The current
-- in-progress hour goes invisible again until the refresh policy
-- materializes it — see 0069 up for why that's undesirable.

BEGIN;

ALTER MATERIALIZED VIEW source_volume_1h SET (timescaledb.materialized_only = true);

COMMIT;
