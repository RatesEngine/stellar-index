-- 0061 down — drop the protocol_contracts registry.
--
-- Loses the factory-descendant child set; gated decoders (blend, aquarius,
-- phoenix, defindex) fall back to live-only seeding (they only learn a child
-- from a factory creation event observed AFTER boot), so events from a child
-- deployed before the current cursor would be dropped until re-seeded via
-- `ratesengine-ops seed-protocol-contracts`.

BEGIN;

DROP TABLE IF EXISTS protocol_contracts;

COMMIT;
