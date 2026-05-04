-- 0020 up — `decoder_stats_5m` rollup hypertable.
--
-- The dispatcher exposes per-source counters via `dispatcher.Stats()`:
--   - events_seen        (matched a decoder)
--   - decode_errors      (matched but decode returned an error)
--   - orphan_events      (e.g. Soroswap swap with no paired sync;
--                        matched + decoded but didn't survive correlation)
--   - last_ledger        (most recent ledger the decoder produced output for)
--
-- These live in process memory only. They feed Prometheus metrics —
-- which is fine for alerting, but not for the showcase
-- /diagnostics/decoders page (showcase-site-data-inventory.md §7.22)
-- which needs queryable history per source.
--
-- This table is the persistent rollup. An aggregator-side worker
-- reads dispatcher.Stats() every 5 minutes, atomically snapshots-and-
-- clears the counters, and writes one row per source per bucket.
-- Atomic snapshot-and-clear is the contract that prevents
-- double-counting between buckets.
--
-- Identity: (bucket, source). The bucket field is the floor of
-- wall-clock to a 5-minute boundary; the source is the
-- internal/sources/* package name (sdex, soroswap, phoenix, …).

BEGIN;

CREATE TABLE decoder_stats_5m (
    -- 5-minute bucket boundary. Floor of wall-clock at flush time.
    bucket          timestamptz NOT NULL,

    -- Source name — matches the SourceName const in each
    -- internal/sources/* decoder. Free-form text rather than an
    -- enum because the enabled set is operator-controlled.
    source          text        NOT NULL,

    events_seen     bigint      NOT NULL DEFAULT 0 CHECK (events_seen   >= 0),
    decode_errors   bigint      NOT NULL DEFAULT 0 CHECK (decode_errors >= 0),
    orphan_events   bigint      NOT NULL DEFAULT 0 CHECK (orphan_events >= 0),

    -- Most recent ledger this source produced an output for during
    -- this bucket. Useful for "how stale is decoder X right now"
    -- monitoring without joining trades.
    last_ledger     integer     CHECK (last_ledger IS NULL OR last_ledger >= 0),

    PRIMARY KEY (bucket, source)
);

COMMENT ON TABLE decoder_stats_5m IS
    '5-minute rollups of dispatcher.Stats() per source. Persists '
    'the in-memory counters so /v1/diagnostics/decoders can query '
    'history. Snapshot-and-clear semantics on the writer side '
    'guarantee no double-counting between buckets.';

COMMENT ON COLUMN decoder_stats_5m.events_seen IS
    'Events whose topic / op type matched the source decoder. '
    'Includes ones that errored or were dropped as orphans — those '
    'are tracked separately in decode_errors / orphan_events.';

COMMENT ON COLUMN decoder_stats_5m.orphan_events IS
    'Decoded events that failed downstream correlation (e.g. '
    'Soroswap swap event with no matching sync within the buffer '
    'window).';

SELECT create_hypertable(
    'decoder_stats_5m',
    'bucket',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

-- Per-source recent history: walk by source ordered by recency.
CREATE INDEX decoder_stats_5m_source_idx
    ON decoder_stats_5m (source, bucket DESC);

ALTER TABLE decoder_stats_5m SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'source',
    timescaledb.compress_orderby   = 'bucket DESC'
);

COMMIT;
