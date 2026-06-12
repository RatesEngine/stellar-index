---
adr: 0035
title: Factory-anchored contract gating for Soroban decoders
status: Accepted
date: 2026-06-12
supersedes: []
superseded_by: null
---

# ADR-0035: Factory-anchored contract gating for Soroban decoders

## Context

A Soroban event decoder implements `dispatcher.Decoder`:

```go
Matches(ev events.Event) bool
Decode(ev events.Event) ([]consumer.Event, error)
```

The dispatcher routes an event to a decoder iff `Matches(ev)` returns
true. Historically, most event-based decoders gated `Matches()` on the
**topic symbol alone** — e.g. Soroswap matched any event whose
`topic[0]` was the symbol `swap`/`sync`/`skim`, Blend matched any of
~23 generic topics (`set_admin`, `deploy`, `claim`, `supply`,
`withdraw`, `borrow`, `repay`, `gulp`, …), Phoenix/Comet/Aquarius/
DeFindex similarly.

This was a deliberate early policy, recorded in `CLAUDE.md` as
"match broadly, filter downstream": the Comet note in particular said
operators "filter downstream by `Trade.Source = "comet"` +
contract-address context rather than at dispatch time."

**That policy is unsound for protocol attribution.** Topic symbols are
NOT unique across protocols:

- Every AMM emits `swap` / `sync` / `deposit` / `withdraw`.
- `set_admin`, `claim`, `supply`, `burn`, `mint` are emitted by SACs
  (Stellar Asset Contracts), token contracts, and unrelated DeFi.
- Any contract can emit a 2-tuple `("SoroswapFactory","new_pair")`-
  shaped event — there is nothing structurally reserved about it.

The consequence is **mis-attribution**: a non-Blend contract that emits
a `("supply", …)` event had its event written into `blend_positions` as
though Blend produced it. A foreign AMM's `swap` was counted as a
Soroswap or Phoenix trade. The system was "counting non-protocol events
as belonging to protocols they don't belong to" — and because topic
collisions are open-ended (any future contract can collide), the
pollution is unbounded and silent.

This also corrupts the ADR-0033 completeness story. The projection
reconcile re-derives expected per-table counts by streaming lake events
through the same decoder (`Matches` → `Decode`). Under topic-only
matching, *both* the served table and the re-derived expectation
include the foreign rows, so they agree — the reconcile reports a clean
100% **over polluted data**. The "100% verified coverage" guarantee was
verifying that we faithfully captured a superset that includes events
that aren't ours.

## Decision

**Soroban event decoders gate `Matches()` on CONTRACT IDENTITY, not on
topic symbol alone. Contract identity is established by anchoring on
each protocol's factory and recursively including every contract the
factory creates — fan out.**

Concretely, per protocol:

1. **Anchor on the factory.** Each protocol has one (or a small, known
   set of) factory/registry contract address(es), verified from the
   `docs/discovery/` audit for that protocol. The factory address is a
   hard-coded constant (mainnet) — the trust root.

2. **Fan out from the factory.** The factory's creation events
   (`new_pair` / `deploy` / `add_pool` / `new_vault` / …) announce
   each child contract it deploys. The decoder decodes those creation
   events to build an in-memory **registry** of child contract IDs.
   Children that are themselves factories (factory-of-factories)
   contribute their descendants too — the registry is the transitive
   closure of "created by something already in the registry."

3. **Gate on the registry.**
   - A factory creation event matches **only** when
     `ev.ContractID == <the protocol's factory>`. This is the
     load-bearing gate: without it, a foreign contract could inject a
     child into the registry and laundering its own events into the
     protocol's tables.
   - A child-contract event (swap/supply/…) matches **only** when
     `ev.ContractID` is a registered descendant of the factory.

4. **Always seed from the factory.** The registry must be complete
   *before* a child's events are processed. Completeness is guaranteed
   by three seed paths, all rooted at the factory:
   - **Live**: the factory's creation events stream in chronologically
     ahead of the child's first business event, populating the registry
     as we go.
   - **Genesis walk**: an operator command walks every factory creation
     event from the factory's deploy ledger to tip (the authoritative
     seed for backfill / a cold start mid-history).
   - **DB warm + RPC**: a startup warm from the persisted registry
     (survives restarts; visible to parallel backfill chunks) and/or an
     RPC walk of the factory for tooling that runs without DB access
     (the reconcile uses this).

This **reverses** the `CLAUDE.md` "match broadly, filter downstream"
policy for protocol attribution. Filtering happens **at dispatch
time**, anchored at the factory — not downstream by source tag.

