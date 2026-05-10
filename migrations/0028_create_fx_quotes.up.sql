-- 0028 up — `fx_quotes` hypertable.
--
-- Persistent daily FX rate snapshots, one row per (date, ticker).
-- Powers the long-running price chart on /currencies/[ticker].
--
-- Pre-2026-05-08 the forex worker held only a 7-day rolling
-- in-memory window — fine for the rate display + 7d sparkline
-- but useless for any longer historical chart. This table makes
-- fx history persistent + arbitrary-depth.
--
-- Writer: `internal/sources/forex/worker.go` calls
--   `(*timescale.Store).InsertFXQuoteBatch` on each refresh tick
--   so live data backfills as it arrives. Backfill from the
--   Massive historical endpoint is a separate one-shot script
--   (see scripts/ops/fx-history-backfill).
--
-- Reader: `(*timescale.Store).ListFXHistory(ctx, ticker, from, to)`
--   returns daily snapshots in date-ascending order. /v1/currencies
--   handler joins this onto the in-memory rate cache to populate
--   `history_1y` + `history_all` on the response.
--
-- Cardinality: ~110 currencies × 1 row/day × 10 years = ~400k rows.
-- Chunks at 30 days = ~120 chunks. Compression after 90 days
-- collapses each chunk to <1MB.
--
-- Why ticker not asset_id: forex tickers are 3-letter ISO codes
-- (USD, EUR, JPY, …). They aren't part of the canonical Asset
-- system that uses dash-separated CODE-ISSUER on the Stellar side.
-- A separate column makes the join semantics explicit.
--
-- `rate_usd` is "1 unit of <ticker> in USD" (Massive's native form).
-- `inverse_usd` is the reciprocal cached for read-time so callers
-- don't have to divide. Both NUMERIC for precision.
--
-- `source` is the upstream data provider (typically `massive`)
-- so the row's provenance is auditable. NULL only on backfill rows
-- we couldn't attribute (recovery from a corrupt run).

BEGIN;

CREATE TABLE fx_quotes (
    bucket       timestamptz NOT NULL,
    ticker       text        NOT NULL,

    -- 1 unit of <ticker> in USD. CHECK guards against the
    -- accidentally-zero / negative writes that would corrupt the
    -- chart Y-axis.
    rate_usd     numeric     NOT NULL CHECK (rate_usd > 0),

    -- Cached reciprocal — saves a per-request division on the
    -- read path which is hot.
    inverse_usd  numeric     NOT NULL CHECK (inverse_usd > 0),

    -- Upstream provider, e.g. 'massive'. NULL allowed only for
    -- legacy backfill rows that pre-date the source attribution.
    source       text,

    -- When the worker observed this quote. Distinct from `bucket`
    -- which is the canonical date the rate is anchored to.
    observed_at  timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (ticker, bucket)
);

SELECT create_hypertable(
    'fx_quotes',
    'bucket',
    chunk_time_interval => INTERVAL '30 days',
    if_not_exists       => TRUE
);

-- Per-ticker history walk (`/v1/currencies/EUR` reads here).
CREATE INDEX fx_quotes_ticker_idx
    ON fx_quotes (ticker, bucket DESC);

-- NOTE: a `(bucket DESC)` index is auto-created by Timescale's
-- create_hypertable() above with the canonical name
-- `fx_quotes_bucket_idx` — exactly what the gap-detector lookup
-- (worker resumes from newest persisted date) needs. We do NOT
-- create one explicitly here; an explicit `CREATE INDEX
-- fx_quotes_bucket_idx ON fx_quotes (bucket DESC)` would
-- collide with Timescale's auto-creation and fail the migration
-- transaction (caught on r1 2026-05-10 — was a duplicate).

ALTER TABLE fx_quotes SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'ticker',
    timescaledb.compress_orderby   = 'bucket DESC'
);

-- Compress chunks older than 90 days. Live read traffic is
-- typically 1y or shorter, so compression reduces footprint
-- without slowing the hot path.
SELECT add_compression_policy('fx_quotes', INTERVAL '90 days');

COMMIT;
