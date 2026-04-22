---
title: Incident (SEV) Playbook
last_verified: 2026-04-22
status: ratified
---

# Incident (SEV) Playbook

**Ratified:** 2026-04-22.
**Binds:** every incident responder on-call for Rates Engine.
**Drilled:** quarterly tabletop exercise; monthly "chaos Friday"
live test in staging.

**Freighter RFP SLA this satisfies:** F3.5 (SEV-1 detect ≤ 15 min /
respond ≤ 30 min / hourly updates) and F3.6 (SEV-2 detect ≤ 30 min /
respond ≤ 60 min / triage ≤ 240 min / daily updates).

---

## 1. Severity definitions

**SEV-1** — Service downtime.

- API returning 5xx on > 5 % of requests for > 2 min, OR
- API latency p95 > 1 s sustained for > 2 min, OR
- Complete ingestion pipeline halt (every source lagging > 15 min), OR
- Data loss suspected (Timescale primary unrecoverable / backup gap).

**SEV-2** — Degraded but serving.

- API p95 > 500 ms (10× the SLA target) sustained > 5 min, OR
- Partial ingestion failure (2+ sources halted; others OK), OR
- Single region unavailable (multi-region deployment absorbing load), OR
- Redis cluster master down (Sentinel failover in progress), OR
- Cross-region replication lag > 30 min on async replica.

**SEV-3** — Noticeable internal degradation.

- Single source lagging 5–15 min, OR
- Backup / restore drill failure (no production impact).

**SEV-4** — Informational / below-threshold anomaly.

- Automated alerts in the "watch" category — filed to tickets, not
  paged. Reviewed weekly.

---

## 2. Timelines (the contractual promises)

| Severity | Detect by | Respond (ack) by | Triage complete by | Status update cadence |
| -------- | --------- | ---------------- | ------------------ | --------------------- |
| SEV-1    | ≤ 15 min  | ≤ 30 min         | ≤ 60 min           | hourly                |
| SEV-2    | ≤ 30 min  | ≤ 60 min         | ≤ 240 min          | daily                 |
| SEV-3    | ≤ 60 min  | next business day | within 1 business day | weekly via tickets |
| SEV-4    | ≤ 24 h    | next weekly review | —                | weekly digest         |

"Detect" = our monitoring catches it. "Respond" = responder
acknowledges the page. "Triage" = we know root cause + have an
action plan. "Update" = public status page + customer
notifications.

---

## 3. Detection channels

| Channel | What it catches | Fires |
| ------- | --------------- | ----- |
| Prometheus / AlertManager | Every alert in [alerts-catalog.md](alerts-catalog.md) | Instantly |
| Synthetic probes (curl every 30 s from 3 regions) | Public-facing outages that bypass internal metrics | ≤ 30 s |
| Cloudflare load-balancer health | Region-level failures | ≤ 45 s |
| Customer report (email, Discord) | Whatever we missed | Variable |
| On-call rotation dashboard | Passive — reviewed every 15 min during oncall | — |

Every SEV-1 has at least **two** independent detection channels (our
alert + a synthetic probe, or our alert + Cloudflare health). If
only one detector fires, assume false-positive *after* a 30-s
re-probe.

---

## 4. Response flow

```
 alert fires
     │
     ▼
┌───────────────┐   no    ┌─────────────────────────┐
│ PagerDuty    │───────▶ │ 30 s retry / second      │
│ dispatches    │         │ detector check          │
│ to primary   │         └────────┬────────────────┘
│ oncall       │                   ▼
└───────┬──────┘            real incident
         │                        │
         ▼                        ▼
 acknowledge ≤ 5 min   ──── open #incident-<id> Slack channel
         │                        │
         ▼                        ▼
 declare severity    ──── post initial status update
         │                        │
         ▼                        ▼
 follow runbook       ──── mitigate (fix or fail-over)
         │                        │
         ▼                        ▼
 reach triage-complete ── post "contained" status
         │                        │
         ▼                        ▼
 root-cause analysis  ──── resolve (restore full service)
         │                        │
         ▼                        ▼
 schedule postmortem  ──── final "all clear" status
```

### 4.1 Acknowledgement (SEV-1/2)

Primary oncall has **5 min** to acknowledge the page (PagerDuty's
escalation policy). If unacknowledged → secondary oncall → backup
engineer → @ash.

Acknowledgement does NOT mean the incident is resolved. It means:
"I'm awake, I've seen the alert, I'm on it."

### 4.2 Incident channel

Every SEV-1 / SEV-2 gets a dedicated Slack channel named
`#incident-<YYYYMMDD>-<short-slug>` (e.g.
`#incident-20260515-timescale-primary-down`). Channel-creation is
automated by a PagerDuty → Slack integration.

Topic of the channel carries:
- Severity + declared-at timestamp
- Commander (IC) name
- Latest action + ETA

### 4.3 Roles

- **Incident Commander (IC)** — first responder auto-assumes until
  a manager joins and takes over.
- **Communications lead** — posts status-page updates + customer
  Slack / Discord messages.
- **Technical lead** — runs the actual diagnostics + fixes.
  On a small team (SEV-2) the IC can wear both IC + tech hats; on
  SEV-1 we split.

Only **one** person commits changes to production during an
incident — the tech lead. Everyone else watches + advises.

### 4.4 Mitigation vs resolution

**Mitigate first, understand later.**

