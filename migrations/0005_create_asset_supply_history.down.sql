-- 0005 down — reverse of 0005_create_asset_supply_history.up.sql.

BEGIN;

-- Compression + retention policies auto-drop with the hypertable.
DROP TABLE IF EXISTS asset_supply_history;

COMMIT;
