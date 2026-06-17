# External connectors

Off-chain data sources that run **parallel to** the on-chain
dispatcher, not under it. Each venue speaks HTTPS / WebSocket to
a vendor API on its own goroutine and emits the same
`canonical.Trade` / `canonical.OracleUpdate` types the on-chain
decoders produce. The aggregator treats both arrival paths
uniformly via `external.Registry`'s `Class` metadata.

For the framework's interfaces (`Streamer`, `Poller`,
`Backfiller`) and the class semantics, see
[`framework.go`](framework.go) +
[`registry.go`](registry.go).

## Venue catalogue

Three classes, one role per venue. Class drives whether a venue's
output is folded into VWAP or kept alongside as a divergence /
sanity signal — see the
[aggregation plan](../../../docs/architecture/aggregation-plan.md).

### `ClassExchange` — contribute to VWAP

Real executed trades from spot venues + interbank-grade FX
references. The aggregator's class filter passes these through
by default.

| Connector | Type | Role | Auth | Notes |
| --- | --- | --- | --- | --- |
| [`binance`](binance/) | Streamer (WS aggTrade) | Highest-liquidity XLM fiat + crypto pairs | None | `@aggTrade` (not `@trade`) — same-millisecond same-price fills merged for ~5–10× lower throughput, lossless for VWAP |
| [`kraken`](kraken/) | Streamer (WS v2 trade) | Strongest XLM fiat coverage (USD/EUR/GBP/AUD/CAD/CHF) | None | One-call subscription with array of channels; floats arrive as JSON numbers — decoder uses `json.Number` to bypass float64 on the price path |
| [`bitstamp`](bitstamp/) | Streamer (WS live_trades) | EUR/GBP depth alongside Kraken; European retail liquidity profile | None | One-subscribe-per-channel; uses `price_str` / `amount_str` (preserves vendor precision); periodic server-initiated reconnect |
| [`coinbase`](coinbase/) | Streamer (WS matches) | US price discovery for XLM/USD | None for matches | Targets the **Exchange** API (ex-Pro), not Coinbase Advanced Trade |
| [`exchangeratesapi`](exchangeratesapi/) | Poller (REST, 5-min cadence) | Triangulation source: XLM/USD × USD/EUR = XLM/EUR | API key | Authoritative first-party FX computation (interbank + ECB blend) — `ClassExchange`, not aggregator. Free tier (EUR base, hourly) unusable for prod |
| [`polygonforex`](polygonforex/) | Poller (REST snapshot) | Top-tier FX reference; "authority that will not make mistakes" | API key | Aggregates interbank/institutional feeds (OANDA among them); Advanced tier ($199/mo) required for the snapshot endpoint we depend on |

### `ClassAggregator` — divergence signal only

Third-party services that publish already-aggregated prices
across many markets. Mixing into our VWAP would double-count the
upstream venues they derive from, so the class filter excludes
them by default. Useful for the future divergence-detection
layer.

| Connector | Type | Tier needed | Notes |
| --- | --- | --- | --- |
| [`coingecko`](coingecko/) | Poller (REST `/simple/price`) | Free | One batched call covers every (asset, quote) combo; ~10–30 req/min limit |
| [`coinmarketcap`](coinmarketcap/) | Poller (REST `/v2 quotes`) | Standard ($79/mo) | Lower tiers prohibit redistribution — $79/mo is the minimum for production |
| [`cryptocompare`](cryptocompare/) | Poller (REST `/data/pricemulti`) | Free works; ~$80/mo lifts redistribution restriction | Simplest aggregator wire shape — flat asset→currency→price map |

### `ClassAuthoritySanity` — daily anchor only

Sovereign / central-bank reference rates. Cadence too slow to
aggregate, but authoritative as a sanity check on intraday VWAP
drift.

| Connector | Type | Cadence | Auth | Role |
| --- | --- | --- | --- | --- |
| [`ecb`](ecb/) | Poller (REST XML) | One TARGET business day | None | EU's official daily fix; alert if our EUR/USD VWAP closes > 50 bps from ECB's daily fix |

## Operator concerns

### Class is registry-controlled, not connector-controlled

Each connector reports its own `Class()` for runtime checks,
but the source of truth is
[`registry.go`](registry.go) — the same map the aggregator's
class filter consults. A venue's `Class` and `Paid` are facts
about the venue (not per-deployment); operators override
`IncludeInVWAP` and `DefaultWeight` via config when they need
to (rare).

### Why FX feeds are `ClassExchange`, not `ClassAggregator`

ExchangeRatesApi and Polygon Forex compute their rates from
interbank / official-blend inputs. They're a first-party
authority on what FX rates *are* in the same way Binance is on
XLM/USDT — not a third-party aggregation across venues we'd
otherwise sample directly. So they're class-exchange and
contribute to fiat-pair VWAP.

### Per-venue pair lists

Hardcoded inside each venue package's `pairs.go` (where it
exists). A future PR exposes per-venue pair override via TOML
once the fleet stabilises; deferred to keep the config surface
narrow until operators actually ask for it.

### Decode-error budgets

Every external connector contributes to the same
`stellarindex_source_decode_errors_total{source="<venue>"}`
counter family as the on-chain decoders. The
[`decode-errors`](../../../docs/operations/runbooks/decode-errors.md)
runbook covers the response. Sustained > 1/s sustained 5 min
across any single source pages P3.

### Amount-decimal normalisation

External feeds emit prices at vendor-native precision
(Binance uses 10^8, Kraken's WS v2 uses up to 10^10, ECB at 10^4
…). The decoder in each package normalises to a shared 10^8
integer scale on ingest — the aggregator can then mix on-chain
trades (per-asset decimals via `canonical.Asset.Decimals()`)
and external trades (uniform 10^8) by reading
`external.Lookup(trade.Source).Class` to know which side of the
boundary a trade came from.

## Adding a new external connector

The five-file convention from on-chain sources translates 1:1:

| File | Purpose |
| --- | --- |
| `events.go` | Topic / function-name constants, error sentinels, `Connector` impl |
| `<streamer / poller / backfiller>.go` | The actual transport — `Start` / `PollOnce` / `Backfill` body |
| `parse.go` (or inlined) | Wire-format → `canonical.Trade` / `OracleUpdate` |
| `pairs.go` (when needed) | Vendor-symbol ↔ canonical-pair mapping |
| `*_test.go` | Decoder unit tests; backfill tests against captured fixtures |

Plus, in this package's root:

1. Register the venue in [`registry.go`](registry.go) with
   `Class` + `IncludeInVWAP` + `Paid` + `BackfillAvailable`.
2. Add a TOML toggle in
   [`internal/config/config.go`](../../config/config.go)
   (`ExternalConfig` struct).
3. Wire it in `cmd/stellarindex-indexer/main.go` (or wherever
   external connectors are launched in the deployment).
4. Add an ADR if the venue has unusual constraints (paid tier,
   redistribution limits, cadence ceilings).

## References

- [`framework.go`](framework.go) — `Streamer` / `Poller` /
  `Backfiller` interfaces; `Class` constants
- [`registry.go`](registry.go) — single source of truth for
  per-venue class + IncludeInVWAP
- [`docs/architecture/aggregation-plan.md`](../../../docs/architecture/aggregation-plan.md)
  — how class metadata drives the orchestrator's filter chain
- API: [`GET /v1/sources`](../../../internal/api/v1/sources.go)
  surfaces this catalogue to consumers
