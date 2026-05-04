-- 0018 up — `freeze_events` hypertable.
--
-- Per ADR-0019. The anomaly engine in `internal/aggregate/freeze`
-- already detects when a closed-bucket value should be refused
-- (single-source manipulation, divergence vs cross-references,
-- outlier-storm). It writes the boolean state to Redis; the
-- `flags.frozen` field on `/v1/price` reads from there.
--
-- Two problems with Redis-only:
--
--   1. State evaporates on Redis restart. We lose history of every
--      freeze that ever fired.
--   2. The "freeze timeline" UI (showcase-site-data-inventory.md
--      §7.18) needs persistent rows to render. So does any
--      post-mortem article that wants to deep-link
--      "/anomalies/freeze-2025-03-14T12:42".
--
-- This table is the durable mirror. Phase 2 of the showcase
-- implementation plan adds a sink to `internal/aggregate/freeze`
-- that writes a row on every clear→firing transition (with
-- `recovered_at NULL` initially) and updates that row's
-- `recovered_at` on the eventual firing→clear transition.
--
-- Identity: (asset_id, quote_id, frozen_at). A single asset/quote
-- pair can have many freezes over time but only one open at any
-- moment.

BEGIN;

CREATE TABLE freeze_events (
    asset_id            text         NOT NULL,
    quote_id            text         NOT NULL,

    -- Wall-clock + ledger of the clear→firing transition.
    frozen_at           timestamptz  NOT NULL,
    frozen_at_ledger    integer      NOT NULL CHECK (frozen_at_ledger >= 0),

    -- Why we froze. Constraint mirrors the kinds the freeze engine
    -- emits today (single_source / divergence / outlier_storm) plus
    -- a manual override hatch for operator-initiated freezes.
    reason              text         NOT NULL CHECK (reason IN
                                                    ('single_source','divergence',
                                                     'outlier_storm','manual')),

    -- The last-known-good value at the moment of the freeze. The
    -- frozen response carries this until recovery; UIs render it
    -- with the "frozen" badge so users see WHAT they got, not
    -- silence.
    frozen_value        numeric      NOT NULL,

    -- firing→clear transition. NULL while the freeze is currently
    -- firing; populated on recovery.
    recovered_at        timestamptz,
    recovered_at_ledger integer      CHECK (recovered_at_ledger IS NULL OR recovered_at_ledger >= frozen_at_ledger),

    -- Free-form per-reason context: source counts at freeze time,
    -- divergence magnitudes per reference, outlier z-scores, etc.
    -- Schema deliberately loose so the freeze engine can record any
    -- diagnostic detail without a migration.
    detail              jsonb,

    PRIMARY KEY (asset_id, quote_id, frozen_at)
);

COMMENT ON TABLE freeze_events IS
    'Durable record of every freeze decision per ADR-0019. Mirrors '
    'the in-memory state currently held in Redis; powers the '
    '/v1/anomalies endpoint and the showcase /anomalies timeline.';

COMMENT ON COLUMN freeze_events.frozen_value IS
    'Last-known-good value at freeze time; what the API serves '
    'with the frozen flag until recovery.';

COMMENT ON COLUMN freeze_events.recovered_at IS
    'NULL while currently firing; populated on firing→clear '
    'transition. Use the partial index for "currently firing" reads.';

SELECT create_hypertable(
    'freeze_events',
    'frozen_at',
    chunk_time_interval => INTERVAL '30 days',
    if_not_exists       => TRUE
);

-- Partial index for the "currently firing" query — typically a
-- short list (handful of pairs at most), much smaller than the
-- full hypertable.
CREATE INDEX freeze_events_firing_idx
    ON freeze_events (frozen_at DESC) WHERE recovered_at IS NULL;

-- Per-asset rate query: "how many freezes for this asset in the
-- last N days?" — sortable by recency.
CREATE INDEX freeze_events_asset_idx
    ON freeze_events (asset_id, quote_id, frozen_at DESC);

ALTER TABLE freeze_events SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'asset_id, quote_id',
    timescaledb.compress_orderby   = 'frozen_at DESC'
);

COMMIT;
