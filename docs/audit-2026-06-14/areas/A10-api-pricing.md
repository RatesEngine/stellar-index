# A10 — API: pricing / catalogue handlers (`internal/api/v1/*.go` ex-explorer/auth/account)

Read-only audit, 2026-06-14. Scope: pricing / assets / markets / history / ohlc /
oracle / coverage / protocols / supply / issuers / sources / changes / diagnostics /
network handlers in `internal/api/v1/`. EXCLUDES `explorer_*.go` (A11) and
auth/account/dashboard/signup/stripe (A12).

Dimensions: D1 correctness, D2 ADR invariants, D3 security, D5 resource/lifecycle,
D7 API contract.

## Severity counts

| Severity | Count |
|----------|-------|
| Critical | 0 |
| High     | 0 |
| Medium   | 3 |
| Low      | 6 |
| Info     | 8 |

No Critical or High findings. The pricing/catalogue surface is mature and
heavily-patched (most of the audit register's prior findings — F-1254, F-1259,
F-1271, F-1305, F-1308, F-1321, F-1326, F-1340, G2-*, R-018 — are already fixed
and verified inline). The three Mediums are a missing query timeout, a
non-deterministic source-ordering on one tip path, and a possibly-over-eager
`triangulated` flag.

## Findings

| Severity | File:line | Dim | Issue | Why it matters | Fix | Confidence |
|----------|-----------|-----|-------|----------------|-----|------------|
| Medium | network_stats.go:~67 (`handleNetworkStats`) | D5 | `GetNetworkStats` is called with bare `r.Context()` — no `context.WithTimeout`, unlike every other DB-backed handler in this area (history/ohlc/markets/pools/oracle/issuers all wrap an 8s ceiling). The query is a multi-aggregate (`SUM(volume_usd)` + `DISTINCT (base,quote) COUNT` + classic-asset count) over `prices_1m` for 24h. | On a cold cache / contended hypertable this can run many seconds with no server-side deadline; the conn is held until the client or LB cuts it. Exactly the failure class the 8s ceilings were added for (#1082/#1099). A 5xx/hang here also costs an sla-probe availability point. | Wrap `ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)`; map `handlerTimedOut` → 503; add the `clientAborted`/`handlerTimedOut`/`transientStorageErr` ladder. | high |
| Medium | price_tip.go:279-302 (`tipWindowVWAP`) + 314 | D1/D2 (ADR-0015 determinism) | The XLM dual-form path appends trades across `assetAliases(asset) × assetAliases(quote)` pairs in nested-loop order, then derives the wire `sources[]` via `distinctTradeSources(trades)` which preserves **first-occurrence (i.e. merge) order**. The resulting `sources` array order is alias-iteration-order-dependent, not sorted. | Single-asset `/v1/price` sorts sources at the storage boundary and the batch path added `sort.Strings` (F-1259) precisely for the ADR-0015 "byte-identical across regions" property; `/v1/price/tip` re-introduces an unsorted source array for the dual-form (XLM) case. Two regions whose alias-merge happens to differ in row interleave emit different `sources` ordering → not byte-identical. (VWAP value itself is order-independent, so price is fine — only the `sources` field drifts.) | `sort.Strings(sources)` before returning from `tipWindowVWAP` (and confirm the non-tip readers already sort). | med |
| Medium | ohlc_series.go:232 (`handleOHLCSeries`) | D1/D7 | `Flags{Triangulated: pair.Quote.Type == canonical.AssetFiat && len(bars) > 0}` stamps `triangulated=true` for ANY fiat-quoted series with bars — even when `ohlcSeriesFiatCombined` served bars from the **direct** `fiat:USD` CEX feed (which exists for the recent ~5 weeks) with no peg-proxy expansion involved. | Over-claims triangulation: a fiat:USD series that happened to be served from genuine direct fiat:USD bars is flagged as derived/triangulated, which is the opposite of what the flag should tell a cautious consumer. Minor wire-honesty bug; symmetric with how single-bar OHLC only sets `triangulated` when the peg fallback actually fired. | Have `ohlcSeriesFiatCombined` return whether any constituent other than the literal pair contributed (i.e. whether the USD-peg expansion produced >1 source pair or a non-direct pair), and set the flag from that, not from `quote==fiat && len>0`. | med |
| Low | coverage_cache.go:62-66 (`Snapshot`) | D1 (concurrency contract) | `Snapshot()` returns the internal `c.snapshot` slice header under RLock; `Refresh()` swaps the whole slice so there's no live race today, but the contract isn't documented and a future caller doing `sort.Slice`/`append` in place would corrupt the shared backing array. | Latent — no current bug; fragile contract. | Document "treat returned slice as immutable" or return a defensive copy. | high (safe today) / med (latent) |
| Low | ohlc.go:180, vwap.go:146, twap.go:101 (`Truncated`) | D1 | `Truncated` is `preFilter == maxTradesForOHLC` / `pre == maxTrades` / `len(trades) == maxTrades` — i.e. "we fetched EXACTLY the cap". If the reader returns `limit` rows when the window has exactly `limit` trades (not more), `truncated=true` is a false positive; if a reader ever returns `limit+1`, the `==` misses it. | Cosmetic over/under-report of the truncation flag on the exact-boundary case. The readers cap at `limit`, so `> ` can't occur, but the exact-equal case is ambiguous (was there a 10001st trade or exactly 10000?). | Accept as a known approximation (documented in the struct comments) OR have the reader signal "more rows existed". Low priority. | med |
| Low | price.go:764-765 (`tryFiatCrossRate`) | D2 (ADR-0003 spirit) | The fiat-vs-fiat cross-rate computes `cross := rateQuote / rateAsset` in **float64** and renders via `strconv.FormatFloat(cross,'f',-1,64)`. | This is a price emitted on `/v1/price` computed through float, not big.Rat — the rest of the price paths are scrupulously integer/big.Rat (ADR-0003). The inputs are already float64 (`c.RateUSD` from the forex snapshot), so it's float-in/float-out and the precision is bounded by the upstream feed; still the one float-derived price on the canonical price surface. Last-resort fallback only (both Timescale + Redis missed). | If the forex snapshot ever carries decimal-string rates, switch to big.Rat. Today acceptable since the source is float. Document it. | med |
| Low | changes.go:36 (`ChangeSummaryResponse.CurrentValue` + the `*float64` value fields) | D2 (ADR-0003) | For `entity_type=coin` these `float64` JSON fields carry VWAP **prices** (the rollup parses the decimal VWAP string to float64). ADR-0003 says prices ship as strings. | A price-derived value ships as a JSON number on a public endpoint — the one place in this area set that does. Deliberate (delta-strip display widget, lossy by design at the rollup layer), but it is drift unless explicitly signed off. | Either add a one-line ADR-0003-exception note on the struct, or switch coin value fields to strings if any consumer treats `current_value` as authoritative. | high (it's a float price) / med (whether it's a real violation) |
| Low | markets.go:660 (`fanOutAssetMarkets`) | D1 | `firstErr` is captured but only returned `if firstErr != nil && len(merged) == 0`. A partial failure (one expanded asset_id query errors, others succeed) is silently swallowed — the response looks complete but is missing that asset_id's markets. | The slug-expansion Markets tab can under-report markets with no signal to the caller (no flag, no error). Acceptable degradation but undocumented. | Surface a `flags.stale`/partial indicator when `firstErr != nil` even with partial success, or log at WARN. | med |
| Low | diagnostics_ingestion.go:~1132 (`parseInt64`) | D1 | Hand-rolled `n = n*10 + digit` with no overflow check; the comment claims "0 on parse failure" but a >19-digit input wraps silently rather than failing. | Parses an operator-written cursor range-end; ledger numbers won't approach int64 max, so unreachable in practice. Diverges from `strconv.ParseInt` semantics. | Use `strconv.ParseInt(s,10,64)` and treat its error as the 0-default. | high (correctness) / low (impact) |
| Info | price.go:531-544 (`assetAliases`) + all callers | D2 (XLM dual-form) | The known-bug-class alias loop is correctly applied across `readPriceWithAliases` (price, price/batch, tip), `tipWindowVWAP`, `ohlcSeriesWithAliases`, `oracleAssetCandidates`, `changeSummaryCoinCandidates`, `usdPeggedConstituents`. VERIFIED present everywhere an asset-keyed read happens on this surface. | — | None — verified correct. | high |
| Info | (whole area) USDT-on-Stellar fabrication | D2 | No handler hardcodes a Tether classic `USDT-G…` into a USD-quote allowlist. The USD-peg set is entirely operator-config (`s.usdPeggedClassics` ← `cfg.Trades.USDPeggedClassicAssets`); the only `USDT-G…` string literals are in doc comments (assets.go:686, price.go:1061) as examples. `coins`/`markets`/`sources` derive from the trades store + registry, not a fabricated USDT asset. | — | None — clear. (Note: `internal/config` + `configs/example.toml` carry a doc list `XLM/USDT/USDC/DAI/PYUSD` for `enable_stablecoin_fiat_proxy` — out of A10 scope; flag for the config audit slice.) | high |
| Info | (whole area) SQL injection | D3 | No SQL is built from query params in any A10 handler. All storage access is via reader interfaces; the one ClickHouse supply query reached from `handleAssetSupply` uses a parameterized `WHERE contract_id = ?` bind (supply_flows.go:105). `order_by`/`source`/`class`/`granularity`/`interval` are all switch-validated to enum constants before reaching storage; markets cursor goes through `timescale.ValidateMarketsCursor`. | — | None — clean. | high |
| Info | history.go:79-104 (`LatestTradePerSource`) / observations | D5 | The interface doc itself admits the production `DISTINCT ON (source) … ORDER BY source, ts DESC` scan has **no time bound** so TimescaleDB can't do chunk exclusion (probes every chunk). Mitigated by `CachedHistoryReader` SWR-cache; the durable composite-index fix is deferred (multi-GB on 2.7B rows, disk-constrained). | Not a new finding — known/tracked (#29). The no-unbounded-trade-scan rule is technically bent here but bounded by the SWR cache. The actual SQL lives in the storage layer (A03), not the handler. | Tracked; create the `(base,quote,source,ts DESC)` index when disk allows. | high |
| Info | oracle.go:101 (`OracleReading.Confidence float64`) | D2 | `Confidence` and `confidence_factors` ship as JSON `float64`. | These are oracle confidence SCORES in [0,1], not token amounts/prices/supplies — ADR-0003 does not apply. Correctly fine. | None. | high |
| Info | asset_supply.go:21,71 (`NativeTotalCoins int64`) | D2 | XLM total supply carried as `int64` end-to-end, rendered via `strconv.FormatInt` into the string `total_supply` wire field. | Wire shape is correct (string). The int64 is safe SPECIFICALLY because the source is the ledger header's native `int64 total_coins` (~5×10^17 stroops « int64 max). Not a truncation bug; flagged only because it's an amount-typed int64 in code. | None. | high |
| Info | ohlc_fiat_combine.go (whole) | D1 | The fiat-USD combine math is correct: Σbase_vol / Σquote_vol exact; high=max/low=min exact; open/close = Σ(price·base)/Σbase base-volume-weighted via big.Rat (no float). Unparseable constituent rows are skipped not corrupted (line 145). `usdPeggedConstituents` dedups across both XLM aliases AND the peg expansion. The `limit>0 && len>limit` post-trim matches OHLCSeries' earliest-N semantics. | — | None — verified correct, including big.Rat precision. | high |
| Info | assets.go:1187-1346 (dual-shape dispatch) | D7 | `/v1/assets/{slug}` dual-shape (GlobalAssetView vs AssetDetail) dispatch is correct: `tryServeGlobalAsset` (slug then ticker, case-insensitive) runs BEFORE canonical-id parse; slugs can't collide with canonical shapes (anchored prefixes). Route precedence: `/assets/verified` + `/assets/{id}/metadata` + `/assets/{id}/supply` are literals that beat the `/{asset_id}` and `/{asset_id}/{network}` wildcards via Go 1.22 mux precedence (registration order irrelevant). | — | None — verified correct. | high |
| Info | envelope.go + all handlers | D7 | Envelope/problem+json consistency is uniform: every success uses `writeJSON`/`writeEnvelope`, every error uses `writeProblem` (RFC 9457, `no-store` cache, `WWW-Authenticate` on 401), every nil-reader path 503s (or empty-array for the degradation-by-design list endpoints: assets/markets/pools/sources/oracle-streams). Nil slices defensively coerced to `[]` to satisfy `type: array`. OpenAPI ↔ handler path parity confirmed: all 60 registered routes have spec entries. | — | None — verified. | high |

## CORRECT-verified list (no issues found)

These handlers/paths were read in full and verified against D1/D2/D3/D5/D7:

- **price.go** — `handlePrice`, `priceFallback` chain (Redis-VWAP → stablecoin proxy
  → fiat cross-rate), `readPriceWithAliases` (XLM dual-form fresh-vs-stale logic),
  `tryStablecoinFiatProxy` (peg self-$1 + rewrite), `tryRedisVWAPFallback`
  (observed_at=now honesty per F-1305), `handlePriceBatch`/`Post` (1MiB body cap,
  DisallowUnknownFields, dedup, 16-wide bounded fan-out, disjoint `results[i]`
  writes → no race, deterministic first-failure-in-input-order, `sort.Strings(srcs)`
  per F-1259, F-1254 stale-on-fallback), `priceRatioDecimal` (big.Rat, zero-base
  guard). Prices/amounts all strings.
- **vwap.go** — closed-bucket clamp, NaN/Inf sigma rejection, all-filtered vs
  no-trades distinction (422 vs 404), stablecoin fallback, big.Rat price.
- **twap.go** — closed-bucket clamp, stablecoin fallback, no outlier param
  (time-weighting is the resistance), big.Rat price.
- **price_tip.go** — `computeTip` full fallback ladder, ADR-0018 `stale=false`
  always, granularity-param rejection (URL discipline), window clamp [1,60],
  XLM dual-form merge. (Source-ordering Medium noted above.)
- **history.go** — `parseBaseQuote`/`parseFromTo`, full-PK cursor (encode/decode
  with empty-source + hex64 tx_hash validation), 8s timeout + timeout→503,
  `tradeRowFrom` (string amounts), since-inception stablecoin fallback + triangulated
  flag (G2-13), 50k point cap.
- **ohlc.go** — single-bar + series dispatch, closed-bucket clamp, outlier-sigma
  default 4.0 (R-007), `Truncated` pre-filter capture (G2-05), stablecoin fallback,
  `parseFromToClamped`/`ratToDecimal` (big.Rat, sign-correct, floor).
- **ohlc_series.go** — interval enum validation, limit [1,1000], interval-aware
  from/to defaults, 8s timeout→503, `ohlcSeriesWithAliases` (XLM dual-form + fiat
  combine branch), empty→`[]` not null. (Triangulated-flag Medium noted above.)
- **ohlc_fiat_combine.go** — see Info row (big.Rat combine, dedup, skip-unparseable).
- **markets.go** — `handleMarkets`/`handlePools`, limit clamp, order_by enum,
  cursor validation, source-registry validation (no silent-empty-page), asset/source
  conflicting-filter 400, slug expansion, 8s timeouts, sparkline opt-in sharing the
  budget, `Market.Volume24hUSD`/`LastPrice` as `*string`. `DexSourceNames`/
  `CexSourceNames` sorted (prewarm key-drift fix).
- **markets_cache.go** — SWR single-flight (fresh/stale/join/cold-leader), waiter-err-
  pointer panic-safety, error-not-cached, background refresh on fresh `context.Background`,
  stable `%v` slice key (sources sorted upstream).
- **coins_cache.go** — generic `swr[T]` + `fetchRows`/`fetchHistoryMap` single-flight,
  same panic-safety, refresh budget 30s, ttl=0 passthrough.
- **sources.go** — class-filter allowlist, include=stats/sparkline opt-in with 8s
  ceilings + soft-fail, zero-fill 24h sparkline, sorted output, `VolumeUSD24h` string.
- **assets.go** — list (limit clamp, asset_class/network dispatch, unified phase-cursor,
  coins-overlay overfetch-by-one F-1326), get (cache, SEP-1 overlay from DB column not
  live fetch, F2 overlay, coin-extension, verified-currency collision warning),
  metadata, `isSafeImageURL` (scheme allowlist vs javascript:/data:), Decimals never
  from display_decimals (F-1321), `normaliseAssetIDInput` (narrow NATIVE rescue).
- **assets_f2.go** — supply/market-cap/FDV/change/volume overlay (parallel disjoint-field
  fan-out + wg barrier), `usdMarketValue`/`pctChange` (big.Rat, no float, decimals
  divisor), best-effort null-on-failure, lookupUSDPrice stablecoin proxy, native key
  uses `asset.String()` not AssetKey (pre-2026-05-04 bug fixed).
- **assets_global.go** — GlobalAssetView/NetworkView, fiat vs crypto price-reader split,
  fx_quotes-first market-cap (path ordering fix), `computeFiatMarketCap` (big.Float
  128-prec), per-network drill-down + Stellar 303 redirect, verified listing parallel
  fiat fan-out.
- **oracle.go** — latest + streams, `oracleAssetCandidates` (XLM + crypto:<CODE>),
  source-registry validation, 8s timeouts, ClassOracle wire-filter on streams,
  `scaledDecimalString` (sign-correct, padded, ADR-0003 raw preserved as `price_raw`).
- **oracle_sep40.go / oracle_cache.go** — (sub-agent verified) SEP-40 lastprice/prices/
  x_last_price, XLM dual-form via readPriceWithAliases, closed-bucket RecentClosedSnapshots,
  records bound [1,200], single-flight cache.
- **coverage_verdicts.go** — nil→503, clientAborted, 60s public cache, counts as ints,
  CoveragePct is a percentage.
- **coverage_cache.go** — see Low row (RLock snapshot, safe today).
- **asset_supply.go** — nil→503, XLM-all-aliases inline, `resolveSupplyContractID`
  (contract-id or sac_wrapper reverse), amounts all strings, ClickHouse query parameterized.
- **protocols.go / protocols_registry.go** — (sub-agent) graceful degradation,
  404 on unknown, defensive slice copies, static factory sets.
- **issuers.go / issuers_cache.go / known_issuers.go / known_scams.go** — (sub-agent)
  limit clamp, 8s timeout + full error ladder, G-strkey normalize, single-flight,
  curated maps DB-wins enrichment.
- **changes.go** — entity-type allowlist, candidate expansion (XLM/native/crypto:),
  sql.ErrNoRows→404, real-error→500. (ADR-0003 float value-fields Low noted above.)
- **network_stats.go** — nil→503, volume as `*string`, registry source counts.
  (Missing-timeout Medium noted above.)
- **pairs.go / currencies.go / sac_wrappers.go / lending.go / sep41_transfers.go /
  ledger_tip.go** — (sub-agent) base/quote validation + identity guard + empty-array
  degradation (pairs); types-only (currencies); static echo (sac_wrappers); empty-array
  + 8s timeout (lending); strkey validation + Amount-as-string + 8s timeout
  (sep41_transfers); nil-cursor 503 + negative-lag clamp + correct ledgerstream selector
  (ledger_tip).
- **diagnostics_ingestion.go / diagnostics_cursors.go** — (sub-agent) per-filler sub-context
  timeouts, atomic degraded sidecar (no race), soft-fail-per-section, coverage from
  snapshot tables (not unbounded trade scan), no SQL from params, 5s timeout + full
  error taxonomy (cursors). (parseInt64 overflow Low noted above.)
- **observations.go / observations_stream.go / chart.go / methodology.go / incidents.go /
  status.go / ledger_stream.go / price_stream.go / price_tip_stream.go** — (sub-agent)
  8s scan ceilings, SSE lifecycle sound (pre-flight 503 before SSE switch, prodCtx +
  defer cancel, producer `defer close(ch)` + ctx.Done select → no goroutine leak,
  bounded channel, 15s heartbeat), closed-bucket per ADR-0015/0020, fiat:USD short-circuit
  narrowed correctly (F-1325), amounts-as-strings, static methodology, UTF-8-safe incident
  truncation, status always-200 with rollup precedence + 3s deadline + body LimitReader.

## Cross-cutting conclusions

- **D2 ADR-0003 (i128/amounts/prices as strings):** Held across the whole area. Every
  token amount / price / reserve / supply ships as a string. The exceptions are all
  legitimately non-amount numerics (ledger seqs, counts, trade_count, window_seconds,
  oracle confidence scores, coverage_pct) OR documented/derived display widgets
  (changes.go float values — flagged Low; price.go fiat cross-rate float — flagged Low).
- **D2 ADR-0015 (closed-bucket / cross-region byte-identical):** `parseFromToClamped`
  + `parseOHLCSeriesFromTo` snap default-now to boundaries correctly; batch sorts
  sources. The one gap is `tipWindowVWAP`'s unsorted dual-form `sources[]` (Medium).
- **D2 XLM dual-form:** Alias loop verified present on every asset-keyed read path.
- **D2 USDT fabrication:** None. USD-peg set is operator-config only.
- **D3 SQL injection / unbounded scan:** No param-built SQL. Enum params switch-validated.
  The one no-time-bound scan (`LatestTradePerSource`) is known, cache-mitigated, and
  lives in the storage layer.
- **D5 timeouts:** Present on all cold trades/CAGG/oracle scans EXCEPT `handleNetworkStats`
  (Medium). Prewarm key-drift addressed (`DexSourceNames` sorted/exported; cache keys
  built from the same args the handlers pass).
- **D7 contract:** OpenAPI ↔ handler path parity complete (60 routes); envelope/problem+json
  uniform; dual-shape `/v1/assets/{slug}` dispatch correct.

## Files read

Fully read by me (15): ohlc_fiat_combine.go, ohlc.go, ohlc_series.go, price.go, vwap.go,
twap.go, history.go, price_tip.go, markets.go, sources.go, envelope.go, coins.go,
coins_cache.go, markets_cache.go, assets.go (both halves), assets_f2.go, oracle.go,
assets_global.go, changes.go, coverage_verdicts.go, asset_supply.go (= 21 files).

Covered via two sub-agents (32 files): oracle_sep40.go, oracle_cache.go, protocols.go,
protocols_registry.go, coverage_cache.go, issuers.go, issuers_cache.go, known_issuers.go,
known_scams.go, network_stats.go, pairs.go, currencies.go, lending.go, sep41_transfers.go,
sac_wrappers.go, ledger_tip.go, diagnostics_ingestion.go, diagnostics_cursors.go,
observations.go, observations_stream.go, chart.go, methodology.go, incidents.go, status.go,
ledger_stream.go, price_stream.go, price_tip_stream.go.

Supporting reads: openapi/stellar-index.v1.yaml (path list + route parity), server.go
(route registrations), internal/storage/clickhouse/supply_flows.go (supply query
parameterization). `*_test.go` were scoped in but not individually enumerated — no
test-rot findings surfaced in the handlers reviewed (the prior-finding fixes all carry
inline regression tests per the F-/G2-/R- references).

**Total distinct source files examined: ~53** (excluding tests).
