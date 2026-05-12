# W12 — Supply, metadata, asset detail enrichment

## Scope

Every field in `/v1/assets/{id-or-slug}` and `/v1/assets/verified`,
the verified-currency catalogue, SEP-1 overlay, and supply
derivation surface.

In scope:
- `internal/supply/*` (xlm/classic/sep41 algorithms + cross-check + textfile + overlay + policy)
- `internal/currency/*` (verified-currency catalogue) + `data/seed.yaml`
- `internal/metadata/*` (SEP-1 / stellar.toml resolution)
- `internal/incidents/*` (incident overlay)
- `internal/api/v1/assets*.go` set
- `cmd/ratesengine-ops/supply.go` (snapshot CLI)
- `cmd/ratesengine-ops/sep1_refresh.go`
- ADR-0011, ADR-0021, ADR-0022, ADR-0023

## Inputs

- Stellar coverage matrix section D (supply derivation) + E (metadata)
- CG/CMC parity matrix section A (asset directory)
- CLAUDE.md R-018 description of the verified-currency catalogue

## Per-file checklist

| File | Role | Tests | Status |
| --- | --- | --- | --- |
| `internal/supply/xlm.go` + `_test.go` | algorithm 1 | | |
| `internal/supply/classic.go` + `_test.go` | algorithm 2 (reads observers) | | |
| `internal/supply/sep41.go` + `_test.go` | algorithm 3 (reads mint/burn/clawback) | | |
| `internal/supply/storage_classic_reader.go` + `_test.go` | reader for classic supply | | |
| `internal/supply/storage_sep41_reader.go` + `_test.go` | reader for SEP-41 supply | | |
| `internal/supply/lcm_reader.go` + `_test.go` | LCM-side reader | | |
| `internal/supply/crosscheck.go` + `_test.go` | cross-source check | | |
| `internal/supply/crosscheck_refresher.go` + `_test.go` | refresher loop | | |
| `internal/supply/refresher.go` + `_test.go` | refresher | | |
| `internal/supply/overlay.go` + `_test.go` | overlay surface | | |
| `internal/supply/policy.go` + `_test.go` | policy decisions | | |
| `internal/supply/config_reader.go` + `_test.go` | config | | |
| `internal/supply/key.go` | key construction | | |
| `internal/supply/textfile.go` + `_test.go` | textfile output for node-exporter | | |
| `internal/supply/supply.go` | top-level | | |
| `internal/supply/doc.go` | package doc | | |
| `internal/currency/*.go` | catalogue accessors | | |
| `internal/currency/data/seed.yaml` | verified-currency seed | | |
| `internal/metadata/*.go` (every file) | SEP-1 fetch + cache | | |
| `internal/incidents/*.go` | incident overlay | | |
| `internal/api/v1/assets.go` + `_test.go` | listing + detail | | |
| `internal/api/v1/assets_coin_extension.go` + `_test.go` | rc.47 coin-equivalence overlay | | |
| `internal/api/v1/assets_f2.go` + `_test.go` + `_internal_test.go` | Freighter F2 fields | | |
| `internal/api/v1/assets_global.go` + `_test.go` | GlobalAssetView | | |
| `internal/api/v1/assets_sep1.go` + `_test.go` | SEP-1 overlay | | |
| `internal/api/v1/assets_verified.go` + `_test.go` | verified listing | | |
| `cmd/ratesengine-ops/supply.go` | snapshot CLI (prior finding F-0503 candidate) | | |
| `cmd/ratesengine-ops/sep1_refresh.go` | SEP-1 refresh | | |

## Per-F2-field check

Walk every field in Freighter F2 spec (per `docs/freighter-rfp.md`)
and confirm:

| Field | Source | Code site | Test | Status |
| --- | --- | --- | --- | --- |
| _populate per RFP_ | | | | |

## Verified-currency catalogue propagation

Verify each downstream consumer reads `internal/currency`:

- CoinGecko poller ticker map → `internal/sources/external/coingecko`
- indexer aggregator pair set → `cmd/ratesengine-indexer/main.go`
- unverified-collision warning → `/v1/assets/{id}` handler
- `/v1/assets/verified` listing → `assets_verified.go`
- explorer verified-badge UI → `web/explorer`

## Two-shape contract on `/v1/assets/{slug-or-id}`

CLAUDE.md surprise: slug → `GlobalAssetView`; canonical asset_id
→ `AssetDetail`. Verify:

- routing decision (catalogue lookup before parse)
- wire-shape discriminators on both paths
- explorer handles both shapes
- OpenAPI documents both shapes

## Adversarial vectors

- F1.1 wrong-issuer surfaced for verified slug
- F1.2 SEP-1 overlay returns attacker-controlled fields
- F1.3 chart endpoint returns outlier spikes (W10 boundary)
- B3.3 SEP-1 SSRF via malicious `home_domain`

## Cross-workstream dependencies

- W05 owns canonical types
- W07 owns supply observers
- W09 owns supply tables
- W10 owns chart aggregate consumption
- W14 owns supply-snapshot-* runbooks
- W17 owns explorer UI

## Closure criteria

- Every per-file row terminal
- Every Freighter F2 field has a code site + test
- Verified-currency propagation table complete
- Two-shape contract proven by handler test
- SSRF defence for SEP-1 fetch proven (or finding raised)
