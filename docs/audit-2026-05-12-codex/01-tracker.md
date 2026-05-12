# Master Tracker

Audit label: `2026-05-12-codex`

Plan prepared: `2026-05-11`

Snapshot anchor: `80c57e38eeee729ec2d879d54286419206cee864`

## Workspace Controls

| Control | Value |
| --- | --- |
| Audit mode | Cold, adversarial, zero-trust toward docs and prior findings |
| Prior audit use | Format and checklist inspiration only |
| Current findings imported | none |
| Tracked file source | `git ls-files` |
| Tracked file count before this directory | `1,747` |
| Execution-time scope drift | current `HEAD` advanced to `8a61c17e...`; the inventory remains reconciled at `1,872` tracked rows on 2026-05-12 after `internal/customerwebhook/ssrf.go` and the committed key-policy middleware/test files entered scope; see `EV-0063`, `EV-0078`, `EV-0097`, `EV-0098`, `EV-0099`, and `EV-0101` |
| File inventory | [inventory/file-coverage.tsv](inventory/file-coverage.tsv) |
| Evidence ledger | [evidence/log.md](evidence/log.md) |
| Command ledger | [evidence/commands.md](evidence/commands.md) |
| Cross-file ledger | [evidence/cross-file-interactions.md](evidence/cross-file-interactions.md) |
| Findings ledger | [05-findings-register.md](05-findings-register.md) |
| Exclusions ledger | [06-exclusions-register.md](06-exclusions-register.md) |

## Planning Outcome

| Item | State | Notes |
| --- | --- | --- |
| Audit control directory created | complete | Planning artifact only |
| Inventory generation protocol | complete | See `inventory/generate.sh` |
| Initial tracked inventory generated | complete | Statuses start as `todo` |
| Findings imported from prior work | not_applicable | Cold audit forbids it |
| R1 runtime queried | complete | Read-only service, firewall, timer, health, capacity, alert, Caddy, SSE, history, and price-parity probes recorded in [evidence/r1-runtime.md](evidence/r1-runtime.md) |
| Plan second pass | complete | Scope reconciled against tracked top-level file counts |
| Plan third pass | complete | Additional competitive, R1, web, and cross-file gates added |
| Claude plan delta reviewed | complete | See [12-claude-plan-delta.md](12-claude-plan-delta.md) |

## Workstream Tracker

| ID | Workstream | Status | Required Evidence |
| --- | --- | --- | --- |
| W01 | Snapshot, repository hygiene, and ownership | in_progress | EV-0015, EV-0018 |
| W02 | Architecture, ADRs, and negative space | todo | code-to-doc trace |
| W03 | Build, toolchain, reproducibility, and release | in_progress | EV-0013, EV-0009, EV-0031, EV-0032, EV-0037, EV-0041, EV-0099 |
| W04 | Dependency, provenance, and supply chain | in_progress | EV-0014, EV-0015, EV-0025, EV-0026, EV-0099 |
| W05 | Configuration and secret boundaries | in_progress | EV-0022, EV-0024, EV-0033, EV-0083, EV-0084, EV-0098 |
| W06 | Canonical identity, asset semantics, and numeric safety | todo | code refs, tests |
| W07 | Ledger ingest, transport, backfill, and dispatch | in_progress | EV-0040, EV-0044, EV-0080 |
| W08 | Stellar DEX and Soroban source decoders | in_progress | EV-0044 |
| W09 | Stellar account, supply, and balance observers | in_progress | EV-0047 |
| W10 | Oracle and reference-price source decoders | in_progress | EV-0045 |
| W11 | External market-data source fleet | in_progress | EV-0046, EV-0098 |
| W12 | Storage, migrations, and query correctness | in_progress | EV-0017, EV-0058, EV-0059, EV-0060, EV-0062, EV-0079, EV-0080, EV-0086, EV-0092 |
| W13 | Redis, cache keys, streaming pub/sub, and freshness | in_progress | EV-0038, EV-0039, EV-0084, EV-0094 |
| W14 | Aggregation, baselines, anomaly, freeze, and confidence | in_progress | EV-0024, EV-0079 |
| W15 | API runtime, middleware, contracts, and client SDK | in_progress | EV-0010, EV-0011, EV-0012, EV-0068, EV-0069, EV-0076, EV-0086, EV-0089, EV-0090, EV-0092, EV-0094, EV-0095, EV-0096, EV-0098, EV-0099, EV-0100 |
| W16 | Dashboard, explorer, status page, SEO, and embeds | in_progress | EV-0012, EV-0014, EV-0090 |
| W17 | Observability, metrics, alerts, status, and incident flow | in_progress | R1-0010, EV-0073, EV-0098 |
| W18 | Operations, R1 runtime, archive completeness, and DR | in_progress | R1-0001 through R1-0010, EV-0073, EV-0082 |
| W19 | Security, auth, abuse, and privacy | in_progress | F-1201, F-1207, EV-0015, EV-0068, EV-0069, EV-0083, EV-0084, EV-0086, EV-0096, EV-0098, EV-0099, EV-0100 |
| W20 | Tests, fixtures, chaos, load, and CI reality | in_progress | EV-0006, EV-0013, EV-0041, EV-0070, EV-0095, EV-0099 |
| W21 | Documentation truth and customer commitments | in_progress | EV-0021, EV-0032, EV-0039, EV-0040, EV-0072, EV-0076, EV-0082, EV-0090, EV-0095, EV-0096 |
| W22 | Competitive product completeness | in_progress | EV-0035, EV-0038, EV-0040, EV-0042 |
| W23 | Generated artifacts and drift | in_progress | F-1203 |
| W24 | Cross-file interaction and system coupling | in_progress | XFI-0001 through XFI-0051 |

## Mandatory Pass Tracker

| Pass | Status | Exit Criteria |
| --- | --- | --- |
| P1 Inventory and topology | in_progress | inventories refreshed; topology evidence added |
| P2 Top-down architecture | in_progress | API/web/R1/deploy slices traced |
| P3 Bottom-up file review | todo | every tracked file terminal |
| P4 End-to-end journeys | in_progress | API removal and R1 ops journeys have evidence refs |
| P5 Hostile environment | in_progress | R1 public exposure, source failure, dependency advisories tested |
| P6 Docs-truth and customer-truth | todo | material doc claims classified |
| P7 Competitive completeness | todo | generic and Stellar-specific gaps classified |
| P8 Second-pass reconciliation | in_progress | cross-file ledger populated with current findings |
| P9 Third-pass adversarial review | in_progress | self-gates identified further findings instead of closure |

## Closure Checklist

| Gate | Status |
| --- | --- |
| No `todo` file rows remain | todo |
| Current tracked tree reconciled to inventory | complete |
| No untriaged `blocked` rows remain | todo |
| All findings have evidence | in_progress |
| All evidence has source anchors | in_progress |
| All exclusions have rationale | todo |
| All remediation items map to findings | in_progress |
| R1 checks complete or explicitly excluded | in_progress |
| Competitive completeness reviewed | todo |
| Competitive parity matrix complete | todo |
| Stellar-depth matrix complete | todo |
| Attack tree complete | todo |
| Public-flip readiness complete | todo |
| WASM/schema evolution gate complete | todo |
| Second pass complete | todo |
| Third pass complete | todo |