A SEV-1 that's mitigated within 15 min but not root-caused until
an hour later is a good outcome. A SEV-1 debugged in-flight while
customers suffer is a bad one. Concrete mitigation tactics for
common SEVs are in the per-runbook file.

### 4.5 Fixing vs reverting

If the incident was triggered by a recent deploy: **revert first,
investigate after.**

If the incident reveals a latent bug that's been there for weeks:
no revert; forward-fix only.

Decision tree:
- Deploy within last 4 h? Likely candidate — revert, observe 15 min.
- Deploy within last 24 h? Possible — check metrics for deploy-time
  correlation before reverting.
- No recent deploy? Forward-fix; investigate infrastructure,
  upstream changes, or load shift.

---

## 5. Public communication

### 5.1 Status page

Public status page lives at `https://status.ratesengine.net`
(TBD — provisioning in Week 8).

Status-page states:
- **Operational** — green; no active incident.
- **Degraded performance** — SEV-2 or equivalent partial outage.
- **Partial outage** — major subsystem down but some API surface
  still works.
- **Major outage** — API unavailable.
- **Under maintenance** — scheduled; not an incident.

### 5.2 Update templates

**Initial (SEV-1):**

> We're investigating an incident affecting the Rates Engine API.
> Requests may fail or return stale data. We acknowledged this at
> {time} and will post an update within the hour.

**Investigating (mid-incident):**

> Update: we've identified that {subsystem} is affected.
> {mitigation being attempted}. Current impact: {scope}. Next
> update by {time}.

**Resolved:**

> The incident is resolved as of {time}. Service is fully
> restored. Root cause: {one-line summary}. We'll post a
> full postmortem within {SEV-1: 72 h, SEV-2: 5 business days}.

### 5.3 Customer Slack / Discord

The `#ratesengine-ops` Slack (internal) is primary. Major
customers have our direct channel for real-time updates during
incidents. Update cadence there matches the status page.

### 5.4 What we do NOT say

- No speculation on root cause until triage is complete.
- No blame on individuals (company policy — blameless).
- No sharing of internal metric values that could expose attack
  surfaces.
- No promising timelines we can't keep.

---

## 6. After the incident

### 6.1 Postmortem

Required for every SEV-1 and SEV-2.

Template: `docs/operations/postmortems/<date>-<slug>.md` (one file
per incident).

Deadlines:
- SEV-1: draft within 72 h, ratified within 1 week.
- SEV-2: draft within 5 business days, ratified within 2 weeks.

Contents (mandatory sections):
- **Summary** — 3-5 sentences.
- **Timeline** — every action with ISO-8601 timestamps.
- **Impact** — measured customer impact (requests failed, users
  affected, data loss if any).
- **Root cause(s)** — often multiple; list each.
- **Contributing factors** — conditions that made the impact
  worse.
- **What went well** — the things to keep.
- **What went poorly** — the things to improve.
- **Action items** — one per observed gap, owner + due date +
  tracking issue. See §6.2.

### 6.2 Action items

Every postmortem generates action items, each a GitHub issue
labelled `postmortem-action` with a due date. The weekly ops
review meets on Mondays and triages the open ones.

**A postmortem is not complete until every action item has an
owner and a due date.** "No action needed" is a valid bucket but
must be stated explicitly.

### 6.3 Blameless policy

Postmortems focus on **system failures**, not individual errors.
The question is never "who pushed the bad deploy" but "why did
the system allow the bad deploy through CI into production." If
a human mistake was the immediate cause, the root cause is the
system that permitted it.

Concretely: no postmortem section names an individual. Action
items are framed around the system change that'd prevent a
recurrence.

---

## 7. Escalation chain

Oncall rotations live in PagerDuty. Nightly coverage is
@ash-primary / @alex-backup (as of Week 1; rotation starts Week 2).

If all oncall unreachable for > 30 min during a SEV-1:
1. Declare the incident in the public Slack anyway (community
   visibility > silence).
2. Use the break-glass credentials in `vault/sealed/incident-
   recovery.seal` (procedure in `docs/operations/runbooks/break-
   glass.md`, TBD). These require two operators to unseal — a
   deliberate speed-bump.

---

## 8. Drills

- **Monthly tabletop** (30 min) — walk through a scripted scenario
  on paper. No systems touched. Tests the playbook itself.
- **Quarterly live chaos** (2 h window) — pre-announced, staging-
  only, break something real + observe detection + response.
- **Annual DR exercise** (4 h window) — simulated total-primary
  failure, flip to cloud DR, serve from there for 1 h, flip back.

Drills produce a short writeup in `docs/operations/drills/` with
the same action-item discipline as postmortems.

---

## 9. References

- [alerts-catalog.md](alerts-catalog.md) — every alert + its runbook.
- [runbooks/](runbooks/) — per-alert playbooks.
- [HA plan](../architecture/ha-plan.md) — topology this playbook
  assumes.
- [ADR-0006 TimescaleDB](../adr/0006-timescaledb-for-price-time-series.md)
- [ADR-0007 Redis cache schema](../adr/0007-redis-cache-schema.md)
- External:
  - Google SRE Book — postmortem culture chapter.
  - PagerDuty's incident response docs
    (<https://response.pagerduty.com/>).

---

## 10. Versioning

This playbook is versioned via `last_verified` in the frontmatter.
Revisions require sign-off from @ash + the current primary oncall.
A revision that weakens any contractual timeline (§2) requires an
ADR; strengthening them does not.
