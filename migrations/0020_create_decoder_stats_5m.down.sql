-- 0020 down — drop decoder_stats_5m.
--
-- Loses the rollup history. dispatcher.Stats() in-memory counters
-- are unaffected; Prometheus continues to scrape them.

BEGIN;

DROP TABLE IF EXISTS decoder_stats_5m;

COMMIT;
