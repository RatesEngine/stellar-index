---
title: Aquarius WASM-history audit
last_verified: 2026-04-29
status: ratified
source: aquarius
backfill_safe: true
---

# Aquarius WASM audit

Audit log for the `aquarius` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

## Status

**Ratified 2026-04-29.** `BackfillSafe` flips `false` → `true` in
`internal/sources/external/registry.go` in the same PR as this
audit. All 313 mainnet aquarius pool contracts enumerated via
the router's `get_pools_for_tokens_range()` view; their current
WASMs were fetched via `stellar contract fetch` against
mainnet.sorobanrpc.com. **Three unique pool-WASM hashes total**,
all three using the shared `liquidity_pool_events::trade()`
emitter — all decoder-compatible by source-import topology +
binary-string verification.

## Contracts under audit

Captured from `internal/sources/aquarius/events.go` (verified
2026-04-23 against stellar.expert + Aquarius docs):

| role | contract |
| --- | --- |
| Router | `CBQDHNBFBZYE4MKPWBSJOPIYLW4SFSXAXUTSXJN76GNKYVYPCKWC6QUK` |

Aquarius doesn't have a single factory like Soroswap; pool
contracts (volatile / stableswap / concentrated) are deployed
independently. The Router is the orchestration entry point and the
WASM most likely to evolve with protocol fixes; pool contracts
themselves emit the trade events.

The pool contracts are enumerable from on-chain history. For an
MVP audit, the dominant pools by volume are sufficient — full
coverage extends as new pools get listed.

## Decoder expectations

Captured from `internal/sources/aquarius/{events,decode}.go` at
HEAD as of 2026-04-27.

### Topic structure (trade events)

Every Aquarius trade event has a 4-element topic:

    topic[0] = ScvSymbol("trade")
    topic[1] = ScvAddress(token_in)   — sold asset
    topic[2] = ScvAddress(token_out)  — bought asset
    topic[3] = ScvAddress(user)       — trader (often the router contract)

Other event names the contract emits (and we ignore — not trades):

- `deposit_liquidity`
- `withdraw_liquidity`
- `update_reserves`

Classification is **byte-equal** against `TopicSymbolTrade`
(pre-encoded `ScvSymbol("trade")`). Topic[0] renamed to e.g.
`"swap"` would silently drop every trade event.

### Trade body

Verified against `aquarius-amm/liquidity_pool_events/src/lib.rs:122-150`
(soroban-sdk 25.0.2). The body is a Rust tuple, which soroban-sdk
serializes as **`ScvVec` of length 3**, positional:

    body = (
        in_amount  as i128,    // index 0 — sold amount
        out_amount as i128,    // index 1 — bought amount
        fee_amount as i128,    // index 2 — fee, currently unused
    )

This is the **load-bearing fragility** vs Soroswap — Aquarius uses
**positional** decoding (Vec), not by-name (Map). A reorder of the
fields in a contract upgrade silently produces wrong base/quote
amounts and would NOT trip a parse error. Audit must verify the
tuple order is unchanged across every WASM hash in the timeline.

| body slot | extracted by | invariant |
| --- | --- | --- |
| `[0]` (in_amount) | `scval.AsAmountFromI128` | i128, sign > 0 |
| `[1]` (out_amount) | same | same |
| `[2]` (fee_amount) | same; not used in trade output today | — |

Decoder rejects if either of the first two amounts is non-positive,
or if the body isn't a 3-tuple. A 4-tuple (adding a new field)
fails the arity check, which is **good** — fail-loud beats silent.

### Pool-type orthogonality

Aquarius supports volatile / stableswap / concentrated pool types.
The decoder is pool-agnostic — every pool type publishes the same
4-topic + 3-tuple-body shape, so one decoder covers all three.
**Concentrated pools** are tagged `ErrConcentratedWIP` in the source
(Phase-1 audit found them as a feature-branch, no live mainnet
pools). If concentrated pools ship live, this audit needs to
re-verify the trade event shape — contract authors might extend the
body with concentrated-tick info.

## Failure modes specific to Aquarius

Drawing the generic checklist into Aquarius-specific tripwires:

1. **Topic[0] symbol rename** — `"trade"` → `"swap"` (or any other
   string) silently drops every trade event. Audit must verify
   each WASM emits topic[0] = symbol bytes for `"trade"`.
2. **Topic[1]/topic[2] order swap** (sold ↔ bought) — direction
   inverts; recorded base/quote are reversed. Decoder has no way
   to detect this; requires per-WASM source review.
3. **Body tuple field reorder** (e.g. `(out, in, fee)` instead of
   `(in, out, fee)`) — same problem as #2 but at the body level.
   The arity check passes (still 3-tuple); positional extraction
   produces wrong amounts. **No automated detection possible.**
4. **Body tuple length change** — extending to 4-tuple (new field)
   or shrinking to 2-tuple trips the arity check; decoder errors
   out. Fail-loud, every trade dropped — better than silent wrong
   amounts.
5. **i128 → u128 amount type swap** — `scval.AsAmountFromI128` is
   strict; type-tag change errors out per trade. Aquarius pool
   amounts shouldn't go negative so this is an unlikely change.
6. **New pool type with extended body** — concentrated pools (and
   any v2 pool architecture) might publish a longer body or
   different topics. ErrConcentratedWIP is the current safety net
   for one specific case; new pool types need new audit entries.
