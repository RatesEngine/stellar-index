# Remediation Plan

This plan is populated from the 2026-05-02 findings register. It must
stay tied to finding IDs rather than free-form themes.

## Triage Model

- Wave 0: launch-blocking correctness or security defects
- Wave 1: data-integrity and runtime-truth defects
- Wave 2: customer-contract and operator-control defects
- Wave 3: documentation, governance, and residual drift

## Wave 0

None in this snapshot.

## Wave 1

- `F-0502` ✅ — stale `/price/stream` allow-list entry removed
  from `scripts/ci/lint-docs.sh` in PR #472. Closes when that
  PR merges to `main`.

## Wave 2

- `F-0501` ✅ — `deploy/monitoring/README.md` rewritten to
  reflect current CI `promtool check rules` enforcement (the
  `monitoring-rules` job installs `promtool` and runs `make
  monitoring-check` on every PR). Rule-firing unit tests
  (`promtool test rules`) remain a future follow-up; the
  README states this precisely. Landed in this PR.
- `F-0503` ✅ — `cmd/ratesengine-ops supply snapshot` flag
  help and error text rewritten in PR #527 so it explains
  that classic and SEP-41 support exists but the CLI snapshot
  writer intentionally remains native-only and routes
  non-XLM assets to the aggregator-resident goroutine path
  (`[supply] aggregator_refresh_enabled`). Closes when that
  PR merges to `main`.

## Wave 3

- Re-run `make verify` after the above doc/control changes —
  done as part of each remediating PR's verify.sh gate.

## Closure Rules

A finding is only closed when:

- code or docs changes are landed
- verification is rerun
- the findings register disposition is updated

## Remediation status

All three open findings (F-0501, F-0502, F-0503) have
remediating changes shipped:

| Finding | PR | State |
| --- | --- | --- |
| F-0501 | this PR | landed in branch `address-audit-2026-05-02-findings` |
| F-0502 | #472 | open |
| F-0503 | #527 | open |

Once #472 + #527 merge, every audit finding is materially
closed and the workspace can be retired (or kept as the audit
trail for future reviews).