### Reference implementation

`internal/sources/soroswap` (F-1347) is the reference:

```go
func (d *Decoder) Matches(ev events.Event) bool {
    kind := classify(&ev)
    if kind == "" {
        return false
    }
    if kind == EventNewPair {
        return ev.ContractID == MainnetFactory // only the factory
    }
    d.mu.RLock()
    _, known := d.pairTokens[ev.ContractID] // only a registered pair
    d.mu.RUnlock()
    return known
}
```

Soroswap already had the registry (`pairTokens`, seeded from `new_pair`
for token resolution); the gate extends that existing dependency from
the swap path to all pair-contract events.

## A protocol can have MORE THAN ONE factory (empirical, 2026-06-12)

The first cut of this ADR (and the Blend implementation) assumed one
documented factory per protocol. **That is empirically false.** Verifying
Blend against the r1 lake before deploy found it has **two** pool
factories — the documented V2 (`CDSYOAVX…`) and an earlier, undocumented
one (`CCZD6ESM…`) — that *both* deploy live pools. Gating on the single
documented factory would have silently dropped the earlier factory's 4
auction-emitting pools (the exact silent-loss this ADR forbids).

Consequences for the model:

- The factory trust root is a **SET**, not a scalar
  (`childgate.WithFactories`, `MainnetPoolFactories`). The creation-event
  gate is set membership (`Registry.IsFactory`), and the genesis seed
  walks **every** factory's creation events.
- The factory set must be **empirically verified complete**, not taken
  from discovery docs (which are incomplete). The repeatable method:
  decode every creation event in the lake (`deploy` / `new_pair` /
  `create`), keep the emitters whose body matches the protocol's creation
  shape, and confirm their children cover the protocol's known contracts.
  For Blend: 2 factories, 27 pools, all 9 active pools accounted for.
- **Verify each protocol's factory set against the lake before gating it.**
  The clean provenance sources are unavailable on r1 — `ledger_entry_changes`
  is empty (ADR-0034 exclusion) and `contract_wasm_history` is unpopulated —
  so the `deploy`-event graph in `contract_events` is the authoritative
  source. Factories whose pools were created **before** the lake's earliest
  ledger (50.4M) can't be enumerated this way and need the WASM-audit
  contract list instead (e.g. Phoenix, whose factory has zero lake events).
- The residual risk is an undocumented factory appearing **after**
  enumeration; mitigated by re-running the deploy-graph enumeration
  periodically. WASM-hash gating would remove this risk entirely but needs
  a complete contract→WASM index the lake does not currently provide.

## Consequences

### Coverage is now a hard function of registry completeness

Under topic-only matching, an un-seeded real pair's events would still
match (and produce a row with unknown tokens). Under the gate, an
un-seeded real child's events are **dropped**. This trades one failure
mode (silent over-capture of foreign events) for another (silent
under-capture if the registry is incomplete).

We accept this because the factory seed is **provably complete**: the
factory creation events are themselves in the lake (continuous +
hash-chained per ADR-0033 Claim 1), so the genesis walk enumerates
every child the protocol ever created. A missing child means a missing
factory event, which the substrate-continuity claim already rules out.
The registry is therefore exactly as trustworthy as the substrate — no
new heuristic is introduced. (Where a protocol has children NOT created
by a factory — e.g. permissionlessly-deployed pools sharing a WASM —
this ADR does not fully solve attribution; see "Open: Comet".)

### The reconcile must seed identically

The ADR-0033 projection reconcile streams lake events through the
decoder, so it must seed the registry the same way before re-deriving
(`seedSoroswapForRecon` → `SeedFromFactoryRPC` is the pattern). Because
`sorobanevents.Reconstruct` populates `ev.ContractID` from the lake row,
the gate evaluates correctly in the reconcile.

### Deploy precondition: historical re-derive to purge pollution

Existing served-table rows were written under topic-only matching and
therefore contain foreign-contract pollution. After a gate lands, the
re-derived expectation excludes those rows but the table still holds
them → the reconcile flags them as **phantoms** (actual > expected).
Resolving this requires a one-time historical **re-derive from the
lake** (gated decoder over the lake, replacing the table contents) per
protocol — the same class of operator deploy-step as the migration
0057-0060 PK changes. Until that purge runs, the reconcile for a
freshly-gated source will (correctly) report phantoms. This is the
intended signal that pollution exists and is being removed, not a
regression.

### Open: Comet (shared WASM, no factory namespace)

