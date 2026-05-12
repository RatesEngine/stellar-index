-- 0030 down — undo the constraint promotion. The DROP CONSTRAINT
-- leaves the underlying unique index in place (Postgres does NOT
-- drop an index when its associated constraint is dropped if the
-- index was originally promoted from an existing index — but the
-- behaviour is actually to drop both, see PG docs §5.3.5). To be
-- safe we explicitly re-create the unique index after dropping the
-- constraint so the schema settles in the pre-0030 state.
--
-- F-1261 (codex audit-2026-05-13): the up migration decompresses
-- chunks + disables compression before the DDL; mirror that here
-- so the down migration is also runnable against a compressed
-- hypertable. Compression is re-enabled at the end with the same
-- 0005 settings, matching what the up path restores.

SELECT decompress_chunk(c, true)
  FROM show_chunks('asset_supply_history') c;

ALTER TABLE asset_supply_history SET (timescaledb.compress = false);

ALTER TABLE asset_supply_history
    DROP CONSTRAINT asset_supply_history_asset_ledger_idx;

CREATE UNIQUE INDEX IF NOT EXISTS asset_supply_history_asset_ledger_idx
    ON asset_supply_history (asset_key, ledger_sequence, time);

ALTER TABLE asset_supply_history SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'asset_key',
    timescaledb.compress_orderby   = 'time DESC'
);
