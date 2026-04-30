-- 0014 up — `sac_balance_observations` hypertable.
--
-- Per ADR-0022 + ADR-0011 Algorithm 2. Stores one row per
-- ContractData-delta on a watched Stellar-Asset-Contract (SAC)
-- wrapper. Backs supply.StorageClassicSupplyReader's
-- SAC-wrapped-component sum.
--
-- Identity: (contract_id, holder, ledger). The SAC contract id
-- (C-strkey) is the watched-set membership signal; holder is
-- either a G-strkey (account-controlled) or a C-strkey (contract-
-- controlled). asset_key is denormalised into the row so the
-- reader can sum by asset without joining a side table — the
-- contract → asset map is stable post-deploy and operator-curated.

BEGIN;

CREATE TABLE sac_balance_observations (
    -- SAC wrapper contract ID, C-strkey.
    contract_id     text         NOT NULL,

    -- Asset the SAC wraps, in supply.AssetKey form. Operator
    -- supplies the contract → asset mapping at config-load; the
    -- observer stamps it here so the reader can sum without a join.
    asset_key       text         NOT NULL,

    -- Holder of the SAC balance. G-strkey or C-strkey.
    holder          text         NOT NULL,

    ledger          integer      NOT NULL CHECK (ledger >= 0),
    observed_at     timestamptz  NOT NULL,

    -- Holder's SAC-wrapped balance in stroops.
    balance_stroops numeric      NOT NULL,
    is_removal      boolean      NOT NULL DEFAULT false,

    ingested_at     timestamptz  NOT NULL DEFAULT now(),

    PRIMARY KEY (contract_id, holder, ledger, observed_at)
);

COMMENT ON TABLE sac_balance_observations IS
    'Per-(SAC contract, holder, ledger) ContractData balance deltas '
    'observed by internal/sources/sac_balances/. Backs Algorithm 2 '
    'SAC-wrapped-component sum per ADR-0022. Hypertable on observed_at.';

SELECT create_hypertable(
    'sac_balance_observations',
    'observed_at',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

CREATE INDEX sac_balance_observations_asset_observed_idx
    ON sac_balance_observations (asset_key, holder, observed_at DESC);

CREATE INDEX sac_balance_observations_contract_observed_idx
    ON sac_balance_observations (contract_id, holder, observed_at DESC);

CREATE INDEX sac_balance_observations_ledger_idx
    ON sac_balance_observations (ledger DESC);

ALTER TABLE sac_balance_observations SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'asset_key, holder',
    timescaledb.compress_orderby   = 'observed_at DESC, ledger DESC'
);

COMMIT;
