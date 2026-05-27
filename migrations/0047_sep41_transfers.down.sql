-- 0047 down — drop the sep41_transfers hypertable (F-0021).
BEGIN;
DROP TABLE IF EXISTS sep41_transfers;
COMMIT;
