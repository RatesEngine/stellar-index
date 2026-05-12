# Severity Rubric

Severity is *not* a feeling — it is a deterministic mapping from
the finding's worst-case business impact to one of five tiers.

When in doubt, a finding goes one tier *higher*, not lower. We
will downgrade later with evidence rather than upgrade later
with regret.

## Tiers

### `critical`

A finding qualifies as critical if any of the following is true:

- **Data loss or corruption** that cannot be reconstructed from
  galexie + immutable upstream archives within the documented
  RTO/RPO (per `docs/architecture/ha-plan.md`).
- **Security breach** where a remote attacker can read or write
  data not authorised to them (data ex-filtration, key compromise,
  privilege escalation, billing meter bypass at scale).
- **Money loss** — billing surface that allows free use of paid
  capacity, or charges users for capacity they didn't consume.
- **Brand-ending incident** — public, sustained wrong-data
  serving that would land in trade-press coverage.
- **Customer-contract violation** — a documented commitment in
  RFPs or proposal that we cannot meet on launch day.

Wave 0 (block public flip).

### `high`

A finding qualifies as high if any of the following is true:

- **Silent bad data.** API returns plausible but wrong values
  with no `divergence_warning` / `stale` / `reduced_redundancy`
  flag. The user can't tell.
- **Prolonged outage with no detection.** The system breaks but
  no alert fires, no runbook covers it, no metric drifts.
- **Operability gap that gates the public flip.** Operators
  cannot recover from a documented failure mode.
- **CG/CMC parity gap on a launch-headline feature.** A surface
  the launch announcement claims we offer is not actually
  shipping.
- **Stellar-depth gap** that makes us look indistinguishable
  from CG/CMC on the differentiator surfaces.
- **Lurking ADR violation** that is not currently exploited but
  would be exploited the first time the surface is reused.

Wave 0 or Wave 1.

### `medium`

A finding qualifies as medium if any of the following is true:

- **Degraded UX** — slow path, ugly error envelope, missing
  pagination, missing cache header.
- **Observability gap** — metric exists but no alert, alert
  exists but no runbook, runbook exists but stale.
- **Operator confusion** — CLI text that overstates / understates
  what the tool actually does (the prior 05-02 supply CLI text
  drift was this tier).
- **Deprecation hygiene** — removed route still referenced in
  examples / Postman / explorer / CHANGELOG.
- **Prewarm/handler drift** — cached reader called with
  different args than the handler.
- **Test density gap** — happy-path-only on a non-critical
  package.

Wave 1 or Wave 2.

### `low`

A finding qualifies as low if any of the following is true:

- **Doc drift** that does not mislead a reader (typo,
  out-of-order section, broken cross-link).
- **Naming inconsistency** that doesn't change behavior.
- **Minor inefficiency** with no SLA impact.
- **Cosmetic CHANGELOG entries.**

Wave 2 or Wave 3.

### `note`

Informational — captures a fact the auditor wants future
auditors to know but requires no action. Examples:

- "Prior finding F-0501 is materially closed in current snapshot."
- "Soroswap reserves now resolved via paired SyncEvent — verified."

## Severity vs Wave

Severity drives the *minimum* wave; wave can also be raised by
launch-impact considerations.

| Severity | Default wave | Can be elevated to | Can be lowered to |
| --- | --- | --- | --- |
| critical | Wave 0 | — | — |
| high | Wave 0 or 1 | Wave 0 | — |
| medium | Wave 1 or 2 | Wave 1 | Wave 2 |
| low | Wave 2 or 3 | Wave 2 | Wave 3 |
| note | Wave 3 | — | n/a |

## Adversarial Multiplier

When a finding has an adversarial-vector reference (a row in
[10-attack-tree.md](10-attack-tree.md)), the severity is
*never* lowered below the attack-tree row's claimed severity.

If the auditor disagrees with the attack-tree severity, update
the attack-tree row first (with rationale), then the finding.

## Closure-Wedge Rule

A finding is **never** closed by:

- "doc says so now" — without code change
- "test exists for happy path now" — without malformed-input test
- "we agreed verbally" — without PR landing
- "alertmanager has the route" — without runbook landing

A finding **may** be closed by:

- code change merged + verify run + R1 probe (where applicable)
- explicit `accepted` disposition with a date and reasoning
- explicit `wontfix` disposition with a date and reasoning;
  this also requires a `note` entry recording the gap as a
  product-positioning choice
