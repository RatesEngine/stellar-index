-- 0034 up — continuous aggregates for oracle_updates.
--
-- Sister to migration 0002 (price aggregates on trades). Same 7-tier
-- grain set per RFP (1m / 15m / 1h / 4h / 1d / 1w / 1mo); same
-- "indefinite for 1h+, retention TBD for sub-hourly" policy as the
-- proposal. Distinguished from prices_* by row semantics:
--
--   trades →  OHLC + VWAP + TWAP + volume (price-discovery events,
--             aggregated by volume-weighting)
--   oracles → first / last / min / max / count + last_decimals
--             (point-in-time observations; aggregated by simple
--              first/last because there is no "volume" dimension)
--
-- One row per (source, asset, quote, bucket). Prevents collapsing
-- different oracles' opinions about the same pair into a single VWAP-
-- equivalent number — preserving per-source identity is the whole
-- point of cross-checking oracles.
--
-- Why no average/median: oracles publish point-in-time observations
-- with the source's own confidence/methodology. Averaging across
-- bucket would import the source's update cadence into the bucket
-- value (a feed that updated 500 times in an hour would be no more
-- "informative" than one that updated 5 times). `last(price, ts)` is
-- the bucket-closing observation — the most-asked-for shape (powers
-- /v1/oracle/lastprice + chart rendering). first/min/max round out
-- the OHLC-equivalent shape.
--
-- Sub-hourly grains do NOT have a retention policy in this migration
-- (matches the operator's "store everything forever" decision in
-- migration 0031). The proposal allows retention on sub-1h tiers; we
-- keep the option open by leaving the policy off — a future migration
-- can add `add_retention_policy('oracle_prices_1m', INTERVAL '...')`
-- without re-creating the CAGG.

BEGIN;

-- ─── 1-minute aggregate ────────────────────────────────────────────
CREATE MATERIALIZED VIEW oracle_prices_1m
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 minute', ts)              AS bucket,
    source,
    asset,
    quote,
    first(price, ts)                         AS first_price,
    last (price, ts)                         AS last_price,
    max  (price)                             AS high_price,
    min  (price)                             AS low_price,
    last (decimals, ts)                      AS last_decimals,
    count(*)                                 AS observation_count
FROM oracle_updates
GROUP BY bucket, source, asset, quote
WITH NO DATA;

CREATE INDEX oracle_prices_1m_lookup_idx
    ON oracle_prices_1m (source, asset, quote, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'oracle_prices_1m',
    start_offset      => INTERVAL '5 minutes',
    end_offset        => INTERVAL '30 seconds',
    schedule_interval => INTERVAL '30 seconds'
);

-- ─── 15-minute aggregate ───────────────────────────────────────────
CREATE MATERIALIZED VIEW oracle_prices_15m
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('15 minutes', ts)            AS bucket,
    source,
    asset,
    quote,
    first(price, ts)                         AS first_price,
    last (price, ts)                         AS last_price,
    max  (price)                             AS high_price,
    min  (price)                             AS low_price,
    last (decimals, ts)                      AS last_decimals,
    count(*)                                 AS observation_count
FROM oracle_updates
GROUP BY bucket, source, asset, quote
WITH NO DATA;

CREATE INDEX oracle_prices_15m_lookup_idx
    ON oracle_prices_15m (source, asset, quote, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'oracle_prices_15m',
    start_offset      => INTERVAL '1 hour',
    end_offset        => INTERVAL '1 minute',
    schedule_interval => INTERVAL '5 minutes'
);

-- ─── 1-hour aggregate (indefinite retention) ───────────────────────
CREATE MATERIALIZED VIEW oracle_prices_1h
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 hour', ts)                AS bucket,
    source,
    asset,
    quote,
    first(price, ts)                         AS first_price,
    last (price, ts)                         AS last_price,
    max  (price)                             AS high_price,
    min  (price)                             AS low_price,
    last (decimals, ts)                      AS last_decimals,
    count(*)                                 AS observation_count
FROM oracle_updates
GROUP BY bucket, source, asset, quote
WITH NO DATA;

