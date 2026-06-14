# A06 — Sources: external CEX/FX (`internal/sources/external/*`)

Read-only audit, 2026-06-14. Dimensions applied: D1 correctness, D2 ADR
invariants (non-uniform amount scaling; source-class VWAP contribution),
D3 security (vendor-key handling), D4 concurrency, D5 resource/lifecycle.

## Scope — subdirectories enumerated (11 venue packages + framework)

```
internal/sources/external/                (framework.go, registry.go, runner.go + *_test.go)
internal/sources/external/binance/        streamer + backfill (ClassExchange, CEX)
internal/sources/external/kraken/         streamer + backfill (ClassExchange, CEX)
internal/sources/external/bitstamp/       streamer + backfill (ClassExchange, CEX)
internal/sources/external/coinbase/       streamer + backfill (ClassExchange, CEX)
internal/sources/external/coingecko/      poller            (ClassAggregator)
internal/sources/external/coinmarketcap/  poller            (ClassAggregator, paid)
internal/sources/external/cryptocompare/  poller            (ClassAggregator, paid)
internal/sources/external/ecb/            poller            (ClassAuthoritySanity)
internal/sources/external/exchangeratesapi/ poller          (ClassExchange, FX, paid)
internal/sources/external/polygonforex/   poller            (ClassExchange, FX, paid)
internal/sources/external/chainlink/      poller + backfill (ClassOracle, EVM JSON-RPC)
```

## Files read — count: 63

Foundation (3): framework.go, registry.go, runner.go.
Per-venue source (.go, non-test):
binance (5: events, pairs, parse, streamer, backfill);
kraken (5); bitstamp (4: events, pairs, parse, streamer + backfill);
coinbase (5: events, pairs, parse, streamer, backfill);
coingecko (1); coinmarketcap (1); cryptocompare (1);
ecb (1); exchangeratesapi (2: events, poller);
polygonforex (2: events, poller);
chainlink (6: poller, client, decode, events, defaults, backfill).
Cross-refs read for verification: `internal/canonical/oracle.go` (OracleUpdate
+ Validate), `internal/aggregate/global.go` (per-source `Decimals` consume
path), config struct + defaults (`internal/config/config.go`), binary wiring
(`cmd/stellarindex-indexer/main.go`, `cmd/stellarindex-ops/main.go`). Test
files surveyed for coverage (counts below), not line-audited.

Test-function counts (per package): binance 30, kraken 38, bitstamp 41,
coinbase 42, coingecko 21, coinmarketcap 10, cryptocompare 9, ecb 14,
exchangeratesapi 12, polygonforex 14, chainlink 11, top-level external 18.
`go build ./internal/sources/external/...` passes.

## Severity summary

| Severity | Count |
|----------|-------|
| Critical | 0 |
| High     | 0 |
| Medium   | 0 |
| Low      | 4 |
| Info     | 5 |

No Critical/High/Medium findings. The non-uniform-decimals invariant
(D2), source-class VWAP gating (D2), vendor-key handling (D3), per-venue
goroutine lifecycle + WS reconnect (D4), and poller-wedge bug class (D5)
are all correctly handled, with prior audit findings (F-0029, F-1235,
F-1237, F-1323/G10-01, G10-02, G10-04, F-0030) verifiably fixed in place.

## Findings

