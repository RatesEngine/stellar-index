-- 0025 up — `routers` + `trades.routed_via` + `aggregator_exposures`.
--
-- Cross-cutting attribution for routers (Soroswap router) and
-- aggregator vaults (DeFindex). Per docs/architecture/showcase-site-
-- data-inventory.md §7.9.1.
--
-- Three pieces, one migration:
--
--   1. `routers` — registry table. (contract_id, name, kind,
--      protocol_slug). Pre-seeded with known routers / vaults
--      via a separate operator step (task #114). Auto-discovery
--      heuristic flags candidates over time.
--
--   2. `trades.routed_via text` — new column on the existing
--      hypertable. NULL = direct trade (no router involved);
--      non-NULL = the router/aggregator name from the registry.
--      Tagged at insert time by the router-attribution observer
--      (Phase 4) which hooks the dispatcher's ContractCallDecoder
--      seam — the same mechanism the Band oracle decoder uses.
--
--   3. `aggregator_exposures` — separate hypertable for vault
--      capital allocation. Distinct from per-tx routed_via tagging
--      because vaults hold capital persistently, not per-tx.
--      Refreshed periodically by querying the vault's on-chain
--      state.
--
-- ALTER TABLE ADD COLUMN on the trades hypertable: PostgreSQL
-- handles this in O(1) — no rewrite, no lock — because the new
-- column is nullable with no DEFAULT.

BEGIN;

CREATE TABLE routers (
    -- The C-strkey of the router / vault contract.
    contract_id     text         PRIMARY KEY,

    -- Human-readable name. Free-form for operator clarity.
    -- Convention: "{protocol}-{role}-{version}" — e.g.
    -- "soroswap-router-v1", "defindex-vault-blendxlm".
    name            text         NOT NULL,

    -- Discriminator. CHECK enumerates what we support:
    --   'router'           — entry contract that routes per-tx
    --                        (Soroswap router pattern).
    --   'aggregator-vault' — wrapper that holds persistent
    --                        capital + deposits into underlying
    --                        protocols (DeFindex pattern).
    kind            text         NOT NULL CHECK (kind IN
                                                ('router','aggregator-vault')),

    -- Which protocol slug this contract belongs to. Joins back to
    -- the protocol catalogue.
    protocol_slug   text         NOT NULL,

    added_at        timestamptz  NOT NULL DEFAULT now(),

    -- True when the auto-discovery heuristic surfaced this
    -- candidate. False when an operator added it manually. Lets
    -- the UI distinguish "trusted" routers from "flagged but
    -- unverified" candidates.
    auto_discovered boolean      NOT NULL DEFAULT false,

    notes           text
);

COMMENT ON TABLE routers IS
    'Registry of router + aggregator-vault contracts. The router-'
    'attribution observer hooks the dispatcher ContractCallDecoder '
    'for every contract listed here.';

CREATE INDEX routers_protocol_idx ON routers (protocol_slug);

-- Add the attribution column to the existing trades hypertable.
-- NULL = direct interaction; non-NULL = the router/aggregator name.
-- Nullable + no default = no rewrite required, fast on existing
-- multi-billion-row hypertable.
ALTER TABLE trades ADD COLUMN routed_via text;

COMMENT ON COLUMN trades.routed_via IS
    'Router / aggregator that drove this trade, if any. References '
    'routers.name. Tagged at insert time by the router-attribution '
    'observer based on same-tx-batch contract invocations.';

-- Partial index — most trades are direct, so indexing only
-- non-NULL values keeps the index small.
CREATE INDEX trades_routed_via_idx ON trades (routed_via)
    WHERE routed_via IS NOT NULL;

CREATE TABLE aggregator_exposures (
    -- The vault contract id.
    vault_contract_id   text         NOT NULL,

    -- Which underlying protocol the capital is deployed in.
    -- Free-form (joins to protocols.slug); not constrained because
    -- the set grows over time.
    underlying_protocol text         NOT NULL,

    observed_at         timestamptz  NOT NULL,
    observed_at_ledger  integer      NOT NULL CHECK (observed_at_ledger >= 0),

    -- USD-denominated exposure at observation time.
    exposure_usd        numeric      NOT NULL CHECK (exposure_usd >= 0),

    -- Per-protocol detail — schema varies by underlying protocol.
    -- e.g. for Blend: {"supply": ..., "borrow": ..., "rate": ...}.
    -- For Aquarius: {"lp_share": ..., "pool_id": ...}.
    detail              jsonb,

    PRIMARY KEY (vault_contract_id, underlying_protocol, observed_at)
);

COMMENT ON TABLE aggregator_exposures IS
    'Per-vault capital allocation tracking. One row per (vault, '
    'underlying_protocol, tick). Distinct from trades.routed_via '
    'because vaults hold capital persistently — this captures the '
    'state, not per-tx flow.';

SELECT create_hypertable(
    'aggregator_exposures',
    'observed_at',
    chunk_time_interval => INTERVAL '7 days',
    if_not_exists       => TRUE
);

CREATE INDEX aggregator_exposures_vault_idx
    ON aggregator_exposures (vault_contract_id, observed_at DESC);
CREATE INDEX aggregator_exposures_protocol_idx
    ON aggregator_exposures (underlying_protocol, observed_at DESC);

ALTER TABLE aggregator_exposures SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'vault_contract_id, underlying_protocol',
    timescaledb.compress_orderby   = 'observed_at DESC'
);

COMMIT;
