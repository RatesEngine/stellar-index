# RFP requirements → source traceability matrix

> **⚠️ Status: Phase 1 source-discovery artefact — superseded for current state.**
>
> This doc was the Phase 1 (discovery) traceability matrix. It maps each
> RFP bullet to the *audit doc* that proved feasibility — useful as a
> historical record and as the source-of-truth for "did we actually
> verify this thing exists in the wild?"
>
> **For current implementation status, see
> [`docs/architecture/coverage-matrix.md`](../architecture/coverage-matrix.md).**
> That doc tracks status, ADR mapping, owner package, and confidence
> per requirement; it's what gates launch readiness.
>
> This doc remains valid for what it documents (Phase 1 sources verified)
> and stays as a historical artefact. Phase-1 audit archive closed
> 2026-04-22 per CLAUDE.md.

---

**Purpose:** map every promise in the two RFPs (Stellar Prices API +
Freighter Asset Detail) to the source / audit doc / open item that
will fulfil it. If a row has no source, we have a coverage gap for
that requirement.

**Authoritative inputs:**

- `docs/stellar-rfp.md` — Stellar Prices API RFP.
- `docs/freighter-rfp.md` — Freighter asset detail RFP + SLA table.
- `docs/ctx-proposal.md` — our awarded proposal.

**How to read this doc:** each row captures one RFP requirement.
`Source(s)` is what we ingest to fulfil it. `Audit doc` is where
we've verified the source actually exists and is usable.
`Status` reflects Phase-1 discovery confidence, not implementation
completeness.

---

## A. Stellar Prices API RFP — core requirements

### A1. Asset Coverage: all current native Stellar assets + SEP-41 Soroban tokens

| Aspect | Source(s) | Audit doc | Status |
| ------ | --------- | --------- | ------ |
| Classic assets (code+issuer) — identity | On-chain `AccountEntry` / `TrustLineEntry` from Galexie ledger meta | [data-sources/galexie.md](data-sources/galexie.md), [protocol-versions.md](protocol-versions.md) | ✅ |
| SEP-41 Soroban tokens — identity + events | Soroban contract events (transfer/mint/burn/clawback), SEP-41 spec v0.4.1 | [notes/sep-41-token-events.md](notes/sep-41-token-events.md) | ✅ |
| SAC-wrapped classic assets — bridge | SAC contract address recognised as canonical asset representation; e.g. `CAS3J7…OWMA` = native XLM SAC | [dexes-amms/aquarius.md](dexes-amms/aquarius.md), [notes/sep-41-token-events.md](notes/sep-41-token-events.md) | ✅ |
| Asset discovery (enumeration) | Walk ledger meta for new `contractCodeHash`; hash-match against Soroswap pair hash, Redstone feed hash, etc. | [data-sources/withobsrvr-stellar-extract.md](data-sources/withobsrvr-stellar-extract.md) | 🧪 (Phase 2 impl) |
| Home domain metadata | `AccountEntry.HomeDomain` → fetch `.well-known/stellar.toml` per SEP-1 | **NO AUDIT DOC YET** — flagged as gap in [adversarial-audit.md §1e](adversarial-audit.md) | ❌ open |

### A2. Oracle Coverage: Chainlink, Redstone, Band, Reflector + others

| Oracle | On-chain? | Audit doc | Status |
| ------ | --------- | --------- | ------ |
| **Reflector** (3 contracts: DEX / CEX / FX) | ✅ Soroban | [oracles/reflector.md](oracles/reflector.md) | ✅ |
| **Redstone** (Adapter + 19 per-feed proxies) | ✅ Soroban | [oracles/redstone.md](oracles/redstone.md) | ✅ |
| **Band** (one StandardReference contract) | ✅ Soroban | [oracles/band.md](oracles/band.md) | ✅ |
| **Chainlink** | ❌ no live Stellar Data Feeds at audit time; using HTTP cross-check | [oracles/chainlink.md](oracles/chainlink.md) | ⚠️ |
| **DIA** (discovered during audit, not in proposal) | 🧪 testnet only | [oracles/dia.md](oracles/dia.md) | 🧪 |
| SEP-40 output compat (so others consume *our* prices) | Our own API wrapper | [oracles/reflector.md](oracles/reflector.md) covers SEP-40 interface | 🧪 (Phase 5 impl) |

### A3. Price Aggregation across Soroswap, Aquarius, SDEX, Blend + others

