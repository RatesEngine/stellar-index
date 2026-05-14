-- 0034 down — drop the oracle_prices_* CAGG ladder.
--
-- Reverse-order to satisfy any inter-CAGG dependency PG might
-- enforce (none today, but future-proof).

BEGIN;

DROP MATERIALIZED VIEW IF EXISTS oracle_prices_1mo CASCADE;
DROP MATERIALIZED VIEW IF EXISTS oracle_prices_1w  CASCADE;
DROP MATERIALIZED VIEW IF EXISTS oracle_prices_1d  CASCADE;
DROP MATERIALIZED VIEW IF EXISTS oracle_prices_4h  CASCADE;
DROP MATERIALIZED VIEW IF EXISTS oracle_prices_1h  CASCADE;
DROP MATERIALIZED VIEW IF EXISTS oracle_prices_15m CASCADE;
DROP MATERIALIZED VIEW IF EXISTS oracle_prices_1m  CASCADE;

COMMIT;
