-- 0039 up — `rozo_events` hypertable (#41).
--
-- One row per observed Rozo intent-bridge contract event on
-- Stellar. Scoped to v1 Payment — the only mainnet-live Rozo
-- contracts at time of writing (three deployments, identical
-- schema; see internal/sources/rozo). The two v1 event types:
--
--   payment — emitted by pay(from, amount, memo); a user bridge-out
--             of USDC with a destination tag (the memo)
--   flush   — emitted by flush(token); an admin sweep of a
--             non-USDC balance accidentally sent to the contract
--
-- Bridge flow, not a market: Rozo publishes no prices and emits no
-- trades, so these rows never reach the trades hypertable or VWAP
-- (registry entry is Class=ClassBridge, IncludeInVWAP=false).
--
-- Storage shape: per-protocol table, the same decision taken for
-- cctp_events (0038) — operator-confirmed 2026-05-22. v1 Payment is
-- simple enough to fully type; there is no jsonb attributes blob.
-- v2 Forwarder / IntentBridge are pre-mainnet — when they deploy,
-- their richer schema gets its own migration rather than widening
-- this table.
--
-- Identity: (contract_id, ledger, tx_hash, op_index, event_type).
-- ts drags into the PK because TimescaleDB requires the partition
-- column there (same as cctp_events / sep41_supply_events).
--
-- Retention: NONE — granular-coverage mission keeps bridge history.

BEGIN;

CREATE TABLE rozo_events (
    -- Emitting Rozo v1 Payment contract C-strkey.
    contract_id   text         NOT NULL,

    -- Soroban event identity.
    ledger        integer      NOT NULL CHECK (ledger >= 0),
    tx_hash       char(64)     NOT NULL,
    op_index      integer      NOT NULL CHECK (op_index >= 0),
    ts            timestamptz  NOT NULL,

    event_type    text         NOT NULL CHECK (event_type IN ('payment', 'flush')),

    -- Both event types carry amount + destination.
    amount        numeric      NOT NULL CHECK (amount >= 0),
    destination   text         NOT NULL,

    -- payment-only: the payer ('from') and the user-supplied memo
    -- (a Binance / Coinbase deposit tag, a merchant order id, …).
    -- NULL on flush rows. An empty-string memo is a real value (a
    -- bridge-out with no tag) and is preserved as '' — only flush
    -- rows store NULL here.
    from_addr     text,
    memo          text,

    -- flush-only: the swept token. NULL on payment rows (v1 pay()
    -- only ever moves USDC, hardcoded at contract init).
    token         text,

    ingested_at   timestamptz  NOT NULL DEFAULT now(),

    PRIMARY KEY (contract_id, ledger, tx_hash, op_index, event_type, ts)
);

COMMENT ON TABLE rozo_events IS
    'Per-event Rozo v1 intent-bridge events on Stellar. Class=bridge, '
    'never contributes to VWAP. Hypertable on ts. See #41 + '
    'docs/architecture/rozo-stellar-coverage.md.';
COMMENT ON COLUMN rozo_events.memo IS
    'payment-only user tag; NULL on flush rows, '''' is a valid tag.';
COMMENT ON COLUMN rozo_events.token IS
    'flush-only swept token; NULL on payment rows (pay() is USDC-only).';

SELECT create_hypertable(
    'rozo_events',
    'ts',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

-- Per-contract walk ("every Rozo event from this deployment").
CREATE INDEX rozo_events_contract_ledger_idx
    ON rozo_events (contract_id, ledger DESC);

-- Cross-contract per-type scan ("recent payment flow").
CREATE INDEX rozo_events_type_ts_idx
    ON rozo_events (event_type, ts DESC);

-- Compression capability enabled; no automatic policy — Rozo is a
-- low-volume table, an operator adds a policy later if storage ever
-- warrants it. Mirrors cctp_events (0038) / sep41_supply_events.
ALTER TABLE rozo_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'contract_id, event_type',
    timescaledb.compress_orderby   = 'ts DESC, ledger DESC'
);

COMMIT;
