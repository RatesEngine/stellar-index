---
title: RFP × Proposal × Delivery — Coverage Matrix
last_verified: 2026-05-10
status: ratified
---

# RFP × Proposal × Delivery Coverage Matrix

**Ratified:** 2026-04-22.
**Re-baselined:** 2026-04-30 + incremental re-baselines 2026-05-01
+ **2026-05-02**.
**Production-verification column added 2026-05-10** — every row
now has a `Prod` column with curl-tested status against
`https://api.ratesengine.net` v0.5.0-rc.39. See
[`../review-2026-05-10.md`](../review-2026-05-10.md) for the
full production findings register (R-001 through R-023).

## Production-verification status (2026-05-10)

| Status | Count | Meaning |
|---|---|---|
| ✅ verified live | 28 | Curl returns the expected wire shape from `api.ratesengine.net` |
| ⚠ partial / borderline | 13 | Code shipped + serving but with caveat (e.g. p95 over target, only 7d backfill, operator config gap) |
| ❌ failing | 5 | Production behaviour disagrees with the matrix's `✅ verified` claim — see review §Section 2 |
| 📦 code-only / ops-only | 18 | Not API-testable (ADR, infra topology, internal gauge, operator-side script). Verified via test suite + ADR + ops-runbook execution |
| 🟡 watched-only | 3 | Code shipped, behaviour gated on operator watched-set config not populated on r1 today |
| ⏳ deferred | 2 | Explicitly post-launch (DIA mainnet, ADR-0019 Phase 3 cross-oracle) |

**The ❌ rows are the action list before public-flip.** All 5 are
listed with reproducible curl commands in
[`../review-2026-05-10.md`](../review-2026-05-10.md) §Appendix B.

The 2026-05-02 pass corrected three internally-contradictory ⚠
caveats:
- **X2.1** "CAGG population pending" → ✅: CAGGs auto-refresh per
  the `add_continuous_aggregate_policy` calls in migrations/0002.
- **S6.4** "OHLC fields … still need aggregator binary" → ✅: the
  CAGGs' `first/last/min/max(quote/base)` columns ARE the OHLC
  fields and they auto-populate. Note added re: the misleadingly-
  named CAGG `twap` column (arithmetic mean, not time-weighted —
  `/v1/twap` computes the real TW average from raw trades).
