-- 0019 up — `divergence_observations` hypertable.
--
-- `internal/divergence/worker.go` periodically compares our VWAP
-- against external references (Chainlink HTTP, CoinGecko, the
-- Reflector trio, Redstone, Band) and computes a delta percent for
-- every (asset, quote, reference) tuple. The result drives the
-- `flags.divergence_warning` boolean on `/v1/price` responses.
--
-- Today only the boolean flag survives — the actual delta values
-- are recomputed each tick and dropped. This means we cannot:
--
--   - Plot divergence over time on the /divergences page
--     (showcase-site-data-inventory.md §7.19).
--   - Verify retroactively whether a manipulation event lined up
--     with our cross-oracle observations.
--   - Provide ground-truth for incident post-mortems that need to
--     show "Reflector drifted N% from us at ledger X".
--
-- This table is the durable record. Phase 2 of the implementation
-- plan adds a sink to the divergence worker writing one row per
-- (asset, quote, reference) per tick.
--
-- Identity: (asset, quote, reference, observed_at). A given
-- comparison happens repeatedly over time; each computation is its
-- own row. The status enum (clear/firing) reflects whether the
-- delta exceeded the configured threshold at the moment of
-- observation.

BEGIN;

CREATE TABLE divergence_observations (
    asset_id           text         NOT NULL,
    quote_id           text         NOT NULL,

    -- Which external reference this observation compares against.
    -- The constraint enumerates every reference the worker can
    -- emit today; new references require a migration.
    reference          text         NOT NULL CHECK (reference IN
                                                   ('chainlink','coingecko',
                                                    'reflector-cex','reflector-fx','reflector-dex',
                                                    'redstone','band')),

    observed_at        timestamptz  NOT NULL,
    observed_at_ledger integer      NOT NULL CHECK (observed_at_ledger >= 0),

    -- Both prices in the same quote currency (we normalize at
    -- worker time so the delta is always meaningful).
    our_price          numeric      NOT NULL,
    ref_price          numeric      NOT NULL,

    -- delta_pct = (our - ref) / ref * 100. Negative means our
    -- VWAP is lower than the reference. NULL would imply we
    -- couldn't compute (ref unavailable) — but that case skips
    -- the row entirely rather than persisting.
    delta_pct          numeric      NOT NULL,

    -- 'firing' when |delta_pct| exceeded the configured threshold
    -- at observation time; 'clear' otherwise. The threshold is
    -- per-reference + per-pair operator config.
    status             text         NOT NULL CHECK (status IN ('clear','firing')),

    PRIMARY KEY (asset_id, quote_id, reference, observed_at)
);

COMMENT ON TABLE divergence_observations IS
    'Per-tick cross-reference divergence comparisons. Mirrors what '
    'the divergence worker computes; powers /v1/divergences and the '
    'showcase divergence-history charts.';

COMMENT ON COLUMN divergence_observations.delta_pct IS
    '(our_price - ref_price) / ref_price * 100. Negative = we are '
    'below the reference.';

COMMENT ON COLUMN divergence_observations.status IS
    'Whether |delta_pct| exceeded the per-(reference,pair) '
    'threshold at observation time. Distinct from the persistent '
    'flag returned by the API (which is "any reference firing").';

SELECT create_hypertable(
    'divergence_observations',
    'observed_at',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

-- Most common read: "give me the history for this (pair, reference)
-- ordered by recency." (asset, quote, reference, observed_at DESC)
-- covers the WHERE + ORDER BY without a sort.
CREATE INDEX divergence_observations_pair_ref_idx
    ON divergence_observations
    (asset_id, quote_id, reference, observed_at DESC);

-- Operator dashboard: "show me everything firing right now."
CREATE INDEX divergence_observations_firing_idx
    ON divergence_observations (observed_at DESC) WHERE status = 'firing';

ALTER TABLE divergence_observations SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'asset_id, quote_id, reference',
    timescaledb.compress_orderby   = 'observed_at DESC'
);

COMMIT;
