-- 0012 down — drop claimable_observations.

BEGIN;

DROP TABLE IF EXISTS claimable_observations CASCADE;

COMMIT;
