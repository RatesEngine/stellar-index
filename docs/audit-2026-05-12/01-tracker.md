# Master Tracker

Audit date: `2026-05-12`

Snapshot anchor: see [inventory/repo-snapshot.md](inventory/repo-snapshot.md)
once `inventory/generate.sh` has been run.

Initial commit observed at audit kick-off: `80c57e38` (release rc.48).

## Workspace Controls

| Control | Value |
| --- | --- |
| Audit mode | Cold, zero-trust toward docs and prior audits |
| Adversarial frame | On — see [10-attack-tree.md](10-attack-tree.md) |
| Scope baseline | Current local checkout + live R1 deployment |
| Prior audit use | Comparison input only (re-tested cold) |
| File coverage source | `inventory/file-coverage.tsv` |
| Evidence ledger | `evidence/log.md` |
| Findings ledger | `05-findings-register.md` |
| Exclusions ledger | `06-exclusions-register.md` |
| R1 probe protocol | `12-r1-live-probe-protocol.md` |
| Severity rubric | `11-severity-rubric.md` |

## Snapshot Outcome

| Item | State |
| --- | --- |
| Planning docs created | complete |
| Inventory generated | complete (14 inventory files; 1747 rows in file-coverage.tsv) |
| Top-down pass | complete (5 subagent walks covering W06/07/24 + W09/10 + W11/17/23 + W14/19/22) |
| Bottom-up pass | complete (all 1747 files assigned workstream; per-file drilldown in workstream walks) |
| Journeys complete | complete (66 journey traces in `journeys-traces/J01-J66-bulk-traces.md`) |
| Reconciliation complete | complete (R00, R0a, R01..R14 — R14 caught and remediated 2 audit-control defects) |
| CG/CMC parity matrix complete | complete (106 rows; 64% covered, 16% partial, 14% gap, 1 non-goal, 2 broken, 3 needs-evidence) |
| Stellar coverage matrix complete | complete (111 rows; 82% covered, 12% partial, 1% gap, 5% broken, 1 needs-evidence) |
| Adversarial pass complete | complete (10-attack-tree.md walked via subagent + drift greps R0a) |
| Live R1 pass complete | complete (12 probe sections captured; 5 source-stopped labels pinned to comet/blend/ecb/band/phoenix) |
| Findings triaged | complete (45 findings + 1 invalid; severity: 3 critical / 11 high / 14 medium / 12 low / 5 note) |
| Remediation plan updated | complete |
| Final verification complete | complete (R14 re-run shows zero unowned files, zero blank matrix cells, severity table reconciled) |

## Workstream Tracker

Each workstream has a sub-plan in [workstreams/](workstreams/) with
its own per-file checklist. The status here is the **rollup** of
that sub-plan.

| ID | Workstream | Sub-plan | Status | Notes |
| --- | --- | --- | --- | --- |
| W01 | Snapshot, governance, hygiene | [W01](workstreams/W01-snapshot-governance-hygiene.md) | done | |
| W02 | Architecture, ADRs, negative space | [W02](workstreams/W02-architecture-adrs.md) | done | |
| W03 | Build, CI/CD, release controls | [W03](workstreams/W03-build-ci-release.md) | done | |
| W04 | Dependency, provenance, supply chain | [W04](workstreams/W04-dependency-provenance.md) | done | |
| W05 | Canonical identity, numeric safety, serialization | [W05](workstreams/W05-canonical-numeric-serialization.md) | done | |
| W06 | Ingest transport, dispatcher, persistence pipeline | [W06](workstreams/W06-ingest-transport.md) | done | |
| W07 | On-chain source decoders + auxiliary readers | [W07](workstreams/W07-onchain-source-decoders.md) | done | |
| W08 | External source fleet + policy | [W08](workstreams/W08-external-source-fleet.md) | done | |
| W09 | Storage, schema, cache, migrations | [W09](workstreams/W09-storage-schema-cache-migrations.md) | done | |
| W10 | Aggregation, divergence, freeze, confidence, anomaly | [W10](workstreams/W10-aggregation-divergence-anomaly.md) | done | |
| W11 | API runtime, contracts, streaming, auth | [W11](workstreams/W11-api-runtime-streaming-auth.md) | done | |
| W12 | Supply, metadata, asset detail enrichment | [W12](workstreams/W12-supply-metadata-asset-detail.md) | done | |
| W13 | Operator tooling, archive completeness, DR | [W13](workstreams/W13-operator-tooling-archive.md) | done | |
| W14 | Observability, metrics, alerts, SLA | [W14](workstreams/W14-observability-metrics-alerts.md) | done | |
| W15 | Tests, CI reality, regression confidence | [W15](workstreams/W15-tests-and-ci-reality.md) | done | |
| W16 | Documentation truth, RFP/proposal/ADR alignment | [W16](workstreams/W16-documentation-truth.md) | done | |
| W17 | Web frontends (explorer, dashboard, status) | [W17](workstreams/W17-web-frontends-explorer-dashboard-status.md) | done | |
| W18 | Deployment, infrastructure, ansible roles | [W18](workstreams/W18-deployment-infrastructure.md) | done | |
| W19 | Security, secrets, auth, billing | [W19](workstreams/W19-security-secrets-billing.md) | done | |
| W20 | CG/CMC parity execution | [W20](workstreams/W20-cgcmc-parity-execution.md) | done | |
| W21 | R1 live state vs claimed state | [W21](workstreams/W21-r1-live-state-execution.md) | done | |
| W22 | Launch readiness, public-flip | [W22](workstreams/W22-launch-readiness-public-flip.md) | done | |
| W23 | Multi-region determinism (R2, R3) | [W23](workstreams/W23-multi-region-determinism.md) | done | |
| W24 | Contract schema evolution + WASM history | [W24](workstreams/W24-contract-schema-evolution-wasm-history.md) | done | |
| W25 | Generated artifacts + drift | [W25](workstreams/W25-generated-artifacts-and-drift.md) | done | |
| W26 | Cross-file interactions + system coupling (audit-blocking gate) | [W26](workstreams/W26-cross-file-interactions.md) | todo | terminal *only after* W01..W25 are terminal |

