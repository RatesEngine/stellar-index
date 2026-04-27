---
title: RFP × Proposal × Delivery — Coverage Matrix
last_verified: 2026-04-22
status: ratified
---

# RFP × Proposal × Delivery Coverage Matrix

**Ratified:** 2026-04-22.
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
| S1.4 | Asset enumeration / discovery | §Asset Identification | 4 | `internal/canonical/discovery` | — | [data-sources/withobsrvr-stellar-extract.md](../discovery/data-sources/withobsrvr-stellar-extract.md) | 🧪 designed | 3 |
| S1.5 | i128/u128 amounts never truncate | §Data Processing | 1 | `internal/canonical.Amount` | ADR-0003 | Tested: `amount_test.go` KALIEN regression | ✅ verified | 5 |

### S2. Oracle coverage — Chainlink, Redstone, Band, Reflector + others

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S2.1 | Reflector (3 contracts: DEX/CEX/FX) | §Oracle Networks — Reflector | 4 | `internal/sources/reflector` | — | [oracles/reflector.md](../discovery/oracles/reflector.md) | ✅ verified | 5 |
| S2.2 | Redstone (Adapter + 19 per-feed proxies) | §Oracle Networks — Redstone | 4 | `internal/sources/redstone` | — | [oracles/redstone.md](../discovery/oracles/redstone.md) | ✅ verified | 5 |
| S2.3 | Band Protocol (native Soroban StandardReference) | §Oracle Networks — Band | 4 | `internal/sources/band` | — | [oracles/band.md](../discovery/oracles/band.md) | ✅ verified | 5 |
| S2.4 | Chainlink (HTTP cross-check until Scale ships) | §Oracle Networks — Chainlink | 4 | `internal/divergence/chainlink` | — | [oracles/chainlink.md](../discovery/oracles/chainlink.md) | ⚠ caveat: HTTP-only for now | 3 |
| S2.5 | "And others" — DIA (if mainnet ships in window) | (not in proposal; adding) | 4–post-launch | `internal/sources/dia` | — | [oracles/dia.md](../discovery/oracles/dia.md) | ⏳ deferred | 2 |
| S2.6 | SEP-40-compat output (others consume *our* prices) | §API | 7 | `internal/api/sep40` | — | [oracles/reflector.md](../discovery/oracles/reflector.md) §SEP-40 interface | 🧪 designed | 3 |

### S3. Price aggregation — Soroswap, Aquarius, SDEX, Blend + others

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S3.1 | SDEX trades via ClaimAtom parsing | §Stellar Classic DEX | 2 | `internal/sources/sdex` | — | [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md) | ✅ verified | 5 |
| S3.2 | Soroswap factory+pair+router events | §Soroban DEXs / Soroswap | 3 | `internal/sources/soroswap` | — | [dexes-amms/soroswap.md](../discovery/dexes-amms/soroswap.md) | ✅ verified | 5 |
| S3.3 | Aquarius 3 pool types | §Soroban DEXs / Aquarius | 3 | `internal/sources/aquarius` | — | [dexes-amms/aquarius.md](../discovery/dexes-amms/aquarius.md) | ✅ verified | 5 |
| S3.4 | Phoenix DEX (8-events-per-swap) | §Soroban DEXs (added post-discovery) | 3 | `internal/sources/phoenix` | — | [dexes-amms/phoenix.md](../discovery/dexes-amms/phoenix.md) | ✅ verified | 5 |
| S3.5 | Comet (Balancer-weighted AMM) | §Soroban DEXs (added post-discovery) | 3 | `internal/sources/comet` | — | [dexes-amms/comet.md](../discovery/dexes-amms/comet.md) | ✅ verified | 4 |
| S3.6 | Blend auctions as directional signal | §Soroban DEXs / Blend | 3 | `internal/sources/blend` | — | [dexes-amms/blend.md](../discovery/dexes-amms/blend.md) | ✅ verified | 4 |
| S3.7 | CEX trade ingestion (Binance, Coinbase, Kraken, …) | §Centralized Exchanges | 4 | `internal/sources/cex/<venue>` | — | [external-refs/cex-feeds.md](../discovery/external-refs/cex-feeds.md) | 🧪 designed | 3 |

