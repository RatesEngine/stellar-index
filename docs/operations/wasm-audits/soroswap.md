---
title: Soroswap WASM-history audit
last_verified: 2026-04-27
status: pending — scaffolding only; per-hash review in follow-up PR
source: soroswap
backfill_safe: false
---

# Soroswap WASM audit

Audit log for the `soroswap` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

## Status

**Scaffolded 2026-04-27.** This document records the contracts to
audit, the decoder's current expectations, and the failure-mode
checklist. The actual `wasm-history` walk + per-hash review lands
in a follow-up PR — `ratesengine-ops wasm-history` is best run
against r1 once its verify-archive walk completes (currently at
~51%, ~15h remaining as of 2026-04-27 16:00 CEST), or against AWS
public bucket from a separate workstation.

`BackfillSafe` stays `false` for `soroswap` until that follow-up
finishes.

## Contracts under audit

Captured from `internal/sources/soroswap/events.go` (verified
2026-04-23 against `soroswap-core/public/mainnet.contracts.json`):

| role | contract / hash |
| --- | --- |
| Factory | `CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2` |
| Router | `CAG5LRYQ5JVEUI5TEID72EYOVX44TTUJT5BQR2J6J77FH65PCCFAJDDH` |
| Pair WASM hash (current) | `18051456816b66f12e773a56f77c5794fac1b1fb7ab6e22d4fad5a412770f73e` |

The pair contracts themselves are deployed by the factory at
runtime; their per-instance contract IDs are enumerable from the
factory's `new_pair` events. For a first-pass audit, the dominant
pairs by volume are sufficient — full coverage extends as new pairs
get listed.

To enumerate active pair instances on r1 (once verifier is idle):

    psql -h localhost ratesengine -c "
      SELECT DISTINCT base_asset, quote_asset, COUNT(*) AS n
        FROM trades
       WHERE source = 'soroswap'
       GROUP BY base_asset, quote_asset
       ORDER BY n DESC
       LIMIT 20"

…or (better) walk factory `new_pair` events for the audit range
directly via `wasm-history`.

## Decoder expectations

Captured from `internal/sources/soroswap/{events,decode}.go` at
HEAD as of 2026-04-27. Any divergence from these in a deployed
WASM hash is an audit finding.

### Topic structure

Every Soroswap pair / factory event has a 2-element topic:

    topic[0] = ScvString
      - "SoroswapPair"     (pair-instance events: swap, sync, deposit, withdraw, skim)
      - "SoroswapFactory"  (factory events: new_pair)
      - "SoroswapRouter"   (declared but currently unused by the decoder)
    topic[1] = ScvSymbol  (event name)
      - "swap"      → trade-bearing event
      - "sync"      → pair-reserve update; correlated with swap
      - "deposit"   → liquidity provider deposit (not a trade)
      - "withdraw"  → liquidity provider withdraw (not a trade)
      - "skim"      → skim of accumulated fees (not a trade; skipped)
      - "new_pair"  → factory event; populates pair→(token0, token1) cache

Classification is **byte-equal** against pre-encoded base64 SCVal
constants (`TopicPrefixPair`, `TopicSymbolSwap`, etc.). A topic[0]
prefix renamed `"SoroswapPair"` → `"SoroswapPairV2"` (or similar)
silently drops every event from the upgraded contract.

### SwapEvent body

Defined in `pair/src/event.rs` as:

    SwapEvent {
        to:           Address,
        amount_0_in:  i128,
        amount_1_in:  i128,
        amount_0_out: i128,
        amount_1_out: i128,
    }

On the wire this serialises to ScvMap with 5 entries. Decoder pulls
**by name** (per CLAUDE.md "decode by Map-field-name not position"):

| field | extracted by | invariant the decoder relies on |
| --- | --- | --- |
| `amount_0_in`  | `scval.AsAmountFromI128` | i128, sign ≥ 0 |
| `amount_1_in`  | same | same |
| `amount_0_out` | same | same |
| `amount_1_out` | same | same |
| `to`           | (not extracted — ignored) | — |

