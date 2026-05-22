-- 0039 down — drop the rozo_events hypertable (#41).
--
-- DROP TABLE removes the hypertable, its chunks, indexes and
-- compression settings in one statement. No CASCADE needed —
-- nothing references rozo_events (the Rozo source is the sole
-- writer; no CAGG or view is built on it).

BEGIN;

DROP TABLE IF EXISTS rozo_events;

COMMIT;