### S4. VWAP + configurable USD volume threshold

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S4.1 | Volume-weighted aggregation across venues | §Aggregation Strategy | 5 | `internal/aggregate` | — | (design; impl pending) | 🧪 designed | 3 |
| S4.2 | USD-denominated volume on non-USD pairs | §Cross-Pair Derivation | 5 | `internal/aggregate/triangulate` | — | (design; impl pending) | 🧪 designed | 3 |
| S4.3 | Per-pair configurable min USD volume | §Security — manipulation | 5 | `internal/config` schema + `internal/aggregate` | — | (design; impl pending) | 🧪 designed | 3 |
| S4.4 | TWAP fallback when volume thresholds not met | §Aggregation Strategy | 5 | `internal/aggregate` | — | (design; impl pending) | 🧪 designed | 3 |

### S5. Real-time price endpoints

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S5.1 | Live event ingest (Soroban getEvents + ledger stream) | §Real-time — Hot path | 3 | `internal/consumer` + `internal/sources/*` StreamLive | — | [data-sources/archival-nodes.md](../discovery/data-sources/archival-nodes.md) | ✅ verified | 5 |
| S5.2 | ≤ 30s staleness (Freighter SLA) | §Latency Targets | 6 | cross-cutting | — | [data-sources/archival-nodes.md](../discovery/data-sources/archival-nodes.md) + HA plan | 🧪 designed | 3 |
| S5.3 | SSE streaming for subscribers | §Streaming Support | 7 | `internal/api/stream` | ADR-0006 (planned) | [oracles/reflector.md](../discovery/oracles/reflector.md) | 🧪 designed | 2 |
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
| S8.1 | `usd_volume` column per trade | §Data Processing | 3 | `internal/canonical.Trade` + writer | — | (design; impl pending) | 🧪 designed | 3 |
| S8.2 | FX anchor for USD conversion | §Forex Providers | 4 | `internal/sources/fx` | — | [external-refs/fx-feeds.md](../discovery/external-refs/fx-feeds.md) | 🧪 designed | 2 |

### S9. Performance SLAs

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S9.1 | ≥ 99.99 % uptime | §Availability | 8–9 | HA plan | [ADR-0008](../adr/0008-ha-topology.md) | (HA plan) | 🧪 designed | 2 |
| S9.2 | p95 ≤ 200 ms, p99 ≤ 500 ms | §Latency Targets | 9 | `internal/api` + Redis caching | [ADR-0009](../adr/0009-latency-budget.md) | (API design + HA plan) | 🧪 designed | 2 |
| S9.3 | 1000 req/min per client | §Rate Limits | 7 | `internal/ratelimit` | — | Bucket + middleware shipped; anonymous tier at 60/min today, apikey tier (1000/min) gated on auth middleware landing. | ⚠ caveat | 3 |
| S9.4 | Defined degradation when prices unavailable | §Degradation Strategy + divergence | 5 | `internal/divergence` + `/api/envelope` | — | (design; impl pending) | 🧪 designed | 2 |

### S10. Open source

| # | Requirement | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| S10.1 | Apache-2.0, fully open | §Open Source & Deployment Model | 1 | `LICENSE` in repo root | — | LICENSE committed | ✅ verified | 5 |

---

## Freighter RFP — V1: Asset metadata

