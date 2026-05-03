---
title: SEV-1 tabletop — Anomaly freeze stuck-engaged on a major pair
last_verified: 2026-05-03
status: ratified
severity: P1
exercises_runbook: ../../runbooks/anomaly-freeze-engaged.md
playbook_section: ../../sev-playbook.md#4-response-flow
---

# SEV-1 tabletop — Anomaly freeze stuck-engaged on a major pair

Scripted scenario for the monthly tabletop drill. ~30 min for
3 people. Exercises the ADR-0019 anomaly-response chain:
`internal/aggregate/anomaly` (Phase 1 thresholds) →
`internal/aggregate/baseline` (Phase 2 statistical baseline) →
`internal/aggregate/freeze` (writer + Looker) →
`/v1/price`'s `flags.frozen` envelope flag.

This is the canonical drill for the freeze-policy path. The
anomaly response is **operationally critical** — a stuck-
engaged freeze on a major pair (XLM/USD) means every consumer
is reading a frozen LKG price; legitimate market moves are
being suppressed; the aggregator is incorrectly trusting its
own caution. Detecting + clearing this is on-call's job.

## Initial conditions

Read aloud at drill setup.

- All Rates Engine services up. SLA probe metrics within target.
  Aggregator is producing closed-bucket VWAPs every minute for
  the configured pair set. `flags.frozen` rate across `/v1/price`
  responses is < 0.1% baseline.
- It is **22:15 UTC, Friday**. Light traffic — start of the
  weekend. Oncall is `<participant 1>`. Backup is
  `<participant 2>`.

## Trigger event

Read aloud at drill T+0.

> At 22:17 UTC, an off-chain CEX feed (Binance) goes down for
> 90 s — venue-side maintenance window. The aggregator's
> Phase 1 anomaly detector sees the source-count for XLM/USD
> drop below `class_diversity_min=3`, fires
> `anomaly.ActionFreeze`, and `freeze.Writer` publishes
> `freeze:native:fiat:USD` with TTL 600 s.
>
> Binance comes back at 22:18:30. The aggregator's class-diversity
> recovers. **But** the freeze marker still has 9 minutes left
> on its TTL — and the next aggregator tick at 22:19 doesn't
> automatically clear the marker; the freeze writer's contract
> is "set on engage; clear on operator-driven evaluate-and-
> clear" (per ADR-0019 Phase 1).
>
> First user-visible signal: `/v1/price?asset=native` returns
> the same closed-bucket value at 22:19, 22:20, … with
> `flags.frozen=true`. Customer dashboards show "frozen at
> $0.0712" for 10 minutes during what should be live trading
> hours. PagerDuty wakes oncall at 22:23 because the
> `frozen_pair_dwell_minutes` alert fires for "frozen for > 5
> consecutive closed buckets on a P1 pair".

## Injection timeline

Drill leader reads each beat in order; pauses after each for
participants to narrate.

| T+ | Beat |
| --- | --- |
| 0:00 | `frozen_pair_dwell_minutes` fires (5 min on XLM/USD). PagerDuty pages oncall. |
| 0:30 | While oncall is acknowledging, an internal Slack message from a customer team: "is the price feed broken? showing the same number for 8 min". |
| 1:00 | Oncall opens the runbook. Decision tree: was the freeze legitimate (real anomaly) or stuck (recovered but not cleared)? |
| 3:00 | `redis-cli GET freeze:native:fiat:USD` returns `engaged_at=...,reason=class_diversity_drop`. The reason is one the operator should recognise. |
| 5:00 | `prometheus` query for source-class diversity over the last 10 min shows: dropped to 2 at 22:17, recovered to 4 at 22:18:30. The source-side recovery happened 7 minutes ago. |
| 8:00 | A second customer DMs: "we've stopped trading XLM/USD for 12 minutes because your `flags.frozen` is firing. Is this real?" |
| 12:00 | The freeze marker is still in Redis with ~7 minutes TTL remaining. Operator considers: wait for TTL? Manually clear? `freeze.Writer.Clear` is exposed via `ratesengine-ops`? |

## Expected response per the playbook

Drill leader compares participant narratives against this
expected sequence.

### Within 5 minutes (per [§2 Timelines](../../sev-playbook.md#2-timelines-the-contractual-promises))

- Oncall acknowledges PagerDuty.
- Oncall opens `#incident-<YYYY-MM-DD>-freeze-stuck` channel.
- Initial post: "Investigating a stuck-frozen flag on XLM/USD.
  Customers may see a non-updating price feed."
- Status page set to *Degraded performance* on **Pricing API**.

