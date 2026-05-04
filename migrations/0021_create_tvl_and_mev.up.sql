-- 0021 up — `tvl_observations` + `mev_events`.
--
-- Two tables that share a migration because they're both populated
-- by aggregator-side analytics workers (Phase 3 of the showcase
-- implementation plan) and neither is consumed by the live ingest
-- path. Splitting them across migrations would just be ceremony.
--
-- ─── tvl_observations ──────────────────────────────────────────────
--
-- Per-protocol TVL ticks. Computed every aggregator tick (1 min):
-- for each protocol the worker iterates its known pools, multiplies
-- per-pool reserves by the latest closed-bucket price, and sums to
-- a USD-denominated total.
--
-- Powers:
--   - /v1/tvl + /v1/protocols/{slug}/tvl/history (§7.9)
--   - /v1/tvl/flow Sankey (§7.1)
--   - The TVL leaderboard on /protocols (§7.8)
--   - "Total Stellar TVL" macro tile on / and /network (§7.21)
--
-- The breakdown jsonb captures per-pool TVL detail so we can render
-- the protocol detail page without re-computing or joining three
-- tables at request time.
--
-- ─── mev_events ────────────────────────────────────────────────────
--
-- Suspicious-pattern detector output. The MEV worker (§7.20)
-- continuously scans for sandwich attacks, oracle deviations,
-- liquidation cascades, and wash-trading. Each detected event is
-- one row.
--
-- Not a hypertable — modest volume (~10s/day expected on Stellar
-- today) and the read pattern is "show me the last 50 of kind X"
-- which a btree index covers fine. Convert to hypertable if volume
-- exceeds expectations.

BEGIN;

CREATE TABLE tvl_observations (
    -- Protocol slug — matches /v1/protocols/{slug}.
    protocol_slug      text         NOT NULL,

    observed_at        timestamptz  NOT NULL,
    observed_at_ledger integer      NOT NULL CHECK (observed_at_ledger >= 0),

    -- USD-denominated total at observation time. Numeric for full
    -- precision (we never want to lose cents on multi-billion TVLs).
    tvl_usd            numeric      NOT NULL CHECK (tvl_usd >= 0),

    -- How many pools / vaults / loci of capital contributed to the
    -- total. Helps rank the "depth" of the protocol independent of
    -- the dollar amount.
    pool_count         integer      NOT NULL CHECK (pool_count >= 0),

    -- Per-pool detail: [{"pool_id": "...", "tvl_usd": ..., "tokens": [...]}, ...].
    -- Loose schema deliberately — different protocol kinds have
    -- different per-pool shapes (lending pool vs AMM pair vs
    -- aggregator vault).
    breakdown          jsonb,

    PRIMARY KEY (protocol_slug, observed_at)
);

COMMENT ON TABLE tvl_observations IS
    'Per-protocol TVL ticks (1-minute cadence) with per-pool '
    'breakdown jsonb. Powers protocol-page TVL charts + the macro '
    'TVL aggregate.';

SELECT create_hypertable(
    'tvl_observations',
    'observed_at',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

-- Recent-history walk per protocol — most common chart query.
-- The PK already orders (protocol_slug, observed_at) so DESC is free.
-- Add a global recency index for the cross-protocol leaderboard.
CREATE INDEX tvl_observations_recent_idx
    ON tvl_observations (observed_at DESC);

ALTER TABLE tvl_observations SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'protocol_slug',
    timescaledb.compress_orderby   = 'observed_at DESC'
);

CREATE TABLE mev_events (
    -- UUID primary key — events have no natural composite key
    -- (multiple sandwiches can land in the same tx batch from
    -- different attackers) and we want to URL-encode them.
    event_id           uuid         PRIMARY KEY DEFAULT gen_random_uuid(),

    detected_at        timestamptz  NOT NULL,
    detected_at_ledger integer      NOT NULL CHECK (detected_at_ledger >= 0),

    -- Pattern kind. The CHECK enumerates every pattern the worker
    -- can emit; new patterns require a migration.
    kind               text         NOT NULL CHECK (kind IN
                                                   ('sandwich','oracle_deviation',
                                                    'liquidation_cascade','wash_trade')),

    -- Optional contextual asset — present for sandwich + oracle
    -- deviation; NULL for cross-asset events.
    asset_id           text,
    quote_id           text,

    -- Every transaction involved in the pattern. Sandwiches
    -- typically have 3 (front-run, victim, back-run); cascades
    -- can have many.
    tx_hashes          text[]       NOT NULL CHECK (array_length(tx_hashes, 1) > 0),

    -- Accounts involved (suspected attacker + victims). NULL when
    -- the pattern doesn't surface specific accounts (e.g. pure
    -- price-deviation events with no clear actor).
    accounts           text[],

    -- Per-pattern context: bid/lot for liquidations, sigma/delta
    -- for oracle deviations, hop-graph for sandwiches, etc.
    detail             jsonb        NOT NULL,

    -- Best-effort estimate of the attacker's profit. NULL when we
    -- can't compute (e.g. wash trades have no profit semantic).
    profit_usd         numeric
);

COMMENT ON TABLE mev_events IS
    'Auto-flagged suspicious-pattern detector output. One row per '
    'detected event; populated by internal/aggregate/mev/. Powers '
    'the showcase /mev page.';

COMMENT ON COLUMN mev_events.profit_usd IS
    'Best-effort attacker-profit estimate; NULL when not '
    'meaningful for the pattern kind.';

-- Recent feed: "show me everything in the last 7 days, newest first."
CREATE INDEX mev_events_detected_idx
    ON mev_events (detected_at DESC);

-- Per-kind tally: "how many sandwiches today?"
CREATE INDEX mev_events_kind_idx
    ON mev_events (kind, detected_at DESC);

-- Per-asset history: "what's been done to AQUA?"
CREATE INDEX mev_events_asset_idx
    ON mev_events (asset_id, detected_at DESC) WHERE asset_id IS NOT NULL;

COMMIT;
