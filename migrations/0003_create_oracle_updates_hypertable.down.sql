-- 0003 down — reverse of 0003_create_oracle_updates_hypertable.up.sql.

BEGIN;

-- Retention + compression policies drop with the hypertable.
DROP TABLE IF EXISTS oracle_updates;

COMMIT;
