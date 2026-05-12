# Remediation Plan

This plan is populated from the 2026-05-12 findings register. It
must stay tied to finding IDs rather than free-form themes.

## Triage Model

Findings are sequenced into four waves. Wave assignment depends
on severity (per [11-severity-rubric.md](11-severity-rubric.md))
**and** launch-blocking impact.

- **Wave 0 — Block public flip.**
  Critical or high findings that could cause data loss, security
  breach, brand-ending incident, money loss, or violation of
  customer commitments at launch.
- **Wave 1 — Pre-public-traffic.**
  Data-integrity, runtime-truth, or operability findings that
  must be fixed before the first paying customer integrates,
  but don't block the flip itself.
- **Wave 2 — Pre-scale.**
  Customer-contract, observability, or operator-control findings
  that should be fixed before traffic ramps past prototype scale.
- **Wave 3 — Documentation, governance, residual drift.**
  Doc rewrites, naming consistency, comment cleanup,
  CHANGELOG corrections.

## Per-Finding Sequence

| Finding | Wave | Owner | Target | PR / Branch | Status |
| --- | --- | --- | --- | --- | --- |
| F-1201 (explorer still calls removed routes) | 0 | _assign_ | before next explorer-deploy.yml run | _open PR_ | open |
| F-1202 (curl example for `/v1/coins`) | 1 | _assign_ | next docs PR | _open PR_ | open |
| F-1203 (R1 binary still serving `/v1/coins`) | 0 | _assign_ | confirm post rc.48 deploy completes | _depends on deploy.yml run_ | open |
| F-1204 (stellar-core still on R1 vs CLAUDE.md claim) | 1 | _assign_ | reconcile doc OR remove process | _open PR_ | open |
| F-1205 (smoke script path mystery) | 1 | _assign_ | resolve path; update both ends | _open PR_ | open |
| F-1206 (R1 disk + memory headroom) | 2 | _assign_ | retention review + mem ranking | _open PR_ | open |
| F-1207 (`.gitignore` missing `*.secrets.yml`) | 2 | _assign_ | one-line gitignore PR | _open PR_ | open |
| F-1208 (supply CLI text drift re-test) | 3 | _assign_ | cold re-test from prior 05-02 | _re-test_ | open |
| F-1209 (parallel codex audit exists) | 3 | _info_ | n/a — R13 reconciliation only | n/a | note |
| F-1210 (handler files for removed routes still in tree) | 0 | _assign_ | next API PR | _open PR_ | open |
| F-1211 (OpenAPI still references removed `/v1/coins`) | 1 | _assign_ | docs PR + spectral lint update | _open PR_ | open |

## Closure Rules

A finding is only closed when:

- code or docs changes are landed
- verification is rerun (`bash scripts/dev/verify.sh` plus,
  for ingest changes, `make test-integration`)
- the findings register disposition is updated to
  `closed-by-PR-####`
- if the change altered live R1 behavior, an R1 probe transcript
  confirms post-change state

## Wave 0 Exit Criteria (public-flip gate)

Before flipping the public DNS / opening the gates, **every
Wave 0 finding** is closed and:

- `docs/operations/launch-day-checklist.md` re-walked end-to-end
- `docs/operations/public-flip.md` executed in dry-run on a
  staging copy
- `docs/operations/customer-demo-script.md` works against R1
  with no internal hand-holding
- the CG/CMC parity matrix has zero `gap` rows of severity
  high or critical
- the Stellar coverage matrix has zero `gap` rows of severity
  high or critical
- a complete R1 probe transcript covers all 12 sections of
  [12-r1-live-probe-protocol.md](12-r1-live-probe-protocol.md)

## Cross-Cutting Themes (populated as findings cluster)

When ≥3 findings share a root cause (e.g. "decoder packages
lack malformed-input tests"), promote it here with the list of
finding IDs and a single remediation commit/PR strategy.

| Theme | Findings | Strategy | Owner | Status |
| --- | --- | --- | --- | --- |
| _populated_ | | | | |

## Remediation Status Roll-Up

| Wave | Open | Accepted | Closed | Total |
| --- | --- | --- | --- | --- |
| 0 | 3 | 0 | 0 | 3 |
| 1 | 4 | 0 | 0 | 4 |
| 2 | 2 | 0 | 0 | 2 |
| 3 | 2 | 0 | 0 | 2 |
