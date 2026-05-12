# Stellar Depth Matrix

This matrix covers the product's durable differentiator: protocol-level
Stellar data that generic market-data competitors do not expose deeply.

Statuses: `covered`, `partial`, `gap`, `non_goal`, `not_applicable`.

Every `gap` requires a finding or explicit product-positioning decision.

## Unified Asset And Identity Model

| ID | Surface | Expected Evidence | Status | Finding |
| --- | --- | --- | --- | --- |
| SD-A01 | Native XLM | canonical parser, API, tests | | |
| SD-A02 | Classic assets | code/issuer parser, slug and API | | |
| SD-A03 | SEP-41 contract assets | contract ID parser and API | | |
| SD-A04 | Fiat assets | `fiat:*` representation and ADR/code | | |
| SD-A05 | SAC/classic equivalence | SAC observer and double-count guard | | |
| SD-A06 | Verified issuer catalogue | seed data and API/UI exposure | | |
| SD-A07 | Collision and scam warnings | known issuers/scams behavior | | |

## On-Chain Market Coverage

| ID | Surface | Expected Evidence | Status | Finding |
| --- | --- | --- | --- | --- |
| SD-B01 | SDEX classic | decoder, storage, tests | | |
| SD-B02 | Soroswap swap/sync pairing | decoder, registry, storage | | |
| SD-B03 | Phoenix grouped events | grouping invariant and tests | | |
| SD-B04 | Aquarius | decoder and fixtures | | |
| SD-B05 | Comet | decoder and contract/topic filtering | | |
| SD-B06 | Blend auctions/lending | decoder, table, API/UI | | |
| SD-B07 | CAP-67/unified events readiness | dispatcher and tests | | |
| SD-B08 | Topic-based dispatch | dispatcher logic | | |
| SD-B09 | Router attribution | migration/store/API/UI | | |
| SD-B10 | TVL/reserves/MEV | migration/store/API/UI | | |
| SD-B11 | Per-WASM decoder safety | audit docs, history, BackfillSafe | | |

## Oracles, FX, And Fiat

| ID | Surface | Expected Evidence | Status | Finding |
| --- | --- | --- | --- | --- |
| SD-C01 | Reflector DEX/CEX/FX contracts | decoder and storage | | |
| SD-C02 | Redstone adapter | decoder, op args, feed ID checks | | |
| SD-C03 | Band relay/force_relay | contract-call decoder | | |
| SD-C04 | SEP-40 endpoint compatibility | handler, OpenAPI, tests | | |
| SD-C05 | ECB/Frankfurter/Polygon/ExchangeRatesAPI | adapters, fallback policy | | |
| SD-C06 | FX history at-or-before | migration/store/API | | |

## Supply And Issuer Semantics

| ID | Surface | Expected Evidence | Status | Finding |
| --- | --- | --- | --- | --- |
| SD-D01 | XLM supply | algorithm and tests | | |
| SD-D02 | Classic supply | observers and tests | | |
| SD-D03 | SEP-41 supply | mint/burn/clawback observers | | |
| SD-D04 | Account/trustline/claimable observers | source packages and storage | | |
| SD-D05 | LP reserve and SAC balance observers | double-count safety | | |
| SD-D06 | Supply cross-checks | crosscheck code and alerts | | |
| SD-D07 | Asset detail supply fields | API/web/docs truth | | |
| SD-D08 | Freeze/clawback semantics | source/API flags | | |

## Provenance And Reproducibility

| ID | Surface | Expected Evidence | Status | Finding |
| --- | --- | --- | --- | --- |
| SD-E01 | Galexie to S3-compatible archive | deploy/runtime evidence | | |
| SD-E02 | Hash DB drift detector | code, migration, R1 state | | |
| SD-E03 | Archive completeness tiers | checker, metrics, alerts | | |
| SD-E04 | Cross-region deterministic serving | tool, API contract, docs truth | | |
| SD-E05 | WASM history | migration, ops command, audit docs | | |
| SD-E06 | Decoder audit logs | docs/operations/wasm-audits coverage | | |

## Stellar-Aware Aggregation And Products

| ID | Surface | Expected Evidence | Status | Finding |
| --- | --- | --- | --- | --- |
| SD-F01 | Class-aware source contribution | registry and aggregation policy | | |
| SD-F02 | Stablecoin late binding | code, flags, docs | | |
| SD-F03 | XLM hub triangulation | code and tests | | |
| SD-F04 | Multi-window baseline | migration/code/tests | | |
| SD-F05 | Confidence/anomaly/freeze | code, API flags, alerts | | |
| SD-F06 | Decoder stats and change summary | migrations, writers, API/UI | | |
| SD-F07 | Freighter F1/F2 fields | RFP fields to API evidence | | |
| SD-F08 | Network stats | endpoint and data source | | |
| SD-F09 | Issuer directory | API/UI/store | | |
| SD-F10 | Soroswap pair registry | migration/store/ops | | |
