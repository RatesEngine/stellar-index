-- 0038 down — drop the cctp_events hypertable (#40).
--
-- DROP TABLE removes the hypertable, its chunks, indexes and
-- compression settings in one statement. CASCADE is not needed —
-- nothing references cctp_events (the CCTP source is the sole
-- writer; no CAGG or view is built on top of it).

BEGIN;

DROP TABLE IF EXISTS cctp_events;

COMMIT;
