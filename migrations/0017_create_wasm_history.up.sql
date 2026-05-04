-- 0017 up — `wasm_versions` + `contract_wasm_history`.
--
-- WASM history made into a queryable, postgres-resident first-class
-- citizen. Today the data lives in `ratesengine-ops wasm-history` JSONL
-- output on r1; usable for one-shot audits but invisible to the API.
-- This migration is the foundation for the protocol/contract explorer
-- pages (per docs/architecture/showcase-site-data-inventory.md §7.10)
-- and for the "contract version timeline" surface that lets users
-- diff WASM versions side by side.
--
-- Two tables, separated by responsibility:
--
--   `wasm_versions` — content-addressed by sha256 of the bytecode.
--   One row per unique (wasm_hash). The bytecode itself is inlined as
--   bytea. Typical Soroban WASM is 50-500 KB; ~50 contracts × ~5
--   versions = ~50 MB total. Trivial inline; postgres TOAST handles
--   compression.
--
--   `contract_wasm_history` — temporal index. One row per
--   (contract_id, first_ledger). `last_ledger IS NULL` flags the
--   currently-running version. Many-to-one against `wasm_versions`
--   because the same wasm can run on many contracts, and a contract
--   can run any wasm at different points in its life.
--
-- Backfilled from existing `wasm-history` JSONL on r1. Live updates
-- come from a new `internal/wasm` observer (Phase 4) watching
-- `UploadContractWasm` ops + `ContractCode` LedgerEntry creates.

BEGIN;

CREATE TABLE wasm_versions (
    -- Content-addressed: sha256 of the bytecode, lower-hex.
    wasm_hash         char(64)    NOT NULL,

    -- The bytecode itself. NOT NULL — a row without bytes is meaningless.
    bytecode          bytea       NOT NULL,
    bytecode_size     integer     NOT NULL CHECK (bytecode_size > 0),

    -- First time we saw this hash on-chain. May predate our ingest;
    -- for backfilled rows from JSONL this is the earliest ledger that
    -- referenced the hash.
    first_seen_at     timestamptz NOT NULL DEFAULT now(),
    first_seen_ledger integer     NOT NULL CHECK (first_seen_ledger >= 0),

    PRIMARY KEY (wasm_hash)
);

COMMENT ON TABLE wasm_versions IS
    'Content-addressed WASM bytecode store. One row per unique '
    'sha256(bytecode). Powers the contract-page "WASM bytecode + WAT" '
    'panel (see docs/architecture/showcase-site-data-inventory.md '
    '§7.10).';

COMMENT ON COLUMN wasm_versions.bytecode IS
    'Inline bytea; postgres TOAST handles compression. Typical size '
    '50-500 KB per row.';

CREATE TABLE contract_wasm_history (
    -- The C-strkey of the contract.
    contract_id   text     NOT NULL,

    -- The ledger range this WASM version was active for this contract.
    -- last_ledger NULL = current version; one such row per contract
    -- at most.
    first_ledger  integer  NOT NULL CHECK (first_ledger >= 0),
    last_ledger   integer  CHECK (last_ledger IS NULL OR last_ledger >= first_ledger),

    -- The WASM hash this contract was running. References the
    -- wasm_versions row that holds the bytes.
    wasm_hash     char(64) NOT NULL REFERENCES wasm_versions(wasm_hash),

    PRIMARY KEY (contract_id, first_ledger)
);

COMMENT ON TABLE contract_wasm_history IS
    'Temporal mapping of contract → WASM version. One row per '
    'upgrade. last_ledger IS NULL marks the currently-running '
    'version. Powers the contract-page WASM-history timeline.';

-- Most common reader query: "what WASM is contract X running NOW?"
-- A partial index on the open-ended row makes that O(1).
CREATE INDEX contract_wasm_history_current_idx
    ON contract_wasm_history (contract_id) WHERE last_ledger IS NULL;

-- Per-contract upgrade history walks: ordered by first_ledger.
-- The PK already covers (contract_id, first_ledger) so reverse
-- scans are free; no extra index needed.

-- "Which contracts ran this WASM?" — the inverse lookup. Modest
-- frequency but useful for the WASM-version-decoder coverage panel.
CREATE INDEX contract_wasm_history_wasm_idx
    ON contract_wasm_history (wasm_hash);

COMMIT;
