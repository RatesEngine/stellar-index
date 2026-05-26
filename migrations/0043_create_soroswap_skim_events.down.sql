-- 0043 down — drop the soroswap_skim_events hypertable (task #28).
--
-- DROP TABLE removes the hypertable, its chunks, indexes and
-- compression settings in one statement. No CASCADE needed — nothing
-- references soroswap_skim_events (the Soroswap decoder is the sole
-- writer; no CAGG or view is built on it).

BEGIN;

DROP TABLE IF EXISTS soroswap_skim_events;

COMMIT;
