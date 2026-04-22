-- 0003 up — oracle_updates hypertable + compression + retention.
--
-- Stores every observation from Reflector (3 contracts), Redstone
-- (19 feeds), Band, Chainlink-HTTP, CoinGecko, CoinMarketCap.
-- One row per observed publication; identity matches
-- canonical.OracleUpdate.ID() =
--   <source>:<ledger>:<tx_hash>:<op_index>
--
-- Price is raw NUMERIC at the source-declared `decimals` scale.
-- See internal/canonical/oracle.go for the rationale (avoids ingest-
-- time normalisation that would either lose precision or force a
-- float). The aggregator scales on read.

BEGIN;

CREATE TABLE oracle_updates (
    source          text         NOT NULL,

    -- On-chain contract address. NULL for off-chain sources
    -- (coingecko, coinmarketcap, chainlink-http).
    contract_id     text,

    -- On-chain identity. For off-chain sources: ledger=0,
    -- tx_hash=payload-hash, op_index=0 (synthesised by the
    -- consumer so identity still holds uniqueness).
    ledger          integer      NOT NULL CHECK (ledger >= 0),
    tx_hash         char(64)     NOT NULL,
    op_index        integer      NOT NULL CHECK (op_index >= 0),

    -- Oracle publication timestamp (not ledger close time).
    ts              timestamptz  NOT NULL,

    asset           text         NOT NULL,
    quote           text         NOT NULL,

    price           numeric      NOT NULL CHECK (price > 0),

    -- uint8 on the Go side; Postgres has no unsigned types so
    -- smallint with a CHECK for the 0–38 range (see NUMERIC
    -- precision ceiling + canonical.OracleUpdate.Validate).
    decimals        smallint     NOT NULL CHECK (decimals BETWEEN 0 AND 38),

    confidence      double precision CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    observer        text,

    ingested_at     timestamptz  NOT NULL DEFAULT now(),

    PRIMARY KEY (source, ledger, tx_hash, op_index, ts)
);

COMMENT ON TABLE oracle_updates IS
    'Every observed oracle publication, one row per (source, ledger, tx_hash, op_index). '
    'Hypertable partitioned on ts. See ADR-0006.';

COMMENT ON COLUMN oracle_updates.contract_id IS
    'NULL for off-chain sources (coingecko, coinmarketcap, chainlink-http).';
COMMENT ON COLUMN oracle_updates.decimals IS
    'Source-declared scale for price. Typical: 14 (Reflector), 8 (Redstone), 9/18 (Band).';

-- Hypertable, 1-day chunks (same as trades).
SELECT create_hypertable(
    'oracle_updates',
    'ts',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists       => TRUE
);

-- Secondary indexes.
-- Asset-centric lookups ("latest Reflector price for XLM").
CREATE INDEX oracle_updates_asset_ts_idx ON oracle_updates (asset, ts DESC);

-- Pair-centric lookups (for cross-pair oracles like Band E18).
CREATE INDEX oracle_updates_pair_ts_idx ON oracle_updates (asset, quote, ts DESC);

-- Source-centric replay + debug.
CREATE INDEX oracle_updates_source_ledger_idx ON oracle_updates (source, ledger DESC);

-- Compression — group by asset + source for good dictionary reuse,
-- order within-chunk by time DESC.
ALTER TABLE oracle_updates SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'source, asset, quote',
    timescaledb.compress_orderby   = 'ts DESC, ledger DESC'
);

SELECT add_compression_policy('oracle_updates', INTERVAL '7 days');
SELECT add_retention_policy   ('oracle_updates', INTERVAL '90 days');

COMMIT;