| Venue | Role | Audit doc | Status |
| ----- | ---- | --------- | ------ |
| **SDEX (Classic DEX)** | Orderbook + liquidity-pool fills via ClaimAtom parsing | [dexes-amms/sdex.md](dexes-amms/sdex.md) | ✅ |
| **Soroswap** (factory + pair + router) | Primary Soroban DEX; event-based ingest | [dexes-amms/soroswap.md](dexes-amms/soroswap.md) | ✅ |
| **Aquarius** (3 pool types: volatile / stableswap / concentrated-WIP) | Major Soroban AMM; unified event schema | [dexes-amms/aquarius.md](dexes-amms/aquarius.md) | ✅ |
| **Phoenix DEX** (new discovery) | Unusual 8-event-per-swap pattern | [dexes-amms/phoenix.md](dexes-amms/phoenix.md) | ✅ |
| **Comet** (new discovery) | Balancer-weighted AMM; also Blend's backstop pool | [dexes-amms/comet.md](dexes-amms/comet.md) | ✅ |
| **Blend** | Lending; auctions as directional price signals (not VWAP) | [dexes-amms/blend.md](dexes-amms/blend.md) | ✅ |
| **CEX data** (Binance, Coinbase, Kraken, Bitstamp, …) | VWAP contributor for major pairs | [external-refs/cex-feeds.md](external-refs/cex-feeds.md) | 🧪 |
| **Other Soroban DeFi** (Phoenix-derived synthetics, FxDAO, OrbitCDP …) | Secondary; asset-specific coverage | **NO AUDIT DOC YET** — flagged in [adversarial-audit.md §1h](adversarial-audit.md) | ❌ open |

### A4. VWAP with configurable USD volume threshold

| Aspect | How | Audit doc | Status |
| ------ | --- | --------- | ------ |
| Volume-weighted aggregation | Internal aggregation engine (Phase 3 implementation) | [../ctx-proposal.md](../ctx-proposal.md) §Aggregation Strategy | 🧪 (impl) |
| USD-denominated volume on non-USD pairs | Triangulation through USD / BTC anchors — see A8 | [../ctx-proposal.md](../ctx-proposal.md) §Cross-Pair Derivation | 🧪 (impl) |
| Per-pair minimum USD volume threshold | Configurable per-deployment; proposal §Market Manipulation and Wash Trading | [../ctx-proposal.md](../ctx-proposal.md) §Security | 🧪 (impl) |
| TWAP fallback when volume thresholds not met | Explicit in proposal §Aggregation Strategy | [../ctx-proposal.md](../ctx-proposal.md) | 🧪 (impl) |

### A5. Real-time price endpoints

| Aspect | How | Audit doc | Status |
| ------ | --- | --------- | ------ |
| Hot-path live event ingest | Our own stellar-rpc + our own captive-core; Soroban events via `getEvents` | [data-sources/archival-nodes.md](data-sources/archival-nodes.md) | ✅ |
| 30-second staleness target (Freighter SLA) | Precomputed aggregates in Redis; live event subscription | [data-sources/archival-nodes.md](data-sources/archival-nodes.md) | 🧪 (capacity TBC) |
| Streaming via SSE | Proposal §Streaming Support; implementation Phase 5 | [../ctx-proposal.md](../ctx-proposal.md) | 🧪 (impl) |
| Degradation signals in response (stale_flag, reduced_redundancy) | Proposal §Error Handling and Degradation Signals | [../ctx-proposal.md](../ctx-proposal.md) | 🧪 (impl) |

### A6. Historical price endpoints + OHLC

| Aspect | How | Audit doc | Status |
| ------ | --- | --------- | ------ |
| Since-inception backfill source | Galexie against our own captive-core from ledger 2; SDF public GCS bucket as accelerator | [data-sources/galexie.md](data-sources/galexie.md), [data-sources/stellar-data-lakes.md](data-sources/stellar-data-lakes.md) | ✅ |
| Pre-P20 (no-Soroban) historical period | ClaimAtom parsing only (V0 + OrderBook + LiquidityPool variants) | [dexes-amms/sdex.md](dexes-amms/sdex.md), [protocol-versions.md](protocol-versions.md) | ✅ |
| Post-P23 unified events | Either unified events or ClaimAtom path; we use ClaimAtom for consistency with pre-P23 | [notes/cap-67-unified-events.md](notes/cap-67-unified-events.md) | ✅ |
| OHLC materialisation | TimescaleDB continuous aggregates | **NO AUDIT DOC YET** — flagged in [adversarial-audit.md §1g](adversarial-audit.md) | ❌ open |
| Retention: 1h+ granularity indefinite; lower granularities capped | TimescaleDB retention policies | **NO AUDIT DOC YET** | ❌ open |

### A7. Supported timeframes + granularities (1h/24h/1w/1mo/1yr/all-time)

