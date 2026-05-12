# W26 — Cross-file interactions and system coupling

## Scope

W26 is the **cross-file workstream gate**: it owns the
[evidence/cross-file-interactions.md](../evidence/cross-file-interactions.md)
ledger AND must remain `in_progress` until every other workstream
W01..W25 has terminal status. This prevents declaring the audit
done with seams unmapped.

In scope:
- every material boundary crossing across the system
- the canonical interaction-class taxonomy (below)
- the per-class roll-up table

## Inputs

- `evidence/cross-file-interactions.md` — master seam ledger
  (`XFI-####` IDs)
- per-workstream cross-references (each W01..W25 declares its
  outbound seams; W26 collects them)

## Required Interaction Classes

For each class below, the ledger must contain at least one
`XFI-####` example that is fully traced. Classes that produce
zero examples = the audit missed a class entirely (finding).

| Class ID | Interaction class | Required example count | Status |
| --- | --- | ---: | --- |
| `XFI-CLASS-01` | binary → config → package | ≥ 6 (one per `cmd/*` binary) | |
| `XFI-CLASS-02` | config example → config parser → deploy template | ≥ 1 | |
| `XFI-CLASS-03` | workflow → script → artifact | ≥ 10 (one per `.github/workflows/*`) | |
| `XFI-CLASS-04` | Dockerfile → binary → runtime flags/env | ≥ 6 (one per `docker/*`) | |
| `XFI-CLASS-05` | migration → Timescale store method → integration test | ≥ 28 (one per migration) | |
| `XFI-CLASS-06` | decoder → event type → pipeline sink → store | ≥ 15 (one per on-chain source) | |
| `XFI-CLASS-07` | external adapter → normalized trade → aggregation | ≥ 9 (one per external adapter) | |
| `XFI-CLASS-08` | aggregator → Redis/Timescale → API handler | ≥ 1 per surface (price, chart, markets, assets) | |
| `XFI-CLASS-09` | API handler → OpenAPI → Go client → frontend client | ≥ 1 per public route family | |
| `XFI-CLASS-10` | metric → alert rule → runbook → service owner | ≥ 1 per alert family | |
| `XFI-CLASS-11` | web route → API hook → UI state → SEO/headers | ≥ 1 per frontend page family | |
| `XFI-CLASS-12` | docs claim → code path → tests/runtime | ≥ 1 per ADR (26 minimum) |
| `XFI-CLASS-13` | systemd unit → ansible role → live R1 process | ≥ 11 (one per unit file in `deploy/systemd/`) | |
| `XFI-CLASS-14` | feature flag / env var → reader → default policy → live behaviour | ≥ 1 per env var documented in `configs/example.toml` | |

## Per-Interaction Loop

For every `XFI-####` row:

1. **Source files.** Exact paths + line ranges
2. **Target files.** Same
3. **Data contract.** Type, schema, encoding, scale, ordering
4. **Failure modes.** What breaks each end?
5. **Tests.** What test crosses the seam end-to-end?
6. **Observability.** Metric / log / alert that covers the seam
7. **Docs claims.** What docs assert about this seam?
8. **Findings or notes.** Real defects raised here

## Closure rule (audit-blocking)

W26 **cannot close** until:

- every class above has the required minimum example count
- every example row is fully populated (no blanks in columns
  3-8 of the per-interaction loop)
- every other workstream W01..W25 is terminal

Marking W26 `done` while *any* other workstream is open is a
process violation that automatically opens an R14 finding.

## Adversarial vectors

- All adversarial vectors in `../10-attack-tree.md` cross at
  least one seam — W26 is the place to verify each crossing
  has a defence noted.

## Cross-workstream dependencies

- All other workstreams contribute outbound seams to W26
- R14 (third-pass falsification) treats unmapped classes as
  audit defects
