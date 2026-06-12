# Blend (lending protocol)

**Status:** ⚠️ Secondary validation source. **Not a spot trading venue.**
We index Blend for price *signals* (auctions, oracle consumption) and
for supply-side metrics, not for direct VWAP/TWAP contribution.

**Contracts repo:** <https://github.com/blend-capital/blend-contracts-v2>
**Verified against:** `pool/src/events.rs`, `pool-factory/src/events.rs`
at clone time (2026-04-22).

## What Blend is

A Soroban-native lending protocol. Users supply collateral, borrow
against it, and pay interest. It is **not** an AMM — there's no
swapping between assets within Blend itself. Prices come in to Blend
from external oracles (Reflector, mostly).

Blend is in our architecture because:

1. **Liquidation auctions** expose directional price signals during
   stress (collateral is sold at a discount to cover bad debt).
2. **Oracle consumption** — we can cross-validate that our aggregated
   prices match what Blend protocol uses on-chain (same Reflector
   feed we ingest directly).
3. **API compatibility** — our API exposes SEP-40-compatible endpoints
   that Blend (or any borrower protocol) could consume.
4. **Supply-side metrics** — how much of each asset is deposited /
   borrowed across pools. Interesting for portfolio tools that
   consume our API.

## Architecture

From the repo layout:

```
pool/              — the core Blend pool contract (one per asset group)
pool-factory/      — deploys new pools
backstop/          — backstop (insurance) module with BLND+USDC pool shares
emitter/           — emission / reward distribution
comet.wasm         — vendored Comet AMM (used for backstop LP)
```

V2 is the current production version.

## Mainnet addresses (verified 2026-04-22 via stellar.expert API)

> **Multiple pool factories (added 2026-06-12, ADR-0035).** Blend has
> MORE THAN ONE pool factory on mainnet — the Phase-1 audit recorded only
> the V2 factory below, but an **earlier factory** also deployed pools
> that are still live. Empirically verified from the r1 lake: decoding
> every `deploy` event and keeping the emitters whose body is an
> `ScVal::Address` (the blend deploy shape) yields exactly two factories,
> which together deploy 27 pools covering all 9 known auction-emitting
> pools. The contract-identity gate (ADR-0035) MUST include both, or the
> earlier factory's pools are silently dropped. Re-run the deploy-graph
> enumeration if a third factory appears. The complete set lives in
> `internal/sources/blend.MainnetPoolFactories`.

### Pool Factory V1 (earlier — lake-verified 2026-06-12)

```
CCZD6ESMOGMPWH2KRO4O7RGTAPGTUPFWFQBELQSS7ZUK63V3TZWETGAG
```

- First `deploy` at ledger `51,499,915`; 17 pools deployed (range
  51.5M–55.8M).
- Deploys 4 of the 9 known auction-emitting pools (CDVQVKOY, CBP7NO6F,
  CDE65QK2, CAQF5KNO) — exactly the pools the V2-only gate dropped.
- Emits only `deploy` (pure factory signature), body = pool `Address`.

### Pool Factory V2

```
CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU
```

- Deployed 2025-04-14 (Unix `1744652527`).
- WASM hash `31328050548831f63d2b72e37bcfd0bb7371b7907135755dbe09ed434d755ca9`.
- Creator `GAQUUKCP33FWX4CP33XLUA3UKYSVXAN7XVESRSJFECU7DMCUANGXKGLZ`.
- Validation: **verified** against
  `blend-capital/blend-contracts-v2`, package `pool-factory`,
  commit `c19abee5b9be4f49e0cda9057e87d343e5dcc095`.
- First `deploy` at ledger `56,615,475`; 10 pools deployed (range
  56.6M–62.9M).

### Backstop V2

```
CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7
```

- Deployed 2025-04-14 (same day, seconds later).
- WASM hash `c1f4502a757e25c611f5a159bc1ab0eef64085adac6c68123dca66e87faffbc2`.
- Same creator as Pool Factory.
- Validation: **verified** against same repo/commit, package
  `backstop`.
- **Heavy production usage**: 14,619 subinvocations, **43,948
  events**, 1,165 storage entries at audit time.

## Pool-factory events

Only one:

```
topics: [Symbol "deploy"]
body:   pool_address: Address
```

Emitted every time a new pool is deployed. We enumerate existing
pools by walking these events backward from the factory's deploy
ledger.

## Pool events (full surface)

Verified from `pool/src/events.rs`. All amounts are `i128`.

### Money-market events (position changes)

| Topics                                         | Body                                       | Signal                                      |
| ---------------------------------------------- | ------------------------------------------ | ------------------------------------------- |
| `["supply", asset, from]`                      | `(tokens_in: i128, b_tokens_minted: i128)` | User deposits tokens (non-collateral).      |
| `["withdraw", asset, from]`                    | `(tokens_out: i128, b_tokens_burnt: i128)` | User withdraws tokens.                      |
| `["supply_collateral", asset, from]`           | `(tokens_in: i128, b_tokens_minted: i128)` | User deposits as collateral.                |
| `["withdraw_collateral", asset, from]`         | `(tokens_out: i128, b_tokens_burnt: i128)` | User withdraws collateral.                  |
| `["borrow", asset, from]`                      | `(tokens_out: i128, d_tokens_minted: i128)`| User draws a loan.                          |
| `["repay", asset, from]`                       | `(tokens_in: i128, d_tokens_burnt: i128)`  | User repays a loan.                         |
| `["flash_loan", asset, from, contract]`        | `(tokens_out: i128, d_tokens_minted: i128)`| Atomic borrow.                              |
| `["gulp", asset]`                              | `(token_delta: i128)`                      | Protocol sweeps fee tokens.                 |
| `["claim", from]`                              | `(reserve_token_ids: Vec<u32>, amount_claimed: i128)` | Rewards claim.                    |

