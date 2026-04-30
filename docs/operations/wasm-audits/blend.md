---
title: Blend WASM-history audit
last_verified: 2026-04-30
status: pending — pool enumeration + per-pool walk required before flip
source: blend
backfill_safe: false
---

# Blend WASM audit

Audit log for the `blend` source's `BackfillSafe` flag. See
[`README.md`](README.md) for the full procedure.

## Status

**Pending 2026-04-30.** Blend was wired into the dispatcher,
registry, and indexer in PRs #273-#275. `BackfillSafe` stays
`false` until this audit completes the per-WASM-hash review of
every Blend pool's deployed WASM.

The audit is structurally similar to soroswap/phoenix/aquarius:
the dispatch layer matches Blend by topic — every per-pool contract
emits the same `("new_auction", ...)`, `("fill_auction", ...)`,
`("delete_auction", ...)` topic shapes — but the actual WASM-bytes
audit lives at the pool-instance level, not at the Pool Factory.

## Contracts under audit

Per `docs/discovery/dexes-amms/blend.md` (verified 2026-04-22 via
stellar.expert + the `blend-contracts-v2` deploy manifest):

| Role | Contract | WASM hash (v2) |
| --- | --- | --- |
| Pool Factory V2 | `CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU` | `31328050548831f63d2b72e37bcfd0bb7371b7907135755dbe09ed434d755ca9` |
| Backstop V2 | `CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7` | `c1f4502a757e25c611f5a159bc1ab0eef64085adac6c68123dca66e87faffbc2` |

**Pool contracts** are deployed at runtime by the Pool Factory's
`deploy()` entrypoint. Each `deploy()` invocation emits the
factory's only event:

```text
topics: [Symbol("deploy")]
body:   pool_address: Address
```

Walking these events backward from the factory's deploy ledger
(L51,499,546) produces the canonical list of every Blend pool
ever deployed on mainnet. As of 2026-04-30 the factory has emitted
only **9 events** (per stellar.expert) — meaning ≤9 pools have
been deployed, which is a small audit surface.

## Audit plan (this is a TODO, not yet executed)

### Phase 1 — Enumerate pool contracts

Pool Factory has no enumeration view function (only `deploy()`),
so pool addresses must be recovered from the factory's emitted
events. Three execution options, in increasing self-sufficiency:

