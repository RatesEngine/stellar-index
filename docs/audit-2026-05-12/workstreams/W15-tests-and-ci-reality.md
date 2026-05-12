# W15 — Tests, CI reality, regression confidence

## Scope

What is actually proven vs what is claimed proven. Distinguish
`go test ./...` happy-path coverage from integration / chaos /
load realism.

In scope:
- repo-wide `go test ./...` (race + coverprofile)
- `test/integration/` (19 files; build tag `integration`)
- `test/chaos/*` (run.sh, scenarios, reports, design note)
- `test/load/*` (k6 scenarios, docker-compose, reports)
- `test/fixtures/{aquarius,phoenix,reflector,soroswap}`
- per-package test density vs risk
- CI matrix coverage (W03 ownership but here we audit test-side
  truth)
- k6 weekly cron schedule + budgets

## Inputs

- `inventory/file-coverage.tsv` (filter test files)
- `04-reconciliation.md` R03 (Tests vs CI)

## Per-package test density

For every package under `internal/`:

| Package | Source files | Test files | Has malformed-input test | Has integration test | Risk class | Status |
| --- | --- | ---: | ---: | --- | --- | --- |
| _populate from inventory_ | | | | | | |

Risk classes:
- `critical` — decoder, dispatcher, storage, supply, auth, ratelimit
- `high` — aggregate, divergence, api/v1, streaming
- `medium` — metadata, currency, notify, usage
- `low` — config, version, doc.go

`critical`+`high` packages with no malformed-input test → finding.

## Integration suite audit

For each file in `test/integration/`:

| File | What it asserts | Requires testcontainers? | In CI? | Status |
| --- | --- | --- | --- | --- |
| `api_registry_cursors_test.go` | | | | |
| `api_test.go` | | | | |
| `assets_test.go` | | | | |
| `baseline_storage_test.go` | | | | |
| `classic_supply_storage_test.go` | | | | |
| `decoders_to_storage_test.go` | end-to-end decoder→sink→storage | | | |
| `discovery_test.go` | | | | |
| `external_fleet_test.go` | external sources | | | |
| `fx_quote_at_or_before_test.go` | FX quote query | | | |
| `issuers_coins_storage_test.go` | | | | |
| `ledgerstream_to_storage_test.go` | ingest pipeline | | | |
| `migrations_test.go` | up + down + idempotency | | | |
| `platform_postgres_stores_test.go` | platform table writes | | | |
| `sep41_supply_storage_test.go` | | | | |
| `storage_test.go` | | | | |
| `supply_storage_test.go` | | | | |
| `trades_range_test.go` | | | | |
| `trades_usd_volume_test.go` | | | | |
| `doc.go` | build-tag declaration | | | |

CI integration job runs how? Investigate workflows.

## Chaos suite audit

- `test/chaos/run.sh` — invocation pattern
- `test/chaos/scenarios/` — scenario catalogue
- `test/chaos/reports/` — historical reports (capture latest
  date + outcome)
- `test/chaos/doc.go` — design note compliance
- `docs/architecture/chaos-suite-design-note.md` reconciliation

## Load suite audit

- `test/load/scenarios/` — k6 scripts
- `test/load/reports/` — historical reports (capture latest)
- `test/load/docker-compose.k6.yaml`
- `test/load/doc.go` — design note compliance
- `docs/architecture/k6-load-tests-design-note.md`
- `.github/workflows/k6-weekly.yml` schedule + budgets
- last weekly run outcome

## Fixture realism

For every fixture file under `test/fixtures/`:
- captured by which script (`scripts/dev/capture-*-fixtures.sh`)?
- last capture date (file mtime + git log)?
- relevant to current contract version (per W24 WASM history)?

## Adversarial vectors

- D1.4 pnpm lockfile unstable across CI runs
- B6.1/B6.2 removed-route abuse not tested?
- A1.* hostile XDR tests count?

## Cross-workstream dependencies

- W03 owns CI workflow definition
- W07 owns decoder fixtures
- W22 owns launch-readiness load-budget claims

## Closure criteria

- Per-package test-density table complete
- Every integration file row complete
- Chaos + load suite age captured
- Fixture realism per-source captured
- Integration tests in CI status known (probably local-only — finding if so)
