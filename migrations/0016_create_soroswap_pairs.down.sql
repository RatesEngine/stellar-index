-- 0016 down — drop the soroswap_pairs registry.
--
-- Loses the bootstrap mapping; the decoder will fall back to its
-- legacy behaviour of dropping every swap whose pair hasn't been
-- seen via a live new_pair event since indexer start.

BEGIN;

DROP TABLE IF EXISTS soroswap_pairs;

COMMIT;
