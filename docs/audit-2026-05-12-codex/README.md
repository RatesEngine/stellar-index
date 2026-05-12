# Codex Cold Adversarial Audit Plan

Audit label: `2026-05-12-codex`

Plan prepared: `2026-05-11`

Snapshot anchor: `80c57e38eeee729ec2d879d54286419206cee864`

This directory is the control center for a cold, adversarial audit of
Rates Engine. It is intentionally not a findings dump. Findings must be
created only after code, configuration, tests, generated artifacts, and
runtime behavior have been verified from primary evidence.

## Documents

| File | Purpose |
| --- | --- |
| [00-plan.md](00-plan.md) | Full audit plan, scope model, workstreams, and pass structure |
| [01-tracker.md](01-tracker.md) | Execution tracker and coverage gates |
| [02-protocol.md](02-protocol.md) | Evidence, per-file, cross-file, hostile, R1, and severity protocol |
| [03-journeys.md](03-journeys.md) | End-to-end user, operator, data, and adversarial journeys |
| [04-reconciliation.md](04-reconciliation.md) | Pass reconciliation, second-pass, and third-pass checks |
| [05-findings-register.md](05-findings-register.md) | Cold findings ledger, initially empty |
| [06-exclusions-register.md](06-exclusions-register.md) | Exclusions ledger, initially empty |
| [07-remediation-plan.md](07-remediation-plan.md) | Remediation planning shell tied to future findings |
| [08-competitive-parity-matrix.md](08-competitive-parity-matrix.md) | CoinGecko/CoinMarketCap parity checklist |
| [09-stellar-depth-matrix.md](09-stellar-depth-matrix.md) | Stellar-native coverage and superiority checklist |
| [10-adversarial-attack-tree.md](10-adversarial-attack-tree.md) | Concrete adversarial test tree |
| [11-r1-live-probe-protocol.md](11-r1-live-probe-protocol.md) | Specific read-only R1 probe plan |
| [12-claude-plan-delta.md](12-claude-plan-delta.md) | Coverage deltas imported after comparing Claude's plan |
| [evidence/](evidence/) | Evidence logs and cross-file interaction ledger |
| [inventory/](inventory/) | Generated tracked-file inventory and area counts |

## Cold-Audit Rules

- Prior audits may be used only to improve checklist shape, never as
  accepted findings or evidence.
- Documentation is not evidence of implementation.
- Every tracked file must reach a terminal inventory status before the
  audit can close.
- Every material cross-file interaction must be traced from caller to
  callee, then to tests, runtime wiring, observability, and docs.
- Every finding must cite primary evidence in this directory.
- R1 access is permitted but is not a substitute for source review; R1
  output can only prove runtime state for the host and time observed.
