-- 0063 down — drop the blend_backstop_events hypertable.
--
-- DROP TABLE removes the hypertable, its chunks, indexes and
-- compression settings in one statement. CASCADE is not needed —
-- nothing references blend_backstop_events (the backstop source is the
-- sole writer; no CAGG or view is built on top of it).

BEGIN;

DROP TABLE IF EXISTS blend_backstop_events;

COMMIT;
