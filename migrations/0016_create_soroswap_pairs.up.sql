-- 0016 up — `soroswap_pairs` registry table.
--
-- Persists the pair_contract → (token0, token1) mapping that the
-- soroswap.Decoder needs to translate raw swap+sync event amounts
-- into canonical Trade rows. Without this mapping, decode_swap can't
-- assign base/quote sides; the decoder's "skipped_unknown_pair"
-- counter ticks and trades silently disappear.
--
-- Why a table and not in-memory:
--
--   The live indexer learns mappings as it sees factory `new_pair`
--   events. That works for live tail, but breaks for two cases that
--   matter:
--
--     1. Cold-start. The first ledgers of the live tail are usually
--        well past the factory's deploy-genesis. Every pair created
--        before the indexer's start ledger is invisible until we
--        backfill the factory's history — which we don't, by design,
--        because seeding via stellar-rpc simulateTransaction is faster
--        and uses a single round-trip per pair (factory_seed.go).
--     2. Parallel backfill. `ratesengine-ops backfill -parallel N`
--        runs N independent dispatchers (one per chunk). Each chunk
--        sees only the new_pair events INSIDE its window — so chunk 7
--        sees a swap on a pair created during chunk 2's window and
--        has no idea what tokens are involved. Sharing in-memory
--        state across chunks would couple the workers; sharing via
--        postgres is the natural seam.
--
-- Population paths (all idempotent on pair_strkey):
--
--   - `ratesengine-ops seed-soroswap-pairs` — initial bootstrap;
--     RPC-walks the factory's all_pairs() / token_0() / token_1()
--     view functions and upserts every row.
--   - Live indexer — every factory `new_pair` event upserts its row
--     as it streams (decoder's recordNewPair hook).
--   - Backfill chunks — same hook fires when a chunk's range covers
--     a `new_pair` event, so even a chunk-only run keeps the table
--     fresh for any later chunk.
--
-- Identity: pair_strkey is the C-strkey of the pair contract (PRIMARY
-- KEY). token0/token1 are the C-strkeys of the SEP-41 token contracts.
-- observed_at is wall-clock at the time of the most-recent upsert —
-- used for operator visibility ("when did we last see this?"), not
-- for any decoder logic.
--
-- Not a hypertable: this is a small, slow-growing reference table
-- (Soroswap mainnet has a few hundred pairs). One row per pair, total
-- size in KB-MB even at 10× growth.

BEGIN;

CREATE TABLE soroswap_pairs (
    -- Pair contract C-strkey. Primary identity.
    pair_strkey   text         NOT NULL,

    -- The two SEP-41 token contracts the pair wraps. Order matches
    -- the Soroswap factory's token_0() / token_1() view functions.
    -- The decoder uses position to assign base/quote on swap; the
    -- aggregator's stablecoin-fiat-proxy pass normalises later.
    token0_strkey text         NOT NULL,
    token1_strkey text         NOT NULL,

    -- Last upsert wall-clock. Operator-visibility only.
    observed_at   timestamptz  NOT NULL DEFAULT now(),

    PRIMARY KEY (pair_strkey)
);

COMMENT ON TABLE soroswap_pairs IS
    'pair_contract → (token0, token1) registry for the Soroswap '
    'decoder. Seeded by ratesengine-ops seed-soroswap-pairs and '
    'kept current by live new_pair events. Without this mapping '
    'the decoder cannot label swap event amounts as base vs quote.';

COMMIT;