| # | Sev | Dim | File:Line | Issue |
|---|-----|-----|-----------|-------|
| A06-1 | Low | D1/metric | binance/streamer.go:258-268 | Dust trades + unknown-symbol drops bump `SourceDecodeErrorsTotal{binance}`, conflating benign drops with schema drift. Coinbase + bitstamp special-case dust to a clean skip; kraken swallows per-entry inside `parseTradeFrame`. Binance is the lone inconsistent one. |
| A06-2 | Low | D4 | runner.go:123-127, 202 | Poller goroutines run under the parent `ctx`, while streamer goroutines run under the derived `streamerCtx`. `wait()` only `wg.Wait()`s then cancels `streamerCtx`; pollers stop only when the *parent* ctx cancels. Benign today (parent ctx is the indexer-shutdown signal and `wg` covers pollers), but the asymmetry means a `cancelStreamers()`-only teardown would not stop pollers — fragile if a future error path relies on it. |
| A06-3 | Low | D1/precision | polygonforex/poller.go:247-267 | `midPriceString` scales ask/bid to 6dp ints, averages, then re-formats to a decimal string via `intToDecimalString`, which `decimalStringToScaledInt` re-parses — a double 6dp round-trip before the final inversion. For FX majors this is below the noise floor, but it is a needless precision-erosion vs computing the mid directly in scaled-int space. |
| A06-4 | Low | D1/precision | exchangeratesapi/poller.go:226-233; polygonforex:200-207; ecb:235-241 | FX inversion `(10^(2·6))/rate` is exact only to 6dp. For a large venue rate (e.g. JPY: 1 USD≈150 JPY → inverted 0.006667) the 6dp floor is fine, but the inversion truncates (`big.Int.Div`, floor) rather than rounds — a consistent downward bias of <1e-6 on every inverted FX quote. Immaterial to VWAP but worth noting for an exact-rate consumer. |
| A06-5 | Info | D2 | (all FX/aggregator/CEX) | Non-uniform amount scaling VERIFIED correct: CEX + reference-aggregator + chainlink stamp 10^8 (`externalAmountDecimals`=8 / `DefaultDecimals uint8 = 8`); FX pollers (ecb/exchangeratesapi/polygonforex) stamp `DefaultDecimals uint8 = 6` on every `OracleUpdate.Decimals`. Consumer (`aggregate/global.go::averageAggregatorPrices`, commonDecimals=14) reads per-source `u.Decimals` and scales up — no hardcoded 10^8 assumption anywhere in the consume path. |
| A06-6 | Info | D3 | chainlink/client.go:176-183; exchangeratesapi/poller.go:146-151; coingecko:256-265; cmc:199; cryptocompare:148; polygonforex:99-103 | Vendor-key handling VERIFIED: chainlink RPC key (in URL path) + exchangeratesapi key (query param, unavoidable per vendor) are redacted from `*url.Error` before logging (G10-04, `redactURLError`/`redactEndpoint`/`redactQuery`); CoinGecko/CMC/CryptoCompare/Polygon pass keys in HEADERS not query strings. No key appears in any log/error path. All venues HTTPS/WSS. |
| A06-7 | Info | D2 | registry.go:31-112 | Source-class VWAP gating VERIFIED: only `ClassExchange` carries `IncludeInVWAP:true` (the 4 CEX + 2 FX + on-chain DEX); aggregators/oracles/lending/router/bridge are `IncludeInVWAP:false`. `Lookup` fail-closes unknown sources to `IncludeInVWAP:false, BackfillSafe:false`. |
| A06-8 | Info | D5 | chainlink/poller.go + decode.go + events.go | Poller-wedge bug class VERIFIED fixed: (a) phase-rollover wedge (F-1323/G10-01) — dedup keys on the FULL uint80 proxy roundId held as `*big.Int`, so an aggregator phase upgrade (aggregatorRoundId→1, phaseId++) still strictly increases; (b) all-feeds-failed liveness (G10-02) — `PollOnce` returns `firstErr` when zero updates AND ≥1 feed errored, so the staleness gauge no longer reads green on a wedged poller; (c) CoinGecko 429 self-wedge (F-0030 + applyBackoff) — backoff floors at MinBackoff, grows past an undersize Retry-After, default interval bumped 60s→300s. |
| A06-9 | Info | D4 | binance/kraken/bitstamp/coinbase streamer.go | WS lifecycle VERIFIED: bounded exponential backoff + ±25% jitter; healthy-connection backoff reset (F-0029, `healthyConnectionThreshold`=5m); TCP keepalive dialer; `defer close(out)` on ctx-cancel; per-frame parse errors metered + skipped, never tear down the stream; `external.Run` cancels already-launched streamers + drains their channels on a later Start error (G10-11, no goroutine leak). |

## D1 — correctness (per-venue parse → canonical.Trade / OracleUpdate)

All four CEX streamers decode vendor JSON into `canonical.Trade` with the
same shape: parse base + price as decimal strings → scaled 10^8 `big.Int`,
compute `quote = base × price / 10^8`, dust-filter `quote.Sign()==0`,
synthesize a 64-hex `TxHash` from `(symbol, trade_id)`, `Ledger=0`,
`OpIndex=0`. No float in any price path (ADR-0003 honoured even where the
vendor sends JSON floats — kraken uses `json.Number`+`UseNumber()`,
coinbase round-trips through `FormatFloat` before integer math). Notable
correct handling:

- **Coinbase candle field order**: backfill explicitly reads the LHOC
  positional layout `[time, low, high, open, close, volume]` (not OHLC) and
  walks the reverse-chronological response backwards — a classic
  silent-wrong-price trap, handled correctly with index comments.
- **Kraken VWAP fallback**: backfill uses the candle's own VWAP field for
  price, falling back to close on a zero-VWAP (zero-volume) bucket.
- **Bitstamp microtimestamp**: prefers µs field, falls back to seconds.
- **Binance quote scale**: `base×price` at 10^16 correctly divided back to
  10^8 (the one place a naive impl would double-scale).
- **Chainlink int256 / uint80**: two's-complement decode, full-width
  roundId, panic-guard on the 8-byte `bigEndianUint64` boundary.

Symbol/pair maps are hardcoded per venue with vendor-specific formats
(binance `XLMUSDT`, kraken `XLM/USD`, bitstamp `xlmusd`, coinbase
`XLM-USD`, polygon `C:USDEUR`) and the streamers reject any pair not in
the configured `PairMap` before subscribing — no blind subscriptions.
`Start` rejects an empty pair list (auto-enumeration not implemented).

