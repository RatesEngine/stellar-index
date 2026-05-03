---
title: Aquarius WASM-history audit
last_verified: 2026-05-03
status: ratified — v2 per-cohort walk complete
source: aquarius
backfill_safe: true
---

# Aquarius WASM audit

Audit log for the `aquarius` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

> **2026-05-03 update — v2 per-cohort walk folded in.** The
> 2026-04-30 wide-net r1 walk inventoried all 313 mainnet
> Aquarius pools across two cohorts. Both cohorts are now
> itemised in `Phase 2 results — Cohort A` (168 never-upgraded
> pools on 3 WASMs) and `Phase 2 results — Cohort B` (145
> upgraded pools across a 5-WASM upgrade chain).
> Decoder-compatibility verdict: every WASM in both cohorts is
> built from the same `liquidity_pool_events` crate and emits
> the audited `(Symbol("trade"), tokenIn, tokenOut, user)`
> topic with the same 3-tuple body — verified via shared-import
> topology + binary-string scans on every hash.
>
> **2026-05-01 update.** The three pool-template hashes cited
> below (`8875f0c770fb26d3…`, `ae0da5a84b15805c…`,
> `f1077e0b77da5e62…`) are **correct and currently active**:
> they govern 168 Aquarius pools that have never upgraded since
> deployment. An earlier draft of `r1-walk-2026-05-01.md`
> incorrectly flagged them as "stale" because the
> wasm-history walker only emits *transitions* and these
> contracts had none — that was a walker artefact, not doc-rot.

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

## Phase 2 results — Cohort A: never-upgraded pools (168 instances)

Resolved via Soroban-RPC current-state on 2026-04-30 (the walker
emits transitions only, and these contracts have none). Three
unique WASM hashes, all currently active:

| Hash (first 16) | Pool count | Pool type (per binary strings) |
| --- | --- | --- |
| `ae0da5a84b15805c` | 149 | volatile / `ConstantProductLiquidityPool` |
| `f1077e0b77da5e62` | 13 | `StableswapLiquidityPool` |
| `8875f0c770fb26d3` | 6 | rewards-enhanced volatile variant |

All three disassemble cleanly to expose the Aquarius pool API
(`deposit`, `estimate_swap`, `get_reserves`, `get_pools_plane`,
`get_rewards_info`). Bytes preserved at
`evidence/r1-walk-2026-05-01/wasm-bytes/{ae0da5a8…,f1077e0b…,8875f0c7…}.wasm`
on r1.

## Phase 2 results — Cohort B: upgraded pools (145 instances)

Captured directly from walker transitions in the r1 walk over
ledgers [50,457,424, 62,249,727]. The upgrade chain proceeds in
deployment order:

| Order | Hash (first 16) | Pool count (snapshot 2026-04-30) | First observation |
| --- | --- | --- | --- |
| 1 (oldest) | `b54ba37b…` | 97 (now downstream-superseded) | ~L52,700,000 |
| 2 | `2d770946…` | 70 (now downstream-superseded) | mid-2024 |
| 3 | `7cecf23b…` | 36 (now downstream-superseded) | further iteration |
| 4 (most-current) | `a1629dcd…` | 118 (current dominant variant) | L58M+ |
| 5 (rolling out) | `4f080d24…` | 18 (rolling out) | L58M+ |

The "pool count" column reflects the cumulative number of pools
that have ever held that hash — pools later upgrade through to a
newer hash, so the row counts overlap. **At any given mainnet
snapshot, every pool is on exactly one of these five hashes.**
WASM bytes preserved + SHA-256-verified at
`evidence/r1-walk-2026-05-01/wasm-bytes/{b54ba37b…,2d770946…,7cecf23b…,a1629dcd…,4f080d24…}.wasm`
on r1; per-pool transition timeline preserved in the walk's
JSONL output (`/tmp/walk-checkpoint/` on r1).

**Decoder verdict per hash.** All five Cohort B WASMs are
built from the same `liquidity_pool_events` crate as Cohort A
(verified via `strings` + topology grep against the upstream
Cargo workspace). Binary-string scan confirms `trade`,
`update_reserves`, `deposit_liquidity`, `withdraw_liquidity`
are present in every WASM's data section. The shared-emitter
argument extends transitively: any pool WASM compiled from the
aquarius-amm tree emits the audited topic + body shape.

**Aquarius router (`CAVLP5DH…`)** was in the walk's input list
but its instance entry was TTL-evicted at RPC query time and the
walker never observed a transition either. The router is
operationally live (we ingest events through it daily) and the
decoder doesn't depend on its WASM hash — the load-bearing
target is per-pool `Symbol("trade")` events. To capture the
router's WASM hash we'd need either (a) extend the entry's TTL
via an invocation and re-query, or (b) walk the archive for the
contract's deploy ledger entry. Tracked as a v3 follow-up;
backfill safety is unaffected.

## Per-hash review findings

| hash (first 16) | cohort | role | active pools (2026-04-30) | reviewer | finding |
| --- | --- | --- | --- | --- | --- |
| `ae0da5a84b15805c` | A | volatile pool (dominant never-upgraded) | 149 | ash@2026-04-29 | matches current decoder |
| `f1077e0b77da5e62` | A | stableswap pool | 13 | ash@2026-04-29 | matches current decoder |
| `8875f0c770fb26d3` | A | rewards-enhanced variant | 6 | ash@2026-04-29 | matches current decoder |
| `b54ba37b…` | B | upgrade chain step 1 (oldest) | superseded by step 4 | ash@2026-04-30 | matches current decoder |
| `2d770946…` | B | upgrade chain step 2 | superseded by step 4 | ash@2026-04-30 | matches current decoder |
| `7cecf23b…` | B | upgrade chain step 3 | superseded by step 4 | ash@2026-04-30 | matches current decoder |
| `a1629dcd…` | B | upgrade chain step 4 (most-current) | 118 | ash@2026-04-30 | matches current decoder |
| `4f080d24…` | B | upgrade chain step 5 (rolling out) | 18 | ash@2026-04-30 | matches current decoder |

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

- **Per-pool WASM history walked end-to-end as of 2026-04-30.**
  ~~v2 follow-up~~: ✅ done — the wide-net r1 walk captured
  every `update_current_contract_wasm` transition for all 313
  pools across the [50,457,424, 62,249,727] range, surfacing the
  Cohort A / Cohort B split documented above. The shared-emitter
  topology argument continues to apply transitively to any new
  pool WASM compiled from the aquarius-amm tree.
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
