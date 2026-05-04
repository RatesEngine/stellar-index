-- 0025 down — drop routers + trades.routed_via + aggregator_exposures.
--
-- The trades hypertable's existing data is unaffected; only the
-- routed_via column is removed.

BEGIN;

DROP TABLE IF EXISTS aggregator_exposures;

DROP INDEX IF EXISTS trades_routed_via_idx;
ALTER TABLE trades DROP COLUMN IF EXISTS routed_via;

DROP TABLE IF EXISTS routers;

COMMIT;
