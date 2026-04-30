---
title: SEV-1 tabletop — Timescale primary failover
last_verified: 2026-04-30
status: ratified
severity: P1
exercises_runbook: ../../runbooks/timescale-primary-down.md
playbook_section: ../../sev-playbook.md#4-response-flow
---

# SEV-1 tabletop — Timescale primary failover

A scripted scenario for the monthly tabletop drill. ~30 min for
3 people. Exercises
[`timescale-primary-down.md`](../../runbooks/timescale-primary-down.md)
and [SEV playbook §4 Response flow](../../sev-playbook.md).

## Initial conditions

Read aloud at drill setup.

- All Rates Engine services up. `/v1/readyz` returns
  `status: ok`. SLA probe metrics are within target. r1 host is
  the primary region.
- It is **14:30 UTC, Tuesday**. Routine traffic — no marketing
  push, no protocol upgrade in flight.
- Oncall is `<participant 1>`. Backup oncall is
  `<participant 2>`. Engineering manager is reachable.

## Trigger event

Read aloud at drill T+0.

> At 14:32 UTC, Timescale's primary container on r1 begins
> rejecting writes with `FATAL: out of disk space` and the
> `pgdata` mount has 0 bytes free. Replica is healthy but
> read-only by config; failover is a manual step. Patroni is
> *not* deployed (per the still-open #11–#16 ansible roles in
> the coverage matrix), so the failover is operator-driven.
>
> The first user-visible signal: API 5xx rate climbs from < 0.1%
> baseline to 4% over 60 seconds.

## Injection timeline

Drill leader reads each beat in order; pauses after each for
participants to narrate their response.

| T+ | Beat |
| --- | --- |
| 0:00 | `ratesengine_api_error_rate_high` fires (>1% for 2m). PagerDuty wakes oncall. |
| 0:30 | While oncall is acknowledging, `ratesengine_api_error_rate_critical` fires (>5% for 2m). |
| 1:00 | `ratesengine_timescale_disk_full` fires (<10% free). |
| 1:30 | `ratesengine_ingestion_insert_errors` fires for every source. |
| 2:00 | `ratesengine_api_price_stale` follows ~60s after the cache TTL bleeds. |
| 5:00 | A second incident channel question: a customer (Freighter) DMs asking why their price feed is degraded. |
| 8:00 | Replica disk is at 30% free — failover budget exists. |
| 12:00 | The `pgBackRest` archive is healthy; backups have NOT been corrupted. |
| 25:00 | Disk-clearing options (drop oldest CAGG chunks, scale PVC) are still being weighed. Engineering manager joins channel. |

## Expected response per the playbook

Drill leader compares participant narratives against this
expected sequence.

### Within 5 minutes (per [§2 Timelines](../../sev-playbook.md#2-timelines-the-contractual-promises))

- Oncall acknowledges in PagerDuty.
- Oncall opens `#incident-<YYYY-MM-DD>-<short>` channel.
- Oncall posts initial impact statement: "API 5xx elevated;
  investigating; updates every 15 min."
- Status page set to *Investigating*: "Some API requests may
  return errors. We're investigating."

### Within 15 minutes — diagnose

Per [`timescale-primary-down.md`](../../runbooks/timescale-primary-down.md)'s
quick-diagnosis section:

- Confirm via `/v1/readyz` that the `postgres` check is failing.
- Identify root cause: disk-full on `pgdata`. The runbook's
  *Typical root causes* section names this as #1.
- Decide between **failover** (cut over to replica + repair
  primary later) or **fix-in-place** (clear disk, restart).

### Within 30 minutes — mitigate

- **Failover path** (operator decision): promote replica with
  `patronictl failover` (when Patroni lands) OR manual `ALTER
  SYSTEM` flip per the runbook's mitigation steps.
- **Fix-in-place path**: drop oldest `prices_1m` chunks (have
  retention policies; safe). Restart Timescale.
- Either way: replica should be serving within 30 min.

### Within 1 hour — communicate

- Status page transitions *Investigating* → *Identified* →
  *Mitigated*.
- Customer-facing post on whatever channel (Slack/Discord) we
  publish to.
- Update sent every 15 min in the incident channel.

### Within 24 hours — postmortem

Postmortem doc per [§6 After the incident](../../sev-playbook.md#6-after-the-incident).
Action items filed with owners + due dates.

## Validation criteria

Score `pass` / `partial` / `fail` per criterion. Aim for ≥ 80% pass.

| # | Criterion |
| --- | --- |
| 1 | Did oncall acknowledge within 5 min and open the channel? |
| 2 | Did the team find the right runbook (`timescale-primary-down.md`) on the first try? |
| 3 | Did anyone confirm the alert against `/v1/readyz` rather than just the metric? |
| 4 | Did the team correctly identify disk-full as the root cause within 15 min? |
| 5 | Did the team decide between failover vs fix-in-place with explicit rationale? |
| 6 | Did anyone reference the still-open `#11–#16 ansible roles` (Patroni / HAProxy) as making this drill harder than it should be? (i.e. recognise the launch-readiness gap) |
| 7 | Did the team run the customer-comms templates correctly (no wild speculation, no "what we don't say" violations per [§5.4](../../sev-playbook.md#54-what-we-do-not-say))? |
| 8 | Did the writeup land within 24 h with action items? |

## Common gaps surfaced (from prior simulations)

- **Failover path is operator-knowledge today.** Patroni hasn't
  landed (open in coverage matrix). A real failover today is a
  manual `pg_basebackup` flip; the runbook documents the steps
  but they're slow under stress. Action item template:
  "Land `infra/patroni` ansible role to make failover one
  command."
- **`/v1/readyz` is the right diagnostic** but the runbook's
  Quick-diagnosis section currently leads with metrics. Action
  item template: "Reorder runbook to put `/v1/readyz` check
  first."
- **Customer comms drift.** Status page templates work for
  external comms; the internal Slack channel comms are ad-hoc.
  Action item template: "Add internal-channel templates to
  [§5.3 Customer Slack/Discord](../../sev-playbook.md#53-customer-slack--discord)."

## Variant scenarios

Run the same script with a different trigger to exercise other
parts of the runbook:

- **Network partition variant.** Replace "out of disk space"
  with "primary unreachable from API + indexer hosts; replica
  reachable but read-only." Tests failover decision under
  partial-information stress.
- **Slow degradation variant.** Replace cliff with disk filling
  over 6 hours from a stuck `pgBackRest` job. Tests early-warning
  alerts (`disk_warning`) and proactive cleanup.

## Pairs with

- [SEV-2 source decoder regression](sev2-source-decoder-regression.md)
  — different severity tier; can be drilled together as a
  back-to-back if time permits.