1. **Walk Pool Factory `deploy` events on r1** (preferred).
   Run `ratesengine-ops wasm-history` against the factory contract
   to capture the timeline of when each pool address was deployed
   (factory's WASM history serves as a side-channel here — every
   pool deploy creates a `LedgerEntryChange` we'd see). However,
   `wasm-history` watches `update_current_contract_wasm` events,
   not generic event publishes — so we'd need a small additional
   tool (or extension to the existing `extract-wasm-from-galexie`)
   that walks LCM and emits `(ledger, pool_address)` for every
   `("deploy")` event from the factory.

2. **Walk via `stellar events`** against a public RPC. Public
   stellar-rpc retention is ~7 days; insufficient since the factory
   has been live since 2025-04-14. Useful only for events emitted
   in the last week.

3. **Manual lookup via Blend Capital docs / stellar.expert**.
   stellar.expert reports the factory has 9 events lifetime — a
   manual review of those 9 deploy events extracts the pool list
   directly without tooling. Fastest path; lowest scalability for
   future re-audits.

**Recommended for v1 audit**: option 1 — extend
`extract-wasm-from-galexie` (or write a small companion subcommand)
to walk Pool Factory `("deploy")` events on r1 and emit a
`pool_address` list. Once the per-pool list is in hand, audit
proceeds identically to phoenix / aquarius.

### Phase 2 — Per-pool wasm-history walk

For each pool address from Phase 1:

```sh
ratesengine-ops wasm-history \
  -config /etc/ratesengine.toml \
  -from 51499546 -to <r1-tip> -parallel 8 \
  -checkpoint-dir /var/log/wasm-history-blend-pools \
  -contracts <pool-1>,<pool-2>,...
```

Captures every `update_current_contract_wasm` event observed on
each pool. Most pools are deployed-and-forgotten; few or no
upgrades expected.

### Phase 3 — Per-WASM-hash review

For each unique WASM hash discovered in Phase 2:

1. Fetch via `stellar contract fetch --wasm-hash <h>` from public
   RPC. If evicted (TTL expired), fall back to
   `ratesengine-ops extract-wasm-from-galexie` against r1.
2. Run `stellar contract info interface --wasm <h>.wasm` and
   compare against the canonical interface (most-recent deployed
   pool's WASM).
3. `strings <h>.wasm | grep -E "new_auction|fill_auction|delete_auction|bid|lot|block"` — confirm the auction event-topic strings + AuctionData field names are present.
4. Compare against the internal/sources/blend decoder's expectations
   per the `Decoder expectations` section below.

Document findings in the per-hash table at the bottom.

### Phase 4 — Decision + flip

If every pool WASM is decoder-compatible, flip
`Registry["blend"].BackfillSafe = true` in
`internal/sources/external/registry.go`, update
`framework_test.go` to move blend from `wantUnsafe` to `wantSafe`,
update CHANGELOG.md, and set this doc's `status: ratified`.

## Decoder expectations

Captured from `internal/sources/blend/{events,decode,auction_data}.go`
at HEAD as of 2026-04-30. Verified against
`.discovery-repos/blend-contracts/pool/src/events.rs` (commit
`c19abee5b9be4f49e0cda9057e87d343e5dcc095`).

### Topic structure (auction events)

Every Blend auction event has a 3-element topic:

```text
topic[0] = Symbol("new_auction" | "fill_auction" | "delete_auction")
topic[1] = u32(auction_type)           // 0=UserLiquidation, 1=BadDebt, 2=Interest
topic[2] = Address(user)               // G or C strkey
```

Classification is byte-equal against pre-encoded `ScvSymbol`
constants. A topic[0] symbol rename silently drops every event.

### `new_auction` body

```text
Vec(
    percent: u32,
    auction_data: AuctionData,
)
```

### `fill_auction` body

```text
Vec(
    filler:               Address,
    fill_percent:         i128,
    filled_auction_data:  AuctionData,
)
```

### `delete_auction` body

Empty (`()` — Soroban unit).

### `AuctionData` shape

`pool/src/auctions/auction.rs::AuctionData` is a `#[contracttype]`
struct with named fields, so soroban-sdk emits it as `ScvMap` with
sorted-by-symbol keys:

```text
ScvMap{
  "bid":   Map<Address, i128>,  // assets the filler spends
  "block": u32,                 // auction-start block
  "lot":   Map<Address, i128>,  // assets the filler receives
}
```

Decoder extracts by name — resilient to field reordering.

### Auction type discriminants

Verified against `pool/src/auctions/auction.rs`:

| `auction_type` | Name | Bid asset | Lot asset |
| --- | --- | --- | --- |
| `0` | UserLiquidation | dTokens | bTokens |
| `1` | BadDebt | dTokens | Underlying (backstop) |
| `2` | Interest | Underlying (backstop) | Underlying |

Decoder rejects values outside this set with `ErrUnknownAuctionType`.

## Failure modes specific to Blend

1. **Topic[0] symbol change** — `"new_auction"` → anything else
   silently drops every event of that variant.
2. **Topic[1] type change** — `u32` → other surfaces
   `ErrMalformedPayload`. Fail-loud, every event in the range
   dropped under that WASM.
3. **AuctionData field rename** — `bid` / `lot` / `block` are
   looked up by name; a rename returns
   `auction_data missing "bid"`. Fail-loud per event.
4. **Inner Map<Address, i128> shape change** — e.g. moving to a
   Vec<(Address, i128)>. `scval.AsMap` errors on non-Map.
5. **i128 type drift** — `scval.AsAmountFromI128` is strict; any
   type-tag change errors out per amount.
6. **New auction_type value** — surfaces `ErrUnknownAuctionType`,
   prompting an audit rather than a silent skip.

## WASM timeline

(*to be filled in by the follow-up PR after Phase 1-3 complete*)

## Per-hash review findings

(*to be filled in by the follow-up PR*)

| variant | hash (first 16) | active range | reviewer | finding |
| --- | --- | --- | --- | --- |
| Pool Factory | `31328050548831f6` | (pending walk) | (pending) | (pending) |
| Backstop | `c1f4502a757e25c6` | (pending walk) | (pending) | (pending) |
| Pool (template) | (pending enumeration) | (pending) | (pending) | (pending) |

## Decision

**`BackfillSafe: false`** — this audit cannot complete until pool
enumeration runs. The wiring PRs (#273-#275) intentionally land
with the flag set to false; the flip occurs in the follow-up that
completes Phase 1-4.

## References

- Procedure: [`README.md`](README.md)
- Decoder source: `internal/sources/blend/{events,decode,auction_data}.go`
- Discovery doc: [`../../discovery/dexes-amms/blend.md`](../../discovery/dexes-amms/blend.md)
- Schema-evolution stance: [`../../architecture/contract-schema-evolution.md`](../../architecture/contract-schema-evolution.md)
- Backfill gate: `internal/sources/external/registry.go` —
  `Registry["blend"].BackfillSafe`
- Upstream contracts: <https://github.com/blend-capital/blend-contracts-v2>
- Local checkout: `.discovery-repos/blend-contracts/`
