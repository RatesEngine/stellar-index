-- 0023 down — drop classic_assets + issuers + anchors.
--
-- Loses the registry. Underlying trade data is unaffected.

BEGIN;

DROP TABLE IF EXISTS anchors;
DROP TABLE IF EXISTS issuers;
DROP TABLE IF EXISTS classic_assets;

COMMIT;
