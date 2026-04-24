-- 0004: relax trades.ledger constraint to allow off-chain sources
-- stamping ledger=0.
--
-- Rationale: PR 169+ introduced the external-connector framework
-- (Binance / Kraken / Bitstamp / Coinbase / FX pollers /
-- aggregators). Those sources have no Stellar ledger concept —
-- they deliberately stamp Ledger=0 and rely on Source + TxHash +
-- OpIndex for uniqueness, same semantics as oracle_updates
-- (migration 0003 which already allows ledger >= 0).
--
-- The original CHECK (ledger > 0) was plausible when every trade
-- came from Galexie ledger metadata; enforcing it against
-- off-chain inserts now rejects valid rows.
-- TestExternalFleet_EndToEnd (PR 181) surfaces this bug.
--
-- Timescale rejects ALTER-with-DROP-CONSTRAINT on compressed
-- hypertables, so we decompress + re-compress around the swap.
-- Any compressed chunks from previous writes get decompressed
-- during the window and re-compressed at the end; compressed
-- chunks from the compression policy resume compressing on the
-- next policy run.

SELECT decompress_chunk(c, true)
FROM show_chunks('trades') c;

ALTER TABLE trades SET (timescaledb.compress = false);

ALTER TABLE trades DROP CONSTRAINT IF EXISTS trades_ledger_check;

ALTER TABLE trades ADD CONSTRAINT trades_ledger_check
    CHECK (ledger >= 0);

ALTER TABLE trades SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'base_asset, quote_asset, source',
    timescaledb.compress_orderby   = 'ts DESC, ledger DESC'
);
