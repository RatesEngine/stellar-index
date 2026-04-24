-- 0004 rollback: restore the ledger > 0 constraint.
--
-- Decompress existing chunks so the constraint swap is allowed
-- (Timescale blocks DROP/ADD CONSTRAINT on a compressed hypertable).
-- We do NOT re-enable compression in this down migration —
-- re-compression happens naturally via the compression policy on
-- the next background-job run, OR would be covered by re-running
-- migration 0001 from scratch. Rolling through all migrations for
-- a full reset is what the down-path drops anyway.
--
-- The stricter constraint is added with NOT VALID so existing rows
-- are not re-validated; operators explicitly VALIDATE when they're
-- confident no off-chain rows remain:
--
--     ALTER TABLE trades VALIDATE CONSTRAINT trades_ledger_check;

SELECT decompress_chunk(c, true)
FROM show_chunks('trades') c;

ALTER TABLE trades SET (timescaledb.compress = false);

ALTER TABLE trades DROP CONSTRAINT IF EXISTS trades_ledger_check;

ALTER TABLE trades ADD CONSTRAINT trades_ledger_check
    CHECK (ledger > 0) NOT VALID;
