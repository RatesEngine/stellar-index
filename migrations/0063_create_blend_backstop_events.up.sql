-- 0063 up — `blend_backstop_events` hypertable.
--
-- One row per observed Blend Backstop contract event on Stellar. The
-- Backstop is the protocol's insurance / shared-liquidity module — a
-- SEPARATE event surface from the Blend pool / pool-factory decoder
-- (internal/sources/blend, tables blend_positions / blend_emissions /
-- blend_admin / blend_auctions). Two backstop contracts emit here: the
-- V2 singleton and the V1 deployment that preceded it.
--
-- Ten event types (see internal/sources/blend_backstop/decode.go):
--
--   deposit             — stake into a pool's backstop
--   claim               — claim accrued emissions (no pool)
--   donate              — donate tokens to a pool's backstop
--   queue_withdrawal    — queue an unstake (carries expiration)
--   withdraw            — execute a queued unstake
--   distribute          — distribute emissions across backstops
--   gulp_emissions      — pull emissions for a token
--   dequeue_withdrawal  — cancel a queued unstake
--   draw                — draw backstop funds to cover bad debt
--   rw_zone_add         — add a pool to the reward zone
--
-- SCHEMA PROVENANCE: the per-event field layouts were
-- REVERSE-ENGINEERED from real mainnet lake samples (2026-06-15),
-- pending Blend-team confirmation. This source is LIVE-CAPTURE ONLY
-- until then — no BackfillSafe flip, no historical re-derive against
-- these schemas.
--
-- The ten event types carry divergent payloads, so universal /
-- high-signal fields are promoted to typed columns (pool, user_address,
-- amount, amount2) and the event-type-specific remainder (expiration,
-- to, from, token, reward-zone index, …) lands in `attributes` jsonb.
-- The jsonb is a storage blob, not a query surface — no GIN index,
-- matching migration 0038 (cctp_events) / 0045 (blend money-market).
--
-- ── Per ADR-0003 every i128 amount is NUMERIC. ──
-- amount / amount2 are i128 and can exceed 2^63 — truncation to int64
-- is the bug we catch in review every time. Storage is NUMERIC; the Go
-- layer uses decimal strings / *big.Int and the JSON wire shape is a
-- string.
--
-- ── Hypertable shape ──
-- Hypertable on ledger_close_time, daily chunks (consistent with
-- blend_positions / blend_emissions / blend_admin / trades). The PK is
-- the full per-event identity:
--   (ledger_close_time, ledger, tx_hash, op_index, event_index)
-- ledger_close_time leads to satisfy TimescaleDB's TS103 requirement
-- (the partitioning column must appear in every unique index on a
-- hypertable). event_index (the contract event's index within its op —
-- events.Event.EventIndex) is the per-event discriminator: a single
-- backstop op can emit more than one event (e.g. a draw that pays
-- several pools), and they share (ledger, tx_hash, op_index) and the
-- same close time, so without event_index a coarse PK would collapse
-- them under ON CONFLICT DO NOTHING — the same coarse-PK loss class
-- fixed for blend_auctions (0058) / comet (0059) / phoenix (0060).
--
-- ── Retention ──
-- NONE. Per the granular-coverage mission, backstop history is kept
-- forever. Compression after 7 days, segment-by (contract_id,
-- event_kind) for dictionary reuse.

BEGIN;