Table from the RFP is verbatim in our proposal and implementable via
TimescaleDB continuous aggregates. No source-discovery work needed;
this is a storage + query implementation task. **NO AUDIT DOC** —
needs `infrastructure/storage-timescaledb.md`.

### A8. Base and quote volume in USD

| Aspect | How | Audit doc | Status |
| ------ | --- | --------- | ------ |
| USD-denominated volume on each trade | Multiply base amount × base asset USD price at trade time; record both base-qty and USD-qty columns | Proposal §Data Processing & Aggregation | 🧪 (impl) |
| FX feed for USD-denominated conversion | [external-refs/fx-feeds.md](external-refs/fx-feeds.md) | 🧪 |

### A9. Performance SLAs (high-availability, low-latency, high-query)

| SLA | Source | Status |
| --- | ------ | ------ |
| ≥ 99.99 % uptime | Proposal §Availability + [decisions.md](decisions.md) | 🧪 (impl) |
| p95 ≤ 200 ms, p99 ≤ 500 ms (Freighter spec) | Proposal §Latency Targets — precomputed in Redis, CDN-cacheable historical | 🧪 (impl) |
| 1000 req/min per client | Proposal §Rate Limits and Throughput | 🧪 (impl) |
| Degraded behaviour when prices unavailable | Proposal §Degradation Strategy + divergence detector | ✅ design doc; 🧪 (impl) |

### A10. Completely open source

Decision in [decisions.md](decisions.md) (implied — repo will be
public on award); proposal §Open Source & Deployment Model covers
the licensing model.

---

## B. Freighter RFP — V1 requirements

### B1. Asset metadata

