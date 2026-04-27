---
title: Aquarius WASM-history audit
last_verified: 2026-04-27
status: pending — scaffolding only; per-hash review in follow-up PR
source: aquarius
backfill_safe: false
---

# Aquarius WASM audit

Audit log for the `aquarius` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

## Status

**Scaffolded 2026-04-27.** This document records the contracts to
audit, the decoder's current expectations, and the failure-mode
checklist. The actual `wasm-history` walk + per-hash review lands
in a follow-up PR — best run on r1 once verify-archive completes.

`BackfillSafe` stays `false` for `aquarius` until that follow-up
finishes.

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

(*to be filled in by the follow-up PR after `wasm-history` runs*)

Expected format — JSON output from `ratesengine-ops wasm-history`,
inlined here as a fenced code block.

## Per-hash review findings

(*to be filled in by the follow-up PR*)

| hash (first 16) | active range | reviewer | finding |
| --- | --- | --- | --- |
| (pending) | (pending) | (pending) | (pending) |

## Decision

**`BackfillSafe: false`** — pending the per-hash review.

Audit must be especially careful here vs Soroswap because
positional Vec decoding has no guard against field reordering. A
single missed contract upgrade that shuffled the tuple silently
inverts every trade in the affected range.

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/aquarius/{events,decode}.go`
- Discovery doc: `docs/discovery/dexes-amms/aquarius.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["aquarius"].BackfillSafe`
- Upstream contract source: `https://github.com/AquaToken/aquarius-amm`
