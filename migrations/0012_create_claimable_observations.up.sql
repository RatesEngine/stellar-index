-- 0012 up — `claimable_observations` hypertable.
--
-- Per ADR-0022 + ADR-0011 Algorithm 2. Stores one row per
-- ClaimableBalanceEntry-delta touching an operator-watched
-- classic credit asset. Backs supply.StorageClassicSupplyReader's
-- claimable-component sum.
--
-- Identity: (claimable_id, ledger). ClaimableBalanceID hex is
-- globally unique — each claimable balance has its own entry,
-- created once and either claimed (Removed) or unchanged.
-- asset_key is a data column rather than part of the PK because
-- a claimable balance's asset doesn't change over its lifetime.

BEGIN;

CREATE TABLE claimable_observations (
    -- ClaimableBalanceID, hex-encoded.
    claimable_id    text         NOT NULL,

    -- Asset the claimable balance pays out, in supply.AssetKey form.
    asset_key       text         NOT NULL,

    ledger          integer      NOT NULL CHECK (ledger >= 0),
    observed_at     timestamptz  NOT NULL,

    balance_stroops numeric      NOT NULL,
    is_removal      boolean      NOT NULL DEFAULT false,

    ingested_at     timestamptz  NOT NULL DEFAULT now(),

    PRIMARY KEY (claimable_id, ledger, observed_at)
);

COMMENT ON TABLE claimable_observations IS
    'Per-(claimable_id, ledger) ClaimableBalance deltas observed '
    'by internal/sources/claimable_balances/. Backs Algorithm 2 '
    'claimable-component sum per ADR-0022. Hypertable on observed_at.';

SELECT create_hypertable(
    'claimable_observations',
    'observed_at',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

CREATE INDEX claimable_observations_asset_observed_idx
    ON claimable_observations (asset_key, claimable_id, observed_at DESC);

CREATE INDEX claimable_observations_ledger_idx
    ON claimable_observations (ledger DESC);

ALTER TABLE claimable_observations SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'asset_key, claimable_id',
    timescaledb.compress_orderby   = 'observed_at DESC, ledger DESC'
);

COMMIT;
