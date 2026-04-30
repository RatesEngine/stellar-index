-- 0013 up — `lp_reserve_observations` hypertable.
--
-- Per ADR-0022 + ADR-0011 Algorithm 2. Stores one row per
-- LiquidityPoolEntry-delta touching an operator-watched classic
-- credit asset. Backs supply.StorageClassicSupplyReader's
-- LP-reserve-component sum.
--
-- Identity: (pool_id, asset_key, ledger). One LP holds two
-- assets; the observer writes two rows per pool-change — one
-- per side of the asset pair — so the reader can sum directly
-- by asset_key without needing to know which side a given asset
-- is on.

BEGIN;

CREATE TABLE lp_reserve_observations (
    -- Pool ID (hex-encoded).
    pool_id         text         NOT NULL,

    -- Which side of the pool this row reflects, in
    -- supply.AssetKey form. (XLM = "XLM"; classic = "CODE:G…".)
    asset_key       text         NOT NULL,

    ledger          integer      NOT NULL CHECK (ledger >= 0),
    observed_at     timestamptz  NOT NULL,

    -- Pool's reserve for this asset side, in stroops.
    balance_stroops numeric      NOT NULL,

    -- True when the LP entry was removed. Both sides go to
    -- balance=0 with is_removal=true.
    is_removal      boolean      NOT NULL DEFAULT false,

    ingested_at     timestamptz  NOT NULL DEFAULT now(),

    PRIMARY KEY (pool_id, asset_key, ledger, observed_at)
);

COMMENT ON TABLE lp_reserve_observations IS
    'Per-(pool, asset, ledger) LiquidityPool-reserve deltas observed '
    'by internal/sources/liquidity_pools/. Backs Algorithm 2 '
    'LP-reserve-component sum per ADR-0022. Two rows per pool-change '
    '(one per side of the asset pair). Hypertable on observed_at.';

SELECT create_hypertable(
    'lp_reserve_observations',
    'observed_at',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

CREATE INDEX lp_reserve_observations_asset_observed_idx
    ON lp_reserve_observations (asset_key, pool_id, observed_at DESC);

CREATE INDEX lp_reserve_observations_ledger_idx
    ON lp_reserve_observations (ledger DESC);

ALTER TABLE lp_reserve_observations SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'asset_key, pool_id',
    timescaledb.compress_orderby   = 'observed_at DESC, ledger DESC'
);

COMMIT;
