-- 0019 down — drop divergence observations.
--
-- Loses persistent divergence history. The live worker continues
-- to set the in-memory firing/clear state on `/v1/price` responses;
-- only the timeline is dropped.

BEGIN;

DROP TABLE IF EXISTS divergence_observations;

COMMIT;
