-- 0028 down — drop fx_quotes.

BEGIN;

SELECT remove_compression_policy('fx_quotes', if_exists => TRUE);
DROP TABLE IF EXISTS fx_quotes;

COMMIT;
