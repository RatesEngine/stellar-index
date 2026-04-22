---
title: Runbook — <alert-name>
last_verified: YYYY-MM-DD
status: draft | ratified
severity: P1 | P2 | P3
---

# Runbook — `<alert_name>`

<!--
Template for every per-alert runbook. Copy to
docs/operations/runbooks/<alert-name>.md and replace the
placeholder sections. Keep it short — a responder woken at 3 AM
reads this first, so structure and speed matter more than
completeness.

Every runbook MUST contain each of the sections below. CI check
fails the PR otherwise (TODO(#0) — add the section check to
scripts/ci/lint-docs.sh).
-->

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `<alert_name>` |
| Severity | P1 / P2 / P3 |
| Detected by | Prometheus rule in `deploy/monitoring/rules/<area>.yml` |
| Typical MTTR | X min |
| Impact | One sentence describing customer impact. |

## Symptoms

What the alert is telling you. 1–3 bullets. Include the expected
dashboard view and the specific metric value range.

## Quick diagnosis (≤ 5 min)

Three or four commands / checks to run first. Each should produce
a clear signal of "yes this is real" or "false alarm."

```sh
# example
kubectl ... / psql ... / curl ...
```

## Mitigation (≤ 15 min)

Steps to bring the service back to green. Prefer reversible
mitigations (fail-over, drain, reset) over forward-fixes during
the incident window.

- [ ] Step 1 — verb + target.
- [ ] Step 2.
- [ ] Verification: the metric that should clear the alert within
      {N} seconds after mitigation.

## Root cause analysis

What to gather for the postmortem. Log files, metric screenshots,
subsystem-specific diagnostics that'd take > 5 min to run.

## Known false-positive patterns

Scenarios where this alert fires but no customer impact exists.
Each documented here is one less 3 AM page for the next responder.

## Related

- Postmortems tagged `<alert-name>` — `docs/operations/postmortems/`
- Upstream docs / ADRs / related runbooks.

## Changelog

- YYYY-MM-DD — initial draft by @x.
- YYYY-MM-DD — revised mitigation step 2 after incident NNN.
