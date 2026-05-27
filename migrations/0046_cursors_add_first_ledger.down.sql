-- 0046 down — drop the `first_ledger` column from ingestion_cursors.
--
-- Reverses 0046 up cleanly. Density-coverage projection survives the
-- rollback: the cursor-first path tolerates a missing first_ledger
-- (NULL semantics already mean "fall back to sourceGenesisLedger");
-- removing the column entirely simply makes every cursor look like
-- the pre-rollout state.

BEGIN;

ALTER TABLE ingestion_cursors
    DROP COLUMN IF EXISTS first_ledger;

COMMIT;