- **S9.4** "three aggregators … Chainlink path remains
  unimplemented" → ✅: the production wiring in
  `cmd/ratesengine-api/main.go::buildDivergenceReferences` is
  CoinGecko + Chainlink (S2.4 itself flipped to ✅ in 2026-04-30
  re-baseline; S9.4 hadn't picked up the cross-reference).

The 2026-05-01 pass flipped X1.2, X1.4–X1.7, X2.2–X2.4, X2.6–X2.7,
X3.1–X3.4, X3.6–X3.7, F6.5 from `🧪 designed` to `✅ verified`
after walking the codebase:
`internal/api/v1/{price_tip,observations,price_stream,
price_tip_stream,observations_stream}.go` ship the X2 surfaces;
`internal/aggregate/{anomaly,baseline,confidence,freeze}` ship
X3.1–X3.4/.6/.7; `cmd/ratesengine-ops verify-archive -tier
{chain,checkpoint,peers,archivist,all}` + `archive-completeness
verify` ship X1.2/.4/.5/.7; per-region tier selection in the
binary covers X1.6.

The 2026-04-30 base re-baseline was prompted by the
docs/audit-2026-04-29/ workspace flagging drift in both directions
(rows marked "designed" that had shipped, rows marked "verified"
that had regressed in production wiring). A separate Codex pass
against the RFPs + proposal also surfaced specific contract gaps
(Blend, Chainlink, Freighter V2 wiring) that are now reflected in
each row's Status / Conf.

**Purpose:** one authoritative table mapping every contractual requirement
to the mechanism that satisfies it. Supersedes the narrower
`docs/discovery/rfp-requirements-matrix.md` (which remains valid as
the source-discovery artefact).

## How to read this doc

Each row captures **one atomic requirement** from either RFP, sourced
verbatim from:

- [docs/stellar-rfp.md](../stellar-rfp.md) — Stellar Prices API RFP.
- [docs/freighter-rfp.md](../freighter-rfp.md) — Freighter asset-detail RFP.

For each row:

| Column | Meaning |
| ------ | ------- |
| **Requirement** | Verbatim or close paraphrase of the RFP bullet. |
| **Proposal commitment** | Where our `docs/ctx-proposal.md` commits to it. |
| **Delivery week** | Which week in [docs/discovery/delivery-plan.md](../discovery/delivery-plan.md) implements it. |
| **Owner binary / package** | The Go `cmd/*` or `internal/*` that delivers it. |
| **ADR** | The architectural decision that binds the implementation. |
| **Verified by** | The discovery doc whose primary-source work proves feasibility. |
| **Status** | `✅ verified`, `🧪 designed, impl pending`, `⏳ deferred`, `⚠ caveat`, `❌ gap`. |
| **Confidence** | Honest 1–5 score: 5 = code+tests, 1 = hand-wave. |

Any row with **status ❌** is a blocker for launch. Any row with
**confidence ≤ 2** is a risk line in the Week-N review.

---

## Stellar Prices API RFP — §3 Requirements

### S1. Asset coverage — classic + SEP-41 Soroban

> **Prod column legend** (added 2026-05-10):
> `✅ YYYY-MM-DD` = curl-verified live; `⚠ YYYY-MM-DD R-NNN` = partial / known gap, see [`../review-2026-05-10.md`](../review-2026-05-10.md) §Section 2; `❌ YYYY-MM-DD R-NNN` = production behaviour disagrees with claim; `📦 code-only` = not API-testable (ADR, infra, migration); `🟡 watched-only` = depends on operator-watched-set config not populated on r1.

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| S1.1 | Classic assets identity (code+issuer) | §Data Ingestion / SDEX | 2 | `internal/sources/sdex` | — | [protocol-versions.md](../discovery/protocol-versions.md), [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md) | ✅ verified | 5 | ✅ 2026-05-10 — `GET /v1/assets/USDC-GA5Z…KZVN` → `type=classic, code=USDC, issuer=GA5Z…` |
| S1.2 | SEP-41 Soroban tokens — events ingest | §Data Ingestion / Soroban DEXs | 3 | `internal/sources/soroswap`, `/aquarius`, etc. | — | [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md) | ✅ verified | 5 | ✅ 2026-05-10 — `assets_indexed=86,516` per `/v1/network/stats` |
| S1.3 | SAC-wrapped classic (native XLM SAC = `CAS3J7…OWMA`) | §Data Ingestion / SDEX | 3 | `internal/canonical` + sources | — | [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md), [dexes-amms/aquarius.md](../discovery/dexes-amms/aquarius.md) | ✅ verified | 4 | ✅ 2026-05-10 — `GET /v1/assets/CAS3J7…OWMA` → `type=soroban, contract_id=CAS3J7…OWMA` |
| S1.4 | Asset enumeration / discovery | §Asset Identification | 4 | `internal/canonical/discovery` | — | [data-sources/withobsrvr-stellar-extract.md](../discovery/data-sources/withobsrvr-stellar-extract.md) | ✅ verified | 4 | ✅ 2026-05-10 — 86,516 assets indexed; `/v1/sac-wrappers` returns 30+ SAC mappings |
| S1.5 | i128/u128 amounts never truncate | §Data Processing | 1 | `internal/canonical.Amount` | ADR-0003 | Tested: `amount_test.go` KALIEN regression | ✅ verified | 5 | 📦 code-only |

### S2. Oracle coverage — Chainlink, Redstone, Band, Reflector + others

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| S2.1 | Reflector (3 contracts: DEX/CEX/FX) | §Oracle Networks — Reflector | 4 | `internal/sources/reflector` | — | [oracles/reflector.md](../discovery/oracles/reflector.md) | ✅ verified | 5 | ✅ 2026-05-10 — `/v1/sources` lists `reflector-dex/cex/fx` (3 entries class=oracle) |
| S2.2 | Redstone (Adapter + 19 per-feed proxies) | §Oracle Networks — Redstone | 4 | `internal/sources/redstone` | — | [oracles/redstone.md](../discovery/oracles/redstone.md) | ✅ verified | 5 | ✅ 2026-05-10 — `/v1/sources` lists `redstone` class=oracle |
| S2.3 | Band Protocol (native Soroban StandardReference) | §Oracle Networks — Band | 4 | `internal/sources/band` | — | [oracles/band.md](../discovery/oracles/band.md) | ✅ verified | 5 | ✅ 2026-05-10 — `/v1/sources` lists `band` class=oracle |
| S2.4 | Chainlink (HTTP cross-check until Scale ships) | §Oracle Networks — Chainlink | 4 | `internal/divergence/chainlink.go` | — | [oracles/chainlink.md](../discovery/oracles/chainlink.md) | ✅ verified — `ChainlinkReference` shipped in #282. `eth_call` against `latestAnswer()` selector `0x50d25bcd`; two's-complement int256 decode; optional inversion. Used as divergence cross-check, NOT a VWAP contributor. | 4 | ⚠ 2026-05-10 — code shipped + crypto-pair defaults landed (#1256); r1 operator config `[divergence.chainlink.feeds]` only has fiat pairs (task #119). Default crypto feeds will activate at next deploy. |
| S2.5 | "And others" — DIA (if mainnet ships in window) | (not in proposal; adding) | 4–post-launch | `internal/sources/dia` | — | [oracles/dia.md](../discovery/oracles/dia.md) | ⏳ deferred | 2 | 📦 code-only — no mainnet integration yet |
| S2.6 | SEP-40-compat output (others consume *our* prices) | §API | 7 | `internal/api/v1/oracle_sep40.go` | — | [oracles/reflector.md](../discovery/oracles/reflector.md) §SEP-40 interface | ✅ verified — `/v1/oracle/{lastprice,prices,x_last_price}` SEP-40-shaped passthrough endpoints shipped | 4 | ✅ 2026-05-10 — `GET /v1/oracle/lastprice?asset=native` → `{price, timestamp}`; `/v1/oracle/prices?asset=native&records=3` → array; `/v1/oracle/x_last_price?base=native&quote=fiat:USD` → 200 |

### S3. Price aggregation — Soroswap, Aquarius, SDEX, Comet + others

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| S3.1 | SDEX trades via ClaimAtom parsing | §Stellar Classic DEX | 2 | `internal/sources/sdex` | — | [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md) | ✅ verified | 5 | ✅ 2026-05-10 — `sdex` in `/v1/sources` (class=exchange subclass=dex); raw trades visible via `GET /v1/history?base=native&quote=USDC-G…` |
| S3.2 | Soroswap factory+pair+router events | §Soroban DEXs / Soroswap | 3 | `internal/sources/soroswap` | — | [dexes-amms/soroswap.md](../discovery/dexes-amms/soroswap.md) | ✅ verified | 5 | ✅ 2026-05-10 — `soroswap` in `/v1/sources` |
| S3.3 | Aquarius 3 pool types | §Soroban DEXs / Aquarius | 3 | `internal/sources/aquarius` | — | [dexes-amms/aquarius.md](../discovery/dexes-amms/aquarius.md) | ✅ verified | 5 | ✅ 2026-05-10 — `aquarius` in `/v1/sources` |
| S3.4 | Phoenix DEX (8-events-per-swap) | §Soroban DEXs (added post-discovery) | 3 | `internal/sources/phoenix` | — | [dexes-amms/phoenix.md](../discovery/dexes-amms/phoenix.md) | ✅ verified | 5 | ✅ 2026-05-10 — `phoenix` in `/v1/sources` |
| S3.5 | Comet (Balancer-weighted AMM) | §Soroban DEXs (added post-discovery) | 3 | `internal/sources/comet` | — | [dexes-amms/comet.md](../discovery/dexes-amms/comet.md) | ✅ verified | 4 | ✅ 2026-05-10 — `comet` in `/v1/sources` |
| S3.6 | Blend auctions as directional signal | §Soroban DEXs / Blend | 5 | `internal/sources/blend` | — | [dexes-amms/blend.md](../discovery/dexes-amms/blend.md), [wasm-audits/blend.md](../operations/wasm-audits/blend.md) | ✅ verified — auction decoder + storage + dispatcher wiring shipped (#273-#275); WASM audit complete 2026-05-02 (Phases 1-4: 11 contracts / 3 unique WASMs / no mid-life upgrades over 11.79M-ledger walk). `BackfillSafe=true`. | 4 | ✅ 2026-05-10 — `blend` in `/v1/sources` (class=lending); `/v1/lending/pools` returns Blend pools with auction counts |
| S3.7 | CEX trade ingestion (Binance, Coinbase, Kraken, …) | §Centralized Exchanges | 4 | `internal/sources/external/*` | — | [external-refs/cex-feeds.md](../discovery/external-refs/cex-feeds.md) | ✅ verified | 4 | ✅ 2026-05-10 — all 4 listed in `/v1/sources` (binance/coinbase/kraken/bitstamp class=exchange subclass=cex); 11 exchange sources total |

### S4. VWAP + configurable USD volume threshold

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| S4.1 | Volume-weighted aggregation across venues | §Aggregation Strategy | 5 | `internal/aggregate/orchestrator` + `prices_*` CAGGs | — | `cmd/ratesengine-aggregator` running per-window VWAP refresh; CAGGs back the API price reader. | ✅ verified | 4 | ✅ 2026-05-10 — `GET /v1/vwap?base=native&quote=fiat:USD&window=300` → `{price, base_volume, quote_volume, trade_count, outliers_filtered, truncated}` |
| S4.2 | USD-denominated volume on non-USD pairs | §Cross-Pair Derivation | 5 | `internal/aggregate/orchestrator/triangulate.go` + provenance marker | — | Triangulation worker writes implied VWAPs + `:provenance` marker (#279); API serves them with `flags.triangulated=true` (#280). | ✅ verified | 4 | ✅ 2026-05-10 — `flags.triangulated=true` on `/v1/twap?base=native&quote=fiat:USD&window=3600` (XLM/USD has no direct trades; comes via stablecoin proxy) |
| S4.3 | Per-pair configurable min USD volume | §Security — manipulation | 5 | `internal/config` schema + `internal/aggregate/orchestrator` | — | `aggregate.min_usd_volume` config field consumed by orchestrator; backed by `prices_1m.volume_usd`. | ✅ verified | 4 | 📦 code-only (config knob; behavioural verification needs synthetic low-volume pair) |
| S4.4 | TWAP fallback when volume thresholds not met | §Aggregation Strategy | 5 | `internal/aggregate/orchestrator` + `internal/api/v1/twap.go` | — | TWAP endpoint `/v1/twap` shipped; aggregator computes via stored bucket VWAPs as a fallback. | ✅ verified | 3 | ✅ 2026-05-10 — `GET /v1/twap?base=native&quote=fiat:USD&window=3600` → 200 with `price, trade_count, truncated` |

### S5. Real-time price endpoints

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| S5.1 | Live event ingest (Galexie/MinIO + ledgerstream + dispatcher) | §Real-time — Hot path | 3 | `cmd/ratesengine-indexer` + `internal/ledgerstream` + `internal/dispatcher` + `internal/sources/*` | — | [data-sources/archival-nodes.md](../discovery/data-sources/archival-nodes.md), [ingest-pipeline.md](ingest-pipeline.md) | ✅ verified | 5 | ✅ 2026-05-10 — `latest_ledger=62,510,233` per `/v1/network/stats`; `markets_count_24h=23,646` |
| S5.2 | ≤ 30s staleness (Freighter SLA) | §Latency Targets | 6 | `cmd/ratesengine-sla-probe` + `deploy/systemd/sla-probe.{service,timer}` | — | [data-sources/archival-nodes.md](../discovery/data-sources/archival-nodes.md) + HA plan | ✅ verified — `ratesengine-sla-probe` measures `observed_at` freshness against the 30s target every 15 min; alerts in `deploy/monitoring/rules/sla-probe.yml` page on sustained breach. | 4 | ✅ 2026-05-10 — `/v1/price/tip?asset=native&quote=fiat:USD.observed_at` lags `as_of` by < 5min (closed-bucket); freshness alert wired |
| S5.3 | SSE streaming for subscribers | §Streaming Support | 7 | `internal/api/streaming` + `/v1/price/stream`, `/v1/observations/stream`, `/v1/price/tip` | — | Hub + per-topic ring buffer; Last-Event-ID resume. | ✅ verified | 4 | ✅ 2026-05-10 — `/v1/price/stream?asset=native&quote=fiat:USD` emits 3 windows (300/3600/86400) `price_update` events within 6s; `/v1/price/tip/stream` emits `tip_update` every 5s |
| S5.4 | Degradation signals (`stale_flag`, `reduced_redundancy`) | §Error Handling and Degradation | 5 | `internal/api/envelope` | — | `envelope.Flags` shipped (stale, reduced_redundancy, triangulated, divergence_warning) | ✅ verified | 3 | ✅ 2026-05-10 — every response carries `flags.{stale, reduced_redundancy, triangulated, divergence_warning}` |

### S6. Historical price endpoints + OHLC

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| S6.1 | Since-inception backfill (ledger 2 → today) | §Historical Data | 2 (scaffold), 5 (run) | `cmd/ratesengine-ops backfill` | — | [data-sources/galexie.md](../discovery/data-sources/galexie.md) + [data-sources/stellar-data-lakes.md](../discovery/data-sources/stellar-data-lakes.md) | ✅ verified | 4 | ⚠ 2026-05-10 — endpoint shipped (`/v1/history/since-inception?asset=native&quote=USDC-G…` → 200) but the **earliest data point on r1 is 2026-05-03** (~7 days), not ledger 2. Backfill not yet executed for older periods. |
| S6.2 | Pre-P20 (no-Soroban) coverage via ClaimAtom | §Historical Data | 2 | `internal/sources/sdex` | — | [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md), [protocol-versions.md](../discovery/protocol-versions.md) | ✅ verified | 5 | 📦 code-only (until backfill runs over pre-P20 ledgers) |
| S6.3 | Post-P23 unified events handling | §Historical Data | 2 | `internal/sources/sdex` | — | [notes/cap-67-unified-events.md](../discovery/notes/cap-67-unified-events.md) | ✅ verified | 5 | ✅ 2026-05-10 — current ingest is post-P23 (mainnet); markets_count_24h=23,646 |
| S6.4 | OHLC continuous aggregates | §Historical — storage | 4 | `internal/storage/timescale` + migrations | ADR-0006 | migrations/0002 creates prices_{1m,15m,1h,4h,1d,1w,1mo} CAGGs with `first/last/min/max(quote/base)` columns + `add_continuous_aggregate_policy` auto-refresh; covered by test/integration/migrations_test.go. Note the CAGG `twap` column is `avg(quote/base)` (arithmetic mean, not true time-weighted) — `/v1/twap` computes the real TW average from raw trades and ignores the CAGG column; see `cmd/ratesengine-aggregator/main.go` ⚠ CAGG TWAP CAVEAT. | ✅ verified | 4 | ❌ 2026-05-10 R-007 — `/v1/ohlc?base=native&quote=fiat:USD&timeframe=24h&granularity=1h` returns `high: 1.0000000000` for XLM (XLM ~$0.17). Stablecoin-proxy contamination polluting OHLC bar. `flags.triangulated=true` is honest but the bar value is wrong. |
| S6.5 | Retention: 1h+ granularity indefinite; <1h capped | §Historical — retention | 4 | Timescale retention policies | ADR-0006 | migrations/0002 wires retention policies per CAGG; covered by TestMigrationsRoundTrip + policy-attachment assertions. | ✅ verified | 4 | 📦 code-only (retention policy verified by migration tests; consumer impact only after data ages past sub-1h cap) |

### S7. Supported timeframes (1h / 24h / 1w / 1mo / 1yr / all-time)

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| S7.1 | 1m / 15m / 1h / 4h / 1d / 1w / 1mo granularities | Verbatim in §Historical Data | 4 | Timescale continuous aggregates | ADR-0006 | migrations/0002 ships all 7 CAGGs; verified by TestMigrationsRoundTrip. | ✅ verified | 4 | ✅ 2026-05-10 — all 7 granularities accepted on `/v1/chart`; all 6 timeframes (1h/24h/1w/1mo/1y/all) accepted |
| S7.2 | 1h+ kept indefinitely, <1h capped | Verbatim in §Historical Data | 4 | Timescale retention | ADR-0006 | migrations/0002 adds 30-day retention only on prices_1m + prices_15m; hourly+ have no retention = indefinite. Verified by assertPolicyAttached in migrations_test.go. | ✅ verified | 4 | ⚠ 2026-05-10 R-013 — retention policy correct but **on r1 only ~7 days of 1h data exists** (backfill not yet run). `/v1/chart?timeframe=1y` returns 172 points (= 7 days × 24h, not 1y). Worth a `truncated: true` flag on chart responses. |

### S8. Base and quote volume in USD

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| S8.1 | `usd_volume` column per trade | §Data Processing | 3 | `internal/canonical.Trade` + `migrations/0001_create_trades_hypertable.up.sql` | — | Column shipped in trades hypertable; CAGGs sum it via `volume_usd`. | ✅ verified | 4 | ✅ 2026-05-10 — `/v1/vwap.quote_volume` populated; `/v1/network/stats.volume_24h_usd=$3,542,086,217` (24h cross-source) |
| S8.2 | FX anchor for USD conversion | §Forex Providers | 4 | `internal/sources/external/{exchangeratesapi,polygonforex}` + `internal/aggregate/stablecoin.go` | — | Stablecoin proxy at aggregator layer (USDC/USDT→USD); FX vendors wired in registry. | ✅ verified | 4 | ✅ 2026-05-10 — `exchangeratesapi`, `polygon-forex` listed in `/v1/sources` (class=exchange subclass=fx); `/v1/currencies` returns FX rates for AED, ALL, etc. |

### S9. Performance SLAs

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| S9.1 | ≥ 99.99 % uptime | §Availability | 8–9 | HA plan + `cmd/ratesengine-sla-probe` | [ADR-0008](../adr/0008-ha-topology.md) | (HA plan) | ⚠ caveat — synthetic 2xx-success-rate gate shipped (#283 + #290 + #294); 99.99% target needs production + multi-region traffic to verify operationally. The probe surfaces the signal; the HA topology is what backs the number. | 3 | ⚠ 2026-05-10 — single-region today (R1 only); 99.99% needs ≥30 days × multi-region. R2/R3 not bootstrapped yet (L4.14/L4.15 🔴). |
| S9.2 | p95 ≤ 200 ms, p99 ≤ 500 ms | §Latency Targets | 9 | `internal/api` + Redis caching + `cmd/ratesengine-sla-probe` | [ADR-0009](../adr/0009-latency-budget.md) | (API design + HA plan) | ✅ verified — synthetic measurement shipped via the SLA probe (#283); RFP-stated targets baked into `default*Target` constants; alerts page on sustained breach. | 4 | ⚠ 2026-05-10 — measured 30-sample run from US East: **p95=246ms** (over 200ms target by 23%), p99=250ms (under 500ms). Single-region cap; multi-region routing may improve. |
| S9.3 | 1000 req/min per client | §Rate Limits | 7 | `internal/ratelimit` + `internal/api/v1/middleware/ratelimit.go` | — | Authenticated tier wired to `api.key_rate_limit_per_min` per F-0008 fix; anon + key buckets are now distinct. | ✅ verified | 4 | ⚠ 2026-05-10 — anonymous tier returns `x-ratelimit-limit: 60`; authenticated tier (with API key) presumably 1000+ but not verified in this review. RFP says "≥ 1000/min per client" — meets it for keyed clients. |
| S9.4 | Defined degradation when prices unavailable | §Degradation Strategy + divergence | 5 | `internal/divergence/{coingecko,chainlink}.go` + `internal/api/v1/envelope.go` | — | Divergence service wires CoinGecko (free tier, default-on) + Chainlink (Enabled=true + non-empty FeedMap) per `cmd/ratesengine-api/main.go::buildDivergenceReferences`; `flags.divergence_warning` surfaces on /v1/price when any reference's tolerance is exceeded. CoinMarketCap + CryptoCompare remain external-source class registries (price contributors), not divergence references — separate role. | ✅ verified | 4 | ✅ 2026-05-10 — `flags.divergence_warning=false` on every observed response; flag surfaces correctly in JSON shape (no synthetic divergence event observed during review window) |

### S10. Open source

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| S10.1 | Apache-2.0, fully open | §Open Source & Deployment Model | 1 | `LICENSE` in repo root | — | LICENSE committed | ✅ verified | 5 | 📦 code-only — `LICENSE` is Apache-2.0; repo public-flip planned for v1.0 per `docs/operations/public-flip.md` |

---

## Freighter RFP — V1: Asset metadata

| # | Field | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| F1.1 | Asset/Token Code | §Asset Identification | 4 | `internal/metadata` | — | [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md), [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md) | ✅ verified | 5 | ✅ 2026-05-10 — `GET /v1/coins/native.code = "XLM"` |
| F1.2 | Current Price (USD) | §Current Price API | 5 | `internal/api/v1/price.go` | — | `/v1/price?asset=…&quote=fiat:USD` shipped; reads from `prices_1m` CAGG (closed-bucket per ADR-0015) with last-trade fallback. Default quote is USD. | ✅ verified | 5 | ✅ 2026-05-10 — `GET /v1/price?asset=native&quote=fiat:USD` → `{price="0.167…", price_type=vwap, observed_at, window_seconds=300}` |
| F1.3 | Asset Type enum (`classic`/`soroban`) | §Asset Identification | 4 | `internal/canonical.AssetType` (typed enum: `native`/`classic`/`soroban`/`fiat`/`crypto`); wire shape via `pkg/client.AssetDetail.Type` (string) | — | [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md) | ✅ verified | 5 | ✅ 2026-05-10 — `GET /v1/assets/native.type="native"`; `/v1/assets/USDC-G….type="classic"`; `/v1/assets/CAS3J7….type="soroban"` |
| F1.4 | Issuer Address (G…) | §Asset Identification | 4 | `internal/canonical.ClassicAsset` (Code + Issuer); wire via `pkg/client.AssetDetail.Issuer` | — | [protocol-versions.md](../discovery/protocol-versions.md) | ✅ verified | 5 | ✅ 2026-05-10 — `GET /v1/assets/USDC-G….issuer = "GA5Z…KZVN"` |
| F1.5 | Contract Address (C…) | §Asset Identification | 4 | `internal/canonical.NewSorobanAsset` (C-strkey); wire via `pkg/client.AssetDetail.ContractID` | — | [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md) | ✅ verified | 5 | ✅ 2026-05-10 — `GET /v1/assets/CAS3J7…OWMA.contract_id = "CAS3J7…OWMA"` |
| F1.6 | Home Domain (SEP-1) | §Asset Identification (needs proposal amendment) | 5 | `internal/metadata` + `internal/api/v1/assets.go applySep1Overlay` | [ADR-0007](../adr/0007-redis-cache-schema.md) | [data-sources/sep1-home-domain.md](../discovery/data-sources/sep1-home-domain.md) | Resolver + cache + overlay all shipped; AssetDetail surfaces sep1_status, name, description, image, org_name, anchor_asset, anchor_asset_type. | ✅ verified | 5 | ⚠ 2026-05-10 R-016, R-017 — `GET /v1/issuers/GA5Z….home_domain = "centre.io", org_name = "Circle"` ✓; **but `GET /v1/assets/USDC-G….sep1_status = "not_applicable"`** — two endpoints disagree. SEP-1 overlay not inlined on `/v1/assets/{id}` for classic credits. |

## Freighter RFP — V1: Historical price chart

Same as S7. No additional requirement.

> **Scope note (chart `price_type=twap`).** `/v1/chart` accepts
> `price_type=vwap` today and rejects `price_type=twap` with a
> 400 Bad Request per ADR-0020. TWAP is reserved, not delivered:
> the on-the-fly TWAP we'd compute from the 1m CAGG would
> produce different values from a future TWAP CAGG (pre-/post-
> shape difference) and create a one-time consumer-visible
> shift, so we'd rather defer than ship-and-rotate. `/v1/twap`
> single-bar TWAP is shipped (Go-side time-weighted compute from
> raw trades) — only the multi-bucket chart variant is the
> reserved item.

## Freighter RFP — V2: Market data extension

> **Scope note.** F2.1 / F2.2 / F2.4 / F2.5 supply pipelines are
> live for **operator-watched assets** (XLM is always-on; classic
> credit assets and SEP-41 tokens via `[supply].watched_classic_assets`
> and `[supply].watched_sep41_contracts` per ADR-0022 and ADR-0023).
> Per-asset opt-in is by design: classic credit issuers and SEP-41
> tokens carry decentralised mint authorities and the
> "is this issuer's supply meaningful at this scale" judgment is
> operator-curated, not blanket. The API returns nullable supply
> fields cleanly when an asset is outside the watched set
> (matches ADR-0011 "we don't fabricate"). Operator can widen
> coverage via TOML config without code change.

| # | Field | Proposal | Week | Owner | ADR | Verified by | Status | Conf | Prod |
| - | ----- | -------- | ---- | ----- | --- | ----------- | ------ | ---- | ---- |
| F2.1 | Market Cap = `circulating × price` | §V2 (addendum) | 6 | `internal/api/v1/assets_f2.go populateMarketCap` + supply pipeline | [ADR-0011](../adr/0011-supply-algorithm.md), [ADR-0021](../adr/0021-account-entry-observer.md), [ADR-0022](../adr/0022-classic-supply-observers.md), [ADR-0023](../adr/0023-sep41-supply-observer.md) | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md) | ✅ verified — read path (#277) + writer end-to-end across all three asset classes: XLM (#285), classic credits (#303-#307), SEP-41 (#309-#312). The aggregator-resident refresher (#301) populates `asset_supply_history` per watched asset on the configured cadence. `market_cap_usd` populates when both supply + USD price exist. **Scope: XLM + watched classic + watched SEP-41 (operator config).** | 4 | ❌ 2026-05-10 R-006 — **NULL on every asset** including XLM. r1 has empty `[supply].watched_*` lists + `sdf_reserve_accounts` not populated (operator action #97). Code is shipped and correct; production has 0 watched assets so 0 F2 fields populate. |
| F2.2 | FDV = `max_supply × price` | §V2 | 6 | `internal/api/v1/assets_f2.go populateMarketCap` + supply pipeline | [ADR-0011](../adr/0011-supply-algorithm.md) | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md) | ✅ verified — same pipeline as F2.1; `fdv_usd` populates when `max_supply` is non-null (uncapped issuers without SEP-1 declaration leave it null per ADR-0011 "we don't fabricate"). | 4 | ❌ 2026-05-10 R-006 — same as F2.1; field absent from `/v1/coins/native` response |
| F2.3 | 24h Trading Volume (USD) | §V2 | 6 | `internal/storage/timescale.Volume24hUSDForAsset` + `internal/api/v1/assets.go` | ADR-0007 | `volume_24h_usd` field on `/v1/assets/{id}` (#278). Reads from `prices_1m` CAGG. | ✅ verified | 4 | ✅ 2026-05-10 — `GET /v1/coins/native.volume_24h_usd = "899548.57"`; `/v1/assets/native.volume_24h_usd = "882756.96"` |
| F2.4 | Circulating Supply (provider-supplied) | §V2 | 6 | `internal/supply/{xlm,classic,sep41}.go` + observers + `cmd/ratesengine-aggregator/main.go::buildSupplyRefreshers` | [ADR-0011](../adr/0011-supply-algorithm.md), [ADR-0021](../adr/0021-account-entry-observer.md), [ADR-0022](../adr/0022-classic-supply-observers.md), [ADR-0023](../adr/0023-sep41-supply-observer.md) | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md) | ✅ verified — XLM (Algorithm 1), classic credit (Algorithm 2), SEP-41 (Algorithm 3) all live. Operator-locked-set subtraction supported per asset via `supply.Policy.PerAsset`. | 4 | 🟡 2026-05-10 R-006 — code shipped, watched-set empty on r1; field NULL universally |
| F2.5 | Total Supply (mint − burn − clawback) | §V2 | 6 | `internal/sources/sep41_supply` observer + `internal/supply/storage_sep41_reader.go` | [ADR-0011](../adr/0011-supply-algorithm.md), [ADR-0023](../adr/0023-sep41-supply-observer.md) | [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md) | ✅ verified — SEP-41 mint/burn/clawback events accumulate into `sep41_supply_events` (#309); the reader composes per-kind sums via `Σ FILTER (WHERE ...)` (#311) and the aggregator refreshes one snapshot per watched contract per cycle (#312). Classic + XLM totals via the same algorithm-correct path. | 4 | 🟡 2026-05-10 R-006 — same as F2.4 |
| F2.6 | Max Supply (nullable, off-chain metadata) | §V2 | 6 | `internal/supply/overlay.go` + `internal/metadata` | [ADR-0011](../adr/0011-supply-algorithm.md) | [data-sources/sep1-home-domain.md](../discovery/data-sources/sep1-home-domain.md) | ✅ verified — overlay policy implemented + integrated end-to-end. Per ADR-0011, `max_supply` stays null for uncapped issuers without SEP-1 declaration / operator override; consumers handle null explicitly. | 4 | 🟡 2026-05-10 R-006 — same as F2.4 |

## Freighter RFP — Performance SLAs

| # | Metric | Requirement | Proposal | Week | Owner | Verified by | Status | Conf | Prod |
| - | ------ | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- | ---- |
| F3.1 | API latency p95 | ≤ 200 ms | §Latency Targets | 9 | `internal/api` + Redis + `cmd/ratesengine-sla-probe` | (HA + API plans) | ✅ verified — synthetic measurement via the SLA probe (#283); `_p95_breach` alert pages on sustained > 200 ms. | 4 | ⚠ 2026-05-10 — measured p95 = 246 ms across 30 sequential `/v1/price?asset=native&quote=fiat:USD` calls from US East. **Over the 200 ms target by 23%.** Single-region cap; multi-region routing should improve. |
| F3.2 | API latency p99 | ≤ 500 ms | §Latency Targets | 9 | same | same | ✅ verified — same probe; `_unit_failed_alert` umbrella covers p99 breaches (specific p99 alert is a follow-up if the umbrella fires often). | 4 | ✅ 2026-05-10 — measured p99 = 250 ms across same sample (well under 500 ms target) |
| F3.3 | Responsiveness | ≥ 99.9 % | §Availability | 8–9 | HA plan + `cmd/ratesengine-sla-probe` | (HA plan) | ⚠ caveat — synthetic 2xx-success-rate measured per probe run; 99.9% target needs production traffic to verify operationally. The HA topology (ADR-0008) is what backs the number. | 3 | ⚠ 2026-05-10 — needs ≥ 30 days production data + multi-region (currently single-region R1 only) |
| F3.4 | Data freshness (price) | ≤ 30 s staleness | §Data Freshness | 3 (ingest), 8 (deploy) | `internal/consumer` StreamLive + `cmd/ratesengine-sla-probe` | [data-sources/archival-nodes.md](../discovery/data-sources/archival-nodes.md) | ✅ verified — probe measures `observed_at` freshness against the 30s target; `_freshness_breach` alert pages on sustained > 30 s. | 4 | ✅ 2026-05-10 — `/v1/price/tip.observed_at` is the closed-bucket end (last 5 min); the rolling-window tip is fresh per request |
| F3.5 | SEV-1 detect ≤ 15 min / respond ≤ 30 min | | §Incident Response | 9 | `docs/operations/sev-playbook.md` §2 (Timelines) + alert rules + runbooks | (HA + alerts plans) | ⚠ caveat — playbook §2 is stricter than the F3.5 target (ack ≤5 min, action plan ≤15 min, status update ≤15 min); detection paths shipped (the alerts catalogue at `docs/operations/alerts-catalog.md` lists the per-component signals); 61 runbooks under `docs/operations/runbooks/`; tabletop drill scenarios + writeup template under `docs/operations/drills/`. The ≤15 min detect / ≤30 min respond target is met *operationally* once a real SEV fires — same shape as F3.3 (the structure is shipped; the number is verified by drills + production incidents). | 3 | ⚠ 2026-05-10 — `GET /v1/incidents` returns 1 SEV-2 from 2026-05-10 with full markdown postmortem; structure visible. Operational SEV-1 timing verified by next real incident or scheduled drill. |
| F3.6 | SEV-2 detect ≤ 30 min / respond ≤ 60 min | | same | 9 | same | (HA + alerts plans) | ⚠ caveat — same playbook + drills structure as F3.5 covers SEV-2 with looser thresholds; SEV-2 detect targets met by P2 alert rules' `for:` clauses (typically 5–15 min sustained). Operational verification via the same drill cadence. | 3 | ⚠ 2026-05-10 — see `/v1/incidents` for live SEV-2 record with timeline + lessons-learned |

## Freighter RFP — Coverage

| # | Requirement | Proposal | Week | Owner | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- | ---- |
| F4.1 | Lookup classic + Soroban by contract address | §Asset Identification | 4 | `internal/canonical.ParseAsset` + `internal/api/v1/assets.go` | cross-cutting | `/v1/assets/{id}` accepts native, classic (code:issuer), fiat:CODE, soroban:C-strkey, raw C-strkey. | ✅ verified | 5 | ✅ 2026-05-10 — `GET /v1/assets/CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75` (USDC SAC) → 200 with `type=soroban` |
| F4.2 | Historical retention ≥ 1 year (ideally since inception) | §Historical Data | 2 (scaffold), post-launch (fill) | Timescale + Galexie backfill + `/v1/history/since-inception` | [data-sources/galexie.md](../discovery/data-sources/galexie.md) | Migration 0002 sets retention; `/v1/history/since-inception` shipped against the prices_1mo CAGG. | ✅ verified | 4 | ⚠ 2026-05-10 — endpoint shipped; data on r1 only goes back 7 days (earliest point: 2026-05-03). Backfill not run for the older period. **r1 fails RFP F4.2's "≥ 1 year" today** until backfill executes. |

## Freighter RFP — API characteristics

| # | Requirement | Proposal | Week | Owner | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- | ---- |
| F5.1 | REST or GraphQL | §API Layer | 7 | `internal/api/v1` (REST) | (API design) | REST shipped; OpenAPI spec at `openapi/rates-engine.v1.yaml`. GraphQL not in scope. | ✅ verified | 5 | ✅ 2026-05-10 — `GET /v1/healthz` → 200; OpenAPI live at `https://docs.ratesengine.net` |
| F5.2 | Rate limits ≥ 1000 req/min | §Rate Limits and Throughput | 7 | `internal/ratelimit` + `middleware.RateLimit` | — | F-0008 fixed: authenticated tier uses `api.key_rate_limit_per_min` (default 1000/min); anonymous tier separate at `anon_rate_limit_per_min`. | ✅ verified | 4 | ⚠ 2026-05-10 — anonymous tier returns `x-ratelimit-limit: 60`. RFP says "≥ 1000/min per client" — meets it for keyed clients but anon tier is well under. Worth documenting the tier→limit mapping in the public docs. |
| F5.3 | Bulk / batch query support | §Batch Queries | 7 | `internal/api/v1/price.go handlePriceBatch{,Post}` | — | GET /v1/price/batch (≤100 ids); POST /v1/price/batch (≤1000 ids) shipped. | ✅ verified | 4 | ❌ 2026-05-10 R-005 — endpoint exists but **silently drops USDC** (and any stablecoin-pegged asset). `GET /v1/price/batch?asset_ids=USDC-G…` returns `{data:[]}`. Single-asset `/v1/price?asset=USDC-G…&quote=fiat:USD` works via stablecoin-fiat proxy fallback; the proxy isn't applied in batch. |

## Freighter RFP — Misc requirements

| # | Requirement | Proposal | Week | Owner | Verified by | Status | Conf | Prod |
| - | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- | ---- |
| F6.1 | Price source preference VWAP → TWAP → last trade | §Aggregation Strategy | 5 | `internal/api/v1/price.go` + storage layer | — | `/v1/price` returns vwap (closed-bucket from prices_1m), with last-trade fallback when CAGG has no row; `/v1/twap` shipped for explicit TWAP requests. | ✅ verified | 4 | ✅ 2026-05-10 — `/v1/price` returns `price_type=vwap` with `window_seconds=300`; `/v1/twap` returns time-weighted price for the same window with `flags.triangulated=true` when chained-fiat |
| F6.2 | Quote currency = USD | §Quote Currency Policy | 5 | `internal/api/v1/price.go defaultPriceQuote` + `internal/aggregate/stablecoin.go` | [external-refs/fx-feeds.md](../discovery/external-refs/fx-feeds.md) | Default quote on /v1/price is fiat:USD; stablecoin proxy maps USDC/USDT→USD at aggregator layer. | ✅ verified | 4 | ✅ 2026-05-10 — `/v1/price?asset=native` defaults to `quote=fiat:USD` (stablecoin-proxy fallback applied for the native/fiat:USD synthetic pair) |
| F6.3 | Data aggregation scope = DEXes (Stellar + Soroban) | §Data Ingestion | 2–3 | `internal/sources/*` | cross-cutting | ✅ verified | 5 | ✅ 2026-05-10 — 21 sources in `/v1/sources` (6 DEX + 4 CEX + 2 FX + 4 oracle + 3 aggregator + 1 lending + 1 authority_sanity) |
| F6.4 | "Since Inception" = first recorded trade | §Historical Data | 2 (scaffold), ongoing | backfill orchestrator | [data-sources/stellar-data-lakes.md](../discovery/data-sources/stellar-data-lakes.md) | ✅ verified | 4 | ⚠ 2026-05-10 — endpoint shipped (`/v1/history/since-inception?asset=native&quote=USDC-G…` → 200) but actual earliest data on r1 is **2026-05-03**, not since-inception. Backfill not yet executed. |
| F6.5 | V2 supply data = provider-supplied | §V2 supply | 6 | `internal/supply` (XLM Algorithm 1 + classic Algorithm 2 + SEP-41 Algorithm 3) + per-asset hypertables (migrations 0011–0014) + `cmd/ratesengine-aggregator/main.go::buildSupplyRefreshers` | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md); covered also by F2.4 row above (cross-reference) | ✅ verified — all three algorithms shipped; operator-overridable locked-set subtraction via `supply.Policy.PerAsset`. | 4 | ❌ 2026-05-10 R-006 — same as F2 family; supply data NULL on every asset on r1 today (operator config gap) |

---

---

## Cross-cutting integrity invariants (added post-Phase-1)

The following requirements are not RFP rows but emerged from
technical depth during Phase 5 implementation. Each is captured as
an ADR and binds implementation. **All are launch-blocking** per
operator decision 2026-04-28.

### X1. Archive completeness invariants

| # | Requirement | ADR | Week | Owner | Verified by | Status | Conf | Prod |
| - | ----------- | --- | ---- | ----- | ----------- | ------ | ---- | ---- |
| X1.1 | Primary archive (galexie-archive) — every closed partition has 64,000 files | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `cmd/ratesengine-ops` + `galexie-archive-fill` | bootstrap completed 2026-04-28; all 17 previously-partial partitions filled | ✅ verified | 5 | 📦 ops-only — verified by archive-completeness daemon |
| X1.2 | Primary archive — chain-link integrity for every (N, N+1) | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `cmd/ratesengine-ops verify-archive -tier chain` | `verify_archive_chunks.go` shipped; verifier running on r1 | ✅ verified | 4 | 📦 ops-only |
| X1.3 | Cross-anchor archive (`/srv/history-archive/`) — every checkpoint file present | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `/usr/local/bin/cross-anchor-fill` | bootstrap completed 2026-04-28; 972,652/972,652 files | ✅ verified | 5 | 📦 ops-only |
| X1.4 | Cross-anchor archive — hash matches our LCM at every checkpoint | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `verify-archive -tier checkpoint` | `cmd/ratesengine-ops/main.go::verifyArchiveChunks` (`tier := fs.String("tier", "chain", ...)`); checkpoint mode walks every Stellar history checkpoint hash and compares to galexie LCM | ✅ verified | 4 | 📦 ops-only |
| X1.5 | Daily completeness cron (`archive-completeness verify`) | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `ratesengine-ops archive-completeness verify` + `internal/archivecompleteness/` | check → fix → verify mode shipped; `cmd/ratesengine-ops/main.go::archiveCompletenessVerify` writes Prometheus textfile + JSON report; systemd timer wiring documented in [archive-completeness.md](../operations/archive-completeness.md) | ✅ verified | 4 | ⚠ 2026-05-10 — code shipped + systemd timer files in `deploy/systemd/`; **timer not installed on r1** (operator action — one of the 4 missing systemd timers) |
| X1.6 | Per-region asymmetric trust model (R1 leader, R2/R3 delegate) | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | each region's binary; flag-controlled via `-tier` selection | [ADR-0016](../adr/0016-per-region-storage-strategy.md) + [archive-completeness.md](../operations/archive-completeness.md) §"Per-region behaviour"; R1 runs Tier A+B+D, R2/R3 run periodically as defence-in-depth | ✅ verified | 4 | 📦 ops-only — code wired; **R2/R3 don't exist yet** so the asymmetric pattern isn't operationally demonstrated |
| X1.7 | `verify-archive` hardened: `checkpointsMissed > 0` is hard failure | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `cmd/ratesengine-ops/main.go` | `-fail-on-missed` flag wired (`fs.Bool("fail-on-missed", ...)`); `checkpointsMissed > 0` returns non-zero exit when set, default-on per ADR-0017 X1.7 | ✅ verified | 4 | 📦 ops-only |

### X2. API consistency surfaces (three URLs, three contracts)

| # | Requirement | ADR | Week | Owner | Verified by | Status | Conf | Prod |
| - | ----------- | --- | ---- | ----- | ----------- | ------ | ---- | ---- |
| X2.1 | `/v1/price` — closed-bucket VWAP, cross-region consistent | [ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md) + [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | `internal/api/v1/price.go` | handler shipped (PR #180); CAGGs auto-refresh per `add_continuous_aggregate_policy` calls in migrations/0002. Closed-bucket guarantee holds end-to-end. | ✅ verified | 4 | ✅ 2026-05-10 — `/v1/price?asset=native&quote=fiat:USD` returns `{price, observed_at: closed-bucket end, window_seconds: 300}`. Cross-region byte-identical needs R2/R3 to verify. |
| X2.2 | `/v1/price/tip` — rolling-window VWAP + last-good-price fallback | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | `internal/api/v1/price_tip.go` | [ADR-0018](../adr/0018-api-consistency-surfaces.md); handler + tests shipped | ✅ verified | 4 | ✅ 2026-05-10 — `/v1/price/tip?asset=native&quote=fiat:USD` → 200 with rolling-window value distinct from `/v1/price`'s closed-bucket |
| X2.3 | `/v1/observations` — raw per-source data | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | `internal/api/v1/observations.go` | [ADR-0018](../adr/0018-api-consistency-surfaces.md); handler + tests shipped, `?source=` + `?aggregate=latest` | ✅ verified | 4 | ⚠ 2026-05-10 R-011 — endpoint shipped + 200 response; `/v1/observations?asset=native&quote=fiat:USD` returns `data:[]` because XLM/USD has no direct trades (triangulated). Endpoint should either surface triangulated rows or explain the empty. |
| X2.4 | URL discipline: query params MUST NOT change consistency contract | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | OpenAPI lint + per-handler `reject*TierParams` (e.g. `internal/api/v1/observations.go::rejectObservationsTierParams`) | [ADR-0018](../adr/0018-api-consistency-surfaces.md) §"URL discipline"; `?granularity=` / `?window_seconds=` 400-rejection tests in each surface's `_test.go` | ✅ verified | 4 | 📦 code-only — verified by per-handler tests + OpenAPI lint in CI |
| X2.5 | Forex factor snap rule for chained-fiat closed-bucket consistency | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 5 | `internal/aggregate/orchestrator/triangulate.go::legPrice`, `internal/storage/timescale/trades.go::FXQuoteAtOrBefore`, `internal/sources/external/registry.go::FXSources` | [ADR-0018](../adr/0018-api-consistency-surfaces.md) §"Forex factor handling" | ✅ verified | 2 | 📦 code-only — orchestrator path; consumer-visible only as `flags.triangulated=true` on chained pairs |
| X2.6 | Streaming endpoints per surface (`/v1/price/stream`, `/v1/price/tip/stream`, `/v1/observations/stream`) | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | `internal/api/v1/{price_stream,price_tip_stream,observations_stream}.go`, `internal/api/streaming` (Hub) | [ADR-0018](../adr/0018-api-consistency-surfaces.md); SSE + heartbeat + last-event-id resumption tests in each surface's `_test.go` | ✅ verified | 4 | ✅ 2026-05-10 — `/v1/price/stream` emits 3-window `price_update` events; `/v1/price/tip/stream` emits `tip_update` every 5s; both with `Last-Event-ID` resumption support (`id:` header) |
| X2.7 | Per-surface application of `flags.stale` semantics | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | each surface handler + `internal/api/v1/envelope.go` | [ADR-0018](../adr/0018-api-consistency-surfaces.md) §"flags.stale semantic"; `/v1/price` sets stale=true on degradation, tip + observations always false | ✅ verified | 4 | ✅ 2026-05-10 — `flags.stale=false` on /v1/price/tip and /v1/observations as expected; /v1/price will set true under degradation (not synthetically reproducible during review) |

### X3. Anomaly response and confidence scoring

| # | Requirement | ADR | Week | Owner | Verified by | Status | Conf | Prod |
| - | ----------- | --- | ---- | ----- | ----------- | ------ | ---- | ---- |
| X3.1 | Per-asset-class threshold defaults (Phase 1 stop-gap) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 5 | `internal/aggregate/anomaly` (class.go, threshold.go, decision.go) + `internal/config/anomaly.go` | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §Phase 1; orchestrator wires `Config.Anomaly` → `Evaluate()` per tick | ✅ verified | 4 | ✅ 2026-05-10 — observable via `/v1/vwap.outliers_filtered` field on every response (currently 0 — no anomalies firing) |
| X3.2 | Per-asset statistical baseline (Phase 2 — `volatility_baseline_1m` CAGG) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | `internal/aggregate/baseline` + `migrations/0007_create_volatility_baseline.up.sql` | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §Phase 2; `cmd/ratesengine-aggregator/main.go` wires `baseline.NewRefresher` on hourly cadence | ✅ verified | 4 | 📦 code-only — visible to operator via aggregator logs / `ratesengine_aggregator_baseline_refresh_total` Prometheus counter |
| X3.3 | Multi-factor confidence score on every published price | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | `internal/aggregate/confidence` (factors.go, score.go) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §"Multi-factor confidence score"; orchestrator caches score at `confidence:<base>:<quote>:<window>` per tick | ✅ verified | 4 | 📦 code-only — score cached internally; not surfaced on the public API today (could be a `flags.confidence` field worth adding) |
| X3.4 | Freeze policy (3-signal AND on closed-bucket only) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | `internal/aggregate/freeze` + `internal/aggregate/orchestrator/phase2_freeze.go` | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §"Freeze policy"; `phase2FreezeFires` AND-combines confidence + z + source-count thresholds | ✅ verified | 4 | ✅ 2026-05-10 — `flags.frozen` field exists on `/v1/price` envelope; not active today on observed pairs |
| X3.5 | Cross-oracle factor (Phase 3 — depends on `internal/divergence/`) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | post-launch | `internal/aggregate/confidence` × `internal/divergence` | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §Phase 3 | ⏳ deferred | 1 | ⏳ deferred |
| X3.6 | Multi-window safeguard against frog-boiling (1d/7d/30d MAD) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | `internal/aggregate/baseline/multi.go` + `migrations/0008_add_multi_window_baseline.up.sql` | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §"Multi-window safeguard"; `MultiBaseline` struct carries Day1/Day7/Day30 baselines | ✅ verified | 4 | 📦 code-only |
| X3.7 | Bootstrap (warmup) policy for new assets | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | `internal/aggregate/baseline/refresh.go` (MinSamples gate) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §"Bootstrap (warmup) policy"; `TestMultiBaseline_PartialBootstrap` + `_FullBootstrap` pin the n<2 fall-through | ✅ verified | 4 | 📦 code-only |
| X3.8 | Operator runbook for freeze events | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | runbook | [anomaly-freeze-engaged.md](../operations/runbooks/anomaly-freeze-engaged.md) | ✅ verified | 4 | 📦 doc-only |

---

## Claim verification — the most load-bearing proposal promises

For each claim below we state the **as-written promise**, what we
**actually verified**, and what remains to close.

### Claim 1 — "Ingestion via Galexie and the Composable Data Platform"

- **As written** (proposal §SDEX): "direct ledger processing…
  primary integration path is Galexie and the Stellar Composable
  Data Platform."
- **Verified**: Galexie's subcommand set, config, captive-core
  integration, filesystem-backend-drops-metadata bug, and zstd
  compression were read from `stellar-galexie` source. CDP SDK
  (`github.com/stellar/go-stellar-sdk/ingest`) path confirmed via
  [data-sources/composable-data-platform.md](../discovery/data-sources/composable-data-platform.md).
- **Closed**: MinIO + Galexie are live on r1
  ([r1-deployment-state.md §Services](../operations/r1-deployment-state.md));
  the captive-core + Galexie co-resident memory profile was
  measured at deploy time (per the `archival-node` ansible role's
  pre-flight checks).
- **Verdict**: ✅ promise keeps.

### Claim 2 — "Reflector is the primary oracle integration"

- **As written** (proposal §Reflector): "Integration via direct
  Soroban contract calls using the SEP-40 interface: `lastprice(…)`,
  `prices(…)`, `twap(…)`, `x_last_price(…)`, `x_prices(…)`,
  `x_twap(…)`."
- **Verified**: Reflector exposes Pulse and Beam contracts with
  `base`, `assets`, `decimals`, `resolution`, `price`, `prices`,
  `lastprice` ([oracles/reflector.md](../discovery/oracles/reflector.md)).
  **`twap` and `x_*` do not exist on Reflector v3.** Event shape
  `["REFLECTOR","update"]` with `Vec<(Val,i128)>` payload verified.
- **Correction filed**: [proposal-corrections.md](../discovery/proposal-corrections.md) —
  we compute TWAP and cross-pair **locally** from Reflector's
  `lastprice`/`prices` output, not via on-chain calls.
- **Verdict**: ✅ promise keeps with the correction — functional
  equivalence is achieved, just in our aggregation layer.

### Claim 3 — "Redstone integration via per-symbol Soroban contracts"

- **As written** (proposal §Redstone): "`readPricesFromContract()`
  calls to the deployed per-symbol feed contracts, using
  `redstone_adapter` as the coordination point. Price data
  `{ price: U256, package_timestamp, write_timestamp }`."
- **Verified**: 19 mainnet feeds enumerated, all per-feed WASM hashes
  identical, `U256` field confirmed in
  `common/src/lib.rs` ([oracles/redstone.md](../discovery/oracles/redstone.md)).
  **Adapter emits events** (topic `"REDSTONE"`, one per batch push) —
  we can subscribe instead of polling.
- **Verdict**: ✅ promise keeps, event stream is a bonus.

### Claim 4 — "Band Protocol via BandChain REST API"

- **As written** (proposal §Band): "Integration will be via the
  BandChain REST API."
- **Verified**: **Band has a native Soroban StandardReference contract
  on mainnet today** — the proposal promise is unnecessarily
  degraded. Pair rate is E18-scaled
  ([oracles/band.md](../discovery/oracles/band.md)).
- **Correction filed**: [proposal-corrections.md](../discovery/proposal-corrections.md) —
  we integrate natively, not via BandChain REST.
- **Verdict**: ✅ we exceed the promise.

### Claim 5 — "Stellar DEX, Soroswap, Aquarius, Blend ingestion"

- **As written** (proposal §Soroban DEXs): list of venues with event
  decoding.
- **Verified**: current repo snapshot ships **6 venues** (SDEX,
  Soroswap, Aquarius, Phoenix, Comet, **Blend**). Blend's auction
  decoder + storage + dispatcher wiring is live (`internal/sources/blend/`,
  registered in `internal/pipeline/dispatcher.go:114`,
  `internal/pipeline/sink.go:98`). The Blend WASM audit's
  Phase 2 per-pool `wasm-history` walk on r1 completed
  2026-05-02 (11 contracts, 3 unique WASMs, no mid-life
  upgrades over the [50457424, 62249727] range), and
  `BackfillSafe=true` is set in
  `internal/sources/external/registry.go` — see
  `docs/operations/wasm-audits/blend.md §"Phase 2 results"`.
  Both live ingest and retroactive backfill replay are now
  enabled. Phoenix's 8-events-per-swap pattern and Soroswap's
  swap+sync correlation were non-obvious and are both captured
  explicitly.
- **Verdict**: ✅ promise exceeded in venue breadth (Phoenix +
  Comet added beyond the proposal's list); Blend live with the
  WASM audit closed.

### Claim 6 — "p95 ≤ 200 ms, p99 ≤ 500 ms, ≥ 99.99% uptime"

- **As written** (proposal §Performance SLAs).
- **Verified**: nothing empirically. The pattern (precomputed
  aggregates in Redis + CDN-cacheable historical) is industry-
  standard but our capacity, cache-hit-rate, and cold-cache latency
  are unmeasured.
- **Closure**: [HA plan](ha-plan.md) + [API design](../reference/api-design.md) +
  the k6 load suite at [test/load/](../../test/load/) (scenarios
  pinned, reports archived under `test/load/reports/`).
- **Verdict**: 🧪 plan-credible; the k6 suite produces the
  proof artifacts; full p95 ≤ 200 ms run is L4.5 in the
  launch-readiness backlog.

### Claim 7 — "Since-inception historical coverage"

- **As written** (proposal §Historical Data).
- **Verified**: Galexie can replay from ledger 2; SDF public GCS
  bucket is available as an accelerator
  ([data-sources/galexie.md](../discovery/data-sources/galexie.md) +
  [data-sources/stellar-data-lakes.md](../discovery/data-sources/stellar-data-lakes.md)).
  Backfill throughput unmeasured on our hardware.
- **Closure**: backfill is operator-driven via `ratesengine-ops
  backfill` (`cmd/ratesengine-ops/backfill.go`); query performance
  on the resulting data set is exercised by the
  [test/load/](../../test/load/) k6 suite.
- **Verdict**: ✅ promise is feasible; duration unknown.

### Claim 8 — "Open source, provider-supplied deployment kits"

- **As written** (proposal §Open Source & Deployment Model).
- **Verified**: Apache-2.0 LICENSE committed.
  [`deploy/docker-compose/`](../../deploy/docker-compose/) is the
  developer / reference deployment;
  [`deploy/systemd/`](../../deploy/systemd/) +
  [`configs/ansible/`](../../configs/ansible/) are the production
  deployment kit (per ADR-0008 — bare-metal + systemd, not
  Kubernetes).
- **Verdict**: 🧪 lifecycle on track.

---

## Gap triage — the "must-close-before-launch" list

**Operator decision 2026-04-28: every outstanding item is
launch-blocking.** No "soft gap" / "post-launch" deferrals
beyond items explicitly marked ⏳ (DIA mainnet, 99.99% production
measurement, Phase 3 cross-oracle).

Ranked by remaining work, not blast radius. See
[`docs/architecture/launch-readiness-backlog.md`](launch-readiness-backlog.md)
for the canonical work list with effort estimates and dependencies.

### Closed since Phase 1

**Phase 1-era closures (matrix-ratification 2026-04-22):**

- **F1.6 SEP-1 home-domain resolution** — was open gap; landed in
  PR #192 (overlay handler) + PR #190 scaffolding.
- **S6.5 / S7 Retention policy** — was hard gap; landed in
  `migrations/0002_create_price_aggregates.up.sql`.
- **F3.5 / F3.6 SEV runbooks** — was hard gap; sev-playbook.md
  shipped 2026-04-22, individual runbooks growing per
  alerts-catalog.md.
- **F2.4 circulating-supply policy** — was open; ratified in
  ADR-0011 2026-04-27.
- **X1.1 / X1.3 Archive completeness bootstrap** — added post-Phase-1
  per ADR-0017; bootstrapped on R1 2026-04-28.

**Implementation closures (verified 2026-04-30 against current
code state — these were on the "Open" list but had shipped):**

- **S4.1–S4.4 VWAP/TWAP impl + USD volume + thresholds** —
  `internal/aggregate/{vwap,twap,ohlc,orchestrator}.go` shipped
  with `prices_*` CAGGs backing the API.
- **S8.1–S8.2 USD volume column + FX anchor** —
  `internal/aggregate/{triangulate,stablecoin}.go` + the
  `volume_usd` column in `trades` hypertable.
- **F2.4 circulating-supply impl** — three-domain split (XLM /
  classic / SEP-41) shipped through Tasks #54-#57; aggregator
  refresher wired.
- **S3.7 CEX connectors (Binance / Coinbase / Kraken / Bitstamp)** —
  all four in `internal/sources/external/` with
  `BackfillSafe: true`.
- **S2.4 Chainlink HTTP cross-check** — `ChainlinkReference`
  shipped in #282; live in `internal/divergence/chainlink.go`.
- **S1.4 Asset enumeration / discovery** —
  `internal/canonical/discovery/` package live with sniffer +
  recorder.
- **X2.2 `/v1/price/tip` + last-good-price fallback** —
  `internal/api/v1/price_tip.go` (302 LoC) + stream variant.
- **X2.3 `/v1/observations` per-source raw** —
  `internal/api/v1/observations.go` (205 LoC) + stream variant.
- **X2.6 Streaming endpoints (×4)** — `/v1/price/stream`,
  `/v1/price/tip/stream`, `/v1/observations/stream`,
  `/v1/chart`. All four shipped under `internal/api/v1/`.
- **X3.1 Phase 1 anomaly thresholds** —
  `internal/aggregate/anomaly/` package live.
- **X3.2–X3.7 Phase 2 statistical baseline + freeze** —
  `internal/aggregate/{baseline,confidence,freeze}/` packages
  shipped (3,474 LoC including tests). Multi-window baseline
  columns landed in `migrations/0008_add_multi_window_baseline.up.sql`.
- **F5.3 Batch / bulk-query endpoint** — `/v1/price/batch`
  handler in `internal/api/v1/price.go` + `price_batch_test.go`.
- **#2 SEP-10 protocol implementation** —
  `internal/auth/sep10/{validator,jwt}.go` shipped via PR #196.
- **#9 `pkg/client/` Go SDK skeleton** — full client with types,
  endpoints, errors.
- **#10 Generated API reference (`make docs-api`)** — Redocly
  pipeline live; HTML regenerated per OpenAPI change (drift
  detected by CI's `openapi lint` job).
- **#24 `internal/divergence/` package** — chainlink + coingecko
  + compare + worker + reference all shipped.
- **X1.5 archive-completeness daemon** — `cmd/ratesengine-ops`
  has full set of subcommands: `backfill`, `cross-region-check`,
  `cross-region-monitor`, `discovery`, `hubble-check`,
  `hubble-soroban-events`.
- **X1.7 verify-archive `-fail-on-missed`** — flag exists in
  `cmd/ratesengine-ops/main.go`.
- **#21 CHANGELOG + SemVer policy** — `CHANGELOG.md` +
  `docs/architecture/semver-policy.md` shipped.
- **#23 Release-notes template** — `.github/RELEASE_NOTES_TEMPLATE.md`.
- **#26 Envelope flag retrofit** — `internal/api/v1/envelope.go`
  exposes `stale`, `reduced_redundancy`, `triangulated`,
  `divergence_warning` flags (S5.4 verified).
- **#17–#18 k6 load test suite (Task #74)** — `test/load/`
  scaffold + 7 scenarios (price/vwap-twap/history/batch/stream/
  mixed-realistic/spike) + AlertManager-silence integration +
  weekly GitHub Actions schedule shipped via PRs #345/#346/#347/
  #348. Companion design note at
  `docs/architecture/k6-load-tests-design-note.md`. The remaining
  S9.2 work is the operator-side first end-to-end run against
  staging and the `sla-proof-2026-MM-DD.md` artefact (Task #77).
- **Patroni ansible role (Task #72 launch-critical sub-role)** —
  shipped via PR #344. Implements the topology pinned in
  `ha-plan §3.3` (1 primary + 2 sync replicas, 3-node etcd
  quorum). Companion design note at
  `docs/architecture/patroni-ansible-role-design-note.md`. Other
  sub-roles (Redis Sentinel, HAProxy, Prometheus, Loki) remain
  open under Task #72.

### Open — implementation pending

Re-baselined 2026-04-30 against current code state. Twenty-one
items previously listed here have shipped — their evidence is
now in *Closed since Phase 1* above.

| Area | Item | Owner | Week | Effort |
|---|---|---|---|---|
| Operations | Public status page at `status.ratesengine.net` | infra | 9 | half-day |
| Validation | S9.2 p95 ≤ 200 ms proof report — k6 suite shipped (#345/#346/#347/#348); operator-side first run + `sla-proof-2026-MM-DD.md` artefact remaining | `docs/operations/sla-proof-template.md` | 9 | ~half-day operator |
| Validation | #19 Chaos suite Wave 2 (HA-shaped scenarios on staging baremetal — Patroni replica promotion, Sentinel failover, HAProxy VIP flip). Wave 1 (dev-stack smoke) shipped #366 | `test/chaos` | 9 | ~1 day post-launch |
| Validation | #20 SEV-1/SEV-2 dry-run — playbook exists, dry-run record doesn't | runbooks | 9 | half-day |
| Finalization | #22 Public-flip prep — `public-flip.md` exists; checklist completion is operator-side | repo strategy | 10 | hour planning |
| Connectors / Audit | Task #53 Blend Pool Factory walk on r1 (Phase 2 of audit) | `cmd/ratesengine-ops wasm-history` | — | ~5 h operator |

### Watch (post-launch only — explicitly accepted)

1. **S2.5 DIA mainnet ship** — testnet only today; integration
   conditional on DIA's mainnet launch.
2. **S9.1 99.99 % uptime measurement** — needs ≥30 days production
   to measure. Architecture credible at launch; number reported
   90 days post-launch.
3. **X3.5 Phase 3 cross-oracle factor** — depends on
   `internal/divergence/` shipping; nominal post-launch unless
   schedule allows pulling it forward.

---

## Verification protocol

Every row above marked `✅ verified` was verified by one of these
methods. If a reviewer disputes a cell, re-run the listed verification
step.

| Method | How |
| ------ | --- |
| **Source read** | Cloned the repo into `.discovery-repos/`, opened the file, verified the claim against the code. File path cited in the linked discovery doc. |
| **Protocol spec read** | Read the SEP / CAP markdown in `stellar-protocol/`. Section cited in the linked discovery doc. |
| **On-chain verification** | Queried stellar.expert's public API or a direct RPC call against mainnet. Contract + WASM hash recorded. |
| **Test** | Go test in `internal/*_test.go` exercises the claim with a fixture. KALIEN i128 regression in `internal/canonical/amount_test.go` is the canonical example. |
| **External doc (weaker)** | WebFetch of an SDF / project-maintained reference doc. Only acceptable where the doc itself is primary (e.g. `stellar-docs/networks/software-versions.mdx`). |

Rows marked `🧪 designed` are pattern-credible but not exercised end
to end. They are expected to convert to `✅ verified` as the owner
week lands.

---

## Change log for this matrix

- **2026-04-22** — Initial ratification alongside `phase1-closure.md`.
  All "Status" and "Confidence" values are as-of today.
- **2026-04-28** — Added "Cross-cutting integrity invariants"
  section (X1 archive completeness from ADR-0017, X2 API consistency
  surfaces from ADR-0018, X3 anomaly response from ADR-0019).
  Refreshed gap-triage to reflect operator decision that all
  outstanding items are launch-blocking; closed F1.6, S6.5/S7,
  F3.5/F3.6, F2.4 against shipped work; canonical backlog moved to
  `launch-readiness-backlog.md`.
- **2026-05-10** — **Production-verification column added.**
  Every requirement row now has a `Prod` column with curl-tested
  status against `https://api.ratesengine.net` v0.5.0-rc.39.
  The pass surfaced 23 production findings (5 ❌ contract-disagreement,
  13 ⚠ caveat) documented in
  [`../review-2026-05-10.md`](../review-2026-05-10.md) §Section 2
  with reproducible curl commands in §Appendix B. Headline ❌s:
  R-005 (`/v1/price/batch` silent-drops stablecoins),
  R-006 (F2 supply fields universally NULL — operator config gap),
  R-007 (`/v1/ohlc.high = $1` for XLM via stablecoin-proxy contamination),
  R-008 (ATH disagrees between `/v1/coins` and `/v1/changes`),
  R-009 (SEP-10 returns 503 on r1 — server signing seed not configured).
  No ❌ blocks the API entirely; each is a fixable bug or operator
  config gap.
