-- 0027 up — `prices_1m_proxy` hypertable.
--
-- ADR-0025 phase 1. Mirror table for the aggregator's
-- stablecoin-fiat-proxy rewritten 1-minute VWAPs.
--
-- Why a separate table:
--
-- `prices_1m` is a TimescaleDB CAGG materialised over the trades
-- hypertable, grouped by `(time_bucket(1m, ts), base_asset,
-- quote_asset)`. It only knows LITERAL trade pairs because that's
-- what the indexer writes. The aggregator's stablecoin-fiat-proxy
-- expansion (orchestrator.fetchForTarget when
-- EnableStablecoinFiatProxy is on) rewrites trades from
-- `XLM/USDC-GA5Z…` onto `XLM/fiat:USD` AT VWAP COMPUTE TIME — by
-- design, not at decode time, so depeg signal stays visible in the
-- raw trade feed (CLAUDE.md / ADR-0014).
--
-- That rewriting today only lands in:
--   * `vwap:<base>:<quote>:<window>` Redis keys (live-cache only).
--   * `vwap:…:provenance` markers.
--   * `ratesengine:closed-bucket:v1` pub/sub events.
--
-- Every read path that needs HISTORY of a rewritten pair (`/v1/vwap`,
-- `/v1/twap`, `/v1/observations`, `/v1/history/since-inception`,
-- `/v1/chart`, the change-summary worker) queries `prices_1m`,
-- which never sees the rewritten target. /v1/price-family is fine
-- because PRs #631/#632/#634 added a Redis fallback for current-
-- value queries; everything else 404s for `?quote=fiat:USD`.
--
-- This table closes the gap. The aggregator orchestrator will UPSERT
-- a row into `prices_1m_proxy` per closed (pair, window=1m) bucket
-- whose target is fiat-quoted, alongside the existing Redis write.
-- API readers UNION `prices_1m` + `prices_1m_proxy` so a caller
-- asking for `?quote=fiat:USD` reaches the rewritten rows.
--
-- Wiring lives in phase 2 (aggregator UPSERT) + phase 3 (API reader
-- fallback) per ADR-0025. **This migration is phase 1: the schema
-- only — empty on creation, no writers, no readers.** Operator
-- review point.
--
-- Costs at launch scale:
--   * Storage: 9 default pairs × 3 windows × 1 row per 5 min ≈
--     7800 rows/day ≈ trivial.
--   * Write IO: one UPSERT per (pair, window) per aggregator tick
--     (default 30s). Marginal vs the existing Redis write + log line.
--   * Read IO: extra UNION in /v1/price-family fallback paths.
--     `prices_1m_proxy_pair_bucket_idx` keeps it O(log n).
--
-- Retention matches `prices_1m` at 30 days. The CAGG-level retention
-- on `prices_1m` already bounds the literal-pair history to that
-- window; the rewritten-pair history shouldn't extend further.
--
-- Schema diverges from `prices_1m` in two ways:
--
--   1. **No first/last/high/low fields.** The aggregator's
--      stablecoin-fiat-proxy expansion merges trades from N source
--      pairs (XLM/USDC-GA5Z…, XLM/USDT-GA…, …) before computing
--      VWAP. OHLC fields are well-defined for a single price series;
--      across a merged source set they're ambiguous (do we take
--      the open of the first source, last of the last, max across
--      all sources? — depending on which you pick the candle is
--      meaningless). VWAP and aggregate volume are well-defined.
--      OHLC for rewritten pairs lands as a post-launch feature with
--      its own design (likely "trade-source-decoupled OHLC" via a
--      different rewriting path).
--   2. **`sources` is the LIST OF CONTRIBUTING SOURCE-PAIR NAMES**
--      (e.g. `["soroswap", "sdex"]`), same shape as `prices_1m.sources`.
--      Useful for the /v1/price envelope's `sources` field on
--      fiat-target queries.

BEGIN;

CREATE TABLE prices_1m_proxy (
    -- Closed-bucket boundary, aligned to the 1-minute grid via
    -- `time_bucket('1 minute', closed_at)` upstream — same convention
    -- as prices_1m.bucket.
    bucket       timestamptz  NOT NULL,

    -- Rewritten target asset. For the launch case this is "native"
    -- or "crypto:XLM" / "crypto:BTC" / "crypto:ETH" — matching the
    -- canonical wire form the aggregator's defaultPairs() emits.
    base_asset   text         NOT NULL,

    -- Rewritten target quote. Always fiat-typed today
    -- ("fiat:USD", "fiat:EUR", "fiat:GBP"); the aggregator only
    -- rewrites onto fiat targets per FiatProxy().
    quote_asset  text         NOT NULL,

    -- The rewritten VWAP. Same units as prices_1m.vwap (quote per
    -- base, decimal-string-safe via NUMERIC).
    vwap         numeric      NOT NULL CHECK (vwap > 0),

    -- Time-weighted variant. Optional because the aggregator
    -- doesn't always compute it (depends on Config.WriteTWAP); the
    -- column accepts NULL so existing readers don't error if the
    -- source isn't populated.
    twap         numeric      CHECK (twap IS NULL OR twap > 0),

    -- Aggregate base-asset volume across all contributing source
    -- pairs in the bucket. Sum across rewritten sources.
    volume       numeric      NOT NULL CHECK (volume >= 0),

    -- USD-denominated volume. Populated from the contributing
    -- trades' usd_volume column, same as prices_1m.volume_usd.
    -- NULL when no contributing trade had a usd_volume value
    -- (see [trades].usd_pegged_classic_assets — operator-gated).
    volume_usd   numeric      CHECK (volume_usd IS NULL OR volume_usd >= 0),

    -- Total trades that contributed across all source pairs.
    trade_count  integer      NOT NULL CHECK (trade_count > 0),

    -- Distinct source connector names that contributed. Matches
    -- prices_1m.sources shape — the /v1/price envelope's `sources`
    -- field reads this for fiat-target queries.
    sources      text[]       NOT NULL,

    PRIMARY KEY (bucket, base_asset, quote_asset)
);

-- Hypertable on bucket; daily chunks match prices_1m's schedule.
SELECT create_hypertable('prices_1m_proxy', 'bucket',
    chunk_time_interval => INTERVAL '1 day');

-- Pair-then-bucket index for the dominant read pattern: "latest row
-- for (base, quote)". Mirrors prices_1m_pair_bucket_idx.
CREATE INDEX prices_1m_proxy_pair_bucket_idx
    ON prices_1m_proxy (base_asset, quote_asset, bucket DESC);

-- 30-day retention matches prices_1m. Rewritten-pair history beyond
-- that window is unrecoverable; the literal `prices_1m` data is
-- intact so a future re-rollup has the source material.
SELECT add_retention_policy('prices_1m_proxy', INTERVAL '30 days');

COMMENT ON TABLE prices_1m_proxy IS
    'Aggregator-rewritten 1m VWAPs for stablecoin-fiat-proxy '
    'targets (XLM/fiat:USD synthesised from XLM/USDC-GA5Z… etc). '
    'Mirrors prices_1m''s shape but holds the rewritten target as '
    'base_asset / quote_asset, so /v1/vwap, /v1/twap, /v1/changes, '
    '/v1/history queries on fiat-target pairs return real data. '
    'Per ADR-0025 phase 1.';

COMMIT;
