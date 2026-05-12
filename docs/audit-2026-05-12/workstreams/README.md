# Workstream Sub-Plans

One file per workstream W01..W24. Each file is the auditor's
working checklist for that workstream and is the canonical
place to record per-file checks.

## How to use

1. Open the workstream sub-plan you're about to execute.
2. Walk every check; mark each result inline.
3. When a finding is identified, log it in
   `../05-findings-register.md` and back-link from the
   workstream check.
4. Capture supporting evidence in `../evidence/workstreams/W##-<topic>.md`.
5. When all checks for a workstream are terminal, mark the
   workstream `done` in `../01-tracker.md`.

## Workstream Index

| ID | Workstream | File |
| --- | --- | --- |
| W01 | Snapshot, governance, hygiene | [W01-snapshot-governance-hygiene.md](W01-snapshot-governance-hygiene.md) |
| W02 | Architecture, ADRs, negative space | [W02-architecture-adrs.md](W02-architecture-adrs.md) |
| W03 | Build, CI/CD, release controls | [W03-build-ci-release.md](W03-build-ci-release.md) |
| W04 | Dependency, provenance, supply chain | [W04-dependency-provenance.md](W04-dependency-provenance.md) |
| W05 | Canonical identity, numeric safety, serialization | [W05-canonical-numeric-serialization.md](W05-canonical-numeric-serialization.md) |
| W06 | Ingest transport, dispatcher, persistence pipeline | [W06-ingest-transport.md](W06-ingest-transport.md) |
| W07 | On-chain source decoders + auxiliary readers | [W07-onchain-source-decoders.md](W07-onchain-source-decoders.md) |
| W08 | External source fleet + policy | [W08-external-source-fleet.md](W08-external-source-fleet.md) |
| W09 | Storage, schema, cache, migrations | [W09-storage-schema-cache-migrations.md](W09-storage-schema-cache-migrations.md) |
| W10 | Aggregation, divergence, freeze, confidence, anomaly | [W10-aggregation-divergence-anomaly.md](W10-aggregation-divergence-anomaly.md) |
| W11 | API runtime, contracts, streaming, auth | [W11-api-runtime-streaming-auth.md](W11-api-runtime-streaming-auth.md) |
| W12 | Supply, metadata, asset detail enrichment | [W12-supply-metadata-asset-detail.md](W12-supply-metadata-asset-detail.md) |
| W13 | Operator tooling, archive completeness, DR | [W13-operator-tooling-archive.md](W13-operator-tooling-archive.md) |
| W14 | Observability, metrics, alerts, SLA | [W14-observability-metrics-alerts.md](W14-observability-metrics-alerts.md) |
| W15 | Tests, CI reality, regression confidence | [W15-tests-and-ci-reality.md](W15-tests-and-ci-reality.md) |
| W16 | Documentation truth, RFP/proposal/ADR alignment | [W16-documentation-truth.md](W16-documentation-truth.md) |
| W17 | Web frontends (explorer, dashboard, status) | [W17-web-frontends-explorer-dashboard-status.md](W17-web-frontends-explorer-dashboard-status.md) |
| W18 | Deployment, infrastructure, ansible roles | [W18-deployment-infrastructure.md](W18-deployment-infrastructure.md) |
| W19 | Security, secrets, auth, billing | [W19-security-secrets-billing.md](W19-security-secrets-billing.md) |
| W20 | CG/CMC parity execution | [W20-cgcmc-parity-execution.md](W20-cgcmc-parity-execution.md) |
| W21 | R1 live state vs claimed state | [W21-r1-live-state-execution.md](W21-r1-live-state-execution.md) |
| W22 | Launch readiness, public-flip | [W22-launch-readiness-public-flip.md](W22-launch-readiness-public-flip.md) |
| W23 | Multi-region determinism (R2, R3) | [W23-multi-region-determinism.md](W23-multi-region-determinism.md) |
| W24 | Contract schema evolution + WASM history | [W24-contract-schema-evolution-wasm-history.md](W24-contract-schema-evolution-wasm-history.md) |
| W25 | Generated artifacts + drift | [W25-generated-artifacts-and-drift.md](W25-generated-artifacts-and-drift.md) |
| W26 | Cross-file interactions + system coupling (gate) | [W26-cross-file-interactions.md](W26-cross-file-interactions.md) |

## Sub-Plan File Structure

Each W##.md file has:

- **Scope** — what this workstream covers + does NOT cover
- **Inputs** — relevant inventories, prior-audit references,
  matrix rows
- **Per-file checklist** — every file in scope, with status
- **Per-decoder / per-route / per-migration loop reminders**
  pulling from `../02-protocol.md`
- **Adversarial vectors** — relevant rows from `../10-attack-tree.md`
- **Cross-workstream dependencies** — what other workstreams
  feed in or out
- **Closure criteria** — what counts as `done` for this workstream
- **Findings raised** — back-links to `../05-findings-register.md`
