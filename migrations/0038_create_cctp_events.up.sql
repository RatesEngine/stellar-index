-- 0038 up — `cctp_events` hypertable (#40).
--
-- One row per observed Circle CCTP v2 contract event on Stellar.
-- The four event types (see internal/sources/cctp/decode.go):
--
--   deposit_for_burn   — outbound USDC burn (supply exit from Stellar)
--   mint_and_withdraw  — inbound USDC mint  (supply entry to Stellar)
--   message_sent       — wire envelope, paired with deposit_for_burn
--   message_received   — wire envelope, paired with mint_and_withdraw
--
-- This is the bridge-flow side of the granular-coverage mission.
-- CCTP does not publish prices — it is a bridge, not a market — so
-- these rows never reach the trades hypertable or VWAP (the
-- registry entry is Class=ClassBridge, IncludeInVWAP=false).
--
-- Storage shape: per-protocol table (vs a shared bridge_events),
-- operator-confirmed 2026-05-22. Rozo (#41) gets its own
-- rozo_events mirror. See docs/architecture/cctp-stellar-coverage.md
-- §Storage shape.
--
-- The four event types carry divergent payloads, so universal /
-- high-signal fields are promoted to typed columns (amount, fee,
-- token, counterparty_domain) and the event-type-specific remainder
-- lands in `attributes` jsonb. The jsonb is a storage blob, not a
-- query surface — no GIN index, matching migration 0029's finding
-- that unindexed-by-content jsonb is the right call here.
--
-- Identity: (contract_id, ledger, tx_hash, op_index, event_type).
-- ts drags into the PK because TimescaleDB requires the partition
-- column there (same as sep41_supply_events / 0015). One CCTP
-- contract emits at most one event per op, so the tuple is unique;
-- event_type is in the key as defence in depth.
--
-- Retention: NONE. Granular coverage is the mission — bridge-flow
-- history is kept forever.

BEGIN;

CREATE TABLE cctp_events (
    -- Emitting contract C-strkey (TokenMessengerMinter /
    -- MessageTransmitter / CctpForwarder).
    contract_id   text         NOT NULL,

    -- Soroban event identity.
    ledger        integer      NOT NULL CHECK (ledger >= 0),
    tx_hash       char(64)     NOT NULL,
    op_index      integer      NOT NULL CHECK (op_index >= 0),
    ts            timestamptz  NOT NULL,

    -- Which of the four CCTP v2 events this row is.
    event_type    text         NOT NULL CHECK (event_type IN (
        'deposit_for_burn', 'mint_and_withdraw',
        'message_sent', 'message_received')),

    -- Promoted typed columns — present for the event types that
    -- carry them, NULL otherwise. NUMERIC per ADR-0003 (i128
    -- amounts never truncate to int64).
    --   amount: deposit_for_burn.amount / mint_and_withdraw.amount
    --   fee:    deposit_for_burn.max_fee / mint_and_withdraw.fee_collected
    --   token:  burn_token (outbound) / mint_token (inbound), a
    --           Stellar Address strkey
    --   counterparty_domain: destination_domain (outbound) /
    --           source_domain (inbound) — the CCTP domain ID of the
    --           other chain (0=Ethereum, 1=Avalanche, 7=Solana, …)
    amount               numeric  CHECK (amount IS NULL OR amount >= 0),
    fee                  numeric  CHECK (fee    IS NULL OR fee    >= 0),
    token                text,
    counterparty_domain  integer  CHECK (counterparty_domain IS NULL OR counterparty_domain >= 0),

    -- Event-type-specific remainder as a jsonb blob. Holds, per type:
    --   deposit_for_burn:  depositor, mint_recipient,
    --                      destination_token_messenger,
    --                      destination_caller, min_finality_threshold,
    --                      hook_data
    --   mint_and_withdraw: mint_recipient
    --   message_sent:      message
    --   message_received:  caller, nonce, finality_threshold_executed,
    --                      sender, message_body
    attributes    jsonb        NOT NULL DEFAULT '{}'::jsonb,

    ingested_at   timestamptz  NOT NULL DEFAULT now(),

    PRIMARY KEY (contract_id, ledger, tx_hash, op_index, event_type, ts)
);

COMMENT ON TABLE cctp_events IS
    'Per-event Circle CCTP v2 bridge events on Stellar. Class=bridge, '
    'never contributes to VWAP. Hypertable on ts. See #40 + '
    'docs/architecture/cctp-stellar-coverage.md.';
COMMENT ON COLUMN cctp_events.counterparty_domain IS
    'CCTP domain ID of the other chain; NULL for message_sent / '
    'mint_and_withdraw which carry no domain field.';
COMMENT ON COLUMN cctp_events.attributes IS
    'Event-type-specific fields as a jsonb blob; not a query surface.';

SELECT create_hypertable(
    'cctp_events',
    'ts',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

-- Per-contract walk ("every event from TokenMessengerMinter, newest
-- first") — covers the WHERE + ORDER BY without a sort.
CREATE INDEX cctp_events_contract_ledger_idx
    ON cctp_events (contract_id, ledger DESC);

-- Cross-contract per-type scan ("recent deposit_for_burn flow").
CREATE INDEX cctp_events_type_ts_idx
    ON cctp_events (event_type, ts DESC);

-- Compression capability enabled (good column-dictionary reuse when
-- grouped by contract + type); no automatic policy — CCTP is a
-- brand-new, low-volume table, so an operator adds a policy later
-- if storage ever warrants it. Mirrors sep41_supply_events (0015).
ALTER TABLE cctp_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'contract_id, event_type',
    timescaledb.compress_orderby   = 'ts DESC, ledger DESC'
);

COMMIT;