Comet uses a shared `("POOL", <event>)` topic across every Balancer-v1
deployment and (per the discovery note) has no per-protocol factory
namespace: any pubnet contract running Balancer-v1 Comet WASM looks
identical on the wire. Factory-anchoring may not fully apply. Options
under evaluation: gate on an operator-configured pool allowlist (e.g.
only Blend's backstop pool), or gate on the deployed WASM hash. Tracked
separately; this ADR establishes the principle, and Comet adopts
whichever mechanism gives a verifiable contract set.

## Rollout

Per-protocol, each as its own change with tests + a reconcile-seed
update + a documented re-derive precondition:

- [x] Soroswap (F-1347) — reference (own `soroswap_pairs` registry; carries tokens).
  Multi-factory verified (2026-06-12): 4 factories on the lake, but the 3
  early ones' 21 pairs have ZERO swaps (defunct), so the gate drops no real
  trades; `new_pair` is gated on the full `MainnetFactories` set regardless,
  so a future trade is captured.
- [x] Blend (factory `deploy` → pool registry) — first consumer of the generic
  `childgate.Registry` + `protocol_contracts` table (migration 0061).
- [ ] Phoenix — NEEDS WORK BEFORE GATING (see readiness below).
- [ ] DeFindex — NEEDS WORK BEFORE GATING (see readiness below).
- [ ] Aquarius — NEEDS WORK BEFORE GATING (see readiness below).
- [ ] Comet (shared WASM — allowlist / WASM-hash; see Open above).

### Per-protocol readiness (assessed 2026-06-12)

Soroswap and Blend were the highest-collision-risk decoders (generic
topics — `swap`/`sync`, and `supply`/`claim`/`set_admin`/`deploy` that
SACs and other DeFi also emit) and are done. The remaining four each have
a concrete blocker that must be resolved **before** gating, because
gating on a wrong factory model would silently drop real events — the one
failure this ADR must never introduce. They are listed in implementation
order; each is its own change with the blocker resolved first.

- **Phoenix.** Factory `CB4SVAWJ…` is verified and the creation event is
  documented (`("create","liquidity_pool")` → `lp_contract_address` in the
  body, `phoenix.md:118`). BLOCKERS: (1) the decoder does not decode the
  `create` event today, so a factory `create` classification + child
  extraction must be added first; (2) `phoenix.md` leaves open whether the
  MULTIHOP contract (`CCLZRD4E…`) emits its own swap events or only relays
  to pools — if it emits directly, a pool-only gate drops multihop swaps.
  Resolve (2) empirically against the lake (does `CCLZRD4E…` appear as a
  swap emitter?) before choosing the gated set. Mitigation: phoenix topics
  are String-encoded (`MustEncodeString`), so they don't collide with the
  far more common Symbol-encoded `swap` — collision risk is already lower
  than soroswap/blend.
- **DeFindex.** Three layers: `DeFindexFactory` (create/n_fee) →
  `DeFindexVault` → `BlendStrategy`. The vault layer is cleanly
  factory-anchored, but `BlendStrategy` contracts are (per the dispatcher
  comment) "not a hand-curated contract set" and may be SHARED strategy
  contracts, not factory-created children — so the strategy layer can't be
  gated by the vault factory. Needs the factory address pinned AND the
  factory→vault→strategy ownership chain verified (is a strategy created by
  its vault, or deployed independently?). Topics are namespaced strings
  (`DeFindexVault`/`BlendStrategy`), so collision risk is low — gating is
  more correctness-hardening than collision-fixing here.
- **Aquarius.** Router-centric (`CBQDHNBF…` is the entry point / "pool
  factory"); pools emit the `trade` event. BLOCKER: `aquarius.md` has open
  TODOs on pool enumeration ("ask Aquarius for / derive from router reads
  the pool registry") — the pool-creation event shape is NOT pinned, and
  the decoder has no creation-event handling. Needs the router's
  pool-creation event (topic + body carrying the pool address) verified
  before a registry can be built. `trade` (Symbol) is moderately generic,
  so gating has real value here once the creation event is known.
- **Comet.** Shared Balancer-v1 WASM, no factory namespace — see "Open:
  Comet" above. Adopts an operator allowlist or WASM-hash gate, not the
  factory fan-out.

## Related

- ADR-0031 (data-derived coverage signal) — the coverage signal this
  protects.
- ADR-0033 (completeness verification) — the reconcile this threads
  through.
- `docs/architecture/contract-schema-evolution.md` — decoders are
  already WASM-version-aware for backfill; this adds contract-identity
  awareness for attribution.
- Reverses the "match broadly, filter downstream" guidance in
  `CLAUDE.md` (updated in the same change).