CREATE TABLE blend_backstop_events (
    -- Emitting backstop contract C-strkey (V1 or V2).
    contract_id        text         NOT NULL,

    -- Soroban event identity.
    ledger             integer      NOT NULL CHECK (ledger >= 0),
    tx_hash            char(64)     NOT NULL,
    op_index           integer      NOT NULL CHECK (op_index >= 0),
    event_index        integer      NOT NULL CHECK (event_index >= 0),
    ledger_close_time  timestamptz  NOT NULL,

    -- Which of the ten backstop events this row is.
    event_kind         text         NOT NULL CHECK (event_kind IN (
        'deposit', 'claim', 'donate',
        'queue_withdrawal', 'withdraw', 'distribute',
        'gulp_emissions', 'dequeue_withdrawal', 'draw',
        'rw_zone_add')),

    -- Promoted typed columns — present for the event types that carry
    -- them, NULL otherwise.
    --   pool:         the per-pool backstop the event addresses
    --                 (NULL for claim / distribute / gulp_emissions)
    --   user_address: the depositor / claimer / withdrawer
    --                 (NULL for donate / distribute / gulp_emissions /
    --                  draw / rw_zone_add)
    --   amount:       the primary i128 amount (deposit/withdraw amount,
    --                 claim/donate/distribute/dequeue amount, queued
    --                 shares, draw amount, gulp_emissions data[0])
    --   amount2:      the secondary i128 amount where the event carries
    --                 two (deposit/withdraw shares, gulp_emissions data[1])
    pool               text,
    user_address       text,
    amount             numeric  CHECK (amount  IS NULL OR amount  >= 0),
    amount2            numeric  CHECK (amount2 IS NULL OR amount2 >= 0),

    -- Event-type-specific remainder as a jsonb blob. Holds, per type:
    --   donate:            from (the donating contract)
    --   queue_withdrawal:  expiration (u64)
    --   gulp_emissions:    token (the emitted token contract)
    --   draw:              to (the recipient of the drawn funds)
    --   rw_zone_add:       index (u32 reward-zone slot)
    attributes         jsonb        NOT NULL DEFAULT '{}'::jsonb,

    ingested_at        timestamptz  NOT NULL DEFAULT now(),

    PRIMARY KEY (ledger_close_time, ledger, tx_hash, op_index, event_index)
);

COMMENT ON TABLE blend_backstop_events IS
    'Per-event Blend Backstop contract events on Stellar (insurance / '
    'shared-liquidity module). Separate surface from the Blend pool '
    'decoder. Hypertable on ledger_close_time. Schemas '
    'lake-reverse-engineered 2026-06-15, pending Blend-team confirmation '
    '— live-capture only.';
COMMENT ON COLUMN blend_backstop_events.amount IS
    'Primary i128 amount; NUMERIC per ADR-0003.';
COMMENT ON COLUMN blend_backstop_events.amount2 IS
    'Secondary i128 amount (deposit/withdraw shares, gulp_emissions '
    'second value); NUMERIC per ADR-0003.';
COMMENT ON COLUMN blend_backstop_events.attributes IS
    'Event-type-specific fields (expiration, to, from, token, index) '
    'as a jsonb blob; not a query surface.';

SELECT create_hypertable(
    'blend_backstop_events',
    'ledger_close_time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists       => TRUE
);

-- Per-contract walk ("every backstop event from V2, newest first").
CREATE INDEX blend_backstop_events_contract_ts_idx
    ON blend_backstop_events (contract_id, ledger_close_time DESC);

-- Cross-contract per-kind scan ("recent deposit flow across backstops").
CREATE INDEX blend_backstop_events_kind_ts_idx
    ON blend_backstop_events (event_kind, ledger_close_time DESC);

-- Per-pool stream ("recent activity in this pool's backstop").
CREATE INDEX blend_backstop_events_pool_ts_idx
    ON blend_backstop_events (pool, ledger_close_time DESC)
    WHERE pool IS NOT NULL;

-- Source-centric replay / debug — walk by ledger.
CREATE INDEX blend_backstop_events_ledger_idx
    ON blend_backstop_events (ledger DESC);

-- Compression capability enabled; segment-by (contract_id, event_kind)
-- for dictionary reuse, ordered newest-first. No automatic policy on a
-- brand-new low-volume table — an operator adds one if storage ever
-- warrants it (mirrors cctp_events / 0038).
ALTER TABLE blend_backstop_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'contract_id, event_kind',
    timescaledb.compress_orderby   = 'ledger_close_time DESC, ledger DESC'
);

COMMIT;
