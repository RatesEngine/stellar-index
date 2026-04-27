---
title: Comet WASM-history audit
last_verified: 2026-04-27
status: pending — scaffolding only; per-hash review in follow-up PR
source: comet
backfill_safe: false
---

# Comet WASM audit

Audit log for the `comet` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

## Status

**Scaffolded 2026-04-27.** This document records the contracts to
audit, the decoder's current expectations, and the failure-mode
checklist. The actual `wasm-history` walk + per-hash review lands
in a follow-up PR — best run on r1 once verify-archive completes.

`BackfillSafe` stays `false` for `comet` until that follow-up
finishes.

## Contracts under audit

Comet has **no canonical mainnet contract list** in our codebase —
the source is a Balancer-v1-style weighted-AMM implementation
deployable as a library. CLAUDE.md flags this:

> **Comet uses a shared `("POOL", <event>)` topic across every pool
> contract**, not a per-protocol namespace. The decoder matches by
> topic bytes, not pool contract ID — any pubnet contract that
> deploys Balancer-v1 Comet code will look identical on the wire.

This makes Comet's audit shape distinct from Soroswap/Aquarius/
Phoenix:

- **There's no factory ID we can name.** Anyone can deploy a Comet
  pool from the published WASM. Each pool is its own contract.
- **The pair-of-WASM-and-topics is the audit unit**, not the
  contract ID. We're auditing whether *any* WASM that emits
  `("POOL", "swap")` events with the expected body shape can have
  its events safely decoded.
- For practical scope, the audit covers **the dominant pools by
  trade volume on mainnet** — Blend's backstop pool is the most
  prominent example. Per-instance contract IDs come from running
  the dispatcher against mainnet and observing which contracts
  dispatch into our Comet decoder.

To enumerate active Comet pool contract IDs (once r1's trades
hypertable is populated):

    psql -h localhost ratesengine -c "
      SELECT DISTINCT base_asset, quote_asset, COUNT(*) AS n
        FROM trades
       WHERE source = 'comet'
       GROUP BY base_asset, quote_asset
       ORDER BY n DESC
       LIMIT 20"

…or by manual reference: Blend Capital's documentation lists the
backstop pool's contract ID at the time of writing.

## Decoder expectations

Captured from `internal/sources/comet/{events,decode}.go` at HEAD
as of 2026-04-27.

### Topic structure (swap events)

Verified 2026-04-23 against the public contract source
(`comet-contracts/src/c_pool/event.rs` and `call_logic/pool.rs:21,184-191`):

    topic[0] = ScvSymbol("POOL")
    topic[1] = ScvSymbol("swap")  // also "join_pool" / "exit_pool" /
                                  // "deposit" / "withdraw" — we ignore
    body     = ScvMap {
        "caller":           Address,
        "token_in":         Address,
        "token_out":        Address,
        "token_amount_in":  i128,
        "token_amount_out": i128,
    }

Classification is **byte-equal** against pre-encoded `ScvSymbol`
constants. v1 decodes only the `swap` variant; non-swap Comet
events return `ErrNotCometSwap` and are skipped.

### Body extraction

Decoder pulls the 5 fields **by name** (Map-keyed). Same robust
pattern as Soroswap (vs Aquarius's positional Vec) — adding a new
field doesn't break extraction; renaming or removing a field does.

| field | extracted by | invariant |
| --- | --- | --- |
| `token_in` | `scval.AsAddressStrkey` | valid Soroban Address |
| `token_out` | same | same |
| `token_amount_in` | `scval.AsAmountFromI128` | i128, sign > 0 |
| `token_amount_out` | same | same |
| `caller` | (extracted but not used in trade output today) | — |

Decoder rejects with `ErrNonPositiveAmounts` if either amount is
zero / negative. Direction is `(token_in, token_amount_in) → base`,
`(token_out, token_amount_out) → quote`.

## Failure modes specific to Comet

1. **Topic[0] symbol change** — `"POOL"` → other namespace
   (`"COMET_POOL"`?) silently drops every event.
2. **Topic[1] symbol change for swap** — `"swap"` → `"trade"`
   silently drops every trade. Other variants (`join_pool`,
   `exit_pool`, …) we already skip; new variants would be
   silently skipped — fine if they're not trades, bad if they
   ARE trades under a new shape.
3. **Body field rename** — `token_in` → `tokenIn`, `token_amount_in`
   → `amount_in`, etc. Decoder fails per event with field-not-found;
   every swap dropped under the renamed WASM.
4. **Body field removal** — same effect as rename.
5. **Body field type change** — i128 → u128, Address → bytes —
   strict extraction errors per event.
6. **Body shape change Map → Vec** — would error at the Map-cast
   step; every swap dropped.
7. **A new pool architecture deployed at the same WASM lineage that
   emits a different swap shape** — since Comet has no central
   factory, a "Comet v2" might emit `("POOL", "swap")` with a
   different body shape, and we'd only learn about it via Hubble
   cross-check (count diff) or a per-pool review of the new pool's
   WASM hash. **This is the unique-to-Comet failure mode** — for
   other Soroban DEXes a factory upgrade is the source of truth;
   for Comet, every pool is a potential variant.

## WASM timeline

(*to be filled in by the follow-up PR after `wasm-history` runs*)

For Comet, the timeline is per-pool-contract, not per-factory.
Audit must enumerate the mainnet pool contracts emitting Comet
events (via r1's trades hypertable) and run `wasm-history` against
each. A v2 of this audit could deduplicate by WASM hash since many
pools likely share one hash.

## Per-hash review findings

(*to be filled in by the follow-up PR*)

| hash (first 16) | example pool | active range | reviewer | finding |
| --- | --- | --- | --- | --- |
| (pending) | (pending) | (pending) | (pending) | (pending) |

## Decision

**`BackfillSafe: false`** — pending the per-hash review.

Comet's no-factory-of-record property means this audit doesn't
have a clean "covered every WASM hash on mainnet" upper bound.
The follow-up PR's BackfillSafe=true will be conditional on
"every pool contract our trades hypertable observed up to the
audit's `to` ledger has been reviewed." New pools deployed
afterwards re-open the audit until reviewed.

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/comet/{events,decode}.go`
- Discovery doc: `docs/discovery/dexes-amms/comet.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["comet"].BackfillSafe`
- Upstream contract source: published in operator-discovery — see
  the Phase-1 capture under `.discovery-repos/comet-contracts/`
