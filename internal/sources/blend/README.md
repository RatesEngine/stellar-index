# Blend (Soroban lending protocol)

This package decodes events from [Blend Capital's Soroban lending
protocol](https://github.com/blend-capital/blend-contracts-v2).
See the protocol verification page:
[`docs/protocols/blend.md`](../../../docs/protocols/blend.md).

## What this is (and isn't)

Blend is **not a spot trading venue** — there's no AMM-style swap
inside Blend itself. We index Blend for:

| Signal | Why we care |
| --- | --- |
| **Liquidation auctions** (`new_auction`, `fill_auction`, `delete_auction`) | Directional price signals during stress. Auction fills expose the actual market-clearing price for collateral being liquidated, which is a *stressed* price (below fair value by design) but a real trade signal. |
| **Money-market events** (supply / withdraw / borrow / repay / flash_loan) | Supply-side metrics: total deposited / borrowed per asset per pool. Feeds the asset-detail surface (Freighter V2). |
| **Credit-risk events** (bad_debt, defaulted_debt) | Protocol-health signals; useful for downstream consumers. |
| **Admin / status** (set_admin, update_pool, set_status, set_reserve) | Operational state for degraded-source detection. |

We **do not** emit Blend events as `canonical.Trade` rows — Blend's
outputs are auctions and position changes, not spot trades. The
indexer's sink routes Blend events to per-protocol Blend storage
(auctions table, positions table, admin events) rather than the
unified trades hypertable.

## Scope of this package

The auction-event surface — the primary directional price signal:

- `new_auction` — auction announcement
- `fill_auction` — partial / full fill by an external filler
- `delete_auction` — auction admin-removed before completion

### Shipped

- ✅ Auction-event surface (this package).
- ✅ `blend_auctions` storage table + writer
  (`migrations/0009_create_blend_auctions.up.sql` plus the
  inserter in `internal/storage/timescale/`).
- ✅ Dispatcher + registry wiring (`internal/sources/external/
  registry.go` flips `blend.BackfillSafe = true` post-audit;
  the dispatcher routes Blend events through the auction
  decoder).
- ✅ WASM audit for the Pool Factory + deployed pools (Task #53,
  evidence at [`docs/operations/wasm-audits/blend.md`](../../../docs/operations/wasm-audits/blend.md);
  11 contracts, 3 unique WASMs, no mid-life upgrades observed).
- ✅ Money-market / credit-risk / admin / factory event surface
  (Task #25, per the every-event principle). Adds the 18 topics
  the auction-era decoder dropped: supply, withdraw,
  supply_collateral, withdraw_collateral, borrow, repay,
  flash_loan, gulp, claim, bad_debt, defaulted_debt,
  reserve_emission_update, gulp_emissions, set_admin,
  update_pool, queue_set_reserve, cancel_set_reserve,
  set_reserve, set_status, deploy. Storage in three per-purpose
  hypertables: `blend_positions` (the 7 position-changing
  events), `blend_emissions` (gulp / claim / emissions /
  bad_debt / defaulted_debt), `blend_admin` (admin / config /
  pool-factory). Migration `0042_create_blend_money_market`.

### Still deferred

- Historical replay over `[Blend genesis, present)` — the live
  ingest captures every event going forward; bulk-fill the
  pre-rc.78 range via `INSERT INTO blend_positions /
  blend_emissions / blend_admin SELECT … FROM soroban_events
  WHERE contract_id IN (<pool contracts>) AND topic_0_sym IN
  (…)` once the `soroban_events` walk lands (ADR-0029 — table
  exists, walk job is in flight).
- Reflector cross-validation — monitor Blend's oracle price
  consumption via Reflector to cross-validate that our aggregated
  prices are consistent with what the protocol is using. Out of
  scope until a consumer needs the cross-check signal.

## Mainnet contracts

Verified on-chain 2026-04-22:

| Role | Contract |
| --- | --- |
| Pool Factory V2 | `CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU` |
| Backstop V2 | `CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7` |

Pool contracts are deployed by the Pool Factory at runtime (via
`deploy()` events). Per-pool enumeration happens via the Pool
Factory's `deploy` event timeline; we don't hard-code the pool list.

## Auction-event wire formats

Verified 2026-04-22 against `pool/src/events.rs` in
blend-contracts-v2 commit `c19abee5b9be4f49e0cda9057e87d343e5dcc095`.

### `new_auction`

```text
topics: [Symbol("new_auction"), u32(auction_type), Address(user)]
body:   (percent: u32, auction_data: AuctionData)
```

### `fill_auction`

```text
topics: [Symbol("fill_auction"), u32(auction_type), Address(user)]
body:   (filler: Address, fill_percent: i128, filled_auction_data: AuctionData)
```

### `delete_auction`

```text
topics: [Symbol("delete_auction"), u32(auction_type), Address(user)]
body:   ()
```

### `AuctionData` shape

`pool/src/auctions/auction.rs::AuctionData` is a `#[contracttype]`
struct with named fields, so soroban-sdk emits it as `ScvMap` with
sorted-by-symbol keys:

```text
{
  "bid":   Map<Address, i128>,  // assets the filler spends
  "block": u32,                 // auction-start block
  "lot":   Map<Address, i128>,  // assets the filler receives
}
```

Decoder extracts by name, not position — resilient to field
reordering (per `docs/architecture/contract-schema-evolution.md`).

### Auction types

`pool/src/auctions/auction.rs` defines three discriminants:

| `auction_type` | Name | Bid asset | Lot asset |
| --- | --- | --- | --- |
| `0` | UserLiquidation | dTokens | bTokens |
| `1` | BadDebt | dTokens | Underlying (backstop) |
| `2` | Interest | Underlying (backstop) | Underlying |

Decoder rejects values outside this set with `ErrUnknownAuctionType`
— a contract upgrade introducing a new auction type surfaces as a
fail-loud audit signal rather than silently routing to the wrong
handler.

## i128 handling

Every amount in Blend events is `i128`. We honor the i128-never-
truncates invariant (CLAUDE.md): amounts surface as `*big.Int`
inside `AuctionData.Bid[i].Amount` / `Lot[i].Amount` and as
`*big.Int` for `FillAuctionEvent.FillPercent`.

## Testing

Unit tests in `decode_test.go` cover:

- `classify()` — every auction-event topic + non-Blend topic
- `decodeNewAuction` happy path + unknown-auction-type rejection
- `decodeFillAuction` happy path
- `decodeDeleteAuction` happy path (empty body)
- `Decoder.Matches` — only auction events match (money-market /
  factory events explicitly excluded)
- `Decoder.Name()` + per-event `EventKind()` / `Source()`

Real-mainnet fixtures land alongside the WASM audit (Task #45)
when we have a captured `new_auction` / `fill_auction` payload to
golden against.

## Failure modes

The decoder is fail-loud per event:

1. **Topic-arity drift** — auction events all use 3 topics; missing
   or extra topics return `ErrMalformedPayload`.
2. **Body-shape drift** — `new_auction` / `fill_auction` bodies are
   tuples (Vec); `delete_auction` is `()`. A shape change errors.
3. **AuctionData field rename** — `bid` / `lot` / `block` are
   looked up by name; a rename returns `auction_data missing "bid"`.
4. **Unknown auction_type** — surfaces `ErrUnknownAuctionType`,
   prompting an audit rather than a silent skip.
5. **i128 type drift** — `scval.AsAmountFromI128` is strict; any
   type-tag change errors out.

## References

- Protocol verification page: [`docs/protocols/blend.md`](../../../docs/protocols/blend.md)
- Schema-evolution stance: [`docs/architecture/contract-schema-evolution.md`](../../../docs/architecture/contract-schema-evolution.md)
- Upstream contracts: <https://github.com/blend-capital/blend-contracts-v2>
- Local source-of-truth checkout: `.discovery-repos/blend-contracts/`
