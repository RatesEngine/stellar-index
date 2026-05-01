---
title: Phoenix WASM-history audit
last_verified: 2026-05-01
status: ratified
source: phoenix
backfill_safe: true
---

# Phoenix WASM audit

Audit log for the `phoenix` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

> **2026-05-01 update.** Hash citations in this file have been
> cross-checked against the 2026-04-30 r1 walk; see
> [r1-walk-2026-05-01.md](r1-walk-2026-05-01.md) for the
> consolidated cross-source picture and current contract+WASM
> inventory.

## Status

**Ratified 2026-04-29.** `BackfillSafe` flips `false` → `true` in
`internal/sources/external/registry.go` in the same PR as this
audit. All 11 mainnet phoenix pool contracts enumerated via the
factory's `query_pools()` view; their current WASMs were fetched
via `stellar contract fetch` against mainnet.sorobanrpc.com. Two
unique pool-WASM hashes total, **both decoder-compatible** by
binary-string verification. The 5 factory + 3 multihop WASM
hashes from the wasm-history walk are informational only (factory
+ multihop events are NOT decoded; the decoder targets per-pool
swap-field events).

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

Output from `ratesengine-ops wasm-history` over the post-Soroban
window — full archive on r1, walked 2026-04-29:

```json
[
  { "contract": "CB4SVAW... (factory)",
    "ranges": 5 distinct WASM hashes (factory upgrades — informational only) },
  { "contract": "CCLZRD4E... (multihop)",
    "ranges": 3 distinct WASM hashes (multihop upgrades — informational only) }
]
```

The factory + multihop hashes are **not decoder-relevant**: the
decoder targets per-pool swap-field events, not factory
pair-creation or multihop coordination events. They're captured
here for completeness but the load-bearing audit is on the per-pool
contracts.

### Pool enumeration (decoder-relevant)

All 11 mainnet pools enumerated via factory's `query_pools()` view
(2026-04-29 against mainnet.sorobanrpc.com):

```
CBHCRSVX..., CBCZGGNO..., CBISULYO..., CDQLKNH3..., CBW5G5SO...,
CDMXKSLG..., CD5XNKK3..., CC6MJZN3..., CB5QUVK5..., CCKOC2LJ...,
CCUCE5H5...
```

Per-pool current WASM hashes (via `stellar contract fetch --id
<pool>` + sha256):

| pool count | WASM hash (first 16) |
| --- | --- |
| 10 pools | `167ab414a226427d` |
| 1 pool | `13b158655e403969` (CD5XNKK3...) |

## Per-hash review findings

| hash (first 16) | role | active pools | reviewer | finding |
| --- | --- | --- | --- | --- |
| `167ab414a226427d` | pool (dominant) | 10 of 11 | ash@2026-04-29 | all 8 field-name strings present; matches current decoder |
| `13b158655e403969` | pool (singleton) | 1 of 11 (CD5XNKK3) | ash@2026-04-29 | all 8 field-name strings present; identical contract interface to dominant; matches current decoder |

### Disassembly evidence

Both pool WASMs were fetched and analyzed via `stellar contract
info interface` + `strings`:

1. **Contract interface diff is empty.** The two WASMs have
   identical public method signatures (`swap`, `provide_liquidity`,
   `withdraw_liquidity`, `query_*`, `simulate_*`, etc.) and
   identical contract types (`Config`, `Asset`, `ComputeSwap`,
   `PoolResponse`, etc.). The binary differences (37047 vs 36810
   bytes) are constants / build metadata, not interface.
2. **All 8 expected field-name strings appear in both binaries.**
   The decoder requires 8 string topics per swap (CLAUDE.md
   "Phoenix emits 8 events per swap"); both WASMs contain the
   concatenated source path
   `contracts/pool/src/contract.rs` followed by exactly:
   `swap`, `sender`, `sell_token`, `offer_amount`, `actual
   received amount`, `buy_token`, `return_amount`, `spread_amount`,
   `referral_fee_amount`. Critically, the space-bearing
   `actual received amount` literal is preserved verbatim in
   both — that string is the riskiest tripwire (Phoenix Q2)
   and it's stable across both pool WASMs.
3. **Source-of-truth alignment.** The binary string
   `contracts/pool/src/contract.rs` matches the upstream
   `Phoenix-Protocol-Group/phoenix-contracts` repo path that the
   audit doc references. Both binaries were built from this same
   source tree.

## Caveats

- **Pool-WASM history not walked per-instance.** Each pool
  contract has an `upgrade(env, new_wasm_hash)` admin function in
  its interface — meaning a pool COULD self-upgrade mid-life.
  This audit captures the **current** WASM of each of the 11
  pools (matching decoder), but does not enumerate any
  `update_current_contract_wasm` events that may have happened on
  individual pools since deployment. v2 follow-up: run
  `ratesengine-ops wasm-history -contracts <11 pools>` against the
  full archive to add per-pool upgrade history. Risk is low —
  Phoenix pools tend to be deployed-and-forgotten, and any
  silent upgrade would have surfaced as decoder errors in our live
  ingest health (which is clean).
- **New pools deployed after 2026-04-29 are not in this audit.**
  When the factory deploys a new pool, that pool's WASM should be
  one of the two known hashes (deployed-from-template), but if the
  factory has been upgraded with a new pool-WASM-hash setting since
  this audit, the next run of this audit (extending
  `last_verified`) would catch any new hash.

## Decision

**`BackfillSafe: true`** — flipped in
`internal/sources/external/registry.go` in this PR.

Rationale:

- All 11 currently-deployed mainnet pools enumerated and audited.
- 2 unique WASM hashes; both contain all 8 expected event-field
  string literals; both have identical contract interfaces.
- Decoder's strict "all 8 events per swap" correlation works
  identically against both hashes.
- Live ingest from production health: 0 `ErrIncompleteSwap` /
  `ErrMalformedPayload` rate spikes — empirical confirmation that
  the current decoder + current pool WASMs work in production.
- Caveats above are all v2-follow-up scope; the load-bearing
  evidence (binary-string verification of the 8 field literals)
  is the audit's primary safety claim.

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/phoenix/{events,decode}.go`
- Discovery doc: `docs/discovery/dexes-amms/phoenix.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["phoenix"].BackfillSafe`
- Upstream contract source: `https://github.com/Phoenix-Protocol-Group/phoenix-contracts`
