# W08 — External source fleet + policy

## Scope

Every non-Stellar venue feeding our aggregator + every FX
adapter, plus the `external` package framework + registry +
policy.

In scope:
- `internal/sources/external/binance/` (REST + WS streamer + backfill + parsing)
- `internal/sources/external/bitstamp/`
- `internal/sources/external/coinbase/`
- `internal/sources/external/kraken/`
- `internal/sources/external/cryptocompare/`
- `internal/sources/external/coingecko/` (poller + backoff + identity + decimal_helpers)
- `internal/sources/external/coinmarketcap/`
- `internal/sources/external/ecb/`
- `internal/sources/external/exchangeratesapi/`
- `internal/sources/external/polygonforex/`
- `internal/sources/forex/` (circulation_data.csv, worker, cache, client)
- `internal/sources/frankfurter/` (client only)
- `internal/sources/external/registry.go` — class/subclass + paid/free + BackfillSafe

## Inputs

- `inventory/external-source-inventory.md`
- ADR-0026 (stablecoin late binding)
- CLAUDE.md "Off-chain sources (CEX/FX) live in `internal/sources/external/`, not `internal/sources/<venue>/`" — but `forex/` and `frankfurter/` *are* off-chain and not under external/. Investigate why; doc it.

## Per-adapter eight-check loop

For each adapter (per `02-protocol.md` §9):

| Check | Result | Evidence |
| --- | --- | --- |
| 1. Vendor truth (URL, rate limits, redistribution licence) | | |
| 2. Auth hygiene (env-only, key absence = source disabled) | | |
| 3. Normalisation (10^8 amount scale, canonical Pair) | | |
| 4. Retry + backoff + jitter (exponential, capped, jittered) | | |
| 5. Clock-skew tolerance | | |
| 6. Class (`ClassExchange` / `ClassAggregator` / `ClassOracle` / `ClassAuthoritySanity`) per registry | | |
| 7. Inclusion policy (aggregator-feeding by default vs divergence-only) | | |
| 8. Backfill safety + failure-mode coverage (5xx, 429, 401, timeout) | | |

## Per-adapter file inventory

| Adapter | Files | Tests | Class (claim) | Inclusion (claim) | Status |
| --- | --- | ---: | ---: | --- | --- |
| `binance` | | | exchange | aggregator-feeding | |
| `bitstamp` | | | exchange | | |
| `coinbase` | | | exchange | | |
| `kraken` | | | exchange | | |
| `cryptocompare` | | | aggregator | divergence-only | |
| `coingecko` | | | aggregator | divergence-only | |
| `coinmarketcap` | | | aggregator | divergence-only | |
| `ecb` | | | authority_sanity | FX feeder | |
| `exchangeratesapi` | | | _verify_ | | |
| `polygonforex` | | | _verify_ | | |
| `forex` (CSV-backed) | | | _verify_ | | |
| `frankfurter` | | | authority_sanity (free, ECB-backed) | | |

## Class-aware contribution policy

Walk `internal/aggregate/*.go` consumption sites; verify each
adapter class is consumed appropriately:

- `ClassExchange` → contributes to VWAP/TWAP
- `ClassAggregator` → divergence reference only (NOT VWAP)
- `ClassOracle` → divergence reference only
- `ClassAuthoritySanity` → FX snap + sanity-check fallback
- mixing two = double-count → finding

## Naming-convention investigation

Why are `forex/` + `frankfurter/` NOT under `external/`?
- Possibly: predates the `external/` framework
- Possibly: framework requires a Source interface not implemented here
- Possibly: integrated via `worker.go` + `client.go` instead of streamer

Capture the actual reason in evidence + decide if a refactor is
warranted (probably W22 cleanup).

## Adversarial vectors

- A2.1..A2.9 (entire hostile-vendor family)
- D1.1 (upstream Go module compromise) — covered W04 but
  affects per-adapter HTTP clients

## Cross-workstream dependencies

- W10 owns aggregator policy; W08 verifies adapter wiring into it
- W19 owns API key hygiene
- W14 owns external-poller-stale alert
- W22 owns the `forex/` vs `external/` cleanup decision

## Closure criteria

- Every adapter has a complete eight-check loop
- Class policy enforced everywhere
- `forex/` + `frankfurter/` naming question resolved
- All adapter failure-mode tests captured
