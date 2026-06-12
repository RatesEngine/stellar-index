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

- [x] Soroswap (F-1347) — reference.
- [ ] Blend (factory `deploy` → pool registry).
- [ ] Aquarius (pool factory).
- [ ] Phoenix (factory → pool registry).
- [ ] DeFindex (factory → vault registry).
- [ ] Comet (shared WASM — allowlist / WASM-hash; see Open above).

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