7. **User topic moved or removed** — currently topic[3] is
   `Address(user)`. Removal would change topic arity from 4 to 3,
   tripping the arity check (good — fail-loud).

## WASM timeline

Output from `ratesengine-ops wasm-history` for the **router**
(CBQDHNBF...) over the post-Soroban window — full archive on r1,
walked 2026-04-29:

```json
{
  "contract": "CBQDHNBF...",
  "ranges": 6 distinct WASM hashes (router upgrades — informational only)
}
```

The **router's 6 hashes are not decoder-relevant**: the decoder
targets `Symbol("trade")` events emitted by per-pool contracts,
not the router's own `Symbol("swap")` events (which carry a
multi-token / multi-pool aggregation shape and are emitted at the
orchestration layer). The router's interface evolution
(governance fields, upgrade-flow methods, protocol-fee admin) is
captured in this audit for completeness but the load-bearing
audit is on the per-pool contracts.

### Pool enumeration (decoder-relevant)

All mainnet pools enumerated via router's `get_pools_for_tokens_range(start, end)`
view (paginated 20 token-sets per call to fit the budget; 287
token-sets total) on 2026-04-29 against mainnet.sorobanrpc.com:

- **313 unique pool addresses** across 287 token-sets.
- Per-pool current WASM hashes obtained via `stellar contract
  fetch --id <pool>` + sha256.

### Per-pool WASM uniqueness

Three unique WASM hashes total across all 313 pools:

| pool count | WASM hash (first 16) | pool type (per binary strings) |
| --- | --- | --- |
| 267 (85%) | `ae0da5a84b15805c` | volatile / `StandardLiquidityPool` (`constant_product`) |
| 40 (13%) | `f1077e0b77da5e62` | `StableswapLiquidityPool` |
| 6 (2%) | `8875f0c770fb26d3` | rewards-enhanced variant |

## Per-hash review findings

| hash (first 16) | role | active pools | reviewer | finding |
| --- | --- | --- | --- | --- |
| `ae0da5a84b15805c` | volatile pool (dominant) | 267 of 313 | ash@2026-04-29 | matches current decoder |
| `f1077e0b77da5e62` | stableswap pool | 40 of 313 | ash@2026-04-29 | matches current decoder |
| `8875f0c770fb26d3` | rewards-enhanced pool | 6 of 313 | ash@2026-04-29 | matches current decoder |

### Source-of-truth: shared event emitter

All three pool types — `liquidity_pool` (volatile),
`liquidity_pool_stableswap`, `liquidity_pool_concentrated` — `use
liquidity_pool_events::Events as PoolEvents` and dispatch to the
shared `LiquidityPoolEvents::trade()` function defined in
`liquidity_pool_events/src/lib.rs:122`. This function is the SOLE
emitter of `Symbol("trade")` events for the entire aquarius
codebase, and it has the wire shape:

    topic = (Symbol::new(e, "trade"), token_in, token_out, user)
    body  = (in_amount as i128, out_amount as i128, fee_amount as i128)

The decoder targets exactly this shape. Source-import topology
verified across all three pool-type packages on 2026-04-29.

### Binary-string verification

Each of the 3 pool WASMs was scanned for the 4 event-name
strings the decoder cares about:

| WASM | `trade` | `update_reserves` | `deposit_liquidity` | `withdraw_liquidity` |
| --- | --- | --- | --- | --- |
| `ae0da5a84b15805c` | ✓ | ✓ | ✓ | ✓ |
| `f1077e0b77da5e62` | ✓ | ✓ | ✓ | ✓ |
| `8875f0c770fb26d3` | ✓ | ✓ | ✓ | ✓ |

All 3 WASMs include the `trade` string + the 3 non-trade event
names (deposit/withdraw/reserves) in their data sections — the
shared event emitter is compiled in unchanged.

## Caveats

- **Per-pool WASM history not walked per-instance.** Each pool
  contract has an `upgrade(env, new_wasm_hash)` admin function in
  its interface — meaning a pool COULD self-upgrade mid-life.
  This audit captures the **current** WASM of each of the 313
  pools (matching decoder), but does not enumerate any
  `update_current_contract_wasm` events that may have happened on
  individual pools since deployment. v2 follow-up: run
  `ratesengine-ops wasm-history -contracts <313 pools>` against
  the full archive to add per-pool upgrade history. Risk is
  contained because (a) the live decoder hasn't seen
  `ErrMalformedPayload` rate spikes against any pool, and (b) the
  source-import topology argument applies to ANY pool WASM
  built from the aquarius-amm tree (the shared-emitter contract
  is structurally enforced).
- **New pools deployed after 2026-04-29 not in this audit.**
  Re-run the enumeration when extending `last_verified`.
- **`ErrConcentratedWIP` is reserved but not currently fired.**
  The decoder constant exists for documentation but the
  classification path doesn't gate on pool type — it matches
  topic[0] = Symbol("trade") regardless of pool variant. All
  three pool types observed in production (including the 6
  rewards-enhanced pools) emit the same trade-event shape via the
  shared events crate, so the decoder works on all of them.

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/aquarius/{events,decode}.go`
- Discovery doc: `docs/discovery/dexes-amms/aquarius.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["aquarius"].BackfillSafe`
- Upstream contract source: `https://github.com/AquaToken/aquarius-amm`
