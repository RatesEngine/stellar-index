-- 0047 up — `sep41_transfers` hypertable (F-0021 closure).
--
-- Materialises every SEP-41 `transfer` event (and the `approve` /
-- `set_admin` / `set_authorized` administrative events) so per-
-- account net-position queries become first-class — the Stellar
-- moat feature CG/CMC structurally cannot offer.
--
-- Closes the F-0021 partial-scope from audit-2026-05-26.
-- mint/burn/clawback already live in sep41_supply_events
-- (Algorithm 3); this table is strictly the audit-trail set.
--
-- Wire shape (per SEP-41 v0.4.1 + cap-67-unified-events.md):
--   transfer        topics:("transfer", from, to)         data: i128 OR map{amount,to_muxed_id}
--   approve         topics:("approve", from, spender)     data: [i128 amount, u32 live_until_ledger]
--   set_admin       topics:("set_admin", admin?)          data: Address(new_admin)
--   set_authorized  topics:("set_authorized", id, asset?) data: bool authorize
--
-- All amounts NUMERIC per ADR-0003 (never BIGINT — i128 can
-- exceed int64).

BEGIN;

CREATE TABLE sep41_transfers (
    ledger_close_time timestamptz NOT NULL,
    ledger            integer     NOT NULL CHECK (ledger >= 0),
    tx_hash           bytea       NOT NULL,
    op_index          smallint    NOT NULL CHECK (op_index >= 0),
    event_index       smallint    NOT NULL DEFAULT 0 CHECK (event_index >= 0),
    contract_id       text        NOT NULL,
    event_kind        text        NOT NULL
                                  CHECK (event_kind IN ('transfer','approve','set_admin','set_authorized')),
    from_addr         text,
    to_addr           text,
    amount            numeric,
    live_until_ledger integer,
    authorized        boolean,
    ingested_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (ledger_close_time, contract_id, ledger, tx_hash, op_index, event_index)
);

COMMENT ON TABLE sep41_transfers IS
    'F-0021 closure: every SEP-41 transfer/approve/set_admin/set_authorized '
    'event materialised for per-account audit-trail + net-position queries. '
    'mint/burn/clawback live in sep41_supply_events (Algorithm 3). '
    'audit-2026-05-26.';

SELECT create_hypertable(
    'sep41_transfers',
    'ledger_close_time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists       => TRUE
);

CREATE INDEX sep41_transfers_contract_from_idx
    ON sep41_transfers (contract_id, from_addr, ledger_close_time DESC);
CREATE INDEX sep41_transfers_contract_to_idx
    ON sep41_transfers (contract_id, to_addr, ledger_close_time DESC);

ALTER TABLE sep41_transfers SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'contract_id',
    timescaledb.compress_orderby   = 'ledger_close_time DESC, ledger DESC'
);

SELECT add_compression_policy('sep41_transfers', INTERVAL '7 days', if_not_exists => TRUE);

COMMIT;
