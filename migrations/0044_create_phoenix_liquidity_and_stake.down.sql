-- 0044 down — drop the Phoenix liquidity + stake hypertables (#27).
--
-- DROP TABLE removes the hypertable, chunks, indexes and compression
-- settings in one statement. CASCADE not needed; nothing references
-- these tables (the aggregator and exposure-pipeline are downstream
-- READ consumers, not foreign-key references).

BEGIN;

DROP TABLE IF EXISTS phoenix_stake_events;
DROP TABLE IF EXISTS phoenix_liquidity;

COMMIT;