### Credit risk / accounting events

| Topics                                     | Body                        | Signal                                       |
| ------------------------------------------ | --------------------------- | -------------------------------------------- |
| `["bad_debt", user, asset]`                | `d_tokens: i128`            | Bad debt recorded against user.              |
| `["defaulted_debt", asset]`                | `d_tokens_burnt: i128`      | Full default — debt burned.                  |
| `["reserve_emission_update"]`              | `(res_token_id: u32, eps: u64, expiration: u64)` | Emissions rate changed.       |
| `["gulp_emissions"]`                       | `emissions: i128`           | Emissions collected.                         |

### Auction events — **primary pricing signals for us**

```
topics: [Symbol "new_auction", auction_type: u32, user: Address]
body:   (percent: u32, auction_data: AuctionData)
```

```
topics: [Symbol "fill_auction", auction_type: u32, user: Address]
body:   (filler: Address, fill_percent: i128, filled_auction_data: AuctionData)
```

```
topics: [Symbol "delete_auction", auction_type: u32, user: Address]
body:   ()
```

**Why we care:** when Blend triggers a liquidation (`new_auction`),
the protocol believes the collateral is worth less than the debt. As
the auction fills, we observe the actual price at which the market
clears. This is a *stressed* price — it's below fair value by design
(to give the liquidator a profit) — but it's a real trade signal.

We **do not** feed auction prices into VWAP directly. Instead we log
them as reference points and surface them on the asset's detail
endpoint as "recent stress-price observations" — useful for risk-
aware consumers but not for the normal quote.

### Admin / status events

| Topics                                                 | Body                                       |
| ------------------------------------------------------ | ------------------------------------------ |
| `["set_admin", admin]`                                 | `new_admin: Address`                       |
| `["update_pool", admin]`                               | `(backstop_take_rate, max_positions, min_collateral)` |
| `["queue_set_reserve", admin]`                         | `(asset, ReserveMetadata)`                 |
| `["cancel_set_reserve", admin]`                        | `asset: Address`                           |
| `["set_reserve"]`                                      | `(asset, index: u32)`                      |
| `["set_status"]`                                       | `new_status: u32` (non-admin trigger)      |
| `["set_status", admin]`                                | `pool_status: u32` (admin trigger)         |

`set_status` is meaningful for our degraded-source detection: if a
pool goes into an admin-frozen state, we drop any of its signals
from our supplementary outputs.

## i128 handling

Every amount in Blend events is `i128`. [i128 invariant](../decisions.md)
applies. Same pattern as Soroswap / Aquarius.

## What we extract

1. **Pool enumeration**: walk `PoolFactory:deploy` events → seed
   `blend_pools` table (`pool_address, deployed_at_ledger`).
2. **Per-pool subscription**: for every deployed pool, subscribe to
   all events. Primary interest: `new_auction`, `fill_auction`,
   `bad_debt`, `defaulted_debt`, `set_status`.
3. **Supply / borrow tallies**: roll `supply`, `withdraw`,
   `supply_collateral`, `withdraw_collateral`, `borrow`, `repay`,
   `flash_loan` into net per-asset positions per pool. Exposed as
   "total supplied / total borrowed" metadata on the asset detail
   endpoint.
4. **Auction observations**: store `new_auction` → `fill_auction`
   pairs, compute per-claim effective price, tag as "stress price"
   in our DB.

## Oracle consumption

Our proposal claims to "monitor Blend's oracle price consumption via
Reflector to cross-validate that our aggregated prices are consistent
with what the protocol is using." Verifying this requires:

- Finding which Reflector oracle contract Blend reads from per pool.
- Comparing the price Blend uses in a given auction against the
  Reflector price at the same ledger.
- Alerting when divergence > threshold.

**Open item:** we need to read Blend's pool config to find the
oracle address. Not captured here — check `pool/src/storage.rs`
or their deploy artefacts.

## SEP-40 output compatibility

Our proposal states: "ensuring our API output is compatible with
Blend's SEP-40 oracle interface requirements." Since we verified
SEP-40's exact shape in [../oracles/reflector.md](../oracles/reflector.md),
this reduces to:

- Expose a SEP-40 view wrapper on top of our REST API (optional).
- Or publish a bridge contract on Soroban that relays our prices
  SEP-40-shaped on-chain (post-Phase 1).

We **don't need to become a SEP-40 oracle ourselves** to pass the
RFP. But we plan for it.

## What we do **NOT** extract

- **No trade records** from Blend events. Supply/borrow/repay aren't
  trades.
- **No spot pricing** from any Blend event. The only price-like
  signal is an auction fill, which is stressed.
- **No liquidity-pool math** (Blend uses b_tokens / d_tokens, not
  LP shares from an AMM).

## Open items

- [ ] Cross-verify the mainnet V2 Pool Factory and Backstop addresses
      against Blend's deploy manifest.
- [ ] Read `pool/src/storage.rs` to see how the oracle address is
      stored per pool; extract the Reflector oracle used by each live
      pool.
- [ ] Audit AuctionData struct — what exactly is inside the
      `filled_auction_data` body? That determines how we compute
      effective liquidation price.
- [ ] Decide retention policy: auction events are low-volume, probably
      keep forever; supply/withdraw/borrow/repay events are higher
      volume, consider rollups + short raw-event retention.
- [ ] Monitor for Blend V3 or successor protocol — their lending
      surface may change.

## Related

- [../oracles/reflector.md](../oracles/reflector.md) — Blend's primary
  price source; our cross-validation target.
- [soroswap.md](soroswap.md), [aquarius.md](aquarius.md) — the actual
  spot-trade venues we use for VWAP.
- [../decisions.md](../decisions.md) — i128 invariant.
