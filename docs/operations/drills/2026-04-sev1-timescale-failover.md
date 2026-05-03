---
title: "2026-04 SEV-1 tabletop — Timescale primary failover"
date: 2026-04-30
type: tabletop
scenario: scenarios/sev1-timescale-primary-failover.md
participants: [ash, ash, ash]
last_verified: 2026-04-30
---

# 2026-04 drill writeup — SEV-1 Timescale primary failover

Pre-launch tabletop dry-run executed solo (`ash` rotated through
oncall / scribe / commander roles) to satisfy L5.7 / Task #76 of
the launch-readiness backlog. Soloing a tabletop is a known
limitation — see *Gaps observed* — but the dry-run still
exercised the full runbook chain and surfaced gaps the playbook
would otherwise have hidden until production.

## Trigger

Copied verbatim from the scenario script:

> At 14:32 UTC, Timescale's primary container on r1 begins
> rejecting writes with `FATAL: out of disk space` and the
> `pgdata` mount has 0 bytes free. Replica is healthy but
> read-only by config; failover is a manual step. Patroni is
> not yet deployed in staging at drill time, so the failover is
> operator-driven via `pg_basebackup` flip per the runbook.
>
> The first user-visible signal: API 5xx rate climbs from < 0.1%
> baseline to 4% over 60 seconds.

## Response narrative

Times are simulated (drill leader read each Beat aloud and
narrated the response). T+0 = trigger injection at 14:32 UTC.

- **T+00:30** — `ratesengine_api_error_rate_high` page acknowledged
  via PagerDuty UI walkthrough. Opened #incident-2026-04-30-tsdb
  Slack channel; posted initial impact statement: "API 5xx
  elevated; investigating; updates every 15 min."
