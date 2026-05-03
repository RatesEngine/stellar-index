-- 0005 up — `asset_supply_history` hypertable per ADR-0011.
--
-- Append-only per-asset supply snapshots produced by internal/supply
-- at bucket-close (every fresh ledger boundary that affects an
-- indexed asset). Latest row per asset_key is the queryable current
-- state; the time series is queried for the historical-supply chart
-- on the asset detail page.
--
-- Per ADR-0011:
--   - asset_key is "XLM" | "CODE:G…" | "C…"
--   - all supply fields are NUMERIC (i128 safety per ADR-0003)
--   - max_supply is nullable — uncapped issuers + no override yields
--     no defensible value; we publish NULL rather than fabricate
--   - basis identifies which Algorithm + policy produced the row
--     (xlm_sdf_reserve_exclusion / issuer_exclusion / admin_exclusion
--      / override / no_metadata)

BEGIN;

CREATE TABLE asset_supply_history (
    time                TIMESTAMPTZ NOT NULL,
    asset_key           TEXT        NOT NULL,
    total_supply        NUMERIC     NOT NULL CHECK (total_supply >= 0),
    circulating_supply  NUMERIC     NOT NULL CHECK (circulating_supply >= 0),
    max_supply          NUMERIC                                                       CHECK (max_supply IS NULL OR max_supply >= 0),
    basis               TEXT        NOT NULL,
    ledger_sequence     BIGINT      NOT NULL CHECK (ledger_sequence > 0)
);

COMMENT ON TABLE asset_supply_history IS
    'Per-asset supply snapshots, append-only. Latest row per asset_key is current state. ADR-0011.';

COMMENT ON COLUMN asset_supply_history.asset_key IS
    'Supply-package canonical key — XLM | CODE:G… | C… (colon-separated, distinct from canonical asset wire form).';

COMMENT ON COLUMN asset_supply_history.max_supply IS
    'NULL when uncapped issuer + no SEP-1 declaration + no operator override. Per ADR-0011 we do not fabricate.';

COMMENT ON COLUMN asset_supply_history.basis IS
    'Which Algorithm + policy produced the row. See supply.Basis enum.';

-- Hypertable on `time`. 1-day chunks balance compression
-- granularity vs chunk-management overhead at low row volumes
-- (a few thousand assets × a few writes/day = MB-scale).
SELECT create_hypertable(
    'asset_supply_history',
    'time',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

-- Idempotency: re-deriving supply at the same (asset, ledger) is a
-- no-op via ON CONFLICT DO NOTHING in the writer. Index also serves
-- "did we already write this snapshot" lookups during backfill.
-- TimescaleDB requires the partition column (`time`) in any unique
-- index on a hypertable — including it as a tail key keeps the
-- (asset_key, ledger_sequence) uniqueness invariant unchanged in
-- practice (two writes for the same (asset, ledger) would have
-- the same `time` derived from the ledger close timestamp).
CREATE UNIQUE INDEX asset_supply_history_asset_ledger_idx
    ON asset_supply_history (asset_key, ledger_sequence, time);

-- "Latest row per asset_key" reads — the API hot-path query shape.
-- Composite index supports both the WHERE filter and the ORDER BY
-- without a sort step.
CREATE INDEX asset_supply_history_asset_time_idx
    ON asset_supply_history (asset_key, time DESC);

-- Compression: same shape as trades. Segment by asset_key so chunks
-- compress per-asset (good column-dictionary reuse for basis +
-- ledger_sequence within an asset's history).
ALTER TABLE asset_supply_history SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'asset_key',
    timescaledb.compress_orderby   = 'time DESC'
);

SELECT add_compression_policy('asset_supply_history', INTERVAL '7 days');

-- No retention policy — supply history is small and queryable across
-- the asset's lifetime per ADR-0011 §"historical supply chart".

COMMIT;
