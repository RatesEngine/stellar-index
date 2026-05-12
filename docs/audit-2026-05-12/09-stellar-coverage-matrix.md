# Stellar-Specific Coverage Matrix

CG/CMC parity (the matrix in [08-cgcmc-parity-matrix.md](08-cgcmc-parity-matrix.md))
gets us to "indistinguishable from the competition." This matrix
is the durable differentiator: surfaces where we *should* be
deeper than CG/CMC, because we read Stellar at the protocol layer
that they don't.

Every `gap` row is a launch-quality finding — the Stellar-depth
claim is what justifies the product existing.

Status values: `covered` / `partial` / `gap` / `non-goal` / `n/a`
(same definitions as the CG/CMC matrix).

## A. Classic + Soroban unified asset model

| # | Surface | Implementation | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| A.1 | Native XLM | `internal/canonical/asset_crypto.go` `native` | covered | EV-1208 + W05 walk | none |
| A.2 | Classic asset (`code-issuer`) | `AssetCrypto` parse | covered | live `/v1/assets/USDC-G…` 200 | none |
| A.3 | SEP-41 contract asset (`C…`) | `AssetCrypto` parse | covered | code | none |
| A.4 | Fiat representation (`fiat:USD`) | `AssetFiat` (ADR-0010) | covered | live `/v1/price?asset=native&quote=fiat:USD` 200 | none |
| A.5 | SAC-wrapped classic asset (USDC SAC vs USDC classic) | `internal/sources/sac_balances/dispatcher_adapter.go:68-241` | covered | dispatcher_adapter_test.go | F-STELLAR-A005 (no integration double-count test, J59) |
| A.6 | Cross-network identity (USDC across chains) | `GlobalAssetView` `networks[]` | covered | EV-1209 (USDC: 6 networks) | none |
| A.7 | Verified-issuer catalogue (hand-curated) | `internal/currency/data/seed.yaml` `//go:embed` | covered | EV-1238 | none |
| A.8 | Unverified-collision warning on slug | `internal/api/v1/assets_global.go` | covered | code grep | none |

## B. On-chain trade ingest (DEX/AMM)

| # | Source | Implementation | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| B.1 | SDEX classic | `internal/sources/sdex/decode.go:138-148` | covered | J03 trace | none |
| B.2 | Soroswap | `internal/sources/soroswap/decode.go:51-104` (SwapEvent + paired SyncEvent) | covered | J01 trace; mainnet fixture | none |
| B.3 | Phoenix | `internal/sources/phoenix/decode.go:65-152` (8-event grouping) | **broken on R1** | J02 trace; F-1212 phoenix STOPPED | F-1212 |
| B.4 | Aquarius | `internal/sources/aquarius/decode.go:55-86` | covered | J25 trace | none |
| B.5 | Comet | `internal/sources/comet/decode.go:21-26` (shared topic) | **broken on R1** | J26 trace; F-1212 comet STOPPED | F-1212, F-1242 |
| B.6 | Blend | `internal/sources/blend/decode.go:27-258` (auctions) | **broken on R1** | J27 trace; F-1212 blend STOPPED | F-1212, F-1238 |
| B.7 | CAP-67 unified events post-Whisk | dispatcher handles both pre + post | partial | F-1242 | F-1242 |
| B.8 | Dispatcher topic routing (not contract address) | `internal/dispatcher/dispatcher.go:255-260, 304-306` | covered | `routing_test.go:114`, `contract_call_test.go:39` | none |
| B.9 | Per-WASM-version decoder safety | `BackfillSafe` flag in `internal/sources/external/registry.go` | covered | `framework_test.go:71-119`; all 8 sources have audit doc | none |
| B.10 | Liquidity-pool reserve observer | `internal/sources/liquidity_pools/dispatcher_adapter.go:55-110` (ADR-0022) | covered | J28 trace | none |
| B.11 | Liquidity-pool depth in `/v1/markets` | not exposed; pool reserves recorded but no depth endpoint | gap | grep `depth` in API returns nothing | F-STELLAR-B011 |
| B.12 | Trades-by-router attribution (migration 0025) | `routers_and_attribution` table | partial | migration present; not exposed via API | F-STELLAR-B012 |
| B.13 | TVL per pool / aggregate (migration 0021) | `tvl_and_mev` table | partial | migration present; not exposed via API | F-STELLAR-B013 |
| B.14 | MEV / sandwich detection (migration 0021) | `tvl_and_mev` table | partial | migration present; no detector code wired | F-STELLAR-B014 |