CREATE INDEX oracle_prices_1h_lookup_idx
    ON oracle_prices_1h (source, asset, quote, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'oracle_prices_1h',
    start_offset      => INTERVAL '4 hours',
    end_offset        => INTERVAL '5 minutes',
    schedule_interval => INTERVAL '15 minutes'
);

-- ─── 4-hour aggregate ──────────────────────────────────────────────
CREATE MATERIALIZED VIEW oracle_prices_4h
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('4 hours', ts)               AS bucket,
    source,
    asset,
    quote,
    first(price, ts)                         AS first_price,
    last (price, ts)                         AS last_price,
    max  (price)                             AS high_price,
    min  (price)                             AS low_price,
    last (decimals, ts)                      AS last_decimals,
    count(*)                                 AS observation_count
FROM oracle_updates
GROUP BY bucket, source, asset, quote
WITH NO DATA;

CREATE INDEX oracle_prices_4h_lookup_idx
    ON oracle_prices_4h (source, asset, quote, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'oracle_prices_4h',
    start_offset      => INTERVAL '1 day',
    end_offset        => INTERVAL '30 minutes',
    schedule_interval => INTERVAL '1 hour'
);

-- ─── 1-day aggregate ───────────────────────────────────────────────
CREATE MATERIALIZED VIEW oracle_prices_1d
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 day', ts)                 AS bucket,
    source,
    asset,
    quote,
    first(price, ts)                         AS first_price,
    last (price, ts)                         AS last_price,
    max  (price)                             AS high_price,
    min  (price)                             AS low_price,
    last (decimals, ts)                      AS last_decimals,
    count(*)                                 AS observation_count
FROM oracle_updates
GROUP BY bucket, source, asset, quote
WITH NO DATA;

CREATE INDEX oracle_prices_1d_lookup_idx
    ON oracle_prices_1d (source, asset, quote, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'oracle_prices_1d',
    start_offset      => INTERVAL '7 days',
    end_offset        => INTERVAL '6 hours',
    schedule_interval => INTERVAL '6 hours'
);

-- ─── 1-week aggregate ──────────────────────────────────────────────
CREATE MATERIALIZED VIEW oracle_prices_1w
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 week', ts)                AS bucket,
    source,
    asset,
    quote,
    first(price, ts)                         AS first_price,
    last (price, ts)                         AS last_price,
    max  (price)                             AS high_price,
    min  (price)                             AS low_price,
    last (decimals, ts)                      AS last_decimals,
    count(*)                                 AS observation_count
FROM oracle_updates
GROUP BY bucket, source, asset, quote
WITH NO DATA;

CREATE INDEX oracle_prices_1w_lookup_idx
    ON oracle_prices_1w (source, asset, quote, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'oracle_prices_1w',
    start_offset      => INTERVAL '4 weeks',
    end_offset        => INTERVAL '1 day',
    schedule_interval => INTERVAL '1 day'
);

-- ─── 1-month aggregate ─────────────────────────────────────────────
-- Timescale uses calendar-month bucketing; requires a timezone arg.
CREATE MATERIALIZED VIEW oracle_prices_1mo
WITH (timescaledb.continuous) AS
SELECT
    time_bucket('1 month', ts, 'UTC')        AS bucket,
    source,
    asset,
    quote,
    first(price, ts)                         AS first_price,
    last (price, ts)                         AS last_price,
    max  (price)                             AS high_price,
    min  (price)                             AS low_price,
    last (decimals, ts)                      AS last_decimals,
    count(*)                                 AS observation_count
FROM oracle_updates
GROUP BY bucket, source, asset, quote
WITH NO DATA;

CREATE INDEX oracle_prices_1mo_lookup_idx
    ON oracle_prices_1mo (source, asset, quote, bucket DESC);

SELECT add_continuous_aggregate_policy(
    'oracle_prices_1mo',
    start_offset      => INTERVAL '3 months',
    end_offset        => INTERVAL '1 day',
    schedule_interval => INTERVAL '1 day'
);

COMMIT;
