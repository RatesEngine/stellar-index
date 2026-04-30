-- 0011 down — drop trustline_observations.

BEGIN;

DROP TABLE IF EXISTS trustline_observations CASCADE;

COMMIT;
