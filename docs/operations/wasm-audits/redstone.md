---
title: Redstone WASM-history audit
last_verified: 2026-04-27
status: pending — scaffolding only; per-hash review in follow-up PR
source: redstone
backfill_safe: false
---

# Redstone WASM audit

Audit log for the `redstone` source's `BackfillSafe` flag. See
`README.md` for the full procedure.

## Status

**Scaffolded 2026-04-27.** Captures the contracts, decoder
expectations, and Redstone-specific failure modes. The actual
`wasm-history` walk + per-hash review lands in a follow-up PR.

`BackfillSafe` stays `false` for `redstone` until that follow-up
finishes.

## Contracts under audit

Redstone's mainnet deployment uses a single Adapter contract that
owns price storage for every feed, plus thin per-feed proxies.
Audit covers the **Adapter contract**.

| role | mainnet contract (operator config) |
| --- | --- |
| Adapter | `cfg.Oracle.Redstone.AdapterContract` |

Concrete address lives in the operator's `ratesengine.toml` (and
Phase-1 discovery doc); not hard-coded. For the audit, enumerate
from the operator's config.

## Decoder expectations

Captured from `internal/sources/redstone/{events,decode}.go` at HEAD
as of 2026-04-27. Re-verified 2026-04-23 against the upstream
`redstone-public-contracts` repo.

### Topic structure

    topic[0] = ScvSymbol("REDSTONE")
    body     = ScvMap {
        "updater":       ScvAddress,
        "updated_feeds": ScvVec<PriceData>,
    }
    PriceData = ScvMap {
        "price":             ScvU256,    // U256 — this is unique to Redstone
        "package_timestamp": ScvU64,
        "write_timestamp":   ScvU64,
    }

Single-element topic — only `topic[0] = "REDSTONE"`. No second
slot; classification is byte-equal against `TopicSymbolRedstone`
alone.

### The "feed IDs are in op args, not event body" trap

CLAUDE.md flags this:

> **Redstone's event body carries no feed_id.** `WritePrices
> { updater, updated_feeds: Vec<PriceData> }` gives prices +
> timestamps, not which feed each entry is. Feed IDs live in the
> tx's `write_prices(updater, feed_ids, payload)` InvokeContract
> op args — plumbed through `events.Event.OpArgs`.

The decoder zips `feed_ids` (from op args) against `updated_feeds`
(from event body) one-to-one. If the adapter's freshness verifier
rejects a feed, it skips that entry in `updated_feeds` WITHOUT
skipping in `feed_ids` — we guard with a strict length check
(`ErrFeedIDCountMismatch`) and skip the whole event rather than
risk attributing a BTC price to ETH.

### Body extraction

Decoder pulls **by name** (Map-keyed) — same robust pattern as
Soroswap and Comet:

| field | extracted by | invariant |
| --- | --- | --- |
| `updater` | `scval.AsAddressStrkey` | valid Soroban Address |
| `updated_feeds` | iterated as Vec | each entry is a PriceData Map |
| `PriceData.price` | `scval.AsU256ToBigInt` | **U256** (not i128 like every other source) |
| `PriceData.package_timestamp` | `scval.AsU64` | seconds |
| `PriceData.write_timestamp` | same | same |

### Function-call gating

The decoder only trusts op args from calls to `write_prices`. Any
other function call (e.g. a composed tx that also calls a different
Redstone method) is rejected with `ErrWrongFunctionCall`. This
defends against decoding an unrelated call's args as feed IDs.

### Known-feeds allow-list

Per CLAUDE.md, Redstone has **19 mainnet feeds**. Feed IDs from op
args that aren't on the known-feeds allow-list are skipped per-entry
with `ErrUnknownFeedID` (other feeds in the same event still land).
A new feed listed on Redstone's mainnet adapter without our
allow-list update is silently dropped — list lives in
`docs/discovery/oracles/redstone.md`.

## Failure modes specific to Redstone

1. **Topic[0] symbol change** — `"REDSTONE"` to anything else
   silently drops every event.
2. **Body field rename** (`updated_feeds` → `feed_updates`,
   `updater` → `caller`, etc.) — by-name extraction errors per
   event; every event dropped under that WASM.
3. **PriceData field rename** — same as #2 but for the inner Map.
4. **`price` type change U256 → i128** — strict
   `AsU256ToBigInt` errors per entry. Fail-loud, every entry
   dropped. **Unique to Redstone** — every other on-chain price
   source uses i128.
5. **Decimals scale change** — Redstone documents 8 decimals
   universally. A switch to 18 decimals (matching Band's E18)
   silently mis-reports every price. **No automated detection** —
   caught only by cross-source divergence vs Reflector / Band.
6. **`write_prices` function signature change** (renamed,
   reordered args) — the decoder reads `feed_ids` from op args
   by position. A reorder would zip the wrong identifiers against
   prices. Per-WASM source review must verify the function
   signature is unchanged.
7. **Feed-ID encoding change** (String → Symbol → Bytes) — strict
   extraction errors per entry. Every entry dropped, but the
   error tells the operator what changed.
8. **Adapter and proxy split** — if Redstone refactored to publish
   events from per-feed proxies instead of the central adapter,
   our decoder would still match the topic but the op-args plumbing
   breaks (write_prices no longer the producing call).
9. **Heartbeat / freshness change** — the documented `0.2%
   deviation OR 24h heartbeat` rule could change. We expose
   `DefaultResolutionSeconds = 24h` for the staleness alert. If
   the heartbeat shortens, no decoder issue but the alert tuning
   is wrong.

## WASM timeline

(*to be filled in by the follow-up PR after `wasm-history` runs*)

## Per-hash review findings

(*to be filled in by the follow-up PR*)

| hash (first 16) | active range | reviewer | finding |
| --- | --- | --- | --- |
| (pending) | (pending) | (pending) | (pending) |

## Decision

**`BackfillSafe: false`** — pending the per-hash review.

Redstone's audit must additionally verify the `write_prices` function
signature is unchanged across every WASM hash, not just event topics
+ body. The op-args plumbing is integral to producing correct trades;
a function-signature drift produces silently wrong attribution with
no event-shape guard.

## References

- Procedure: `docs/operations/wasm-audits/README.md`
- Decoder source: `internal/sources/redstone/{events,decode}.go`
- Discovery doc: `docs/discovery/oracles/redstone.md`
- Schema-evolution stance: `docs/architecture/contract-schema-evolution.md`
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["redstone"].BackfillSafe`