## C. Oracle ingest

| # | Source | Implementation | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| C.1 | Reflector DEX contract | `internal/sources/reflector/decode.go:30-152` | covered | J34 | none |
| C.2 | Reflector CEX contract | same package, separate variant | covered | J35 | none |
| C.3 | Reflector FX contract | same package, separate variant | covered | J36 | none |
| C.4 | Reflector TWAP / x_* methods (ABSENT on chain) | computed locally in `internal/aggregate/twap.go` | covered | code | none |
| C.5 | Redstone Adapter | `internal/sources/redstone/decode.go:21-153` (WritePrices + op_args feed_id) | covered | J37 | none |
| C.6 | Band relay() / force_relay() | `internal/sources/band/decode.go:31-198` (ContractCallDecoder) | **broken on R1** | J38; F-1212 band STOPPED | F-1212 |
| C.7 | DIA / Chainlink (HTTP-only) for divergence | `internal/divergence/chainlink.go` opt-in | covered | J19; config defaults `Enabled:false` | none |
| C.8 | SEP-40 oracle compatibility (we serve SEP-40 too) | `internal/api/v1/oracle.go` + `oracle_sep40.go` | partial | F-1216 (path mismatch — handler exists but route only `/v1/oracle/lastprice`) | F-1216 |

## D. Supply derivation (algorithms 1/2/3)

| # | Surface | Implementation | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| D.1 | XLM supply (algorithm 1) | `internal/supply/xlm.go` | covered | J15 | none |
| D.2 | Classic supply (algorithm 2) | `internal/supply/classic.go` reading observers | covered | J16 | none |
| D.3 | SEP-41 supply (algorithm 3) | `internal/supply/sep41.go` reading mint/burn/clawback | covered | J17 | none |
| D.4 | Account-entry observer (ADR-0021) | `internal/sources/accounts/decode.go:30-110` | covered | J32 | none |
| D.5 | Trustline observer | `internal/sources/trustlines/decode.go:36-90` | covered | J31 | none |
| D.6 | Claimable-balance observer | `internal/sources/claimable_balances/decode.go:22-90` | covered | J33 | none |
| D.7 | LP-reserve observer (counted in classic supply) | `internal/sources/liquidity_pools/dispatcher_adapter.go:55-110` | covered | J28 | none |
| D.8 | SAC-balance observer | `internal/sources/sac_balances/dispatcher_adapter.go:68-241` | covered | J29 | none |
| D.9 | SEP-41 mint/burn/clawback observer (ADR-0023) | `internal/sources/sep41_supply/decode.go:14-97` | covered | J30 | none |
| D.10 | Classic-asset registry stats | migration 0023 + 0024 | covered | inventory | F-1239 (first_seen ON CONFLICT bug) |
| D.11 | Cross-check supply between sources | `internal/supply/crosscheck.go` + `crosscheck_refresher.go` | covered | unit tests | none |
| D.12 | Supply snapshot textfile output | `internal/supply/textfile.go` | covered | unit tests | none |
| D.13 | `/v1/assets/{id}` `circulating_supply` field | `internal/api/v1/assets_f2.go` | covered | unit tests | none |
| D.14 | `total_supply` and `max_supply` fields | F2 fields | covered | unit tests | none |
| D.15 | SAC vs classic supply double-count guard | dedupe at read-time per per-observer policy | partial | no integration test (J59) | F-STELLAR-A005 |

## E. Metadata + SEP-1 overlay

