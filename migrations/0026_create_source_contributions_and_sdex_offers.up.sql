-- 0026 up — `price_source_contributions` + `sdex_offer_events`.
--
-- Two unrelated tables in one migration because they're both small
-- and both Phase 2/4 dependencies. Splitting would be ceremony.
--
-- ─── price_source_contributions ────────────────────────────────────
--
-- Per-closed-bucket per-source weight + volume. Today the aggregator
-- computes which sources contributed how much to a VWAP at compute
-- time but discards the breakdown — only the names appear in the
-- response's `sources: [...]` array.
--
-- This table persists the breakdown so we can render the
-- source-contribution donut on every price card across the showcase
-- (`{sdex: 38%, binance: 27%, kraken: 22%, coinbase: 13%}`). Every
-- price page asks "where did this price come from" — that's the
-- single most-visible miss across the planning doc.
--
-- Hypertable on bucket. One row per (asset, quote, bucket, source)
-- on every closed-bucket flush. Volume scales linearly with active
-- pairs × source count × buckets-per-window. Manageable.
--
-- ─── sdex_offer_events ─────────────────────────────────────────────
--
-- Full SDEX offer lifecycle (creates, updates, deletes). The
-- existing SDEX decoder (`internal/sources/sdex/`) only captures
-- offer fills — i.e. when an offer matches and produces a trade.
-- Resting offers (the order book) are invisible to us today.
--
-- Capturing the lifecycle gives us:
--   - SDEX order-book depth ladder (`/v1/orderbook`).
--   - Slippage simulator (`/v1/slippage`).
--   - Bid-ask spread chart (`/v1/spread`).
--   - "Liquidity heatmap" panels.
--
-- Hypertable on observed_at. One row per ManageOffer / CreatePassive
-- / SetTrustLineFlags op that touches an offer. Volume is high but
-- bounded by Stellar's tx throughput.

BEGIN;

CREATE TABLE price_source_contributions (
    -- The pair this contribution belongs to.
    asset_id      text         NOT NULL,
    quote_id      text         NOT NULL,

    -- Closed-bucket boundary. Aligned to the granularity of the
    -- VWAP being computed — same as `prices_1m.bucket_start` etc.
    bucket        timestamptz  NOT NULL,

    -- Which source contributed.
    source        text         NOT NULL,

    -- Fraction of the VWAP this source provided. 0.0-1.0; sums to
    -- 1.0 across all rows for the (asset, quote, bucket).
    weight        numeric      NOT NULL CHECK (weight >= 0 AND weight <= 1),

    -- USD-denominated volume this source contributed. Helps the
    -- donut tooltip show "binance contributed 27% / $4.2M".
    volume_usd    numeric      CHECK (volume_usd IS NULL OR volume_usd >= 0),

    -- Number of trades from this source within the bucket.
    trade_count   integer      NOT NULL CHECK (trade_count >= 0),

    PRIMARY KEY (asset_id, quote_id, bucket, source)
);

COMMENT ON TABLE price_source_contributions IS
    'Per-closed-bucket source breakdown of every VWAP. Powers the '
    'source-contribution donut on every price card.';

SELECT create_hypertable(
    'price_source_contributions',
    'bucket',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

-- Most common read: "what's the breakdown for this pair right now?"
-- (asset, quote, bucket DESC) is covered by the PK. The bucket-only
-- recency index is auto-created by create_hypertable above; no need
-- to add a duplicate (an explicit index named *_bucket_idx would
-- collide with TimescaleDB's auto-generated one).

ALTER TABLE price_source_contributions SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'asset_id, quote_id, source',
    timescaledb.compress_orderby   = 'bucket DESC'
);

CREATE TABLE sdex_offer_events (
    -- Stellar offer ID (uint64). Same offer ID can have many events
    -- (create + multiple updates + delete).
    offer_id           bigint       NOT NULL,

    -- The op that emitted the event.
    ledger             integer      NOT NULL CHECK (ledger >= 0),
    tx_hash            char(64)     NOT NULL,
    op_index           integer      NOT NULL CHECK (op_index >= 0),
    observed_at        timestamptz  NOT NULL,

    -- Event kind. CHECK enumerates the lifecycle states the
    -- decoder can emit.
    kind               text         NOT NULL CHECK (kind IN
                                                   ('create','update','delete','partial_fill','full_fill')),

    -- Seller account (the one offering).
    seller_g_strkey    text         NOT NULL,

    -- The pair being offered: selling base for quote.
    base_asset_id      text         NOT NULL,
    quote_asset_id     text         NOT NULL,

    -- Offer amount + price. Price is rational (n/d) on Stellar;
    -- we store both forms so the read side can choose.
    amount             numeric      CHECK (amount IS NULL OR amount >= 0),
    price_n            bigint,
    price_d            bigint,
    -- Computed price = price_n / price_d. Stored alongside for
    -- index-friendly range queries.
    price              numeric,

    -- For fill events: the matched amount (base) + counterparty.
    matched_amount     numeric      CHECK (matched_amount IS NULL OR matched_amount >= 0),
    counterparty       text,

    PRIMARY KEY (ledger, tx_hash, op_index, observed_at)
);

COMMENT ON TABLE sdex_offer_events IS
    'Full SDEX offer lifecycle (create / update / delete / fill). '
    'Captures resting offers — the order book — alongside the '
    'fills the existing trades hypertable already has.';

SELECT create_hypertable(
    'sdex_offer_events',
    'observed_at',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

-- "What's the current order book for pair X?" — most common read.
-- Group by (base, quote) ordered by recency.
CREATE INDEX sdex_offer_events_pair_idx
    ON sdex_offer_events (base_asset_id, quote_asset_id, observed_at DESC);

-- "What's been happening with offer X over its lifetime?"
CREATE INDEX sdex_offer_events_offer_idx
    ON sdex_offer_events (offer_id, observed_at DESC);

-- "Show me activity by this account."
CREATE INDEX sdex_offer_events_seller_idx
    ON sdex_offer_events (seller_g_strkey, observed_at DESC);

ALTER TABLE sdex_offer_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'base_asset_id, quote_asset_id',
    timescaledb.compress_orderby   = 'observed_at DESC, ledger DESC'
);

COMMIT;
