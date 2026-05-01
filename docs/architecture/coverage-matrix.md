---
title: RFP × Proposal × Delivery — Coverage Matrix
last_verified: 2026-05-01
status: ratified
---

# RFP × Proposal × Delivery Coverage Matrix

**Ratified:** 2026-04-22.
**Re-baselined:** 2026-04-30 + **incremental re-baseline 2026-05-01**
— rows X1.2, X1.4–X1.7, X2.2–X2.4, X2.6–X2.7, X3.1–X3.4, X3.6–X3.7,
F6.5 flipped from `🧪 designed` to `✅ verified` after walking the
codebase: `internal/api/v1/{price_tip,observations,price_stream,
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

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S1.1 | Classic assets identity (code+issuer) | §Data Ingestion / SDEX | 2 | `internal/sources/sdex` | — | [protocol-versions.md](../discovery/protocol-versions.md), [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md) | ✅ verified | 5 |
| S1.2 | SEP-41 Soroban tokens — events ingest | §Data Ingestion / Soroban DEXs | 3 | `internal/sources/soroswap`, `/aquarius`, etc. | — | [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md) | ✅ verified | 5 |
| S1.3 | SAC-wrapped classic (native XLM SAC = `CAS3J7…OWMA`) | §Data Ingestion / SDEX | 3 | `internal/canonical` + sources | — | [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md), [dexes-amms/aquarius.md](../discovery/dexes-amms/aquarius.md) | ✅ verified | 4 |
| S1.4 | Asset enumeration / discovery | §Asset Identification | 4 | `internal/canonical/discovery` | — | [data-sources/withobsrvr-stellar-extract.md](../discovery/data-sources/withobsrvr-stellar-extract.md) | ✅ verified | 4 |
| S1.5 | i128/u128 amounts never truncate | §Data Processing | 1 | `internal/canonical.Amount` | ADR-0003 | Tested: `amount_test.go` KALIEN regression | ✅ verified | 5 |

### S2. Oracle coverage — Chainlink, Redstone, Band, Reflector + others

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S2.1 | Reflector (3 contracts: DEX/CEX/FX) | §Oracle Networks — Reflector | 4 | `internal/sources/reflector` | — | [oracles/reflector.md](../discovery/oracles/reflector.md) | ✅ verified | 5 |
| S2.2 | Redstone (Adapter + 19 per-feed proxies) | §Oracle Networks — Redstone | 4 | `internal/sources/redstone` | — | [oracles/redstone.md](../discovery/oracles/redstone.md) | ✅ verified | 5 |
| S2.3 | Band Protocol (native Soroban StandardReference) | §Oracle Networks — Band | 4 | `internal/sources/band` | — | [oracles/band.md](../discovery/oracles/band.md) | ✅ verified | 5 |
| S2.4 | Chainlink (HTTP cross-check until Scale ships) | §Oracle Networks — Chainlink | 4 | `internal/divergence/chainlink.go` | — | [oracles/chainlink.md](../discovery/oracles/chainlink.md) | ✅ verified — `ChainlinkReference` shipped in #282. `eth_call` against `latestAnswer()` selector `0x50d25bcd`; two's-complement int256 decode; optional inversion. Used as divergence cross-check, NOT a VWAP contributor. | 4 |
| S2.5 | "And others" — DIA (if mainnet ships in window) | (not in proposal; adding) | 4–post-launch | `internal/sources/dia` | — | [oracles/dia.md](../discovery/oracles/dia.md) | ⏳ deferred | 2 |
| S2.6 | SEP-40-compat output (others consume *our* prices) | §API | 7 | `internal/api/v1/oracle_sep40.go` | — | [oracles/reflector.md](../discovery/oracles/reflector.md) §SEP-40 interface | ✅ verified — `/v1/oracle/{lastprice,prices,x_last_price}` SEP-40-shaped passthrough endpoints shipped | 4 |

### S3. Price aggregation — Soroswap, Aquarius, SDEX, Comet + others

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S3.1 | SDEX trades via ClaimAtom parsing | §Stellar Classic DEX | 2 | `internal/sources/sdex` | — | [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md) | ✅ verified | 5 |
| S3.2 | Soroswap factory+pair+router events | §Soroban DEXs / Soroswap | 3 | `internal/sources/soroswap` | — | [dexes-amms/soroswap.md](../discovery/dexes-amms/soroswap.md) | ✅ verified | 5 |
| S3.3 | Aquarius 3 pool types | §Soroban DEXs / Aquarius | 3 | `internal/sources/aquarius` | — | [dexes-amms/aquarius.md](../discovery/dexes-amms/aquarius.md) | ✅ verified | 5 |
| S3.4 | Phoenix DEX (8-events-per-swap) | §Soroban DEXs (added post-discovery) | 3 | `internal/sources/phoenix` | — | [dexes-amms/phoenix.md](../discovery/dexes-amms/phoenix.md) | ✅ verified | 5 |
| S3.5 | Comet (Balancer-weighted AMM) | §Soroban DEXs (added post-discovery) | 3 | `internal/sources/comet` | — | [dexes-amms/comet.md](../discovery/dexes-amms/comet.md) | ✅ verified | 4 |
| S3.6 | Blend auctions as directional signal | §Soroban DEXs / Blend | 5 | `internal/sources/blend` | — | [dexes-amms/blend.md](../discovery/dexes-amms/blend.md), [wasm-audits/blend.md](../operations/wasm-audits/blend.md) | ⚠ caveat — auction decoder + storage + dispatcher wiring shipped (#273-#275); WASM audit (Pool Factory + per-pool walks) pending in Task #53. BackfillSafe stays false until that lands. | 3 |
| S3.7 | CEX trade ingestion (Binance, Coinbase, Kraken, …) | §Centralized Exchanges | 4 | `internal/sources/external/*` | — | [external-refs/cex-feeds.md](../discovery/external-refs/cex-feeds.md) | ✅ verified | 4 |

### S4. VWAP + configurable USD volume threshold

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S4.1 | Volume-weighted aggregation across venues | §Aggregation Strategy | 5 | `internal/aggregate/orchestrator` + `prices_*` CAGGs | — | `cmd/ratesengine-aggregator` running per-window VWAP refresh; CAGGs back the API price reader. | ✅ verified | 4 |
| S4.2 | USD-denominated volume on non-USD pairs | §Cross-Pair Derivation | 5 | `internal/aggregate/orchestrator/triangulate.go` + provenance marker | — | Triangulation worker writes implied VWAPs + `:provenance` marker (#279); API serves them with `flags.triangulated=true` (#280). | ✅ verified | 4 |
| S4.3 | Per-pair configurable min USD volume | §Security — manipulation | 5 | `internal/config` schema + `internal/aggregate/orchestrator` | — | `aggregate.min_usd_volume` config field consumed by orchestrator; backed by `prices_1m.volume_usd`. | ✅ verified | 4 |
| S4.4 | TWAP fallback when volume thresholds not met | §Aggregation Strategy | 5 | `internal/aggregate/orchestrator` + `internal/api/v1/twap.go` | — | TWAP endpoint `/v1/twap` shipped; aggregator computes via stored bucket VWAPs as a fallback. | ✅ verified | 3 |

### S5. Real-time price endpoints

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S5.1 | Live event ingest (Galexie/MinIO + ledgerstream + dispatcher) | §Real-time — Hot path | 3 | `cmd/ratesengine-indexer` + `internal/ledgerstream` + `internal/dispatcher` + `internal/sources/*` | — | [data-sources/archival-nodes.md](../discovery/data-sources/archival-nodes.md), [ingest-pipeline.md](ingest-pipeline.md) | ✅ verified | 5 |
| S5.2 | ≤ 30s staleness (Freighter SLA) | §Latency Targets | 6 | `cmd/ratesengine-sla-probe` + `deploy/systemd/sla-probe.{service,timer}` | — | [data-sources/archival-nodes.md](../discovery/data-sources/archival-nodes.md) + HA plan | ✅ verified — `ratesengine-sla-probe` measures `observed_at` freshness against the 30s target every 15 min; alerts in `deploy/monitoring/rules/sla-probe.yml` page on sustained breach. | 4 |
| S5.3 | SSE streaming for subscribers | §Streaming Support | 7 | `internal/api/streaming` + `/v1/price/stream`, `/v1/observations/stream`, `/v1/price/tip` | — | Hub + per-topic ring buffer; Last-Event-ID resume. | ✅ verified | 4 |
| S5.4 | Degradation signals (`stale_flag`, `reduced_redundancy`) | §Error Handling and Degradation | 5 | `internal/api/envelope` | — | `envelope.Flags` shipped (stale, reduced_redundancy, triangulated, divergence_warning) | ✅ verified | 3 |

### S6. Historical price endpoints + OHLC

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S6.1 | Since-inception backfill (ledger 2 → today) | §Historical Data | 2 (scaffold), 5 (run) | `cmd/ratesengine-ops backfill` | — | [data-sources/galexie.md](../discovery/data-sources/galexie.md) + [data-sources/stellar-data-lakes.md](../discovery/data-sources/stellar-data-lakes.md) | ✅ verified | 4 |
| S6.2 | Pre-P20 (no-Soroban) coverage via ClaimAtom | §Historical Data | 2 | `internal/sources/sdex` | — | [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md), [protocol-versions.md](../discovery/protocol-versions.md) | ✅ verified | 5 |
| S6.3 | Post-P23 unified events handling | §Historical Data | 2 | `internal/sources/sdex` | — | [notes/cap-67-unified-events.md](../discovery/notes/cap-67-unified-events.md) | ✅ verified | 5 |
| S6.4 | OHLC continuous aggregates | §Historical — storage | 4 | `internal/storage/timescale` + migrations | ADR-0006 | migrations/0002 creates prices_{1m,15m,1h,4h,1d,1w,1mo} CAGGs; covered by test/integration/migrations_test.go. OHLC fields in CAGGs still need aggregator binary to populate at runtime. | ⚠ caveat | 3 |
| S6.5 | Retention: 1h+ granularity indefinite; <1h capped | §Historical — retention | 4 | Timescale retention policies | ADR-0006 | migrations/0002 wires retention policies per CAGG; covered by TestMigrationsRoundTrip + policy-attachment assertions. | ✅ verified | 4 |

### S7. Supported timeframes (1h / 24h / 1w / 1mo / 1yr / all-time)

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S7.1 | 1m / 15m / 1h / 4h / 1d / 1w / 1mo granularities | Verbatim in §Historical Data | 4 | Timescale continuous aggregates | ADR-0006 | migrations/0002 ships all 7 CAGGs; verified by TestMigrationsRoundTrip. | ✅ verified | 4 |
| S7.2 | 1h+ kept indefinitely, <1h capped | Verbatim in §Historical Data | 4 | Timescale retention | ADR-0006 | migrations/0002 adds 30-day retention only on prices_1m + prices_15m; hourly+ have no retention = indefinite. Verified by assertPolicyAttached in migrations_test.go. | ✅ verified | 4 |

### S8. Base and quote volume in USD

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S8.1 | `usd_volume` column per trade | §Data Processing | 3 | `internal/canonical.Trade` + `migrations/0001_create_trades_hypertable.up.sql` | — | Column shipped in trades hypertable; CAGGs sum it via `volume_usd`. | ✅ verified | 4 |
| S8.2 | FX anchor for USD conversion | §Forex Providers | 4 | `internal/sources/external/{exchangeratesapi,polygonforex}` + `internal/aggregate/stablecoin.go` | — | Stablecoin proxy at aggregator layer (USDC/USDT→USD); FX vendors wired in registry. | ✅ verified | 4 |

### S9. Performance SLAs

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S9.1 | ≥ 99.99 % uptime | §Availability | 8–9 | HA plan + `cmd/ratesengine-sla-probe` | [ADR-0008](../adr/0008-ha-topology.md) | (HA plan) | ⚠ caveat — synthetic 2xx-success-rate gate shipped (#283 + #290 + #294); 99.99% target needs production + multi-region traffic to verify operationally. The probe surfaces the signal; the HA topology is what backs the number. | 3 |
| S9.2 | p95 ≤ 200 ms, p99 ≤ 500 ms | §Latency Targets | 9 | `internal/api` + Redis caching + `cmd/ratesengine-sla-probe` | [ADR-0009](../adr/0009-latency-budget.md) | (API design + HA plan) | ✅ verified — synthetic measurement shipped via the SLA probe (#283); RFP-stated targets baked into `default*Target` constants; alerts page on sustained breach. | 4 |
| S9.3 | 1000 req/min per client | §Rate Limits | 7 | `internal/ratelimit` + `internal/api/v1/middleware/ratelimit.go` | — | Authenticated tier wired to `api.key_rate_limit_per_min` per F-0008 fix; anon + key buckets are now distinct. | ✅ verified | 4 |
| S9.4 | Defined degradation when prices unavailable | §Degradation Strategy + divergence | 5 | `internal/divergence/{coingecko,coinmarketcap,cryptocompare}.go` + `internal/api/v1/envelope.go` | — | Divergence service runs against three aggregators; `flags.divergence_warning` surfaces on /v1/price. Chainlink path remains unimplemented (S2.4). | ⚠ caveat | 3 |

### S10. Open source

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S10.1 | Apache-2.0, fully open | §Open Source & Deployment Model | 1 | `LICENSE` in repo root | — | LICENSE committed | ✅ verified | 5 |

---

## Freighter RFP — V1: Asset metadata

| # | Field | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| F1.1 | Asset/Token Code | §Asset Identification | 4 | `internal/metadata` | — | [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md), [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md) | ✅ verified | 5 |
| F1.2 | Current Price (USD) | §Current Price API | 5 | `internal/api/v1/price.go` | — | `/v1/price?asset=…&quote=fiat:USD` shipped; reads from `prices_1m` CAGG (closed-bucket per ADR-0015) with last-trade fallback. Default quote is USD. | ✅ verified | 5 |
| F1.3 | Asset Type enum (`classic`/`soroban`) | §Asset Identification | 4 | `pkg/types.AssetType` | — | [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md) | ✅ verified | 5 |
| F1.4 | Issuer Address (G…) | §Asset Identification | 4 | `pkg/types.ClassicAsset` | — | [protocol-versions.md](../discovery/protocol-versions.md) | ✅ verified | 5 |
| F1.5 | Contract Address (C…) | §Asset Identification | 4 | `pkg/types.SorobanAsset` | — | [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md) | ✅ verified | 5 |
| F1.6 | Home Domain (SEP-1) | §Asset Identification (needs proposal amendment) | 5 | `internal/metadata` + `internal/api/v1/assets.go applySep1Overlay` | [ADR-0007](../adr/0007-redis-cache-schema.md) | [data-sources/sep1-home-domain.md](../discovery/data-sources/sep1-home-domain.md) | Resolver + cache + overlay all shipped; AssetDetail surfaces sep1_status, name, description, image, org_name, anchor_asset, anchor_asset_type. | ✅ verified | 5 |

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

| # | Field | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| F2.1 | Market Cap = `circulating × price` | §V2 (addendum) | 6 | `internal/api/v1/assets_f2.go populateMarketCap` + supply pipeline | [ADR-0011](../adr/0011-supply-algorithm.md), [ADR-0021](../adr/0021-account-entry-observer.md), [ADR-0022](../adr/0022-classic-supply-observers.md), [ADR-0023](../adr/0023-sep41-supply-observer.md) | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md) | ✅ verified — read path (#277) + writer end-to-end across all three asset classes: XLM (#285), classic credits (#303-#307), SEP-41 (#309-#312). The aggregator-resident refresher (#301) populates `asset_supply_history` per watched asset on the configured cadence. `market_cap_usd` populates when both supply + USD price exist. **Scope: XLM + watched classic + watched SEP-41 (operator config).** | 4 |
| F2.2 | FDV = `max_supply × price` | §V2 | 6 | `internal/api/v1/assets_f2.go populateMarketCap` + supply pipeline | [ADR-0011](../adr/0011-supply-algorithm.md) | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md) | ✅ verified — same pipeline as F2.1; `fdv_usd` populates when `max_supply` is non-null (uncapped issuers without SEP-1 declaration leave it null per ADR-0011 "we don't fabricate"). | 4 |
| F2.3 | 24h Trading Volume (USD) | §V2 | 6 | `internal/storage/timescale.Volume24hUSDForAsset` + `internal/api/v1/assets.go` | ADR-0007 | `volume_24h_usd` field on `/v1/assets/{id}` (#278). Reads from `prices_1m` CAGG. | ✅ verified | 4 |
| F2.4 | Circulating Supply (provider-supplied) | §V2 | 6 | `internal/supply/{xlm,classic,sep41}.go` + observers + `cmd/ratesengine-aggregator/main.go::buildSupplyRefreshers` | [ADR-0011](../adr/0011-supply-algorithm.md), [ADR-0021](../adr/0021-account-entry-observer.md), [ADR-0022](../adr/0022-classic-supply-observers.md), [ADR-0023](../adr/0023-sep41-supply-observer.md) | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md) | ✅ verified — XLM (Algorithm 1), classic credit (Algorithm 2), SEP-41 (Algorithm 3) all live. Operator-locked-set subtraction supported per asset via `supply.Policy.PerAsset`. | 4 |
| F2.5 | Total Supply (mint − burn − clawback) | §V2 | 6 | `internal/sources/sep41_supply` observer + `internal/supply/storage_sep41_reader.go` | [ADR-0011](../adr/0011-supply-algorithm.md), [ADR-0023](../adr/0023-sep41-supply-observer.md) | [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md) | ✅ verified — SEP-41 mint/burn/clawback events accumulate into `sep41_supply_events` (#309); the reader composes per-kind sums via `Σ FILTER (WHERE ...)` (#311) and the aggregator refreshes one snapshot per watched contract per cycle (#312). Classic + XLM totals via the same algorithm-correct path. | 4 |
| F2.6 | Max Supply (nullable, off-chain metadata) | §V2 | 6 | `internal/supply/overlay.go` + `internal/metadata` | [ADR-0011](../adr/0011-supply-algorithm.md) | [data-sources/sep1-home-domain.md](../discovery/data-sources/sep1-home-domain.md) | ✅ verified — overlay policy implemented + integrated end-to-end. Per ADR-0011, `max_supply` stays null for uncapped issuers without SEP-1 declaration / operator override; consumers handle null explicitly. | 4 |

## Freighter RFP — Performance SLAs

| # | Metric | Requirement | Proposal | Week | Owner | Verified by | Status | Conf |
| - | ------ | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- |
| F3.1 | API latency p95 | ≤ 200 ms | §Latency Targets | 9 | `internal/api` + Redis + `cmd/ratesengine-sla-probe` | (HA + API plans) | ✅ verified — synthetic measurement via the SLA probe (#283); `_p95_breach` alert pages on sustained > 200 ms. | 4 |
| F3.2 | API latency p99 | ≤ 500 ms | §Latency Targets | 9 | same | same | ✅ verified — same probe; `_unit_failed_alert` umbrella covers p99 breaches (specific p99 alert is a follow-up if the umbrella fires often). | 4 |
| F3.3 | Responsiveness | ≥ 99.9 % | §Availability | 8–9 | HA plan + `cmd/ratesengine-sla-probe` | (HA plan) | ⚠ caveat — synthetic 2xx-success-rate measured per probe run; 99.9% target needs production traffic to verify operationally. The HA topology (ADR-0008) is what backs the number. | 3 |
| F3.4 | Data freshness (price) | ≤ 30 s staleness | §Data Freshness | 3 (ingest), 8 (deploy) | `internal/consumer` StreamLive + `cmd/ratesengine-sla-probe` | [data-sources/archival-nodes.md](../discovery/data-sources/archival-nodes.md) | ✅ verified — probe measures `observed_at` freshness against the 30s target; `_freshness_breach` alert pages on sustained > 30 s. | 4 |
| F3.5 | SEV-1 detect ≤ 15 min / respond ≤ 30 min | | §Incident Response (needs runbook) | 9 | `docs/operations/sev-playbook.md` | (pending) | ⏳ deferred | 1 |
| F3.6 | SEV-2 detect ≤ 30 min / respond ≤ 60 min | | same | 9 | same | (pending) | ⏳ deferred | 1 |

## Freighter RFP — Coverage

| # | Requirement | Proposal | Week | Owner | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- |
| F4.1 | Lookup classic + Soroban by contract address | §Asset Identification | 4 | `internal/canonical.ParseAsset` + `internal/api/v1/assets.go` | cross-cutting | `/v1/assets/{id}` accepts native, classic (code:issuer), fiat:CODE, soroban:C-strkey, raw C-strkey. | ✅ verified | 5 |
| F4.2 | Historical retention ≥ 1 year (ideally since inception) | §Historical Data | 2 (scaffold), post-launch (fill) | Timescale + Galexie backfill + `/v1/history/since-inception` | [data-sources/galexie.md](../discovery/data-sources/galexie.md) | Migration 0002 sets retention; `/v1/history/since-inception` shipped against the prices_1mo CAGG. | ✅ verified | 4 |

## Freighter RFP — API characteristics

| # | Requirement | Proposal | Week | Owner | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- |
| F5.1 | REST or GraphQL | §API Layer | 7 | `internal/api/v1` (REST) | (API design) | REST shipped; OpenAPI spec at `openapi/rates-engine.v1.yaml`. GraphQL not in scope. | ✅ verified | 5 |
| F5.2 | Rate limits ≥ 1000 req/min | §Rate Limits and Throughput | 7 | `internal/ratelimit` + `middleware.RateLimit` | — | F-0008 fixed: authenticated tier uses `api.key_rate_limit_per_min` (default 1000/min); anonymous tier separate at `anon_rate_limit_per_min`. | ✅ verified | 4 |
| F5.3 | Bulk / batch query support | §Batch Queries | 7 | `internal/api/v1/price.go handlePriceBatch{,Post}` | — | GET /v1/price/batch (≤100 ids); POST /v1/price/batch (≤1000 ids) shipped. | ✅ verified | 4 |

## Freighter RFP — Misc requirements

| # | Requirement | Proposal | Week | Owner | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- |
| F6.1 | Price source preference VWAP → TWAP → last trade | §Aggregation Strategy | 5 | `internal/api/v1/price.go` + storage layer | — | `/v1/price` returns vwap (closed-bucket from prices_1m), with last-trade fallback when CAGG has no row; `/v1/twap` shipped for explicit TWAP requests. | ✅ verified | 4 |
| F6.2 | Quote currency = USD | §Quote Currency Policy | 5 | `internal/api/v1/price.go defaultPriceQuote` + `internal/aggregate/stablecoin.go` | [external-refs/fx-feeds.md](../discovery/external-refs/fx-feeds.md) | Default quote on /v1/price is fiat:USD; stablecoin proxy maps USDC/USDT→USD at aggregator layer. | ✅ verified | 4 |
| F6.3 | Data aggregation scope = DEXes (Stellar + Soroban) | §Data Ingestion | 2–3 | `internal/sources/*` | cross-cutting | ✅ verified | 5 |
| F6.4 | "Since Inception" = first recorded trade | §Historical Data | 2 (scaffold), ongoing | backfill orchestrator | [data-sources/stellar-data-lakes.md](../discovery/data-sources/stellar-data-lakes.md) | ✅ verified | 4 |
| F6.5 | V2 supply data = provider-supplied | §V2 supply | 6 | `internal/supply` (XLM Algorithm 1 + classic Algorithm 2 + SEP-41 Algorithm 3) + per-asset hypertables (migrations 0011–0014) + `cmd/ratesengine-aggregator/main.go::buildSupplyRefreshers` | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md); covered also by F2.4 row above (cross-reference) | ✅ verified — all three algorithms shipped; operator-overridable locked-set subtraction via `supply.Policy.PerAsset`. | 4 |

---

---

## Cross-cutting integrity invariants (added post-Phase-1)

The following requirements are not RFP rows but emerged from
technical depth during Phase 5 implementation. Each is captured as
an ADR and binds implementation. **All are launch-blocking** per
operator decision 2026-04-28.

### X1. Archive completeness invariants

| # | Requirement | ADR | Week | Owner | Verified by | Status | Conf |
| - | ----------- | --- | ---- | ----- | ----------- | ------ | ---- |
| X1.1 | Primary archive (galexie-archive) — every closed partition has 64,000 files | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `cmd/ratesengine-ops` + `galexie-archive-fill` | bootstrap completed 2026-04-28; all 17 previously-partial partitions filled | ✅ verified | 5 |
| X1.2 | Primary archive — chain-link integrity for every (N, N+1) | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `cmd/ratesengine-ops verify-archive -tier chain` | `verify_archive_chunks.go` shipped; verifier running on r1 | ✅ verified | 4 |
| X1.3 | Cross-anchor archive (`/srv/history-archive/`) — every checkpoint file present | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `/usr/local/bin/cross-anchor-fill` | bootstrap completed 2026-04-28; 972,652/972,652 files | ✅ verified | 5 |
| X1.4 | Cross-anchor archive — hash matches our LCM at every checkpoint | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `verify-archive -tier checkpoint` | `cmd/ratesengine-ops/main.go::verifyArchiveChunks` (`tier := fs.String("tier", "chain", ...)`); checkpoint mode walks every Stellar history checkpoint hash and compares to galexie LCM | ✅ verified | 4 |
| X1.5 | Daily completeness cron (`archive-completeness verify`) | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `ratesengine-ops archive-completeness verify` + `internal/archivecompleteness/` | check → fix → verify mode shipped; `cmd/ratesengine-ops/main.go::archiveCompletenessVerify` writes Prometheus textfile + JSON report; systemd timer wiring documented in [archive-completeness.md](../operations/archive-completeness.md) | ✅ verified | 4 |
| X1.6 | Per-region asymmetric trust model (R1 leader, R2/R3 delegate) | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | each region's binary; flag-controlled via `-tier` selection | [ADR-0016](../adr/0016-per-region-storage-strategy.md) + [archive-completeness.md](../operations/archive-completeness.md) §"Per-region behaviour"; R1 runs Tier A+B+D, R2/R3 run periodically as defence-in-depth | ✅ verified | 4 |
| X1.7 | `verify-archive` hardened: `checkpointsMissed > 0` is hard failure | [ADR-0017](../adr/0017-archive-completeness-invariants.md) | 8 | `cmd/ratesengine-ops/main.go` | `-fail-on-missed` flag wired (`fs.Bool("fail-on-missed", ...)`); `checkpointsMissed > 0` returns non-zero exit when set, default-on per ADR-0017 X1.7 | ✅ verified | 4 |

### X2. API consistency surfaces (three URLs, three contracts)

| # | Requirement | ADR | Week | Owner | Verified by | Status | Conf |
| - | ----------- | --- | ---- | ----- | ----------- | ------ | ---- |
| X2.1 | `/v1/price` — closed-bucket VWAP, cross-region consistent | [ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md) + [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | `internal/api/v1/price.go` | shipped (PR #180); CAGG population pending | ⚠ caveat | 3 |
| X2.2 | `/v1/price/tip` — rolling-window VWAP + last-good-price fallback | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | `internal/api/v1/price_tip.go` | [ADR-0018](../adr/0018-api-consistency-surfaces.md); handler + tests shipped | ✅ verified | 4 |
| X2.3 | `/v1/observations` — raw per-source data | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | `internal/api/v1/observations.go` | [ADR-0018](../adr/0018-api-consistency-surfaces.md); handler + tests shipped, `?source=` + `?aggregate=latest` | ✅ verified | 4 |
| X2.4 | URL discipline: query params MUST NOT change consistency contract | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | OpenAPI lint + per-handler `reject*TierParams` (e.g. `internal/api/v1/observations.go::rejectObservationsTierParams`) | [ADR-0018](../adr/0018-api-consistency-surfaces.md) §"URL discipline"; `?granularity=` / `?window_seconds=` 400-rejection tests in each surface's `_test.go` | ✅ verified | 4 |
| X2.5 | Forex factor snap rule for chained-fiat closed-bucket consistency | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 5 | `internal/aggregate/orchestrator/triangulate.go::legPrice`, `internal/storage/timescale/trades.go::FXQuoteAtOrBefore`, `internal/sources/external/registry.go::FXSources` | [ADR-0018](../adr/0018-api-consistency-surfaces.md) §"Forex factor handling" | ✅ verified | 2 |
| X2.6 | Streaming endpoints per surface (`/v1/price/stream`, `/v1/price/tip/stream`, `/v1/observations/stream`) | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | `internal/api/v1/{price_stream,price_tip_stream,observations_stream}.go`, `internal/api/streaming` (Hub) | [ADR-0018](../adr/0018-api-consistency-surfaces.md); SSE + heartbeat + last-event-id resumption tests in each surface's `_test.go` | ✅ verified | 4 |
| X2.7 | Per-surface application of `flags.stale` semantics | [ADR-0018](../adr/0018-api-consistency-surfaces.md) | 7 | each surface handler + `internal/api/v1/envelope.go` | [ADR-0018](../adr/0018-api-consistency-surfaces.md) §"flags.stale semantic"; `/v1/price` sets stale=true on degradation, tip + observations always false | ✅ verified | 4 |

### X3. Anomaly response and confidence scoring

| # | Requirement | ADR | Week | Owner | Verified by | Status | Conf |
| - | ----------- | --- | ---- | ----- | ----------- | ------ | ---- |
| X3.1 | Per-asset-class threshold defaults (Phase 1 stop-gap) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 5 | `internal/aggregate/anomaly` (class.go, threshold.go, decision.go) + `internal/config/anomaly.go` | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §Phase 1; orchestrator wires `Config.Anomaly` → `Evaluate()` per tick | ✅ verified | 4 |
| X3.2 | Per-asset statistical baseline (Phase 2 — `volatility_baseline_1m` CAGG) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | `internal/aggregate/baseline` + `migrations/0007_create_volatility_baseline.up.sql` | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §Phase 2; `cmd/ratesengine-aggregator/main.go` wires `baseline.NewRefresher` on hourly cadence | ✅ verified | 4 |
| X3.3 | Multi-factor confidence score on every published price | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | `internal/aggregate/confidence` (factors.go, score.go) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §"Multi-factor confidence score"; orchestrator caches score at `confidence:<base>:<quote>:<window>` per tick | ✅ verified | 4 |
| X3.4 | Freeze policy (3-signal AND on closed-bucket only) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | `internal/aggregate/freeze` + `internal/aggregate/orchestrator/phase2_freeze.go` | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §"Freeze policy"; `phase2FreezeFires` AND-combines confidence + z + source-count thresholds | ✅ verified | 4 |
| X3.5 | Cross-oracle factor (Phase 3 — depends on `internal/divergence/`) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | post-launch | `internal/aggregate/confidence` × `internal/divergence` | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §Phase 3 | ⏳ deferred | 1 |
| X3.6 | Multi-window safeguard against frog-boiling (1d/7d/30d MAD) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | `internal/aggregate/baseline/multi.go` + `migrations/0008_add_multi_window_baseline.up.sql` | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §"Multi-window safeguard"; `MultiBaseline` struct carries Day1/Day7/Day30 baselines | ✅ verified | 4 |
| X3.7 | Bootstrap (warmup) policy for new assets | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | `internal/aggregate/baseline/refresh.go` (MinSamples gate) | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) §"Bootstrap (warmup) policy"; `TestMultiBaseline_PartialBootstrap` + `_FullBootstrap` pin the n<2 fall-through | ✅ verified | 4 |
| X3.8 | Operator runbook for freeze events | [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) | 6 | runbook | [anomaly-freeze-engaged.md](../operations/runbooks/anomaly-freeze-engaged.md) | ✅ verified | 4 |

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
- **Open**: MinIO + Galexie smoke test (Week 3). Captive-core +
  Galexie co-resident memory profile.
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
  `internal/pipeline/sink.go:98`). What's still
  pending on Blend is the WASM audit's Phase 2 — per-pool
  `wasm-history` walk on r1 — which keeps `BackfillSafe=false` in
  `internal/sources/external/registry.go` until it lands. Live
  ingest works fine; only retroactive backfill replay is gated.
  Phoenix's 8-events-per-swap pattern and Soroswap's swap+sync
  correlation were non-obvious and are both captured explicitly.
- **Verdict**: ✅ promise exceeded in venue breadth (Phoenix +
  Comet added beyond the proposal's list); Blend live with the
  documented backfill caveat from the WASM audit.

### Claim 6 — "p95 ≤ 200 ms, p99 ≤ 500 ms, ≥ 99.99% uptime"

- **As written** (proposal §Performance SLAs).
- **Verified**: nothing empirically. The pattern (precomputed
  aggregates in Redis + CDN-cacheable historical) is industry-
  standard but our capacity, cache-hit-rate, and cold-cache latency
  are unmeasured.
- **Closure**: [HA plan](ha-plan.md) + [API design](../reference/api-design.md) +
  Week 9 load-test.
- **Verdict**: 🧪 plan-credible; proof deferred to Week 9.

### Claim 7 — "Since-inception historical coverage"

- **As written** (proposal §Historical Data).
- **Verified**: Galexie can replay from ledger 2; SDF public GCS
  bucket is available as an accelerator
  ([data-sources/galexie.md](../discovery/data-sources/galexie.md) +
  [data-sources/stellar-data-lakes.md](../discovery/data-sources/stellar-data-lakes.md)).
  Backfill throughput unmeasured on our hardware.
- **Closure**: Week 5 runs the full backfill; the Week 9 load test
  validates query performance on the resulting data set.
- **Verdict**: ✅ promise is feasible; duration unknown.

### Claim 8 — "Open source, provider-supplied deployment kits"

- **As written** (proposal §Open Source & Deployment Model).
- **Verified**: Apache-2.0 LICENSE committed; `deploy/docker-compose`
  + `deploy/k8s` planned (Weeks 8–9).
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
