# W13 — Operator tooling, archive completeness, DR

## Scope

Every `cmd/ratesengine-ops` subcommand + SLA probe + migrate +
archive completeness daemon + the operator scripts under
`scripts/ops/` + every operator runbook (the runbook content
audit lives in W14; this workstream owns the *tool* side).

In scope:
- `cmd/ratesengine-ops/*.go` (12 subcommands)
- `cmd/ratesengine-sla-probe/*`
- `cmd/ratesengine-migrate/*`
- `internal/archivecompleteness/*`
- `configs/audit/wasm-walk-contracts.yaml`
- `scripts/ops/cf-pages-bootstrap.sh`, `circulation-fetch`,
  `fx-history-backfill`, `pre-launch-check.sh`,
  `recompute-usd-volume-soroban.sql`

## Inputs

- `docs/operations/archival-node-bringup.md`
- `docs/operations/archive-completeness.md`
- `docs/operations/backfill-procedure.md`
- `docs/operations/sla-probe.md`
- `docs/operations/wasm-audits/`

## Per-subcommand checklist

| Subcommand | File | Purpose | Tests | Runbook | Status |
| --- | --- | --- | --- | --- | --- |
| `backfill` | `backfill.go` + `_test.go` | backfill a range; gated by BackfillSafe | | `docs/operations/backfill-procedure.md` | |
| `cross-region-check` | `cross_region_check.go` + `_test.go` | one-shot drift check | | | |
| `cross-region-monitor` | `cross_region_monitor.go` + `_test.go` | continuous drift monitor | | | |
| `discovery` | `discovery.go` | asset discovery worker | | | |
| `hubble-check` | `hubble_check.go` + `_test.go` | trade-count check vs stellar-etl | | | |
| `hubble-soroban-events` | `hubble_soroban_events.go` + `_test.go` | event-count check | | | |
| `mint-key` | `mint_key.go` | API-key minting | | | |
| `seed-soroswap-pairs` | `seed_soroswap_pairs.go` | seed table 0016 | | | |
| `sep1-refresh` | `sep1_refresh.go` | SEP-1 metadata refresh | | | |
| `supply` | `supply.go` | supply snapshot CLI | | `docs/operations/supply-snapshot.md` | |
| `upgrade-key` | `upgrade_key.go` | key tier change | | | |
| `verify-archive-chunks` | `verify_archive_chunks.go` + `_test.go` + `_integration_test.go` | archive completeness verifier | | `docs/operations/archive-completeness.md` | |
| `wasm-extract` | `wasm_extract.go` + `_test.go` | extract WASM bytes from network | | `docs/operations/wasm-audits/` | |
| `wasm-history` | (test only) | WASM history walk | | | |
| `main.go` | dispatch + global flags | | | | |

## SLA probe sub-audit

- `cmd/ratesengine-sla-probe/*` — probe loop, textfile writer,
  measurement model
- `configs/healthchecks/ratesengine-sla-probe.{service,timer}`
- `configs/healthchecks/sla-probe.sh`
- output → `/var/lib/node_exporter/textfile/*.prom`
- consumed by `deploy/monitoring/rules/sla-probe.yml`

## Migrate sub-audit

- `cmd/ratesengine-migrate/main.go` runs all 28 migrations in
  order
- `migrations/README.md` accuracy
- failure path: half-applied migration recovery

## Archive completeness sub-audit

- `internal/archivecompleteness/*` Tier A/B/C/D semantics
- ADR-0017 invariants
- alert + runbook coverage:
  - `archive-completeness-stale`
  - `archive-divergence`
  - `archive-files-missing`
  - `archive-publish`
  - `archive-repair-source-degraded`
  - `verify-archive-run-stale`
  - `verify-archive-unit-failed`

## Per-ops-script audit

| Script | Purpose | Tests / dry-run mode | Status |
| --- | --- | --- | --- |
| `scripts/ops/cf-pages-bootstrap.sh` | bootstrap Cloudflare Pages | | |
| `scripts/ops/circulation-fetch` | fetch fiat circulation | | |
| `scripts/ops/fx-history-backfill` | rc.44 Frankfurter backfill | | |
| `scripts/ops/pre-launch-check.sh` | pre-launch validation | | |
| `scripts/ops/recompute-usd-volume-soroban.sql` | one-off USD-volume recompute | | |

## CLI help text accuracy (prior F-0503 lesson)

For every subcommand:
- run `--help` and capture
- compare against current code reality
- compare against current supply/aggregator architecture

The `supply` subcommand was the source of F-0503 — text claimed
classic + SEP-41 computers were unshipped but they exist now.
Re-test cold: did the rc.42..rc.48 work fix the text?

## Adversarial vectors

- E1.1..E1.4 privileged commands
- E1.3 backfill DoS via huge range
- C4.1..C4.3 archive corruption / replication

## Cross-workstream dependencies

- W14 owns runbook content
- W18 owns systemd unit provisioning
- W24 owns WASM audit doc per source

## Closure criteria

- Every subcommand row complete
- Every ops script row complete
- SLA probe + migrate + archive completeness sub-audits complete
- CLI help text accuracy verified for all subcommands
