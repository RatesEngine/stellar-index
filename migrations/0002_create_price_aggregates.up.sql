-- 0002 up — continuous aggregates + refresh + retention.
--
-- Implements the grain set named in the historical-
-- price table: 1m / 15m / 1h / 4h / 1d / 1w / 1mo. Each CAGG is a
-- VWAP (volume-weighted) + TWAP (time-weighted) pre-computation
-- that the /v1/history endpoint queries directly.
--
-- Retention:
--   - sub-hourly grains (1m, 15m) retained 30 days.
--   - hourly+ grains (1h, 4h, 1d, 1w, 1mo) retained INDEFINITELY
--     — matches the commitment for the all-time view.
--
-- Refresh policy:
--   Each CAGG refreshes its "recent" slice on a cadence shorter than
--   its own grain. Coarser grains refresh less often.
--
-- Column semantics per grain row:
--   bucket           = start of the aggregation window (timestamptz)
--   base_asset       = canonical base
--   quote_asset      = canonical quote
--   vwap             = Σ(price × v) / Σ(v) — volume-weighted
--   twap             = Σ(price) / N       — time-weighted (equal-weight)
--   volume           = Σ(base_amount)     — base-asset volume in window
--   volume_usd       = Σ(usd_volume)      — USD-denominated volume
--   trade_count      = N                  — trades contributing
--   sources          = distinct source list that contributed
--   first_price      = price of earliest trade  → OHLC `open`
--   last_price       = price of latest trade    → OHLC `close`
--   high_price       = max price                → OHLC `high`
--   low_price        = min price                → OHLC `low`

BEGIN;

