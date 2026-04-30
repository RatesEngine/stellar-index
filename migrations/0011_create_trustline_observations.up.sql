-- 0011 up — `trustline_observations` hypertable.
--
-- Per ADR-0022 + ADR-0011 Algorithm 2. Stores one row per
-- TrustlineEntry-delta touching an operator-watched classic
-- credit asset. Backs supply.StorageClassicSupplyReader's
-- trustline-component sum for the watched asset list.
--
-- Identity: (account_id, asset_key, ledger). Multiple changes
-- to the same trustline in one ledger collapse last-writer-wins.
-- The observer (Task #63) only fires on assets present in
-- [supply] watched_classic_assets so the table is bounded by
-- operator config, not by network-wide trustline volume
-- (which is ~100M).
--
-- Retention: NONE for now. Per-watched-asset volume is bounded
-- by holder count (typically thousands per asset, not millions).
-- A retention policy lands when operational data shows it's
-- needed.

BEGIN;

CREATE TABLE trustline_observations (
    -- Holder G-strkey. The trustline's source account.
    account_id      text         NOT NULL,

    -- Asset the trustline references, in supply.AssetKey form
    -- (CODE:G… for classic credits). Same form the reader uses
    -- to sum across observations.
    asset_key       text         NOT NULL,

    ledger          integer      NOT NULL CHECK (ledger >= 0),
    observed_at     timestamptz  NOT NULL,

    -- Post-change trustline balance in stroops. NUMERIC per
    -- ADR-0003 — classic-asset balances are i64 in XDR but we
    -- carry NUMERIC end-to-end for consistency.
    balance_stroops numeric      NOT NULL,

    -- True when the change removed the trustline. Balance is 0
    -- on removal rows; the reader treats is_removal=true as
    -- "holder no longer has trustline at this ledger."
    is_removal      boolean      NOT NULL DEFAULT false,

    ingested_at     timestamptz  NOT NULL DEFAULT now(),

    PRIMARY KEY (account_id, asset_key, ledger, observed_at)
);

COMMENT ON TABLE trustline_observations IS
    'Per-(account, asset, ledger) Trustline deltas observed by '
    'internal/sources/trustlines/. Backs Algorithm 2 trustline-'
    'component sum per ADR-0022. Hypertable on observed_at.';

SELECT create_hypertable(
    'trustline_observations',
    'observed_at',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

-- Reader query: "latest observation per (account, asset) at-or-
-- before ledger N, summed by asset." The DISTINCT ON path keys
-- on (account_id, asset_key) so this index covers it.
CREATE INDEX trustline_observations_asset_observed_idx
    ON trustline_observations (asset_key, account_id, observed_at DESC);

-- Replay / debug — walk by ledger.
CREATE INDEX trustline_observations_ledger_idx
    ON trustline_observations (ledger DESC);

ALTER TABLE trustline_observations SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'asset_key, account_id',
    timescaledb.compress_orderby   = 'observed_at DESC, ledger DESC'
);

COMMIT;