Trade direction is derived from which of the four amounts is
non-zero. A well-formed swap has exactly one in/out pair non-zero —
either `(amount_0_in, amount_1_out)` or `(amount_1_in,
amount_0_out)` — never both. Decoder rejects with
`ErrMalformedPayload` if the no-direction case is hit.

### SyncEvent body

    SyncEvent {
        new_reserve_0: i128,
        new_reserve_1: i128,
    }

Currently parsed but only used for correlation — the decoder emits
the trade once a `(swap, sync)` pair is observed for the same
`(ledger, tx_hash, op_index)`. The reserve values themselves are
not used in trade output today.

### NewPairEvent body

Emitted by the factory each time a pair contract is deployed. Used
to populate the pair→(token0, token1) registry the swap decoder
depends on.

    NewPairEvent {
        token_0:          Address,
        token_1:          Address,
        pair:             Address,
        new_pairs_length: u32,
    }

Decoder extracts `token_0`, `token_1`, `pair` by name. Treats every
Address as a Soroban contract (`canonical.NewSorobanAsset`). A
`NewPairEvent` whose `token_0` or `token_1` is the native-XLM SAC
contract is handled at asset-resolution layer, not here.

## Failure modes specific to Soroswap

Drawing the generic checklist (see `README.md`) into Soroswap-
specific tripwires:

1. **Topic[0] prefix change** — historically Soroswap used
   `"SoroswapPair"`; if a future upgrade switches to
   `"SoroswapPairV2"` (or moves to a Symbol instead of String for
   the prefix slot), classification drops every event silently.
   Verify each WASM emits `("SoroswapPair", "swap")` shape.
2. **SwapEvent direction encoding change** — current decoder
   relies on the "exactly one in/out pair non-zero" invariant.
   If a future contract introduces a single-direction `amount_in`
   / `amount_out` pair (no `_0` / `_1` suffix) or adds a `direction:
   bool` field, the decoder errors out for every event.
3. **Sync event removed or split** — decoder requires `(swap,
   sync)` correlation. If a contract upgrade emits only `swap` or
   merges sync into swap as additional fields, every swap stays in
   the buffer until the orphan-eviction timer fires and gets dropped.
4. **`to` field removed** — currently ignored by the decoder so this
   is a non-event, but worth noting as a "we'd need to track this
   if requirements change" finding.
5. **NewPairEvent field renamed** — `token_0` / `token_1` / `pair`
   are pulled by name. A rename to e.g. `tokenA` / `tokenB` /
   `pair_address` causes every `new_pair` to fail extraction; pairs
   created under that WASM are missing from the in-memory
   registry; their swap events get dropped (no token0/token1
   resolution possible).
6. **i128 → u128 amount type swap** — `scval.AsAmountFromI128` is
   strict. A type-tag change would error out per swap. (Soroswap
   is unlikely to make this change since negative amounts in
   `amount_*_in/out` aren't meaningful, but worth confirming.)
7. **Skim added as a fee-collection event with non-zero `amount_*`
   fields matching SwapEvent's shape** — current decoder skips
   `skim` by topic name only; if an upgrade makes skim look like a
   swap on the wire, classification by `topic[1]` keeps us safe
   but warrants a check.

## WASM timeline

(*to be filled in by the follow-up PR after `wasm-history` runs*)

Expected format — JSON output from `ratesengine-ops wasm-history`,
inlined here as a fenced code block. Per-hash review findings sit
in the section below as a one-liner per hash.

## Per-hash review findings

(*to be filled in by the follow-up PR*)

| hash (first 16) | active range | reviewer | finding |
| --- | --- | --- | --- |
| (pending) | (pending) | (pending) | (pending) |

## Decision

**`BackfillSafe: false`** — pending the per-hash review.

Will flip to `true` in the follow-up PR if every WASM hash in the
audited range matches the current decoder. Otherwise the follow-up
ships a decoder fix + fixture test against the divergent WASM
hash, then flips.

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/soroswap/{events,decode}.go`
- Discovery doc: `docs/discovery/dexes-amms/soroswap.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["soroswap"].BackfillSafe`
- Upstream contract source: `https://github.com/soroswap/core`
