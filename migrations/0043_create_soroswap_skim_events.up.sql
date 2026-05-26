-- 0043 up — `soroswap_skim_events` hypertable (task #28).
--
-- One row per observed Soroswap pair-contract `skim` event. The 5th
-- pair-contract event (alongside swap/sync/deposit/withdraw); the
-- Uniswap-v2-style mechanism where a caller claims excess tokens
-- that accumulated in the pool above its recorded reserves.
--
-- Skim isn't a trade — it doesn't move price, doesn't feed VWAP, and
-- never lands in the trades hypertable. We capture it for protocol
-- completeness (every emitted topic gets classified — no shortcuts;
-- granular-coverage mission) and so reserve-divergence analytics
-- can correlate skim with the immediately-following sync event.
--
-- Body shape per Phase-1 audit (docs/discovery/dexes-amms/soroswap.md
-- §"SoroswapPair, skim"):
--
--   struct SkimEvent { skimmed_0: i128, skimmed_1: i128 }
--
-- The decoder accepts both `skimmed_0`/`skimmed_1` and the
-- `amount_0`/`amount_1` aliases (some Uniswap-v2 derivatives use the
-- latter); a `to` Address field is optional and stored when present.
-- Per contract-schema-evolution.md the columns are nullable where the
-- on-wire shape might evolve.
--
-- Identity: (ledger_close_time, ledger, tx_hash, op_index,
-- event_index). ledger_close_time leads the PK because TimescaleDB
-- requires the partition column in every unique index on a
-- hypertable (TS103 error otherwise — see migration 0041 lesson).
-- event_index is in the PK so a future Soroban op shape that emits
-- multiple skims per op (none today) still has unique rows.
--
-- Historical fill: the live decoder writes rows from rc.79+; pre-rc.79
-- skim events are recoverable by replaying soroban_events (ADR-0029
-- raw landing zone, migration 0041) filtered to topic_0_sym='skim' +
-- contract_id matching a known Soroswap pair. Operator runbook ships
-- alongside this migration in CHANGELOG's task #28 follow-up.
--
-- Volume estimate: skim is rare (caller-initiated when reserves drift
-- below the actual balance — typically a direct-transfer bug). Phase-1
-- audit doc literally tags it "rare". Single-digit rows/day expected
-- across all Soroswap pairs combined; compression policy mirrors the
-- broader hypertables for consistency rather than necessity.
--
-- Retention: NONE — granular-coverage mission preserves every row.

BEGIN;

CREATE TABLE soroswap_skim_events (
    -- Timestamp comes first in the PK; partition column.
    ledger_close_time timestamptz NOT NULL,

    -- Identity within Soroban: (ledger, tx_hash, op_index, event_index).
    ledger            integer     NOT NULL CHECK (ledger >= 0),
    tx_hash           bytea       NOT NULL, -- 32-byte raw hash
    op_index          smallint    NOT NULL CHECK (op_index >= 0),
    event_index       smallint    NOT NULL CHECK (event_index >= 0),

    -- The emitting Soroswap pair contract (C-strkey).
    contract_id       text        NOT NULL,

    -- Optional recipient address — empty/NULL on today's Soroswap
    -- WASM; populated if a future upgrade adds a `to` field. Stored
    -- as text (strkey) for parity with other Soroswap address columns.
    to_address        text,

    -- i128 excess amounts of token0 / token1 the skim transferred
    -- out of the pool. NUMERIC per ADR-0003 (never bigint).
    amount_0          numeric     NOT NULL,
    amount_1          numeric     NOT NULL,

    ingested_at       timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (ledger_close_time, ledger, tx_hash, op_index, event_index)
);

COMMENT ON TABLE soroswap_skim_events IS
    'Per-event Soroswap pair-contract skim events — caller-initiated '
    'claim of excess pool balance above reserves. Not a trade; never '
    'feeds VWAP. Hypertable on ledger_close_time. See task #28 + '
    'docs/discovery/dexes-amms/soroswap.md §SkimEvent.';
COMMENT ON COLUMN soroswap_skim_events.to_address IS
    'Optional recipient strkey; NULL when the contract WASM does not '
    'surface a `to` field in the event body (true on Soroswap today).';
COMMENT ON COLUMN soroswap_skim_events.amount_0 IS
    'i128 token0 excess transferred. NUMERIC per ADR-0003.';
COMMENT ON COLUMN soroswap_skim_events.amount_1 IS
    'i128 token1 excess transferred. NUMERIC per ADR-0003.';

SELECT create_hypertable(
    'soroswap_skim_events',
    'ledger_close_time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists       => TRUE
);

-- Per-pair walk ("every skim from this pair contract").
CREATE INDEX soroswap_skim_events_contract_ts_idx
    ON soroswap_skim_events (contract_id, ledger_close_time DESC);

-- Compression capability — segment-by contract_id (a per-pair scan
-- is the dominant analytical query), order-by newest-first.
ALTER TABLE soroswap_skim_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'contract_id',
    timescaledb.compress_orderby   = 'ledger_close_time DESC, ledger DESC'
);

-- Compress chunks older than 7 days — same horizon as trades /
-- oracle_updates / soroban_events for operator-mental-model
-- consistency. Skim is low-volume so this is mostly defensive.
SELECT add_compression_policy(
    'soroswap_skim_events',
    INTERVAL '7 days',
    if_not_exists => TRUE
);

COMMIT;
