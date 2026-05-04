-- 0024 up — `classic_asset_stats_5m` rollup hypertable.
--
-- Per-asset summary stats refreshed every 5 minutes. Aggregator
-- worker joins:
--   - `trades` hypertable        → volume_24h_usd, last_trade_ledger
--   - `trustline_observations`   → trustline_count
--   - `account_observations`     → outstanding_supply (issuer's
--                                  outstanding balance computed
--                                  per Algorithm 2)
--
-- Powers the asset-detail Overview tab + the /coins directory's
-- sortable columns. Pre-computed because the underlying joins are
-- expensive (hypertable scan + per-asset aggregation) and we need
-- O(1) lookups on every list render.
--
-- Why hypertable: time-keyed, append-only at refresh time. Lets
-- TimescaleDB compress old buckets after a retention window
-- (default: keep 30 days hot, compress beyond).

BEGIN;

CREATE TABLE classic_asset_stats_5m (
    bucket            timestamptz NOT NULL,
    asset_id          text        NOT NULL,    -- references classic_assets.asset_id

    -- Trustlines that have been opened to this asset. NULL when
    -- we don't have data yet (asset newly observed).
    trustline_count   bigint      CHECK (trustline_count IS NULL OR trustline_count >= 0),

    -- Issuer's outstanding balance — supply computer's Algorithm 2
    -- output. NULL when the issuer is not in our watched-classic
    -- list (we skip the supply computation for unwatched assets).
    outstanding_supply numeric    CHECK (outstanding_supply IS NULL OR outstanding_supply >= 0),

    -- Rolling 24h trade volume in USD. NULL when no recent trades
    -- OR when no USD-volume rate is available for the pair (off-
    -- chain CEX/FX feeds typically have this; on-chain assets
    -- only get USD volume if their quote is in the operator's
    -- usd_pegged_classic_assets list per L2.2).
    volume_24h_usd    numeric     CHECK (volume_24h_usd IS NULL OR volume_24h_usd >= 0),

    last_trade_ledger integer     CHECK (last_trade_ledger IS NULL OR last_trade_ledger >= 0),

    PRIMARY KEY (bucket, asset_id)
);

COMMENT ON TABLE classic_asset_stats_5m IS
    'Per-asset summary stats refreshed every 5 minutes. Pre-computed '
    'so /v1/coins + /coins/{slug} list views are O(1) lookups.';

SELECT create_hypertable(
    'classic_asset_stats_5m',
    'bucket',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

-- Per-asset history walk: chart trustline / supply / volume
-- evolution over time on /coins/{slug}.
CREATE INDEX classic_asset_stats_5m_asset_idx
    ON classic_asset_stats_5m (asset_id, bucket DESC);

ALTER TABLE classic_asset_stats_5m SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'asset_id',
    timescaledb.compress_orderby   = 'bucket DESC'
);

COMMIT;