| # | Field | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| F1.1 | Asset/Token Code | §Asset Identification | 4 | `internal/metadata` | — | [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md), [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md) | ✅ verified | 5 |
| F1.2 | Current Price (USD) | §Current Price API | 5 | `internal/aggregate` | — | cross-cutting | 🧪 designed | 3 |
| F1.3 | Asset Type enum (`classic`/`soroban`) | §Asset Identification | 4 | `pkg/types.AssetType` | — | [dexes-amms/sdex.md](../discovery/dexes-amms/sdex.md) | ✅ verified | 5 |
| F1.4 | Issuer Address (G…) | §Asset Identification | 4 | `pkg/types.ClassicAsset` | — | [protocol-versions.md](../discovery/protocol-versions.md) | ✅ verified | 5 |
| F1.5 | Contract Address (C…) | §Asset Identification | 4 | `pkg/types.SorobanAsset` | — | [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md) | ✅ verified | 5 |
| F1.6 | Home Domain (SEP-1) | §Asset Identification (needs proposal amendment) | 5 | `internal/metadata/homedomain` | ADR-0010 (planned: SEP-1 cache) | [data-sources/sep1-home-domain.md](../discovery/data-sources/sep1-home-domain.md) | 🧪 designed | 3 |

## Freighter RFP — V1: Historical price chart

Same as S7. No additional requirement.

## Freighter RFP — V2: Market data extension

| # | Field | Proposal | Week | Owner | ADR | Verified by | Status | Conf |
| - | ----- | -------- | ---- | ----- | --- | ----------- | ------ | ---- |
| F2.1 | Market Cap = `circulating × price` | §V2 (addendum) | 6 | `internal/supply` + `/aggregate` | — | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md) | 🧪 designed | 3 |
| F2.2 | FDV = `max_supply × price` | §V2 | 6 | `internal/supply` | — | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md) | 🧪 designed | 3 |
| F2.3 | 24h Trading Volume (USD) | §V2 | 6 | Timescale materialised view | ADR-0007 | cross-cutting | 🧪 designed | 3 |
| F2.4 | Circulating Supply (provider-supplied) | §V2 | 6 | `internal/supply/circulating` | [ADR-0011](../adr/0011-supply-algorithm.md) | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md) | 🧪 designed | 3 |
| F2.5 | Total Supply (mint − burn − clawback) | §V2 | 6 | `internal/supply/total` | — | [notes/sep-41-token-events.md](../discovery/notes/sep-41-token-events.md) | ✅ verified math; impl pending | 4 |
| F2.6 | Max Supply (nullable, off-chain metadata) | §V2 | 6 | `internal/metadata/maxsupply` | ADR-0010 | [data-sources/sep1-home-domain.md](../discovery/data-sources/sep1-home-domain.md) | 🧪 designed | 2 |

## Freighter RFP — Performance SLAs

| # | Metric | Requirement | Proposal | Week | Owner | Verified by | Status | Conf |
| - | ------ | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- |
| F3.1 | API latency p95 | ≤ 200 ms | §Latency Targets | 9 | `internal/api` + Redis | (HA + API plans) | 🧪 designed | 2 |
| F3.2 | API latency p99 | ≤ 500 ms | §Latency Targets | 9 | same | same | 🧪 designed | 2 |
| F3.3 | Responsiveness | ≥ 99.9 % | §Availability | 8–9 | HA plan | (HA plan) | 🧪 designed | 2 |
| F3.4 | Data freshness (price) | ≤ 30 s staleness | §Data Freshness | 3 (ingest), 8 (deploy) | `internal/consumer` StreamLive | [data-sources/archival-nodes.md](../discovery/data-sources/archival-nodes.md) | 🧪 designed | 3 |
| F3.5 | SEV-1 detect ≤ 15 min / respond ≤ 30 min | | §Incident Response (needs runbook) | 9 | `docs/operations/sev-playbook.md` | (pending) | ⏳ deferred | 1 |
| F3.6 | SEV-2 detect ≤ 30 min / respond ≤ 60 min | | same | 9 | same | (pending) | ⏳ deferred | 1 |

## Freighter RFP — Coverage

| # | Requirement | Proposal | Week | Owner | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- |
| F4.1 | Lookup classic + Soroban by contract address | §Asset Identification | 4 | `internal/api/lookup` | cross-cutting | 🧪 designed | 3 |
| F4.2 | Historical retention ≥ 1 year (ideally since inception) | §Historical Data | 2 (scaffold), post-launch (fill) | Timescale + Galexie backfill | [data-sources/galexie.md](../discovery/data-sources/galexie.md) | 🧪 designed (capacity TBC) | 3 |