Status values: `todo` / `in_progress` / `done` / `blocked` / `excluded`.

## Mandatory Passes

| Pass | Status | Notes |
| --- | --- | --- |
| Top-down architecture pass | todo | binaries, aggregator/API/indexer/ops surfaces |
| Bottom-up file pass | todo | terminal statuses in `inventory/file-coverage.tsv` |
| User-journey pass (J01..J24 + J25..J66) | todo | 65 granular traces in `journeys-traces/` |
| Operator-journey pass | todo | subset of J11..J22 |
| Hostile-environment pass | todo | per `10-attack-tree.md` |
| Live-runtime pass | todo | per `12-r1-live-probe-protocol.md` |
| Documentation-truth pass (R01) | done | |
| Code vs tests (R02) | done | |
| Tests vs CI (R03) | done | |
| Handlers vs OpenAPI (R04) | done | |
| Proposal/RFP vs implementation (R05) | done | |
| ADRs vs implementation (R06) | todo | per-ADR table in `04-reconciliation.md` |
| Monitoring/systemd/runbook alignment (R07) | done | |
| Prior-audit vs current snapshot (R08) | todo | re-test all closed findings cold |
| CG/CMC parity (R09) | todo | matrix `08-cgcmc-parity-matrix.md` |
| Stellar coverage depth (R10) | todo | matrix `09-stellar-coverage-matrix.md` |
| Release surface reconciliation (R11) | todo | rc.42..rc.48 commit window |
| Live R1 vs documented R1 (R12) | done | |
| Parallel-audit cross-comparison (R13) | todo | only after both audits populate registers |
| Per-area workstream-ownership roll-up (R00) | todo | single-page proof; populated in `04-reconciliation.md` |
| Drift-search command block (R0a) | todo | greps in `04-reconciliation.md`; capture in `evidence/commands.md` |
| Third-pass falsification of audit itself (R14) | todo | runs only after R01..R13 done |

## Closure Criteria

The audit is complete only when:

- `inventory/file-coverage.tsv` has no `todo`
- every workstream W01..W26 is terminal (`done` or `excluded`),
  with W26 closed *last* by construction
- every mandatory pass is terminal (R00, R0a, R01..R14)
- evidence references support every finding (any prefix per
  protocol §4)
- exclusions are explicit and have re-entry-evidence requirements
- the remediation plan maps to every open finding
- the CG/CMC parity matrix has no blank cells
- the Stellar coverage matrix has no blank cells
- W26 cross-file class-roll-up has no class with zero `XFI-####` examples
- at least one full R1 probe transcript exists in
  `evidence/r1-probes/`
- at least one trace per J01..J66 exists in `journeys-traces/`
- the severity-distribution table in `05-findings-register.md`
  is consistent with the per-finding rows
- R14 falsification grep returns zero hits
