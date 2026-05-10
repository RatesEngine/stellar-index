-- One-shot UPDATE to populate trades.usd_volume for Soroban DEX
-- trades that landed BEFORE the SAC wrapper config was added to
-- /etc/ratesengine.toml. Without the SAC mapping the indexer's
-- on-chain usd_volume path can't resolve `quote_asset = C…` back
-- to an underlying classic, so the rows insert with NULL volume.
--
-- Background: PR #990 added 10 SAC entries to [supply.sac_wrappers].
-- New trades inserted after that pick up usd_volume correctly via
-- store.usdVolumeQuoteSpec in InsertTrade. Historical rows stay
-- NULL until corrected. This script does the correction.
--
-- Logic mirrors USDVolumeQuoteSpec.QuoteUSDPegInfo: when the trade's
-- quote_asset is a SAC contract that resolves (via the wrapper map)
-- to an asset on the operator's USD-pegged list, set
--   usd_volume = quote_amount / 10^7
-- (Stellar classic credits are uniformly 7-decimal, so the wrapped
-- form inherits the same scale.)
--
-- Hard-coded values match the rc.29 r1 config:
--   USD-pegged classic: USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN
--   SAC wrapper for it: CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75
--
-- Run-once: idempotent because the WHERE clause filters out any
-- row that already has a non-NULL usd_volume. Safe to re-run after
-- adding more SAC mappings — just extend the IN list.
--
-- Operator usage:
--   PGPASSWORD=$(cat /etc/ratesengine/postgres-password.txt) \
--   psql -h 127.0.0.1 -U ratesengine -d ratesengine \
--        -f scripts/ops/recompute-usd-volume-soroban.sql

BEGIN;

\echo 'Trades with NULL usd_volume that COULD be priced (preview):'
SELECT source,
       COUNT(*)                       AS rows,
       MIN(ts)                        AS earliest,
       MAX(ts)                        AS latest
  FROM trades
 WHERE usd_volume IS NULL
   AND quote_asset IN (
     'CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75'  -- USDC SAC
   )
 GROUP BY source
 ORDER BY rows DESC;

\echo 'Applying UPDATE...'
UPDATE trades
   SET usd_volume = (quote_amount::numeric / 10000000::numeric)
 WHERE usd_volume IS NULL
   AND quote_asset IN (
     'CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75'
   )
   AND quote_amount > 0;

\echo 'Done. Per-source effect:'
SELECT source, COUNT(*) AS priced
  FROM trades
 WHERE usd_volume IS NOT NULL
   AND quote_asset IN (
     'CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75'
   )
 GROUP BY source
 ORDER BY priced DESC;

COMMIT;