| # | Surface | Implementation | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| E.1 | SEP-1 / stellar.toml fetch | `internal/metadata/*.go` | covered | code grep | none |
| E.2 | SEP-1 caching + refresh policy | `internal/metadata/` + `cmd/ratesengine-ops/sep1_refresh.go` | covered | unit tests | none |
| E.3 | SEP-1 SSRF defence | _verify (allow-listed schemes, no internal IPs)_ | needs_evidence | grep `internal/metadata` for SSRF guards | F-STELLAR-E003 |
| E.4 | Asset code + image / logo overlay | `assets_sep1.go` | partial | logo URL field only if SEP-1 toml provides | F-CGCMC-A009 |
| E.5 | Issuer organisation overlay | `assets_sep1.go` | covered | EV-1209 (`verified_issuer`) | none |
| E.6 | Home-domain change detection | `cmd/ratesengine-ops/sep1_refresh.go` | partial | no automatic alert on change | F-STELLAR-E006 |

## F. Ingest provenance + reproducibility

| # | Surface | Implementation | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| F.1 | Galexie → MinIO ledger archive | per ADR-0002 | covered | EV-1226 captive-core under galexie | none |
| F.2 | Per-ledger sha256 record (drift detector) | `internal/hashdb/*.go` (ADR-0017) | covered | inventory | none |
| F.3 | Tier-A/B/C/D archive completeness | `internal/archivecompleteness/*.go` | covered | J18 | F-1219 (alerts not loaded on R1) |
| F.4 | Cross-region byte-identical surface (ADR-0015) | last-closed-bucket contract | covered | `aggregates.go:247,295` | F-1234 (only R1 today) |
| F.5 | WASM history (per source, per upgrade) | migration 0017 + ops `wasm_history` | covered | inventory | none |
| F.6 | Per-source decoder audit log | `docs/operations/wasm-audits/` | covered | 8 audit docs present | none |
| F.7 | `BackfillSafe` flag enforcement | `internal/sources/external/registry.go` | covered | `framework_test.go:71-119` | F-1243 (supply observers fail-closed) |
| F.8 | Backfill range validation | `cmd/ratesengine-ops/backfill.go:397` | covered | code | none |

## G. Aggregation policy (Stellar-aware)

| # | Surface | Implementation | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| G.1 | Class-aware contribution (exchange-only by default) | `orchestrator.filterForVWAP` (`orchestrator.go:911-920`) | covered | `orchestrator_test.go:331,373` | none |
| G.2 | Aggregator + oracle reported but not contributing | aggregator policy | covered | code | none |
| G.3 | Stablecoin late binding (ADR-0026) | `internal/aggregate/stablecoin.go:24-37` | covered | unit tests | F-1230 (no depeg integ test) |
| G.4 | Triangulation across XLM hub | `internal/aggregate/triangulate.go` | covered | unit tests | none |
| G.5 | Per-source-class outlier policy | `internal/aggregate/outliers.go` | covered | unit tests | none |
| G.6 | Multi-window volatility baseline | migration 0008 + `aggregate/baseline/` | covered | unit tests | none |
| G.7 | Confidence scoring (ADR-0019) | `aggregate/confidence/score_test.go:38-260` | covered | 13 unit tests | none |
| G.8 | Anomaly response + freeze (ADR-0019) | `aggregate/{anomaly,freeze}/` + freeze_events table | covered | unit tests | F-1228, F-1229 (freeze_value=0; MarkRecovered unused) |
| G.9 | Source-contributions table for transparency | migration 0026; `/v1/sources` | covered | server.go:828 | none |
| G.10 | Decoder stats 5m (visibility) | migration 0020 + `dispatcher/statsflush/` | covered | J60 | F-1219 (alerts not loaded) |
| G.11 | Change summary 5m (% change) | migration 0022 + `aggregate/changesummary/` | covered | J60 | none |

## H. Ecosystem-specific products

