-- 0066 up — `supply_1d` continuous aggregate over asset_supply_history.
--
-- Daily last-known supply per asset_key — the supply leg of crypto
-- market-cap-over-time (/v1/chart?price_type=market_cap for on-chain
-- assets). handleChartMarketCap multiplies this day's circulating
-- supply by the day's USD price (the existing prices_1d / stablecoin-
-- proxy series the normal chart already serves) to produce a daily
-- market-cap point — unblocking the crypto branch that previously
-- returned HTTP 501.
--
-- One row per (asset_key, day-bucket): asset_supply_history is
-- append-only with many snapshots/day, so last() picks the day's
-- closing supply (the right figure for a daily cap). max_supply is
-- nullable; last() carries the latest value (NULL or not) through.
--
-- WITH NO DATA: the create does NOT backfill. After applying this
-- migration the operator MUST materialize the history once, e.g.
--   CALL refresh_continuous_aggregate('supply_1d', NULL, now());
-- The source is MB-scale (a few thousand assets), so a full
-- NULL→now materialization back to ~2015 is cheap. The
-- add_continuous_aggregate_policy below keeps it current thereafter.

BEGIN;

CREATE MATERIALIZED VIEW supply_1d
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 day', time)        AS bucket,
    asset_key,
    last(circulating_supply, time)    AS circulating_supply,
    last(total_supply, time)          AS total_supply,
    last(max_supply, time)            AS max_supply
FROM asset_supply_history
GROUP BY bucket, asset_key
WITH NO DATA;

CREATE INDEX supply_1d_asset_bucket_idx
    ON supply_1d (asset_key, bucket DESC);

-- 7-day lookback covers late-arriving backfilled snapshots; 1-hour
-- grace lets the day settle; 6-hour cadence is ample (supply changes
-- slowly relative to a day bucket).
SELECT add_continuous_aggregate_policy(
    'supply_1d',
    start_offset       => INTERVAL '7 days',
    end_offset         => INTERVAL '1 hour',
    schedule_interval  => INTERVAL '6 hours'
);

-- No retention — supply history is small + queryable across the
-- asset's full lifetime per ADR-0011 (matches asset_supply_history).

COMMIT;
