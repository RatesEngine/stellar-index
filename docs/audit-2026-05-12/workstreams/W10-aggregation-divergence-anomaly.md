# W10 — Aggregation, divergence, freeze, confidence, anomaly

## Scope

From raw trades to served price + confidence + warning flags +
freeze state.

In scope:
- `internal/aggregate/*.go` (top-level)
- `internal/aggregate/anomaly/*`
- `internal/aggregate/baseline/*`
- `internal/aggregate/changesummary/*`
- `internal/aggregate/confidence/*`
- `internal/aggregate/freeze/*`
- `internal/aggregate/orchestrator/*`
- `internal/divergence/*`
- `cmd/ratesengine-aggregator/main.go` + wiring
- ADR-0015 (last-closed-bucket), ADR-0019 (anomaly response +
  confidence), ADR-0026 (stablecoin late binding)

## Inputs

- `docs/architecture/aggregation-plan.md`
- `docs/architecture/oracle-manipulation-defense.md`
- `04-reconciliation.md` R06 (ADR-0019, 0026)

## Per-file checklist

| File | Role | Tests | Status |
| --- | --- | --- | --- |
| `internal/aggregate/vwap.go` + `_test.go` | volume-weighted average price | | |
| `internal/aggregate/twap.go` + `_test.go` | time-weighted average price | | |
| `internal/aggregate/ohlc.go` + `_test.go` | OHLC derivation (ADR-0020) | | |
| `internal/aggregate/outliers.go` + `_test.go` | per-source-class outlier policy | | |
| `internal/aggregate/triangulate.go` + `_test.go` | XLM-hub triangulation | | |
| `internal/aggregate/stablecoin.go` + `_test.go` | ADR-0026 late binding | | |
| `internal/aggregate/global.go` + `_test.go` | global aggregate orchestration | | |
| `internal/aggregate/doc.go` | package doc accurate | | |
| `internal/aggregate/anomaly/*` | anomaly detection surface | | |
| `internal/aggregate/baseline/*` | multi-window volatility (migration 0007/0008) | | |
| `internal/aggregate/changesummary/*` | % change summary (migration 0022) | | |
| `internal/aggregate/confidence/*` | confidence scoring (ADR-0019) | | |
| `internal/aggregate/freeze/*` | freeze events (migration 0018) | | |
| `internal/aggregate/orchestrator/*` | wiring of all sub-modules | | |
| `internal/divergence/coingecko.go` | CG reference | | |
| `internal/divergence/chainlink.go` + `_test.go` | Chainlink reference | | |
| `internal/divergence/compare.go` | comparison logic | | |
| `internal/divergence/reference.go` | reference selection | | |
| `internal/divergence/worker.go` + `_test.go` | worker loop | | |
| `internal/divergence/doc.go` | package doc accurate | | |
| `cmd/ratesengine-aggregator/main.go` | every sub-module wired + goroutine scheduling | | |

## ADR-0019 invariants

- confidence score in `/v1/price` envelope
- anomaly detection → freeze trigger → divergence_warning flag
- recover-from-freeze path documented + tested

## ADR-0026 invariants

- aggregator maps `USDT→USD`, `USDC→USD`, `PYUSD→USD`,
  `EUROC→EUR`, `EUROB→EUR`, `MXNe→MXN` at VWAP compute time
- ingest stores REAL pair (XLM/USDT), not mapped pair
- on depeg event, divergence_warning fires

## ADR-0015 invariants

- `/v1/price` serves last-closed bucket, not in-progress
- All regions serve byte-identical for the same closed bucket
- Cross-region tools verify (W23)

## VWAP min-trade-count

Investigate why FX pairs use `VWAPMinTradeCount=1` (per rc.43
fix). Is this safe? Does the test exercise the boundary?

## Per-source-class contribution policy

Walk `internal/aggregate/global.go` (or wherever sources are
filtered):

- `ClassExchange` → included in VWAP
- `ClassAggregator` → excluded from VWAP, available as divergence reference
- `ClassOracle` → excluded from VWAP, available as divergence reference
- `ClassAuthoritySanity` → FX snap + sanity check fallback only

Any deviation = finding.

## Adversarial vectors

- A3.1 USDT depeg hidden by late binding
- A3.2 XLM/EUR triangulation when USD/EUR vendor silent
- A3.3 PYUSD/USD route used for fiat market cap when PYUSD depegs
- A2.* hostile vendor responses feed into the VWAP

## Cross-workstream dependencies

- W07 owns decoder-level Trade emission
- W08 owns adapter Class assignment
- W09 owns continuous aggregate refresh
- W11 owns `/v1/price` envelope flags
- W14 owns aggregator-silent / outlier-storm / class-drop-spike / fx-snap-fallback-dominant runbooks

## Closure criteria

- Every per-file row complete
- ADR-0015, ADR-0019, ADR-0026 invariants proven by test reference
- Class contribution policy enforced
- VWAPMinTradeCount=1 for FX rationale captured
- Depeg simulation outcome captured (test or evidence)
