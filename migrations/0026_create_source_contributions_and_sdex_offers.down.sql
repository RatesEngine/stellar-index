-- 0026 down — drop price_source_contributions + sdex_offer_events.

BEGIN;

DROP TABLE IF EXISTS sdex_offer_events;
DROP TABLE IF EXISTS price_source_contributions;

COMMIT;
