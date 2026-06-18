-- 0066 down — drop the supply_1d continuous aggregate.
-- DROP MATERIALIZED VIEW removes the CAGG, its refresh policy, and
-- the supply_1d_asset_bucket_idx index in one step.

BEGIN;

DROP MATERIALIZED VIEW IF EXISTS supply_1d;

COMMIT;