| Field | Source | Audit doc | Status |
| ----- | ------ | --------- | ------ |
| Asset / Token Code | On-chain `AccountEntry` or SAC `symbol()` (SEP-41) | [notes/sep-41-token-events.md](notes/sep-41-token-events.md) | ✅ |
| Current Price (USD) | Our aggregation layer | [oracles/reflector.md](oracles/reflector.md) + [dexes-amms/*.md](dexes-amms/) + [external-refs/*.md](external-refs/) | 🧪 |
| Asset Type (classic / soroban) | Derived from asset representation | [dexes-amms/sdex.md](dexes-amms/sdex.md) | ✅ |
| Issuer Address (G…) | Classic-asset field | [protocol-versions.md](protocol-versions.md) | ✅ |
| Contract Address (C…) | Soroban SAC / custom token contract address | [notes/sep-41-token-events.md](notes/sep-41-token-events.md) | ✅ |
| Home Domain | `AccountEntry.HomeDomain` + SEP-1 stellar.toml | **NO AUDIT DOC** — gap | ❌ open |

### B2. Historical price chart (1h / 24h / 1w / 1mo / since-inception)

Same as A6 / A7 above. **Storage design doc still missing.**

---

## C. Freighter RFP — V2 scope (market data extension)

### C1. Market Cap, FDV, Trading Volume, Supplies

| Field | Source | Audit doc | Status |
| ----- | ------ | --------- | ------ |
| **Market Cap** = `circulating_supply × current_price` | Derived | Depends on C2 + A5 | 🧪 |
| **FDV** = `max_supply × current_price` | Derived | Depends on C4 + A5 | 🧪 |
| **24h Trading Volume (USD)** | Sum of `usd_volume` column in our `trades` hypertable for past 24h | [dexes-amms/sdex.md](dexes-amms/sdex.md), [dexes-amms/soroswap.md](dexes-amms/soroswap.md), [external-refs/cex-feeds.md](external-refs/cex-feeds.md) | 🧪 (impl) |
| **Circulating Supply** (RFP: "provider-supplied") | Running sum of SEP-41 mint − burn − clawback events per token; for classic assets: issuer-balance-aware derivation | [notes/sep-41-token-events.md](notes/sep-41-token-events.md) has the event math; **classic-side supply calc has NO audit doc** | ❌ open |
| **Total Supply** | Same mint − burn − clawback sum, no exclusions | [notes/sep-41-token-events.md](notes/sep-41-token-events.md) | 🧪 |
| **Max Supply** (nullable) | Off-chain metadata: SEP-1 stellar.toml `max_supply` or operator-configured | **NO AUDIT DOC** | ❌ open |

### C2. Circulating-supply algorithm — open design question

Per-asset-class design decisions that have not been made yet:

- **Classic assets**: `total_issued − issuer_balance − admin_reserves − known_vesting_contracts`?
  This is policy — operator-configurable, or do we pick sensible defaults?
- **SEP-41 tokens**: `(mint − burn − clawback)` running sum minus any locked
  set (vesting contracts, treasury multisig, …)?

Needs `data-sources/supply-data.md` (as flagged in adversarial-audit §1d).

---

## D. Freighter RFP — Performance SLAs

All under "Phase 6 infrastructure hardening" in our proposal.
Implementation tasks; source-discovery already done via
[data-sources/archival-nodes.md](data-sources/archival-nodes.md)
and [decisions.md](decisions.md).

| SLA | Status | Notes |
| --- | ------ | ----- |
| API latency p95 ≤ 200 ms | 🧪 | Precompute + cache |
| API latency p99 ≤ 500 ms | 🧪 | |
| Responsiveness ≥ 99.9 % | 🧪 | HA deployment, redundant ingest |
| Data freshness (price) ≤ 30 s staleness | 🧪 | Direct event subscription, not Galexie batch |
| SEV-1 detection ≤ 15 min / response ≤ 30 min | 🧪 | Ops runbook needed |
| SEV-2 detection ≤ 30 min / response ≤ 60 min | 🧪 | Ops runbook needed |

---

## E. Freighter RFP — API characteristics

| Requirement | Plan | Status |
| ----------- | ---- | ------ |
| REST or GraphQL | REST canonical, GraphQL as optional thin layer | 🧪 (impl) |
| Rate limits ≥ 1000 req/min | Redis-backed bucket + per-key quotas | 🧪 (impl) |
| Bulk / batch query support | Explicit batch endpoint per proposal | 🧪 (impl) |

Design doc still missing — flagged as
`infrastructure/api-layer.md` in adversarial-audit §1f.

---

## F. Freighter misc requirements

| Requirement | Plan | Status |
| ----------- | ---- | ------ |
| Price preference: VWAP > TWAP > last-trade | Implemented in our aggregation layer | 🧪 (impl) |
| Quote currency: USD | FX feeds + USD anchor | [external-refs/fx-feeds.md](external-refs/fx-feeds.md) | 🧪 |
| Data aggregation scope: DEXs | All seven DEX/AMM audit docs | ✅ |
| "Since Inception" = first recorded trade | Our genesis-backfill via Galexie | [data-sources/stellar-data-lakes.md](data-sources/stellar-data-lakes.md) | ✅ |

---

## Summary — gap triage

### Fully covered (source verified + audit doc exists)

- Every DEX / AMM ingestion surface (SDEX, Soroswap, Aquarius, Blend, Phoenix, Comet).
- Every on-chain oracle (Reflector, Redstone, Band).
- Galexie / CDP / Ingest SDK.
- SEP-41 token events + supply-math.
- CAP-67 unified events + protocol-version handling.
- stellar-archivist + archival-node plan.
- CEX / FX / CoinGecko / CoinMarketCap (design docs; Phase-2 impl).
- Existing CTX Rates — as reference.

### Open gaps blocking Phase 2 (ingestion)

- **Phoenix/Comet/Aquarius mainnet factory/plane/calculator
  addresses** — some captured, some residual. Low effort to close
  via live RPC calls.
- **Liveness audit** of existing CEX connectors (which still work,
  which don't).
- **FxDAO / OrbitCDP / Laina / Slender / DeFindex / EquitX** —
  residual DeFi protocols not yet audited.

### Open gaps blocking Phase 3 (aggregation)

- **Supply-data audit** for Freighter V2 market-cap math (classic +
  Soroban + locked-set policy).
- **SEP-1 / home-domain resolution** design.

### Open gaps blocking Phase 5 (API)

- **API layer design** — auth, rate limit, SSE, versioning, caching.
- **CDN strategy** for historical endpoints.

### Open gaps blocking Phase 6 (SLA validation)

- **Infrastructure design docs**: TimescaleDB hypertables, Redis
  cache schema, MinIO topology, reverse-proxy/observability stack.
- **Load-test plan** + capacity calculations.
- **SEV-1 / SEV-2 playbook**.

---

## Process gaps (not tied to a specific RFP requirement)

- **Push corrections back into ctx-proposal.md**:
  - Reflector has no on-chain `twap` / `x_*`.
  - Soroswap SwapEvent has no post-state reserves.
  - Band has a native Soroban contract (not just BandChain REST).
  - Reflector is three contracts.
  - Redstone has 19 feeds including RWA set.
  - cdp-pipeline-workflow has verified correctness bugs we document.

- **VERSIONS.md** — pin every dependency SHA/tag.
- **Fixture set** — per §10 of [adversarial-audit.md](adversarial-audit.md).
- **Empirical pre-P20 ledger replay** to settle "Galexie preserves
  native XDR."

---

## Use this doc as a backlog

If a row is 🧪 or ❌ open, it's a ticket. Phase 2 backlog =
"everything that must be ✅ before coding."

Review cadence: re-read this doc end-of-each-phase and collapse
open items.