-- 1-minute VWAP/OHLC aggregate.
CREATE MATERIALIZED VIEW prices_1m
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 minute', ts)              AS bucket,
    base_asset,
    quote_asset,
    -- VWAP (guarded against zero volume — shouldn't happen per CHECK on trades)
    sum( (quote_amount / base_amount) * base_amount ) / sum(base_amount) AS vwap,
    avg( quote_amount / base_amount )                                    AS twap,
    sum(base_amount)                                                     AS volume,
    sum(coalesce(usd_volume, 0))                                         AS volume_usd,
    count(*)                                                             AS trade_count,
    array_agg(DISTINCT source)                                           AS sources,
    first(quote_amount / base_amount, ts)                                AS first_price,
    last (quote_amount / base_amount, ts)                                AS last_price,
    max  (quote_amount / base_amount)                                    AS high_price,
    min  (quote_amount / base_amount)                                    AS low_price
FROM trades
GROUP BY bucket, base_asset, quote_asset
WITH NO DATA;

CREATE INDEX prices_1m_pair_bucket_idx ON prices_1m (base_asset, quote_asset, bucket DESC);

-- Refresh every 30 s, covering the last 5 minutes — guaranteed to
-- pick up every newly inserted trade even after late-arriving
-- backfills of ~minutes-old events.
SELECT add_continuous_aggregate_policy(
    'prices_1m',
    start_offset      => INTERVAL '5 minutes',
    end_offset        => INTERVAL '30 seconds',
    schedule_interval => INTERVAL '30 seconds'
);

-- 30-day retention on the materialised rows.
SELECT add_retention_policy('prices_1m', INTERVAL '30 days');


-- 15-minute aggregate.
CREATE MATERIALIZED VIEW prices_15m
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('15 minutes', ts)           AS bucket,
    base_asset,
    quote_asset,
    sum( (quote_amount / base_amount) * base_amount ) / sum(base_amount) AS vwap,
    avg( quote_amount / base_amount )                                    AS twap,
    sum(base_amount)                                                     AS volume,
    sum(coalesce(usd_volume, 0))                                         AS volume_usd,
    count(*)                                                             AS trade_count,
    array_agg(DISTINCT source)                                           AS sources,
    first(quote_amount / base_amount, ts)                                AS first_price,
    last (quote_amount / base_amount, ts)                                AS last_price,
    max  (quote_amount / base_amount)                                    AS high_price,
    min  (quote_amount / base_amount)                                    AS low_price
FROM trades
GROUP BY bucket, base_asset, quote_asset
WITH NO DATA;

CREATE INDEX prices_15m_pair_bucket_idx ON prices_15m (base_asset, quote_asset, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'prices_15m',
    start_offset      => INTERVAL '1 hour',
    end_offset        => INTERVAL '1 minute',
    schedule_interval => INTERVAL '5 minutes'
);

SELECT add_retention_policy('prices_15m', INTERVAL '30 days');


-- 1-hour aggregate — RETAINED INDEFINITELY per the spec.
CREATE MATERIALIZED VIEW prices_1h
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', ts)               AS bucket,
    base_asset,
    quote_asset,
    sum( (quote_amount / base_amount) * base_amount ) / sum(base_amount) AS vwap,
    avg( quote_amount / base_amount )                                    AS twap,
    sum(base_amount)                                                     AS volume,
    sum(coalesce(usd_volume, 0))                                         AS volume_usd,
    count(*)                                                             AS trade_count,
    array_agg(DISTINCT source)                                           AS sources,
    first(quote_amount / base_amount, ts)                                AS first_price,
    last (quote_amount / base_amount, ts)                                AS last_price,
    max  (quote_amount / base_amount)                                    AS high_price,
    min  (quote_amount / base_amount)                                    AS low_price
FROM trades
GROUP BY bucket, base_asset, quote_asset
WITH NO DATA;

CREATE INDEX prices_1h_pair_bucket_idx ON prices_1h (base_asset, quote_asset, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'prices_1h',
    start_offset      => INTERVAL '4 hours',
    end_offset        => INTERVAL '5 minutes',
    schedule_interval => INTERVAL '15 minutes'
);

-- No retention policy on 1h+ — indefinite by design.


-- 4-hour aggregate — indefinite.
CREATE MATERIALIZED VIEW prices_4h
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('4 hours', ts)              AS bucket,
    base_asset,
    quote_asset,
    sum( (quote_amount / base_amount) * base_amount ) / sum(base_amount) AS vwap,
    avg( quote_amount / base_amount )                                    AS twap,
    sum(base_amount)                                                     AS volume,
    sum(coalesce(usd_volume, 0))                                         AS volume_usd,
    count(*)                                                             AS trade_count,
    array_agg(DISTINCT source)                                           AS sources,
    first(quote_amount / base_amount, ts)                                AS first_price,
    last (quote_amount / base_amount, ts)                                AS last_price,
    max  (quote_amount / base_amount)                                    AS high_price,
    min  (quote_amount / base_amount)                                    AS low_price
FROM trades
GROUP BY bucket, base_asset, quote_asset
WITH NO DATA;

CREATE INDEX prices_4h_pair_bucket_idx ON prices_4h (base_asset, quote_asset, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'prices_4h',
    start_offset      => INTERVAL '1 day',
    end_offset        => INTERVAL '30 minutes',
    schedule_interval => INTERVAL '1 hour'
);


-- 1-day aggregate — indefinite.
CREATE MATERIALIZED VIEW prices_1d
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 day', ts)                AS bucket,
    base_asset,
    quote_asset,
    sum( (quote_amount / base_amount) * base_amount ) / sum(base_amount) AS vwap,
    avg( quote_amount / base_amount )                                    AS twap,
    sum(base_amount)                                                     AS volume,
    sum(coalesce(usd_volume, 0))                                         AS volume_usd,
    count(*)                                                             AS trade_count,
    array_agg(DISTINCT source)                                           AS sources,
    first(quote_amount / base_amount, ts)                                AS first_price,
    last (quote_amount / base_amount, ts)                                AS last_price,
    max  (quote_amount / base_amount)                                    AS high_price,
    min  (quote_amount / base_amount)                                    AS low_price
FROM trades
GROUP BY bucket, base_asset, quote_asset
WITH NO DATA;

CREATE INDEX prices_1d_pair_bucket_idx ON prices_1d (base_asset, quote_asset, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'prices_1d',
    start_offset      => INTERVAL '7 days',
    end_offset        => INTERVAL '6 hours',
    schedule_interval => INTERVAL '6 hours'
);


-- 1-week aggregate — indefinite.
CREATE MATERIALIZED VIEW prices_1w
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 week', ts)               AS bucket,
    base_asset,
    quote_asset,
    sum( (quote_amount / base_amount) * base_amount ) / sum(base_amount) AS vwap,
    avg( quote_amount / base_amount )                                    AS twap,
    sum(base_amount)                                                     AS volume,
    sum(coalesce(usd_volume, 0))                                         AS volume_usd,
    count(*)                                                             AS trade_count,
    array_agg(DISTINCT source)                                           AS sources,
    first(quote_amount / base_amount, ts)                                AS first_price,
    last (quote_amount / base_amount, ts)                                AS last_price,
    max  (quote_amount / base_amount)                                    AS high_price,
    min  (quote_amount / base_amount)                                    AS low_price
FROM trades
GROUP BY bucket, base_asset, quote_asset
WITH NO DATA;

CREATE INDEX prices_1w_pair_bucket_idx ON prices_1w (base_asset, quote_asset, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'prices_1w',
    start_offset      => INTERVAL '4 weeks',
    end_offset        => INTERVAL '1 day',
    schedule_interval => INTERVAL '1 day'
);


-- 1-month aggregate — indefinite.
-- Timescale uses calendar-month bucketing; requires a timezone arg.
CREATE MATERIALIZED VIEW prices_1mo
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 month', ts, 'UTC')       AS bucket,
    base_asset,
    quote_asset,
    sum( (quote_amount / base_amount) * base_amount ) / sum(base_amount) AS vwap,
    avg( quote_amount / base_amount )                                    AS twap,
    sum(base_amount)                                                     AS volume,
    sum(coalesce(usd_volume, 0))                                         AS volume_usd,
    count(*)                                                             AS trade_count,
    array_agg(DISTINCT source)                                           AS sources,
    first(quote_amount / base_amount, ts)                                AS first_price,
    last (quote_amount / base_amount, ts)                                AS last_price,
    max  (quote_amount / base_amount)                                    AS high_price,
    min  (quote_amount / base_amount)                                    AS low_price
FROM trades
GROUP BY bucket, base_asset, quote_asset
WITH NO DATA;

CREATE INDEX prices_1mo_pair_bucket_idx ON prices_1mo (base_asset, quote_asset, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'prices_1mo',
    start_offset      => INTERVAL '3 months',
    end_offset        => INTERVAL '1 day',
    schedule_interval => INTERVAL '1 day'
);

COMMIT;