### Within 10 minutes — diagnose

Per [`anomaly-freeze-engaged.md`](../../runbooks/anomaly-freeze-engaged.md):

- `redis-cli GET freeze:native:fiat:USD` reads the marker.
- Inspect the `engaged_at` + `reason` fields. Confirm:
  1. Engagement timestamp is consistent with the alert.
  2. Reason matches a recovered upstream condition (in this
     drill: `class_diversity_drop` and class diversity is now ≥
     the threshold).
- Check `prometheus` for the source-class-diversity gauge over
  the past 15 min. If recovered, the freeze is stuck — Phase 1
  doesn't auto-clear.

### Within 20 minutes — mitigate

**Operator-driven clear** (the right call here):

```
ratesengine-ops freeze clear --asset native --quote fiat:USD
```

(or the equivalent `redis-cli DEL freeze:native:fiat:USD` if
the ops command isn't yet shipped — runbook has both forms).

Verify on the next aggregator tick:

```
curl -sS https://api.ratesengine.net/v1/price?asset=native | jq '.flags'
# → flags.frozen should be false on the response
```

Watch for **re-freeze**: if the underlying condition isn't
actually recovered, the next tick will re-engage. If that
happens, escalate to investigating the source itself.

### Within 30 minutes — communicate

- Status page transitions *Degraded performance* → *Mitigated*
  with body: "A stuck price-freeze flag on XLM/USD has been
  cleared; live updates resumed at <UTC>."
- Customer-facing post: same shape, slightly more context.

### Within 24 hours — postmortem

Postmortem covers:

- Why did the operator-driven-clear contract exist (ADR-0019
  Phase 1 explicitly chose "operator clears" over "auto-clear
  on first recovered tick" — defence against flapping)?
- Did the freeze marker TTL (600s in this drill) match the
  operational reality? Should it be shorter for major pairs?
- Was the runbook clear about the TTL semantics?

## Validation criteria

| # | Criterion |
| --- | --- |
| 1 | Did oncall classify this as SEV-1 (frozen feed on a major pair = effective service-down for that pair)? |
| 2 | Did the team consult `freeze:native:fiat:USD` directly via `redis-cli` (not just the metric)? |
| 3 | Did the team correctly distinguish "freeze is stuck" vs "freeze is legitimate" by checking the source-class-diversity timeline? |
| 4 | Did anyone correctly identify that ADR-0019 Phase 1 freeze is **operator-cleared by design**, not a bug? |
| 5 | Did the team use `ratesengine-ops freeze clear` (or `redis-cli DEL`) rather than waiting for TTL? |
| 6 | After clearing, did anyone verify on the next tick that `flags.frozen` was indeed false? |
| 7 | Did the postmortem capture the Phase 1 clear-policy rationale rather than recommending "just auto-clear"? |
| 8 | Status-page wording stayed factual (no speculation about cause until §"Identified")? |

## Common gaps surfaced (from prior simulations)

- **The clear-policy rationale gets lost in the heat of the
  moment.** Operators want to "fix" auto-clear; they need to
  understand it's a deliberate ADR-0019 decision. Action item
  template: "Add a one-line `WHY` summary to the runbook's
  clear-procedure section."

- **Re-freeze recovery loop.** If the source condition that
  triggered freeze hasn't actually cleared (it can flap), a
  manual clear will be re-engaged on the next tick. Operators
  need a "verify before clearing" check — the runbook covers
  it but it's at the bottom; should be the first step.
  Action item template: "Reorder the runbook so 'verify
  upstream recovery' precedes 'clear'."

- **`ratesengine-ops freeze clear` may not be shipped yet.**
  In the absence of the ops command, the redis-cli form works
  but feels unsafe ("am I deleting the right key?"). Action
  item template: "Ship `ratesengine-ops freeze clear` if not
  already, and add a dry-run flag."

## Variant scenarios

- **Phase 2 freeze variant.** The aggregator's Phase 2
  statistical baseline (multi-window MAD per ADR-0019) fires
  freeze on XLM/USD because of a real legitimate market move
  — the freeze is correct, but the customer doesn't see why.
  Tests whether oncall correctly DEFENDS the freeze rather
  than clearing it.
- **Cascade freeze variant.** A stuck freeze on XLM/USD also
  affects every triangulated pair (XLM is the leg). Tests
  whether oncall checks the triangulation graph + clears the
  root cause vs the leaves.

## Pairs with

- [SEV-1 Timescale primary failover](sev1-timescale-primary-failover.md)
  — same severity tier; both exercise the SEV-1 response shape.