| # | Surface | Implementation | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| H.1 | Freighter F1 fields (per RFP) | `internal/api/v1/assets_f2.go` | covered | unit tests | needs F1 row-by-row reconciliation in W16 |
| H.2 | Freighter F2 fields (per RFP) | same | covered | unit tests | same |
| H.3 | SEP-40 oracle endpoint | `internal/api/v1/oracle.go,oracle_sep40.go` | partial | F-1216 (path mismatch) | F-1216 |
| H.4 | Lending pool data (Blend) | `/v1/lending/pools` | **broken on R1** | server.go:824; F-1212 blend STOPPED | F-1212 |
| H.5 | Network stats (validators, ledger close time, etc.) | `/v1/network/stats` | covered | server.go:699 | none |
| H.6 | Asset issuers directory | `/v1/issuers`, `/v1/issuers/{g_strkey}` | covered | server.go:693,694 | none |
| H.7 | Known issuers / known scams | `internal/api/v1/known_issuers.go,known_scams.go` | covered (interfaces only) | no public route registered | F-STELLAR-H007 |
| H.8 | Soroswap pairs registry | migration 0016 + `internal/pipeline/soroswap_registry.go` | covered | unit tests | none |
| H.9 | SDEX offer book | migration 0026 (sdex_offers table) | partial | not exposed via API (B.25 gap) | F-CGCMC-B025 |
| H.10 | Methodology endpoint | `/v1/methodology` | covered | server.go:835 | none |

## I. FX + fiat depth

| # | Surface | Implementation | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| I.1 | ECB official FX | `internal/sources/external/ecb/` | **broken on R1** | F-1212 ecb STOPPED | F-1212 |
| I.2 | Frankfurter (ECB-backed free) | `internal/sources/frankfurter/client.go` | covered | code | none |
| I.3 | Polygon Forex (paid) | `internal/sources/external/polygonforex/` | covered | code | none |
| I.4 | exchangeratesapi (paid) | `internal/sources/external/exchangeratesapi/` | covered | code | none |
| I.5 | FX history backfill | `scripts/ops/fx-history-backfill/main.go` | covered | inventory | none |
| I.6 | Fiat:fiat charts | `/v1/chart` (rc.44 fiat:fiat fix) | covered | live R1 200 | none |
| I.7 | Fiat market cap derivation | rc.42 / rc.43 fix | covered | rc.43 commit | none |
| I.8 | FX snap fallback policy | aggregator FX snap | covered | code; alert `aggregator-fx-snap-fallback-dominant` | F-1219 (alert not loaded) |
| I.9 | FX quote at-or-before history (migration 0028) | `internal/storage/timescale/fx_quotes.go` | covered | `test/integration/fx_quote_at_or_before_test.go` | none |

## J. Operator-grade Stellar tooling

| # | Surface | Implementation | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| J.1 | Hubble cross-check (stellar-etl trade counts) | `cmd/ratesengine-ops/hubble_check.go` | covered | unit tests | none |
| J.2 | Hubble Soroban event check | `cmd/ratesengine-ops/hubble_soroban_events.go` | covered | unit tests | none |
| J.3 | Verify-archive chunks | `cmd/ratesengine-ops/verify_archive_chunks.go` | covered | integration test | none |
| J.4 | WASM extract + history operator command | `cmd/ratesengine-ops/{wasm_extract,wasm_history}.go` | covered | unit tests | none |
| J.5 | Cross-region drift monitor | `cmd/ratesengine-ops/cross_region_*.go` | partial | F-1234 | F-1234 |
| J.6 | SEP-1 refresh operator command | `cmd/ratesengine-ops/sep1_refresh.go` | covered | inventory | none |
| J.7 | Soroswap pairs seeder | `cmd/ratesengine-ops/seed_soroswap_pairs.go` | covered | inventory | none |
| J.8 | Discovery worker | `cmd/ratesengine-ops/discovery.go` | covered | inventory | none |
| J.9 | Mint / upgrade key operator commands | `cmd/ratesengine-ops/{mint_key,upgrade_key}.go` | covered | inventory | F-1240 (no audit log) |
| J.10 | Backfill operator command | `cmd/ratesengine-ops/backfill.go` | covered | unit tests + BackfillSafe gate | none |
| J.11 | Supply snapshot operator command | `cmd/ratesengine-ops/supply.go` | covered | F-1208 (re-test text) | F-1208 |
| J.12 | Decoder WASM matrix doc | `docs/operations/wasm-audits/decoder-wasm-matrix.md` | covered | inventory | none |
| J.13 | Protocol epoch doc | `docs/operations/wasm-audits/protocol-epochs.md` | covered | inventory | none |

