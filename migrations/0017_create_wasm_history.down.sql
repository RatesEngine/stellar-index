-- 0017 down — drop the WASM history tables.
--
-- Loses every captured bytecode + temporal mapping. Re-applying 0017
-- recovers an empty state; the JSONL backfill on r1 is the only way
-- to repopulate without re-walking ledger history.

BEGIN;

DROP TABLE IF EXISTS contract_wasm_history;
DROP TABLE IF EXISTS wasm_versions;

COMMIT;
