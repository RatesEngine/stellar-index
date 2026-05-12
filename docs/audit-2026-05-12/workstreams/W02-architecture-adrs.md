# W02 — Architecture, ADRs, negative space

## Scope

Audit declared invariants vs implementation. Every ADR is
re-tested cold. Every architecture-doc claim is reconciled
against `internal/` reality. Negative space (deleted /
renamed / unwired packages) is identified.

In scope:
- `docs/adr/0001..0026.md` (note: 0012 is missing — investigate)
- `docs/architecture/*.md`
- package graph (`go list -deps ./...`)
- import-boundary lint baseline + rules
- new packages since 05-02 audit
- legacy / dead packages
- `internal/version`, `internal/config`, `internal/events`,
  `internal/stellarrpc`

## Inputs

- `inventory/adr-inventory.md`
- `inventory/file-coverage.tsv` (filter `top_level=internal`)
- `04-reconciliation.md` R06 ADR table
- `evidence/log.md` EV-1204 (stellar-core still on R1)

## Per-ADR re-test

(Use the table in `04-reconciliation.md` R06; record verdict + EV-#### per row.)

| ADR | Invariant under test | Verification step | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| 0001 | No Horizon | grep `horizon` across `internal/` + `cmd/`; `lint-imports.sh` baseline | | |
| 0002 | S3-compat storage only | `internal/storage/*` adapter list; galexie config | | |
| 0003 | i128/u128 never to int64 | grep `int64\(` + `\.Lo\b`; check decoder paths | | |
| 0004 | Tier-1 three-validator aspiration | check infra docs + ansible roles + roadmap | | |
| 0005 | Monorepo (single go.mod) | confirm only one `go.mod` in tree | | |
| 0006 | Timescale for prices | confirm `internal/storage/timescale/*` + migrations | | |
| 0007 | Redis cache schema (cachekeys is sole builder) | grep imports of `redisclient` outside `cachekeys` | | |
| 0008 | HA topology | docs + ansible roles for patroni / sentinel / haproxy | | |
| 0009 | Latency budget | SLA probe code + p50/p95 from runbook | | |
| 0010 | Off-chain fiat representation (`fiat:USD`) | `AssetFiat` parse + handlers | | |
| 0011 | Supply algorithm | `internal/supply/{xlm,classic,sep41}` | | |
| 0012 | **MISSING** — investigate | `ls docs/adr/0012*` returns nothing | | |
| 0013 | go-stellar-sdk/xdr scoped to scval | lint-imports baseline | | |
| 0014 | Crypto ticker representation | `AssetCrypto` parse | | |
| 0015 | Last-closed-bucket rate serving | `/v1/price` handler logic | | |
| 0016 | Per-region storage strategy | `r1/r2/r3-deployment-state.md` | | |
| 0017 | Archive completeness invariants | `internal/archivecompleteness/*` + hashdb | | |
| 0018 | API consistency surfaces | envelope + pagination cursors | | |
| 0019 | Anomaly response + confidence scoring | `aggregate/{anomaly,confidence,freeze}/*` | | |
| 0020 | Chart API contract | `/v1/chart` handler vs spec | | |
| 0021 | Account-entry observer | `internal/sources/accounts/*` + main.go wiring | | |
| 0022 | Classic supply observers | observer set + classic supply reader | | |
| 0023 | SEP-41 supply observer | `sources/sep41_supply/*` + reader | | |
| 0024 | Redis HA via Sentinel | `redisclient` Sentinel awareness | | |
| 0025 | Caddy + Cloudflare trusted proxy | Caddy config + middleware | | |
| 0026 | Stablecoin fiat-proxy late binding | `aggregate/stablecoin.go` + tests | | |

## Per-architecture-doc reconciliation

For each file, run R01 reconciliation and record:

| Doc | Truth claims tested | Verdict | Evidence |
| --- | --- | --- | --- |
| `docs/architecture/aggregation-plan.md` | | | |
| `docs/architecture/chaos-suite-design-note.md` | | | |
| `docs/architecture/coins-to-assets-migration.md` | | | |
| `docs/architecture/contract-schema-evolution.md` | | | |
| `docs/architecture/coverage-matrix.md` | | | |
| `docs/architecture/ecosystem-review-2026-04-23.md` | | | |
| `docs/architecture/explorer-data-inventory.md` | | | |
| `docs/architecture/explorer-implementation-plan.md` | | | |
| `docs/architecture/ha-plan.md` | | | |
| `docs/architecture/haproxy-ansible-role-design-note.md` | | | |
| `docs/architecture/infrastructure/archival-node-spec.md` | | | |
| `docs/architecture/infrastructure/existing-k8s-assessment.md` | | | |
| `docs/architecture/infrastructure/hosting-options.md` | | | |
| `docs/architecture/infrastructure/multi-region-topology.md` | | | |
| `docs/architecture/infrastructure/validator-rollout.md` | | | |
| `docs/architecture/ingest-pipeline.md` | | | |
| `docs/architecture/k6-load-tests-design-note.md` | | | |
| `docs/architecture/launch-readiness-backlog.md` | | | |
| `docs/architecture/loki-ansible-role-design-note.md` | | | |
| `docs/architecture/multi-network-assets-migration.md` | | | |
| `docs/architecture/oracle-manipulation-defense.md` | | | |
| `docs/architecture/patroni-ansible-role-design-note.md` | | | |
| `docs/architecture/platform-spec.md` | | | |
| `docs/architecture/prometheus-ansible-role-design-note.md` | | | |
| `docs/architecture/redis-sentinel-ansible-role-design-note.md` | | | |
| `docs/architecture/repo-hygiene-plan.md` | | | |
| `docs/architecture/semver-policy.md` | | | |
| `docs/architecture/status-page-hosting-comparison.md` | | | |
| `docs/architecture/supply-pipeline.md` | | | |

## New-package wiring check

For each package added since 05-02, verify it is wired in
`cmd/*/main.go` (or it is a library used by another wired
package), exercised by tests, and described in some doc.

| Package | Wired in | Tests present | Doc | Status |
| --- | --- | --- | --- | --- |
| `internal/aggregate/anomaly` | | | | |
| `internal/aggregate/baseline` | | | | |
| `internal/aggregate/changesummary` | | | | |
| `internal/aggregate/confidence` | | | | |
| `internal/aggregate/freeze` | | | | |
| `internal/aggregate/orchestrator` | | | | |
| `internal/api/streampublish` | | | | |
| `internal/canonical/discovery` | | | | |
| `internal/currency/data` | | | | |
| `internal/dispatcher/statsflush` | | | | |
| `internal/incidents` | | | | |
| `internal/notify` | | | | |
| `internal/platform/postgresstore` | | | | |
| `internal/sources/forex` | | | | |
| `internal/sources/frankfurter` | | | | |
| `internal/sources/external/exchangeratesapi` | | | | |
| `internal/sources/external/polygonforex` | | | | |
| `internal/usage` | | | | |
| `internal/auth/sep10` | | | | |

## Legacy / dead-code check

| Package | Question | Status |
| --- | --- | --- |
| `internal/consumer` | CLAUDE.md says "legacy orchestration seam; current prod ingest is dispatcher-based" — confirm; identify any remaining caller | |
| `internal/stellarrpc` | confirm only used by `rpc-probe` ops diagnostic + fixture capture, not prod ingest | |
| `internal/version` | populated by ldflags? called by all binaries? | |
| `internal/config` | every config field documented in `docs/reference/config/`? | |
| `internal/events` | transport-neutral type model; verify all decoders go through it (not direct xdr) | |

## Adversarial vectors

- A1.* (every hostile XDR family hits dispatcher routing logic)
- A1.9 WASM upgrade mid-backfill (W24 owns the test)
- D2.1 `.discovery-repos/*` accidentally imported (W04 owns the grep)

## Cross-workstream dependencies

- W04 verifies no `.discovery-repos/` imports
- W06 verifies dispatcher behavior (one of the seams here)
- W14 verifies metric-name registration aligns with rule files
- W16 owns the doc-truth pass (R01) for the `docs/architecture/` set

## Closure criteria

- Every ADR row in R06 has a verdict
- 0012 mystery resolved (history check, decision documented)
- Every architecture doc has a verdict
- Every new package is `wired+tested+documented` or has a finding
- Every legacy package is either `still used`, `removed`, or `finding raised`
