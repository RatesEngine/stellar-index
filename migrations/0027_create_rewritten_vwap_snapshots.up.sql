-- 0027 up — `rewritten_vwap_snapshots` hypertable.
--
-- ADR-0025 phase 1, **Option B**. Alternative to the bucketed
-- `prices_1m_proxy` schema in the sister branch
-- (#639 + the original "Schema sketch" of ADR-0025).
--
-- Choose between this migration and the bucketed one — only one
-- should ever land on main. See ADR-0025 "Open questions for the
-- operator" for the bucketed-vs-snapshot tradeoff. This file
-- implements the snapshot model.
--
-- ─── Why this shape ────────────────────────────────────────────────
--
-- The aggregator orchestrator does NOT today produce minute-aligned
-- bucket VWAPs for any pair. It produces ROLLING-WINDOW VWAPs at
-- every tick (default Interval=30s), parameterised by lookback
-- (`Windows=[5m, 1h, 24h]`). At each tick it writes one Redis key
-- per (pair, window): `vwap:<base>:<quote>:<window-seconds>`.
--
-- A bucketed mirror table would force the orchestrator to ALSO
-- compute minute-bucket VWAPs in a parallel code path — meaningful
-- new work + drift risk against the rolling path. The snapshot
-- model below mirrors what the orchestrator already produces 1:1:
-- one row per (pair, window, tick).
--
-- Phase 2 wiring (separate PR, after this lands) is genuinely a
-- 1-line UPSERT alongside the Redis SET in
-- `orchestrator.cacheClosedBucket`.
--
-- ─── Reader semantics ──────────────────────────────────────────────
--
-- * **`/v1/price` family for fiat-target queries**: latest snapshot
--   for the smallest available window (typically 5m). Already works
--   via the Redis fallback (#631 / #632 / #634); this table just
--   gives the same data a historical anchor.
-- * **`/v1/vwap` / `/v1/twap` with explicit window**: latest
--   snapshot whose `window_seconds` matches the requested window.
--   Returns 404 if the requested window isn't on the aggregator's
--   configured Windows list (matches today's behaviour for literal
--   pairs whose Window isn't pre-computed).
-- * **`/v1/observations` / `/v1/history/since-inception` /
--   `/v1/chart`**: walk the snapshot time series for the chosen
--   window, downsample to the requested granularity if needed.
-- * **change-summary worker**: read snapshots at d30/d7/h24/h1
--   lookback offsets via index seeks; deltas computed from the four
--   pinned timestamps.
--
-- ─── Costs at launch scale ─────────────────────────────────────────
--
-- * **Storage**: 9 default pairs × 3 windows × 1 tick / 30s ×
--   86_400 s/day = ~77 800 rows/day. 30-day retention ≈ 2.3 M rows.
--   At ~120 B per row (incl. NUMERIC vwap, text[] sources) that's
--   ~280 MB total. Trivial.
-- * **Write IO**: one INSERT per (pair, window) per tick, alongside
--   the existing Redis SET. Writes are append-only — no UPDATE
--   churn — so postgres can append without re-balancing.
-- * **Read IO**: latest-snapshot reads use the
--   `(base_asset, quote_asset, window_seconds, observed_at DESC)`
--   primary-key prefix → O(log n) seek + 1-row fetch. Range reads
--   for history / change-summary use the same prefix → range scan.
--
-- ─── Schema notes ──────────────────────────────────────────────────
--
-- * **observed_at** is the orchestrator's tick timestamp at the
--   moment of the write — same value the Redis key TTL is computed
--   from. Granular to milliseconds (Postgres timestamptz is
--   microsecond, but tick cadence is 30s so practical resolution
--   is seconds).
-- * **window_seconds** is the lookback window the rolling VWAP
--   covers (300, 3600, 86400 in defaults). Stored as integer for
--   tighter packing + cheaper index entries than text/interval.
-- * **OHLC fields are absent** by the same rationale as ADR-0025's
--   bucketed sketch: rolling-window VWAPs across multiple source
--   pairs have no well-defined open/close/high/low (cf. CLAUDE.md
--   on the cross-pair merge).

BEGIN;

CREATE TABLE rewritten_vwap_snapshots (
    -- Wall-clock timestamp the orchestrator wrote this snapshot.
    -- The "observed_at" the API surfaces in price envelopes derives
    -- from this; the value is "the rolling VWAP as of this moment."
    observed_at      timestamptz  NOT NULL,

    -- Rewritten target asset (e.g. "native", "crypto:XLM"). Same
    -- canonical wire form the aggregator's defaultPairs() emits.
    base_asset       text         NOT NULL,

    -- Rewritten target quote (always fiat-typed today: "fiat:USD",
    -- "fiat:EUR", "fiat:GBP"). aggregate.FiatProxy() determines the
    -- proxy targets.
    quote_asset      text         NOT NULL,

    -- Lookback window the rolling VWAP covers. 300 / 3600 / 86400
    -- under default config; operators can override
    -- [aggregate].windows. Stored as integer for cheaper index entries.
    window_seconds   integer      NOT NULL CHECK (window_seconds > 0),

    -- The rewritten VWAP. Quote per base, decimal-string-safe via
    -- NUMERIC.
    vwap             numeric      NOT NULL CHECK (vwap > 0),

    -- Sum of base-asset volume across all contributing source
    -- pairs in the lookback window.
    volume           numeric      NOT NULL CHECK (volume >= 0),

    -- USD-denominated volume across the same window. NULL when
    -- no contributing trade had usd_volume populated (depends on
    -- [trades].usd_pegged_classic_assets — operator-gated).
    volume_usd       numeric      CHECK (volume_usd IS NULL OR volume_usd >= 0),

    -- Total trades that contributed to this rolling-window VWAP.
    trade_count      integer      NOT NULL CHECK (trade_count > 0),

    -- Distinct source connector names that contributed
    -- (["soroswap", "sdex"]). Surfaced via the /v1/price envelope
    -- `sources` field for fiat-target queries.
    sources          text[]       NOT NULL,

    -- (base, quote, window, observed_at DESC) makes "latest snapshot
    -- for this pair+window" a single index-seek + 1-row fetch, and
    -- "snapshots in [from, to) for this pair+window" a range scan.
    PRIMARY KEY (base_asset, quote_asset, window_seconds, observed_at)
);

-- Hypertable on observed_at; daily chunks. Smaller chunk size than
-- prices_1m's 7-day default because writes are dense (one per pair
-- per window per 30s).
SELECT create_hypertable('rewritten_vwap_snapshots', 'observed_at',
    chunk_time_interval => INTERVAL '1 day');

-- 30-day retention matches prices_1m. A future change-summary worker
-- needs at most 30 days of history (d30 lookback); beyond that the
-- snapshot data isn't load-bearing.
SELECT add_retention_policy('rewritten_vwap_snapshots', INTERVAL '30 days');

COMMENT ON TABLE rewritten_vwap_snapshots IS
    'Aggregator-rewritten rolling-window VWAP snapshots. One row '
    'per (base, quote, window) per orchestrator tick. Mirrors the '
    'aggregator''s Redis vwap: keys 1:1; gives /v1/vwap, /v1/twap, '
    '/v1/changes, /v1/history a historical store for rewritten '
    'fiat-target pairs. Per ADR-0025 phase 1 (Option B).';

COMMIT;