- **T+01:30** — Hit `/v1/readyz`; the `postgres` check returns
  `error: connection refused`. Cross-referenced
  `runbooks/timescale-primary-down.md` Quick-diagnosis section.
  (Followed the metric → readyz path, which is the runbook's
  current ordering — see action item #2 below.)
- **T+03:00** — Set status page to *Investigating*: "Some API
  requests may return errors. We're investigating." Used the
  SEV-1 Initial template from sev-playbook.md §5.2 verbatim.
- **T+05:00** — Confirmed `disk_full` alert; identified
  `pgdata` mount at 0 bytes free. Decision tree: failover (cut
  to replica) vs. fix-in-place (drop oldest CAGG chunks +
  restart). Chose **fix-in-place** — replica's 30% headroom
  meant we could keep primary if we cleared 50GB of oldest
  `prices_1m` chunks within the disk-full grace window.
- **T+08:00** — Customer DM (Freighter) simulated. Replied
  with the playbook §5.3 internal-channel template.
  ("Acknowledged; we have an active incident; tracking at
  status.ratesengine.net.")
- **T+12:00** — Verified `pgBackRest` archive intact via
  `pgbackrest info`. (No backup corruption — drill condition.)
- **T+18:00** — Dropped 30 days of `prices_1m` chunks via
  `SELECT drop_chunks('prices_1m', '30 days'::interval);` — the
  exact command from the runbook's Mitigation section. Disk
  freed: ~120GB. Restarted Timescale. `/v1/readyz` returns
  `status: ok` within 90 s.
- **T+22:00** — Status page transitioned *Investigating* →
  *Identified* → *Mitigated*. 5xx rate back below 0.5%.
- **T+25:00** — Posted resolution to incident channel + status
  page. Engineering manager joined (simulated). Postmortem
  scheduled for T+24h.

## Gaps observed

- **Solo tabletop is structurally weaker than 3-person drill.**
  No challenge function on decisions, no scribe / commander
  separation, no hand-off rehearsal. The playbook's roles
  surface (commander / scribe / sub-incidents) wasn't fully
  exercised. Action: book a real 3-person tabletop with the
  next on-call rotation hire (post-launch).
- **`/v1/readyz` is the right diagnostic but the runbook leads
  with metrics.** The "look at error_rate then check
  postgres_check" ordering wastes 60-90 seconds during a real
  incident. Reordering to put `/v1/readyz` first would shave
  ~1 min off detection.
- **Customer-comms internal-channel templates are present but
  not muscle-memory.** The §5.3 internal Slack template was
  effective once located, but I had to re-read the playbook
  to find it. A copy in the runbook's Mitigation section
  (alongside the customer-facing status-page template) would
  make the path one-stop.
- **Patroni absence makes failover path slow under stress.**
  Confirmed by the drill — fix-in-place was chosen partly
  because the manual `pg_basebackup` flip is operator-knowledge
  today. The `infra/patroni` ansible role landed under PR #344
  (closing this gap) but the Patroni-driven failover hasn't
  been drilled against a real Patroni cluster yet — added as a
  follow-up scenario.
- **Disk-clearing options weren't rehearsed.** I knew the
  `drop_chunks` command from the runbook but hadn't run it
  against staging — first-time muscle memory. Action: include
  a quarterly chaos drill where we actually run the command on
  staging.

## Action items

Each item filed against this PR's branch as inline TODOs in the
relevant docs; not opened as separate GitHub issues during the
solo drill. A real 3-person drill should file these under the
`drill-action` label.

- [x] **Reorder `runbooks/timescale-primary-down.md` Quick-diagnosis
      to lead with `/v1/readyz`** — owner @ash, done in same PR
      (filed inline below).
- [x] **Cross-link §5.3 internal-channel template from
      `timescale-primary-down.md` Mitigation** — owner @ash, done
      in same PR.
- [ ] **Quarterly chaos drill that actually runs `drop_chunks` on
      staging** — owner @ash, due 2026-Q3 (post-launch chaos
      Wave 2).
- [ ] **Add Patroni-driven failover scenario script as a
      successor to `sev1-timescale-primary-failover.md`** —
      owner @ash, due 2026-Q3.
- [ ] **3-person tabletop after launch with the next on-call
      hire** — owner @ash, due 2026-Q3.

## Score

| # | Criterion (verbatim from scenario) | Score | Notes |
| --- | --- | --- | --- |
| 1 | Did oncall acknowledge within 5 min and open the channel? | pass | Acknowledged T+00:30; channel T+00:45. |
| 2 | Did the team find the right runbook (`timescale-primary-down.md`) on the first try? | pass | Found via the alert's `runbook_url` annotation. |
| 3 | Did anyone confirm the alert against `/v1/readyz` rather than just the metric? | partial | Yes, but only after checking metrics first — runbook ordering nudges toward metric-first. Action item #1. |
| 4 | Did the team correctly identify disk-full as the root cause within 15 min? | pass | T+05:00 root-cause; T+18:00 mitigated. |
| 5 | Did the team decide between failover vs fix-in-place with explicit rationale? | pass | Chose fix-in-place; rationale stated (replica headroom + grace window). |
| 6 | Did anyone reference the still-open `#11–#16 ansible roles`? | pass | Patroni absence flagged; PR #344 already closes the role itself. |
| 7 | Did the team run the customer-comms templates correctly? | pass | Used SEV-1 Initial verbatim; no §5.4 violations. |
| 8 | Did the writeup land within 24 h with action items? | pass | This document. |

**Overall:** pass with two `partial`-flagged action items remediated
inline (runbook reorder + cross-link).

## References

- Scenario script:
  [`scenarios/sev1-timescale-primary-failover.md`](scenarios/sev1-timescale-primary-failover.md)
- Runbook exercised:
  [`runbooks/timescale-primary-down.md`](../runbooks/timescale-primary-down.md)
- SEV playbook: [`../sev-playbook.md`](../sev-playbook.md)
- Launch-readiness backlog: L5.7 / Task #76

## Sign-off

- **Drill leader:** ash
- **Scribe:** ash
- **Posted action items by:** 2026-04-30
- **Promoted to playbook update:** yes — runbook reorder lands in
  this PR.
