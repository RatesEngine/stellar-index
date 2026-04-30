-- 0014 down — drop sac_balance_observations.

BEGIN;

DROP TABLE IF EXISTS sac_balance_observations CASCADE;

COMMIT;
