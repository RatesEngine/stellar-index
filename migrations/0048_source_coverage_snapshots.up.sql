-- ADR-0031: source_coverage_snapshots is the data-derived coverage
-- read surface. The gap detector (running in the aggregator binary)
-- upserts one row per (source, table) after every successful scan
-- cycle. The /v1/diagnostics/ingestion handler (running in the API
-- binary) reads from this table to surface the data-derived
-- DensityPct + GapFreePct alongside the legacy cursor-derived
-- density (Phase 1 shadow mode). After Phase 2 the cursor-derived
-- computation is deleted and this is the sole coverage source.
--
-- One row per (source, table). Updates are idempotent — UPSERT on
-- (source, table) PK overwrites with the most recent scan's
-- numbers. No history: gap_detector_runs_total carries the
-- cycle-count signal already; this table only ever holds the
-- latest reading.
--
-- This is a regular table (not a hypertable) — ~16 rows total,
-- updated once per 30-min cycle. Indexes for the read pattern
-- (single-source lookup) are unnecessary at this scale.

CREATE TABLE source_coverage_snapshots (
    source              TEXT      NOT NULL,
    "table"             TEXT      NOT NULL,
    distinct_ledgers    BIGINT    NOT NULL CHECK (distinct_ledgers >= 0),
    expected_ledgers    BIGINT    NOT NULL CHECK (expected_ledgers >= 0),
    max_gap_ledgers     BIGINT    NOT NULL CHECK (max_gap_ledgers >= 0),
    gap_count           BIGINT    NOT NULL CHECK (gap_count >= 0),
    -- density_pct and gap_free_pct are stored as numeric for
    -- exact comparison + zero-cost render. Both [0.0, 1.0].
    density_pct         NUMERIC   NOT NULL CHECK (density_pct  BETWEEN 0 AND 1),
    gap_free_pct        NUMERIC   NOT NULL CHECK (gap_free_pct BETWEEN 0 AND 1),
    last_updated        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source, "table")
);

COMMENT ON TABLE source_coverage_snapshots IS
    'ADR-0031 data-derived coverage projection. Gap detector upserts; diagnostic handler reads. ~16 rows, updated per cycle (30 min).';
