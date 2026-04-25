# Redstone connector

Ingests on-chain oracle updates from
[RedStone](https://app.redstone.finance) — one Soroban Adapter
contract owns price storage for every feed; thin per-feed proxies
delegate reads to the Adapter. Primary Phase-1 reference:
[`docs/discovery/oracles/redstone.md`](../../../docs/discovery/oracles/redstone.md).

## What this ingests

RedStone publishes a single `("REDSTONE",)` event each time the
relayer pushes a batch update. Topic-0 is `Symbol("REDSTONE")`;
the body carries the new prices for every feed in that batch.

| Field | Where it appears | Decoded as |
| --- | --- | --- |
| `updater` | Body Map | `Address` (relayer identity, ignored for VWAP — kept for audit) |
| `updated_feeds` | Body Map → `Vec<PriceData>` | One row per feed updated this batch |
| `price` (per feed) | `PriceData.price` | `U256` at fixed `DECIMALS = 8` |
| `package_timestamp` / `write_timestamp` | `PriceData` | `u64` Unix seconds |
| **`feed_ids`** | **InvokeContract op args** (NOT the event body) | `Vec<String>` — see Q1 below |

The decoder emits one `canonical.OracleUpdate` per `(feed_id, price)`
pair in the batch, with synthetic `op_index` values spaced by 1024
so each feed in a batch keeps a unique identity in the
`oracle_updates` table.

Mainnet address (Phase-1 verified):

| Contract | Address |
| --- | --- |
| Adapter | `CA526Y2NQWGWVVQ7RFFPGAZMU66PSYJ3UC2MTVAV4ZU7OM5BOPHDXUSG` |

19 thin per-feed proxy contracts exist but are not subscribed to
— they emit no events and only serve `price()` reads. Confirmed
2026-04-23 via stellar.expert's contract API.

## Quirks

### Q1 — `feed_ids` aren't in the event body

The relayer calls
`adapter.write_prices(updater, feed_ids: Vec<String>, payload)`
on-chain. The contract emits its event WITHOUT the `feed_ids`
list — the body has prices + timestamps but no asset
identifiers. The decoder reads `feed_ids` from
`events.Event.OpArgs` (populated by `internal/dispatcher` from
the InvokeContract op envelope) and zips one-to-one against
`updated_feeds`.

**Length must match.** When the adapter's freshness verifier
rejects a feed, the entry skips in `updated_feeds` without
skipping in `feed_ids`, breaking the zip. The decoder treats a
length mismatch as `ErrFeedIDCountMismatch` and skips the whole
event rather than attributing prices to the wrong assets — see
[`docs/discovery/oracles/redstone.md`](../../../docs/discovery/oracles/redstone.md)
for the full analysis. Logged + counted under
`ratesengine_source_decode_errors_total{source="redstone"}`.

### Q2 — Event body is wrapped in `ScVal::Bytes`

The Rust adapter does
`self.to_xdr(env).to_val()`, which produces an `ScVal::Bytes`
holding XDR-serialised body bytes — NOT the `ScVal::Map` you'd
expect a structured event body to be. The decoder type-tests +
unwraps the inner XDR before destructuring into the Map shape.
Confirmed against real mainnet event
`349bd590c679a9d69ac0ff3eb49a673f95cf9d77016fc3d019eb654c772c7a8b`
in the regression fixture.

### Q3 — Feed-ID modelling: market rates only, NAV-priced RWAs skipped

Every feed publishes at `DECIMALS = 8`, so price-scale handling
is uniform. The asset modelling is not.

`feedIDToCanonicalAsset` delegates to
`canonical.IsKnownCrypto` (the ADR-0014 allow-list). RedStone's
naming convention matches our global-ticker form (`BTC` is `BTC`,
not `btc-usd`), so anything on the allow-list — majors
(BTC/ETH/SOL/…), stablecoins (USDT/USDC/DAI/PYUSD/USDP),
EUR-pegged tokens (EURC/EUROC) — decodes cleanly.

RWA-class feeds (BENJI, GILTS, TESOURO, CETES, …) surface as
`ErrUnknownFeedID` and are skipped at the per-feed level. Their
"price" is a NAV reference, not a market rate, and folding them
into our crypto VWAP would mis-feed the aggregator. Adding RWA
support is a deliberate future ADR — start with proper
canonical-asset modelling that distinguishes NAV from market.

### Q4 — Quote asset is always USD

The covered set is USD-denominated (per the adapter docs +
`app.redstone.finance` UI). Decoder hardcodes
`Asset.QuoteAsset = canonical.NewFiatAsset("USD")` for every
emitted update.

### Q5 — Update cadence: 0.2% deviation OR 24h heartbeat

A feed may go quiet for up to 24 hours if the underlying price
hasn't moved more than 0.2% in either direction. The decoder
publishes
`DefaultResolutionSeconds = 24 * 60 * 60` as the
`ratesengine_oracle_resolution_seconds` gauge so the
`oracle-stale` alert (which fires at `> 10× resolution`) has the
correct threshold for a quiet feed.

### Q6 — `i128` everywhere — but the price is `U256`

Most amount fields across our Soroban event surface are `i128`.
RedStone's price is `U256`. We use `*big.Int` regardless via
`internal/scval` decoding, so the canonical wire form
(`canonical.Amount` → `NUMERIC` in Postgres) handles both
without truncation.

## Files

| File | Role |
| --- | --- |
| [`events.go`](events.go) | Topic / function-name constants, error sentinels |
| [`decode.go`](decode.go) | Pure decode-from-event → `[]canonical.OracleUpdate`; OpArgs zip |
| [`decode_test.go`](decode_test.go) | Decoder unit tests with synthetic event bodies |
| [`consumer.go`](consumer.go) | Dispatcher-side adapter glue |
| [`dispatcher_adapter.go`](dispatcher_adapter.go) | Topic-match registration |

## Operational notes

- **Class**: Oracle (per `external.Registry`) — `IncludeInVWAP=false`
  by default. Visible on `/v1/sources` for transparency, excluded
  from VWAP because RedStone publishes already-aggregated
  derived prices with its own governance and methodology.
- **Backfill**: supported. The Adapter's events are durable
  Soroban events so backfill from a galexie archive works the
  same way live ingest does (subject to the OpArgs availability
  noted under Q1 — `events.Event.OpArgs` is populated for
  backfill ledgers via `internal/dispatcher` PR 166).
- **Decode-error budget**: `ErrFeedIDCountMismatch` should be
  rare. A sustained increase in
  `ratesengine_source_decode_errors_total{source="redstone"}`
  warrants checking the Adapter's freshness verifier behaviour
  — possibly a contract upgrade widening rejection criteria.

## Verdict

Adopting RedStone gives us a second oracle alongside Reflector at
near-zero ongoing cost — one event subscription, one
`(feed_id, price)` zip per batch, fail-closed on mismatch. The
allow-list discipline keeps non-market feeds out until they have
proper asset modelling.

## References

- Discovery: [`docs/discovery/oracles/redstone.md`](../../../docs/discovery/oracles/redstone.md)
- Adapter source: <https://github.com/redstone-finance/redstone-public-contracts>
  (path: `packages/stellar-connector/deployments/stellarMultiFeed/contracts/redstone-adapter/src/event.rs`)
- ADR-0014 — crypto-ticker representation
- Related sources: [`reflector`](../reflector/README.md) (the other
  Soroban-native oracle on pubnet)
