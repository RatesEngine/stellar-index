-- 0007 down — drop volatility_baseline_1m.

BEGIN;

DROP INDEX IF EXISTS volatility_baseline_1m_computed_idx;
DROP TABLE IF EXISTS volatility_baseline_1m;

COMMIT;
