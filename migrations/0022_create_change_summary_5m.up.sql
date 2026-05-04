-- 0022 up — `change_summary_5m` materialized rollup.
--
-- The single endpoint that powers every multi-window delta strip
-- on the showcase. Every "$0.1234 · 1h: +0.5% · 24h: +3.2% · 7d:
-- −1.1% · 30d: +18.4%" badge across the site reads one row from
-- this table.
--
-- Refreshed every 5 minutes by an aggregator worker (Phase 3,
-- showcase-site-data-inventory.md §11). For each known
-- (entity_type, entity_id) the worker:
--   - Reads the current value from the appropriate source table
--     (prices_1m for coins, tvl_observations for protocols, etc.).
--   - Looks back 1h / 24h / 7d / 30d to compute deltas.
--   - Walks the entity's full history to refresh ATH / ATL.
--   - Maintains the streak-direction + streak-day counter.
--   - Computes acceleration (sign of d²/dt² over a smoothed window).
--
-- O(N) per refresh where N = entity count (≈10k assets + 10
-- protocols + 200 pairs + 30 sources). Trivial cost on a per-tick
-- basis.
--
-- Why this exists: multi-window deltas on every entity in every
-- list view would otherwise mean N+1 queries per page render. By
-- pre-computing into a single keyed-on-PK table, every list view
-- becomes a JOIN of the source table with this one — single round
-- trip, single cache key.
--
-- Not a hypertable: this is keyed on the entity, not on time.
-- Rows are UPDATE'd in place, not INSERT'd. Bounded cardinality
-- (entity count). One refresh = ~10k UPDATEs.

BEGIN;

CREATE TABLE change_summary_5m (
    -- Discriminator + identifier. The CHECK enumerates the entity
    -- types the worker computes for; adding a new entity type
    -- requires a migration AND a worker code change so the
    -- coupling is explicit.
    entity_type      text         NOT NULL CHECK (entity_type IN
                                                 ('coin','protocol','pair','source')),
    entity_id        text         NOT NULL,

    refreshed_at     timestamptz  NOT NULL DEFAULT now(),
    current_value    numeric      NOT NULL,

    -- Multi-window deltas. value = the snapshot at that point in
    -- the past; delta_pct = (current - value) / value * 100.
    -- Both nullable: a young entity with <1h of history has
    -- everything NULL, an entity with <24h has only h1_*
    -- populated, etc.
    h1_value         numeric,
    h1_delta_pct     numeric,
    h24_value        numeric,
    h24_delta_pct    numeric,
    d7_value         numeric,
    d7_delta_pct     numeric,
    d30_value        numeric,
    d30_delta_pct    numeric,

    -- All-time high / low and when they happened.
    ath_value        numeric,
    ath_at           timestamptz,
    atl_value        numeric,
    atl_at           timestamptz,

    -- Streak: direction over the last N consecutive observations
    -- (worker config). 'up' / 'down' / 'flat'; NULL if the entity
    -- has too little history to compute.
    streak_direction text         CHECK (streak_direction IN ('up','down','flat')),
    streak_days      integer      CHECK (streak_days IS NULL OR streak_days >= 0),

    -- Sign of d²/dt² over a smoothed window. Used for the
    -- acceleration arrow (↗↗ vs ↗→ vs ↗↘).
    acceleration     text         CHECK (acceleration IN
                                        ('increasing','flat','decreasing')),

    PRIMARY KEY (entity_type, entity_id)
);

COMMENT ON TABLE change_summary_5m IS
    'O(1) lookup table for multi-window deltas + ATH/ATL + streak + '
    'acceleration per entity. Refreshed every 5 min. Powers every '
    'delta strip on the showcase (one endpoint = one query). See '
    'docs/architecture/showcase-site-data-inventory.md §6.1 + §9.6.';

COMMENT ON COLUMN change_summary_5m.refreshed_at IS
    'When the worker last refreshed this row. Stale rows (>10 min) '
    'indicate a worker problem; the diagnostics page surfaces them.';

-- Refreshed-at recency index — for the "is the worker keeping up"
-- diagnostic + for staleness checks at read time.
CREATE INDEX change_summary_5m_refreshed_idx
    ON change_summary_5m (refreshed_at DESC);

-- Per-type browse: "show me every coin's deltas, sorted by 24h
-- percent change" hits this index.
CREATE INDEX change_summary_5m_type_idx
    ON change_summary_5m (entity_type, h24_delta_pct DESC NULLS LAST);

COMMIT;