## Freighter RFP — API characteristics

| # | Requirement | Proposal | Week | Owner | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- |
| F5.1 | REST or GraphQL | §API Layer | 7 | `internal/api/v1` (REST), optional `/graphql` later | (API design) | 🧪 designed | 3 |
| F5.2 | Rate limits ≥ 1000 req/min | §Rate Limits and Throughput | 7 | `internal/ratelimit` | Bucket + middleware shipped; anonymous tier 60/min today, 1000/min on apikey tier pending auth. | ⚠ caveat | 3 |
| F5.3 | Bulk / batch query support | §Batch Queries | 7 | `internal/api/batch` | (API design) | 🧪 designed | 3 |

## Freighter RFP — Misc requirements

| # | Requirement | Proposal | Week | Owner | Verified by | Status | Conf |
| - | ----------- | -------- | ---- | ----- | ----------- | ------ | ---- |
| F6.1 | Price source preference VWAP → TWAP → last trade | §Aggregation Strategy | 5 | `internal/aggregate` | (design) | 🧪 designed | 3 |
| F6.2 | Quote currency = USD | §Quote Currency Policy | 5 | `internal/aggregate/fiat` | [external-refs/fx-feeds.md](../discovery/external-refs/fx-feeds.md) | 🧪 designed | 2 |
| F6.3 | Data aggregation scope = DEXes (Stellar + Soroban) | §Data Ingestion | 2–3 | `internal/sources/*` | cross-cutting | ✅ verified | 5 |
| F6.4 | "Since Inception" = first recorded trade | §Historical Data | 2 (scaffold), ongoing | backfill orchestrator | [data-sources/stellar-data-lakes.md](../discovery/data-sources/stellar-data-lakes.md) | ✅ verified | 4 |
| F6.5 | V2 supply data = provider-supplied | §V2 supply | 6 | `internal/supply` | [data-sources/supply-data.md](../discovery/data-sources/supply-data.md) | 🧪 designed | 3 |

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
- **Verified**: 6 venues (SDEX, Soroswap, Aquarius, Phoenix, Comet,
  Blend) have full event-schema audits with mainnet addresses where
  available. Phoenix's 8-events-per-swap pattern and Soroswap's
  swap+sync correlation were non-obvious and are both captured
  explicitly.
- **Verdict**: ✅ promise exceeded (Phoenix + Comet added).

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

Ranked by blast radius.

### Hard gaps (block launch)

1. **F3.5 / F3.6 SEV-1/SEV-2 runbook** — no runbook yet.
   Owner: Week 9. Deliverable: `docs/operations/sev-playbook.md`.
2. **S9.2 p95 ≤ 200 ms proof** — no load test yet.
   Owner: Week 9. Deliverable: `test/load/` + report.
3. **S6.5 / S7 Retention policy** — not in a migration yet.
   Owner: Week 4. Deliverable: `migrations/0001_timescale_hypertables.sql`
   + continuous-aggregate DDL.

### Soft gaps (block a V2 milestone, not V1 launch)

4. **F2.4 circulating-supply policy** — algorithm designed, policy
   choices (locked-set defaults) not ratified.
   Owner: Week 6. Deliverable: ADR-0011.
5. **F2.6 max_supply off-chain resolution** — SEP-1 path captured;
   the `[CURRENCIES] max_supply` field is not universally populated.
   Owner: Week 6. Deliverable: `internal/metadata/maxsupply` fallback
   policy doc.

### Watch (monitor during 10-week window)

6. **S2.5 DIA mainnet ship** — testnet only today.
7. **S9.1 99.99 % uptime** — need at least 30 days of production to
   measure. Accepted as post-launch SLA with a gradient: Week 10 we
   show a credible architecture, 90 days post-launch we show the
   number.
8. **S9.4 Degradation envelope** — `stale_flag`, `reduced_redundancy`
   are design-only. Spec to land in `openapi/rates-engine.v1.yaml`
   alongside each endpoint (Week 7).

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
