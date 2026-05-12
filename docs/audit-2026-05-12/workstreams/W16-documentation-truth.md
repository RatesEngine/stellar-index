# W16 — Documentation truth, RFP/proposal/ADR alignment

## Scope

Every doc claim re-tested against code or runtime.

In scope:
- `docs/stellar-rfp.md`
- `docs/freighter-rfp.md`
- `docs/ctx-proposal.md`
- `docs/discovery/proposal-corrections.md`
- `docs/discovery/rfp-requirements-matrix.md`
- `docs/discovery/delivery-plan.md`
- `docs/discovery/engineering-standards.md`
- `docs/discovery/phase1-closure.md`
- `docs/discovery/decisions.md`
- `docs/discovery/protocol-versions.md`
- `docs/discovery/VERSIONS.md`
- `docs/discovery/repo-structure-plan.md`
- `docs/discovery/adversarial-audit.md`
- `docs/discovery/existing-ctx-rates.md`
- every doc under `docs/operations/` (≈40 files)
- every doc under `docs/architecture/` (W02 owns ADR + arch
  reconciliation; W16 owns the rest)
- every doc under `docs/reference/api/`, `docs/reference/config/`,
  `docs/reference/metrics/`
- `docs/blog/2026-05-07-shipping-the-ia-restructure.md`
- `docs/launch-task-list.md`
- `docs/getting-started.md`
- `docs/review-2026-05-10.md`
- `CHANGELOG.md` (entries vs PRs)
- ADR-side reconciliation is owned by W02 R06 — here we cover
  non-ADR docs

## Inputs

- `inventory/adr-inventory.md` (cross-ref)
- `04-reconciliation.md` R01 + R05
- prior-audit closed findings (re-test cold per R08)

## Per-doc reconciliation

| Doc | Truth claims tested | Verdict | Evidence | Finding |
| --- | --- | --- | --- | --- |
| `docs/stellar-rfp.md` | every customer requirement | | | |
| `docs/freighter-rfp.md` | F1..F12 fields | | | |
| `docs/ctx-proposal.md` | every committed item | | | |
| `docs/discovery/proposal-corrections.md` | every correction landed | | | |
| `docs/discovery/rfp-requirements-matrix.md` | per-row truth | | | |
| `docs/discovery/delivery-plan.md` | calendar vs reality | | | |
| `docs/discovery/engineering-standards.md` | compliance audit | | | |
| `docs/discovery/phase1-closure.md` | Phase 1 closed 2026-04-22 — re-verify scope is locked | | | |
| `docs/discovery/decisions.md` | every decision still standing | | | |
| `docs/discovery/protocol-versions.md` | Whisk / P23 / P22 truth | | | |
| `docs/discovery/VERSIONS.md` | overlaps with root `VERSIONS.md`? — reconcile | | | |
| `docs/discovery/repo-structure-plan.md` | structure plan vs current tree | | | |
| `docs/discovery/adversarial-audit.md` | covers what? | | | |
| `docs/discovery/existing-ctx-rates.md` | how legacy CTX rates inform us | | | |
| `docs/operations/r1-deployment-state.md` | every line vs live R1 (W21) | | | |
| `docs/operations/r2-deployment-state.md` | docs-only state; flag honestly | | | |
| `docs/operations/r3-deployment-state.md` | same | | | |
| `docs/operations/launch-day-checklist.md` | every checklist item executable today | | | |
| `docs/operations/pre-launch-hardening.md` | every item shipped | | | |
| `docs/operations/release-process.md` | matches `cut-release.sh` + `release.yml` | | | |
| `docs/operations/deploy-workflow.md` | matches `deploy.yml` | | | |
| `docs/operations/public-flip.md` | flip plan walkable | | | |
| `docs/operations/multi-region-cutover.md` | actionable; honest about R2/R3 absence | | | |
| `docs/operations/customer-demo-script.md` | every step works against R1 | | | |
| `docs/operations/cdn-setup.md` / `cf-pages-setup.md` / `explorer-deployment.md` / `status-page-setup.md` | up to date with Cloudflare and Wrangler reality | | | |
| `docs/operations/galexie-backfill.md` / `backfill-procedure.md` | match ops binary | | | |
| `docs/operations/archival-node-bringup.md` / `archive-completeness.md` | match `verify-archive-chunks` | | | |
| `docs/operations/hubble-event-counts.md` | match `hubble_check` + `hubble_soroban_events` | | | |
| `docs/operations/sac-wrappers-and-usd-volume.md` | match sac_balances + USD volume code | | | |
| `docs/operations/sep1-resolution.md` | match `internal/metadata` | | | |
| `docs/operations/sev-playbook.md` | severity routing vs alertmanager | | | |
| `docs/operations/sla-probe.md` | match `cmd/ratesengine-sla-probe` | | | |
| `docs/operations/sla-proof-procedure.md` + `sla-proof-template.md` | actionable | | | |
| `docs/operations/status-page-setup.md` | match `web/status` + `status-page.yml` | | | |
| `docs/operations/supply-snapshot.md` | match `cmd/ratesengine-ops/supply.go` | | | |
| `docs/operations/rollback.md` | match `deploy.yml` rollback path | | | |
| `docs/operations/post-launch-queries.md` | SQL snippets execute | | | |
| `docs/operations/perf-todo.md` | currency of todos | | | |
| `docs/operations/operator-unblock-2026-05-08.md` | resolved? archive if so | | | |
| `docs/operations/wasm-audits/<source>.md` | per-source audit log (W24) | | | |
| `docs/operations/drills/*` | drill scenarios + reports | | | |
| `docs/operations/incidents/*` | incident catalogue | | | |
| `docs/operations/postmortems/*` | template + per-incident files | | | |
| `docs/operations/runbooks/*` | covered in W14 | n/a | | |
| `docs/operations/alerts-catalog.md` | match rule files | | | |
| `docs/reference/api/*` | auto-gen freshness | | | |
| `docs/reference/config/*` | auto-gen freshness | | | |
| `docs/reference/metrics/*` | auto-gen freshness | | | |
| `docs/blog/2026-05-07-shipping-the-ia-restructure.md` | published claims accurate | | | |
| `docs/launch-task-list.md` | active task list | | | |
| `docs/getting-started.md` | walkable cold | | | |
| `docs/review-2026-05-10.md` | review notes still relevant? | | | |
| `CHANGELOG.md` (rc.42..rc.48 window) | entries vs PRs (R11) | | | |

## Prior-audit closed-finding re-test (R08)

Carryover from `docs/audit-2026-04-29` + `docs/audit-2026-05-02`:

- F-0501 (monitoring README claim re CI promtool) — re-test
- F-0502 (OpenAPI lint planned-allow-list for `/v1/price/stream`)
  — re-test
- F-0503 (supply CLI text drift) — re-test
- every other prior finding regardless of status

## Adversarial vectors

- F2.2 Public blog post claim diverges from shipped behavior
- G.* compliance / legal docs

## Cross-workstream dependencies

- W02 owns ADR + `docs/architecture/` reconciliation
- W14 owns runbook content
- W21 cross-refs `docs/operations/r1-deployment-state.md`
- W22 owns launch readiness

## Closure criteria

- Every per-doc row complete
- Every prior-audit finding re-tested cold and recorded
- Phase 1 closure claim re-verified (R-018 still in scope?)
- CHANGELOG rc.42..rc.48 entries verified
