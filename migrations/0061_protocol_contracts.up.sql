-- 0061: protocol_contracts — factory-descendant registry for gated decoders.
--
-- ADR-0035 (factory-anchored contract gating). Factory-anchored Soroban
-- decoders (blend, aquarius, phoenix, defindex) gate Matches() on contract
-- identity: an event is the protocol's only if it comes from the protocol's
-- factory or a contract that factory created (fan out). The factory address
-- is a hard-coded constant in each decoder; this table is the durable record
-- of the *children* — the contracts each factory has deployed.
--
-- Why a table (vs re-walking the factory's creation events every boot):
-- mirrors soroswap_pairs (migration 0016). The live indexer learns a new
-- child the moment it decodes a factory creation event and upserts it here;
-- every consumer (indexer, projector, the ADR-0033 recognition/reconcile
-- audits) warms its in-memory childgate.Registry from this table at startup
-- so a restart resumes with a COMPLETE registry even though the projector
-- cursor has advanced past the creation events. An incomplete registry would
-- silently DROP a real child's events (ADR-0035 coverage note), so this table
-- is load-bearing for coverage — keep it forever (no retention).
--
-- Genesis fill: `ratesengine-ops seed-protocol-contracts -source <name>`
-- walks the factory's creation events from the lake (cheap: the
-- (contract_id, topic_0_sym) index on soroban_events) and upserts every
-- child. Run once per source as a deploy precondition before relying on the
-- gate. Soroswap keeps its richer soroswap_pairs table (it carries token
-- identities); this generic table is the contract-set-only case.

BEGIN;

CREATE TABLE protocol_contracts (
    -- Logical source name (blend, aquarius, phoenix, defindex). Matches
    -- internal/sources/<name>.SourceName and the projector registry key.
    source       text        NOT NULL,

    -- C-strkey of the factory-descended child contract (the pool / pair /
    -- vault). This is the value the decoder's Matches() gate looks up.
    contract_id  text        NOT NULL,

    -- C-strkey of the factory that created it (the decoder's trust root).
    -- Recorded for provenance / audit — which anchor each child descends
    -- from — not consulted by the gate.
    factory_id   text        NOT NULL,

    -- Ledger of the creation event (e.g. Blend pool-factory `deploy`).
    -- Operator visibility + ordering; NULL when seeded from a source that
    -- doesn't carry it.
    first_ledger bigint,

    -- Last upsert wall-clock. Operator visibility only.
    observed_at  timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (source, contract_id)
);

COMMENT ON TABLE protocol_contracts IS
    'Factory-descendant child-contract registry for factory-anchored '
    'Soroban decoders (ADR-0035). Seeded by ratesengine-ops '
    'seed-protocol-contracts and kept current by live factory creation '
    'events. Each decoder warms its childgate.Registry from this table; an '
    'incomplete registry silently drops a real child''s events, so this is '
    'a coverage-load-bearing table — never apply retention.';

COMMIT;
