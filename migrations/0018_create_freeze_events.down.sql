-- 0018 down — drop the freeze events hypertable.
--
-- Loses persistent freeze history. The Redis-resident "is this pair
-- currently frozen" state is unaffected; only the durable timeline
-- is gone.

BEGIN;

DROP TABLE IF EXISTS freeze_events;

COMMIT;
