---
title: Reflector WASM-history audit
last_verified: 2026-04-27
status: pending — scaffolding only; per-hash review in follow-up PR
sources: reflector-dex, reflector-cex, reflector-fx
backfill_safe: false
---

# Reflector WASM audit

Audit log for the three Reflector source variants —
`reflector-dex`, `reflector-cex`, `reflector-fx`. All three share
**one decoder** and **one event shape**; they differ only in which
on-chain contract emits the events. We audit them as a single unit.

See `README.md` for the full procedure.

## Status

**Scaffolded 2026-04-27.** This document records the contracts to
audit, the decoder's current expectations, and the failure-mode
checklist. The actual `wasm-history` walk + per-hash review lands
in a follow-up PR — best run on r1 once verify-archive completes.

`BackfillSafe` stays `false` for all three Reflector sources until
that follow-up finishes.

## Contracts under audit

Per CLAUDE.md "Reflector is three separate contracts (DEX / CEX /
FX), not one." Each variant maps to a distinct mainnet contract:

| variant | source name | mainnet contract (operator config) |
| --- | --- | --- |
| DEX | `reflector-dex` | `cfg.Oracle.Reflector.DEXContract` |
| CEX | `reflector-cex` | `cfg.Oracle.Reflector.CEXContract` |
| FX  | `reflector-fx`  | `cfg.Oracle.Reflector.FXContract` |

Concrete addresses live in the operator's TOML config (and Phase-1
discovery doc); they're not hard-coded in the decoder so the same
WASM-history audit can run against testnet or mainnet given a
contract-ID list.

For the actual audit: enumerate the three current mainnet contracts
from the operator's `ratesengine.toml` and run `wasm-history`
against all three.

## Decoder expectations

Captured from `internal/sources/reflector/{events,decode}.go` at
HEAD as of 2026-04-27. Re-verified 2026-04-23 against the upstream
`#[contractevent]` macro expansion.

### Topic structure

    topic[0] = ScvSymbol("REFLECTOR")
    topic[1] = ScvSymbol("update")
    topic[2] = ScvU64(timestamp)        // unix seconds
    body     = ScvVec<(ScVal, ScI128)>  // per-entry tuple

The 3-element topic shape is unusual — `timestamp` is hoisted out
of the body and into a `#[topic]` slot via the `#[contractevent]`
macro. **Important historical correction in the source comments:**
the previous decoder comment claimed body was
`Map{"prices": Vec<(Asset, i128)>, "timestamp": u64}` — that's
WRONG; `#[contractevent]` expands tuple-shaped fields to ScvVec
with the fields in declaration order.

Classification is byte-equal against `TopicSymbolReflector` +
`TopicSymbolUpdate`. Any of those drifting silently drops every
event.

### Body extraction

Each tuple in the outer `Vec<(ScVal, I128)>` is one (asset, price)
pair. The first element identifies the asset — it can be:

- `ScvAddress` (Soroban contract address — for DEX/CEX variant)
- `ScvSymbol` (a fiat code like "USD" or asset symbol — for FX variant)

The decoder skips entries whose first element is neither
ScvAddress nor ScvSymbol (per `ErrUnknownAssetIdentifier`).

The second element is the price as `i128` at Reflector's documented
14-decimal scale.

The decoder fans **one event** out into **N OracleUpdate rows** —
one per (asset, price) tuple in the vector. To preserve the
unique-key constraint on `(source, ledger, tx_hash, op_index)`, the
fanout uses a per-entry op_index stride (matching the SDEX pattern).

### Asset identification

For `reflector-dex` / `reflector-cex` (Soroban Address tuples), the
asset is `canonical.NewSorobanAsset(strkey)`. For `reflector-fx`
(Symbol tuples), it's `canonical.NewFiatAsset(symbol_str)`.

A future contract upgrade that swapped DEX from Address to Symbol
(or vice versa) would still decode but produce wrong asset
classifications.

## Failure modes specific to Reflector

1. **Topic[0] / topic[1] symbol change** — `"REFLECTOR"` or
   `"update"` to anything else silently drops every event.
2. **Topic[2] type change** — `u64` → `i64` or `Symbol` for
   timestamp would error per event (`AsU64FromTopic` strict).
   Fail-loud, but every event in the range dropped.
3. **Body shape change Vec → Map** — the outer-Vec assumption
   breaks; every event errors at extraction.
4. **Per-entry tuple field reorder** — currently `(asset, price)`;
   a swap to `(price, asset)` would produce nonsense (the i128
   would be parsed as an Address). **Almost certainly fail-loud
   per entry**, but every event dropped under that WASM.
5. **Per-entry tuple length change** (e.g. adding a confidence
   score) — would error at the AsTupleN(2) check; every entry
   skipped.
6. **Asset identifier type mix-up across variants** — DEX/CEX
   start emitting Symbols (or FX starts emitting Addresses).
   Decoder still produces output but with wrong asset
   classification — silent. Per-WASM source review must verify
   each variant's tuple type matches its expected shape.
7. **Price scale change** — Reflector documents 14 decimals; if a
   contract upgrade switched to E18 or similar, the i128 still
   decodes but every recorded price is off by 10^N. **No automated
   detection** — caught only by cross-check against external
   oracle data sources.
8. **Vector overflow past OpIndex fanout stride** — if Reflector
   ever emits more than `opIndexFanoutStride` (1024) entries in a
   single event, our op_index synthesis collides. `ErrPriceVectorOverflow`
   surfaces this; would require a stride bump.

## WASM timeline

(*to be filled in by the follow-up PR after `wasm-history` runs*)

Three contracts to scan — DEX/CEX/FX. Audit each timeline
independently; same decoder reviews against each variant's
emitted events.

## Per-hash review findings

(*to be filled in by the follow-up PR*)

| variant | hash (first 16) | active range | reviewer | finding |
| --- | --- | --- | --- | --- |
| DEX | (pending) | (pending) | (pending) | (pending) |
| CEX | (pending) | (pending) | (pending) | (pending) |
| FX  | (pending) | (pending) | (pending) | (pending) |

## Decision

**`BackfillSafe: false`** — for all three sources, pending the
per-hash review.

The flip needs to verify all three variants independently. The
shared-decoder property means a single source review covers the
event shape, but each variant's contract has its own deployment
history and could have evolved independently.

Per CLAUDE.md, **on-chain `twap` / `x_*` methods do not exist** in
Reflector v3 — those are computed locally. This audit only covers
the `update` event flow; if Reflector ever adds on-chain TWAP
methods, that's an entirely new code path requiring its own audit.

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/reflector/{events,decode}.go`
- Discovery doc: `docs/discovery/oracles/reflector.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["reflector-{dex,cex,fx}"].BackfillSafe` (three entries)