The `external.Connector` sub-interface split (Streamer / Poller /
Backfiller) is clean: CEX = Streamer+Backfiller, FX/aggregator/sovereign =
Poller, chainlink = Poller(+a non-interface `Backfill` with its own
signature). All concrete types satisfy `Connector` (Name+Class) with
compile-time `var _ = external.Class*` assertions.

## D2 — ADR invariants

- **Non-uniform amount scaling (the headline)**: confirmed correct and
  read-not-assumed end to end (A06-5). i128-never-int64 (ADR-0003)
  honoured — all amounts/prices are `*big.Int`; chainlink even keeps the
  uint80 roundId as `*big.Int` to avoid a 64-bit truncation that was the
  root of the phase-wedge bug.
- **Source-class VWAP gating** (A06-7): correct; fail-closed on unknown
  sources.
- **FX `Class=ClassExchange`** (exchangeratesapi/polygonforex): a
  deliberate choice documented in each package — "authoritative
  first-party computation from interbank feeds, not a third-party
  aggregation," so they DO contribute to VWAP for fiat pairs. Consistent
  with `registry.go` and CLAUDE.md.

## D3 — security

No secrets in code (keys come from config/env). Vendor-key redaction
verified (A06-6). All endpoints HTTPS/WSS. Response bodies are
`io.LimitReader`-bounded (1–20 MB per venue). Error-page snippets are
length-clamped (and CoinGecko's is UTF-8-boundary-safe) so a hostile/large
vendor error page can't flood the journal. No SQL here (sources only emit
canonical structs). No SSRF surface (endpoints are operator-configured
constants, not request-derived).

## D4 — concurrency / lifecycle

`external.Run` is the single fan-out point: one goroutine per streamer
(reconnect-forever) + one per poller (ticker). Streamer goroutines are
bound to a derived cancellable `streamerCtx` with leak-safe teardown on a
late Start error (G10-11). The only blemish is the poller-vs-streamer ctx
asymmetry (A06-2) — benign today. Per-venue WS loops verified leak-free
and reconnect-correct (A06-9). The CoinGecko poller's cooldown mutex
guards `nextAllowedAt`/`currentBackoff`; the chainlink `roundCache` mutex
guards the per-feed high-water map and stores a *copy* of the `*big.Int`
to prevent caller mutation. Chainlink `PollOnce` fans out feed eth_calls
under a bounded `Concurrency` semaphore (default 8).

## D5 — resource / lifecycle

Poller intervals are sane and self-documented: CEX stream (no poll); FX
60s; aggregators 60s (CG 300s after F-0030); ECB 6h; chainlink 30s. All
HTTP clients carry 30s timeouts. Backfill bounds: binance paginates 1000
candles/req and stops on a short page; kraken honours the 720-interval cap
+ `last` cursor with no-progress guard; coinbase 300-candle pages with
empty-window advance (no infinite loop on illiquid ranges); chainlink
walks `eth_getLogs` in 5k-block chunks with per-chunk error tolerance,
a head-block probe, and ctx-cancel checks. No unbounded scans. The
poller-wedge bug class is the one to watch and it is comprehensively
fixed (A06-8).

## CORRECT list (verified-good, no action)

1. Non-uniform decimals: CEX/aggregator/chainlink = 8, FX = 6, stamped per
   source on `OracleUpdate.Decimals` and read per-source by the aggregator
   (no 10^8 assumption in consumers). (A06-5)
2. Source-class VWAP gating + fail-closed `Lookup` fallback. (A06-7)
3. Vendor-key redaction across all keyed venues (chainlink RPC key,
   exchangeratesapi query key, header-based keys for CG/CMC/CC/Polygon).
   (A06-6)
4. Chainlink phase-rollover wedge fixed via full-uint80 `*big.Int` roundId
   dedup. (A06-8a)
5. Chainlink all-feeds-failed liveness fix — wedged poller no longer reads
   healthy. (A06-8b)
6. CoinGecko 429 self-wedge fixed (backoff floor + growth + 300s default).
   (A06-8c)
7. WS reconnect: bounded backoff + jitter + healthy-connection reset
   (F-0029) + keepalive dialer, across all 4 CEX. (A06-9)
8. `external.Run` leak-safe streamer teardown on late Start error
   (G10-11). (D4)
9. Decode-error metering on parse failures (F-1235) wired in all 4 CEX
   streamers. (D1)
10. CMC ambiguous-ticker fix — query by numeric id, resolve response by
    `coin.Symbol` (F-1237). (D1)
11. No float in any price path; all amounts/prices `*big.Int` (ADR-0003).
12. Coinbase LHOC candle field-order trap handled explicitly. (D1)
13. i128-faithful inversion math for FX + chainlink (big.Int, no downcast).
14. `OracleUpdate.Validate` enforced shape (positive price, ≤38 decimals,
    valid tx_hash/asset/quote) — FX@6dp + chainlink@8dp both pass.
15. Strong, behaviour-level test coverage (242 venue test funcs + 18
    framework) incl. reconnect, backoff, dust, decode-error, and
    candle-field-order regression tests.
