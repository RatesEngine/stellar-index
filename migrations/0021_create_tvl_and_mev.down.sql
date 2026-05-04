-- 0021 down — drop tvl_observations + mev_events.

BEGIN;

DROP TABLE IF EXISTS mev_events;
DROP TABLE IF EXISTS tvl_observations;

COMMIT;
