-- 0008 down — drop the multi-window baseline columns.

BEGIN;

ALTER TABLE volatility_baseline_1m
    DROP COLUMN IF EXISTS median_1d,
    DROP COLUMN IF EXISTS mad_1d,
    DROP COLUMN IF EXISTS n_1d,
    DROP COLUMN IF EXISTS median_7d,
    DROP COLUMN IF EXISTS mad_7d,
    DROP COLUMN IF EXISTS n_7d;

COMMIT;
