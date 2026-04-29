-- 0008 up — add 1d + 7d baseline columns to volatility_baseline_1m.
--
-- Per ADR-0019 §"Multi-window safeguard against frog-boiling": each
-- pair carries baselines at three time scales (1d / 7d / 30d). The
-- existing median / mad / sample_count columns hold the 30d
-- baseline; this migration adds the 1d and 7d slots alongside.
--
-- All new columns are NULLABLE — a pair early in its lifetime may
-- have enough data for a 30d window but not yet for a 7d or even
-- 1d window after a data-gap recovery; NULL signals "not enough
-- samples in this window" and the API's MultiBaseline reader treats
-- it as bootstrap on that scale.
--
-- Pre-launch additive migration: no existing rows when this runs in
-- production, so no backfill needed.

BEGIN;

ALTER TABLE volatility_baseline_1m
    ADD COLUMN median_1d DOUBLE PRECISION,
    ADD COLUMN mad_1d    DOUBLE PRECISION CHECK (mad_1d IS NULL OR mad_1d >= 0),
    ADD COLUMN n_1d      INTEGER          CHECK (n_1d IS NULL OR n_1d >= 2),
    ADD COLUMN median_7d DOUBLE PRECISION,
    ADD COLUMN mad_7d    DOUBLE PRECISION CHECK (mad_7d IS NULL OR mad_7d >= 0),
    ADD COLUMN n_7d      INTEGER          CHECK (n_7d IS NULL OR n_7d >= 2);

COMMENT ON COLUMN volatility_baseline_1m.median_1d IS
    'Median bucket-to-bucket VWAP percent change over the last 1d (NULL when n_1d would be < 2).';
COMMENT ON COLUMN volatility_baseline_1m.mad_1d IS
    '1.4826-scaled MAD over the 1d window. NULL when bootstrap.';
COMMENT ON COLUMN volatility_baseline_1m.n_1d IS
    'Sample count for the 1d window. NULL when bootstrap.';
COMMENT ON COLUMN volatility_baseline_1m.median_7d IS
    'Median return over the last 7d. NULL when bootstrap.';
COMMENT ON COLUMN volatility_baseline_1m.mad_7d IS
    '1.4826-scaled MAD over the 7d window. NULL when bootstrap.';
COMMENT ON COLUMN volatility_baseline_1m.n_7d IS
    'Sample count for the 7d window. NULL when bootstrap.';

COMMIT;
