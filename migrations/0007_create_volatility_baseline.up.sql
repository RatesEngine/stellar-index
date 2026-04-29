-- 0007 up — `volatility_baseline_1m` per-pair statistical baseline.
--
-- Per ADR-0019 Phase 2: each (base, quote) pair carries a robust
-- statistical baseline computed from the last 30 days of 1m bucket
-- VWAPs. The aggregator's baseline-refresh worker computes
-- Median + MAD (1.4826-scaled) using `internal/aggregate/baseline`
-- and UPSERTs into this table on each refresh cycle.
--
-- Why a plain table, not a CAGG:
--   The L2.5 backlog row is shorthanded "CAGG + MAD math", but
--   Median + MAD are only computable in Postgres via percentile_cont
--   — a non-parallel, non-incremental aggregate. A CAGG built on
--   percentile_cont would re-scan the whole 30-day window on every
--   refresh anyway, with no incremental advantage. Keeping the
--   compute in Go (where we have a thoroughly-tested baseline
--   package, see PR #246) and writing one flat row per pair is
--   cheaper to read AND easier to backfill.
--
-- Current-state semantics: ONE row per pair. Refreshes UPSERT and
-- overwrite. Auditing the evolution of a baseline over time is a
-- distinct concern (logs / metrics) — this table is the API hot-
-- path read for "what's THE baseline for this pair, right now".

BEGIN;

CREATE TABLE volatility_baseline_1m (
    base_asset    TEXT             NOT NULL,
    quote_asset   TEXT             NOT NULL,
    -- When the aggregator wrote this baseline. Used by the
    -- baseline_quality factor in the confidence score (ADR-0019
    -- §"Multi-factor confidence score": staleness ramp).
    computed_at   TIMESTAMPTZ      NOT NULL,
    -- The training window the median + MAD were computed over.
    -- window_end is exclusive; window_start = window_end - 30d
    -- in steady state, but smaller during bootstrap.
    window_start  TIMESTAMPTZ      NOT NULL,
    window_end    TIMESTAMPTZ      NOT NULL,
    -- Median bucket-to-bucket return. Float64 — the math is
    -- statistical, not amount-precision-sensitive.
    median        DOUBLE PRECISION NOT NULL,
    -- 1.4826-scaled MAD; σ-equivalent for normal data so a 5σ
    -- threshold is z >= 5 with no further conversion.
    mad           DOUBLE PRECISION NOT NULL CHECK (mad >= 0),
    -- Number of returns that fed the computation. Used by the
    -- baseline_quality factor (ramps 0.5 → 1.0 with sample count).
    sample_count  INTEGER          NOT NULL CHECK (sample_count >= 2),
    -- Sanity: window_end strictly after window_start.
    CONSTRAINT volatility_baseline_window_chk
        CHECK (window_end > window_start),
    -- One row per pair.
    PRIMARY KEY (base_asset, quote_asset)
);

COMMENT ON TABLE volatility_baseline_1m IS
    'Per-pair robust-stats baseline (ADR-0019 Phase 2). Aggregator refreshes; one row per pair.';

COMMENT ON COLUMN volatility_baseline_1m.median IS
    'Median bucket-to-bucket VWAP percent change over the training window.';

COMMENT ON COLUMN volatility_baseline_1m.mad IS
    'Median absolute deviation, scaled by 1.4826 to be σ-equivalent for normal data.';

COMMENT ON COLUMN volatility_baseline_1m.computed_at IS
    'Wall-clock when the aggregator last refreshed this row. Drives baseline_quality staleness factor.';

-- "Find baselines that need refreshing" lookup — the aggregator's
-- refresh worker scans for rows whose computed_at is older than
-- the refresh cadence.
CREATE INDEX volatility_baseline_1m_computed_idx
    ON volatility_baseline_1m (computed_at);

COMMIT;
