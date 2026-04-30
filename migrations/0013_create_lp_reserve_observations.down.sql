-- 0013 down — drop lp_reserve_observations.

BEGIN;

DROP TABLE IF EXISTS lp_reserve_observations CASCADE;

COMMIT;