## K. Stellar-protocol invariants we enforce

| # | Invariant | Implementation | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| K.1 | i128 / u128 never truncated (ADR-0003) | NUMERIC + big.Int | covered | EV-1235 grep zero hits | none |
| K.2 | No Horizon (ADR-0001) | lint-imports.sh | covered | EV-1234 grep zero hits | none |
| K.3 | S3-compat storage only (ADR-0002) | galexie + minio | covered | EV-1226, EV-1233 | none |
| K.4 | go-stellar-sdk/xdr scoped to scval (ADR-0013) | lint-imports.sh | covered | code | none |
| K.5 | Stellar-rpc not in production ingest | lint-imports.sh + r1 process inventory | covered | EV-1226 (captive-core under galexie, expected) | F-1204 (doc-text fix) |
| K.6 | Ingest runs on Galexie chain only | dispatcher wiring | covered | code | none |
| K.7 | Per-WASM contract-schema audit gate | BackfillSafe flag | covered | `framework_test.go:71-119` | none |
| K.8 | Decoder dispatches by topic[0] symbol, not contract address | dispatcher | covered | `dispatcher.go:255-260` | F-1242 (Comet shared topic untested) |
| K.9 | Map field-name decoding (not positional) | per-decoder code review | partial | most decoders use `MustMapField` (named), but `sep41_supply/decode.go:75-97` reads counterparty by topic position (works for 3- and 4-topic) | F-1242 |

## Roll-up

| Section | Total rows | Covered | Partial | Gap | Non-goal | Broken | Needs evidence |
| --- | --- | --- | --- | --- | --- | --- | --- |
| A | 8 | 7 | 1 | 0 | 0 | 0 | 0 |
| B | 14 | 7 | 4 | 1 | 0 | 3 | 0 |
| C | 8 | 6 | 1 | 0 | 0 | 1 | 0 |
| D | 15 | 14 | 1 | 0 | 0 | 0 | 0 |
| E | 6 | 3 | 2 | 0 | 0 | 0 | 1 |
| F | 8 | 8 | 0 | 0 | 0 | 0 | 0 |
| G | 11 | 11 | 0 | 0 | 0 | 0 | 0 |
| H | 10 | 7 | 2 | 0 | 0 | 1 | 0 |
| I | 9 | 8 | 0 | 0 | 0 | 1 | 0 |
| J | 13 | 12 | 1 | 0 | 0 | 0 | 0 |
| K | 9 | 8 | 1 | 0 | 0 | 0 | 0 |
| **Total** | **111** | **91** | **13** | **1** | **0** | **6** | **1** |

**Stellar depth: 82% covered + 12% partial = 94% baseline coverage.**

Six rows show as **broken on R1 today**:
- B.3 Phoenix (F-1212)
- B.5 Comet (F-1212)
- B.6 Blend (F-1212)
- C.6 Band (F-1212)
- H.4 Lending Blend (F-1212)
- I.1 ECB FX (F-1212)

All six trace to the single root cause: **F-1212 ingestion-source-stopped**.
Restoring those 5 sources (phoenix, comet, blend, band, ecb)
restores 6 matrix rows to `covered` in one operation.

The single true `gap` is **B.11 LP-depth in `/v1/markets`** — no API
endpoint exposes pool depth. Partial coverage on B.12 (router
attribution), B.13 (TVL), B.14 (MEV) means we have the data
shape (migrations 0021, 0025) but no public surface yet.

The audit cannot close while any cell is blank. Every `broken`
row is a Wave 0 pre-flip blocker. Every `gap` is a launch-quality
finding because Stellar depth *is* the product positioning.
