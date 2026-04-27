---
title: Phoenix WASM-history audit
last_verified: 2026-04-27
status: pending — scaffolding only; per-hash review in follow-up PR
source: phoenix
backfill_safe: false
---

# Phoenix WASM audit

Audit log for the `phoenix` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

## Status

**Scaffolded 2026-04-27.** This document records the contracts to
audit, the decoder's current expectations (which are unusually
load-bearing for Phoenix), and the failure-mode checklist. The
actual `wasm-history` walk + per-hash review lands in a follow-up
PR — best run on r1 once verify-archive completes.

`BackfillSafe` stays `false` for `phoenix` until that follow-up
finishes.

## Contracts under audit

Captured from `internal/sources/phoenix/events.go` (verified
2026-04-23 against Phoenix-Protocol-Group/phoenix-contracts deploy
scripts):

| role | contract |
| --- | --- |
| Factory | `CB4SVAWJA6TSRNOJZ7W2AWFW46D5VR4ZMFZKDIKXEINZCZEGZCJZCKMI` |
| Multihop | `CCLZRD4E72T7JCZCN3P7KNPYNXFYKQCL64ECLX7WP5GNVYPYJGU2IO2G` |

Pool contracts are deployed by the factory at runtime; per-instance
contracts emit the swap events. Audit covers the factory + multihop
WASM evolution; per-pool contracts share a factory-deployed WASM
hash so a single per-WASM-hash review covers all pools.

## Decoder expectations

Captured from `internal/sources/phoenix/{events,decode}.go` at HEAD
as of 2026-04-27. **Phoenix's event shape is the most unusual of
any of our Soroban sources** and the decoder is correspondingly
fragile.

### The 8-events-per-swap quirk (CLAUDE.md "Phoenix emits 8 events per swap")

Verified against `phoenix-contracts/contracts/pool/src/contract.rs:1172-1185`.
A single Phoenix swap publishes **8 distinct contract events** — one
per field — instead of one event with all fields packed in the body.
Every event has the same 2-element topic shape:

    topic[0] = ScvString("swap")
    topic[1] = ScvString(<field name>)
    body     = the field value (i128 amounts, Address tokens, etc.)

The 8 field names (verified against the contract source):

| field name | body type | meaning |
| --- | --- | --- |
| `sender` | Address | trader |
| `sell_token` | Address | base asset |
| `offer_amount` | i128 | base amount sold |
| `"actual received amount"` (with spaces) | i128 | received gross |
| `buy_token` | Address | quote asset |
| `return_amount` | i128 | quote amount delivered (net of fees) |
| `spread_amount` | i128 | slippage component |
| `referral_fee_amount` | i128 | optional referral cut |

A `RawSwap` is correlated by `(ledger, tx_hash, op_index)`; the
buffer waits for all 8 field events before emitting a trade. Fewer
than 8 → `ErrIncompleteSwap`; the buffer's eviction policy must
drop these eventually.

### Why topic[0] / topic[1] are ScvString, not ScvSymbol

Embedded spaces in `"actual received amount"` (Phoenix Q2) — Soroban
Symbols are identifier-shape only (no spaces), so the contract
emits all 8 string literals as `ScvString` rather than `ScvSymbol`.
Both topic[0] (`"swap"`) and topic[1] (the field name) come through
as `ScvString` even though their content is identifier-like in 7
of the 8 cases.

Classification is **byte-equal** against pre-encoded `ScvString`
constants. A switch from `ScvString` → `ScvSymbol` for any field
silently drops every event of that field — and dropping even one
of the 8 means `RawSwap` never completes, and **every swap in the
range gets dropped**.

### Trade direction

Computed from `(sell_token, offer_amount)` → base, `(buy_token,
return_amount)` → quote. No `base_is_seller` flag; direction is
authoritative from the topic addresses.

## Failure modes specific to Phoenix

Drawing the generic checklist into Phoenix-specific tripwires:

1. **Topic[0] string change** — `"swap"` → `"trade"` (or any
   variant) silently drops every event of every field. Catastrophic.
2. **Any of the 8 field name string spellings change** — the
   correlation layer expects all 8; even one missing causes the
   `RawSwap` to never complete. Special attention needed for the
   space-bearing `"actual received amount"` — typo / canonicalisation
   (e.g. underscores) would orphan every swap.
3. **Topic[1] type change ScvString → ScvSymbol for fields without
   spaces** — possible if Phoenix later refactors to use Symbols
   for the 7 spaceless fields. Byte-equal classification breaks.
4. **i128 → u128 amount type swap** for any of the 4 i128 fields
   (offer / actual / return / spread / referral) — strict
   `AsAmountFromI128` errors out per event; the swap never
   completes; every swap dropped.
5. **Field added (9th event)** — the buffer waits for all 8 and
   emits when complete. A 9th event would be ignored (not in the
   matched set), so swaps would still emit on the 8 we recognise.
   But if the 9th event carries amount info that should affect
   accounting, we'd silently miss it.
6. **Field removed (7 events per swap)** — `RawSwap` never
   completes; every swap dropped.
7. **Body type for an Address field changes** (e.g. ScvAddress →
   ScvBytes) — decoder errors on extraction; swap never completes.
8. **The 8 events for a single swap arrive across multiple ops or
   txs (correlation key invalidated)** — Phoenix Q1 specifies
   `(ledger, tx_hash, op_index)` is sufficient; if a contract
   upgrade splits the publish across two ops, correlation breaks.
   Requires per-WASM source review.

## WASM timeline

(*to be filled in by the follow-up PR after `wasm-history` runs*)

## Per-hash review findings

(*to be filled in by the follow-up PR*)

| hash (first 16) | active range | reviewer | finding |
| --- | --- | --- | --- |
| (pending) | (pending) | (pending) | (pending) |

## Decision

**`BackfillSafe: false`** — pending the per-hash review.

Phoenix is the highest-risk Soroban source by audit scope: 8 string
constants must all match exactly, and any one diverging silently
drops every swap in the affected range. Source review must check
all 8 field names against each WASM hash, not just topic[0].

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/phoenix/{events,decode}.go`
- Discovery doc: `docs/discovery/dexes-amms/phoenix.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["phoenix"].BackfillSafe`
- Upstream contract source: `https://github.com/Phoenix-Protocol-Group/phoenix-contracts`
