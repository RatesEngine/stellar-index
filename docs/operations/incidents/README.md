---
title: Incidents — public-facing customer comms
last_verified: 2026-05-05
status: living doc
---

# Incidents

Per-incident customer-facing communication. The status page
([`deploy/status-page/`](../../../deploy/status-page/) — vendor TBD
per [`docs/architecture/status-page-hosting-comparison.md`](../../architecture/status-page-hosting-comparison.md))
renders these markdown files as the public-facing record.

## Two parallel records

| | `docs/operations/incidents/` (this dir) | `docs/operations/postmortems/` |
|---|---|---|
| **Audience** | Customers (public) | Internal eng + stakeholders |
| **Timing** | DURING the incident, real-time | AFTER resolution, retrospective |
| **Tone** | Status update — "what's happening, what we're doing, ETA if known" | Root cause analysis — "why it happened, why our defences didn't catch it, what we change" |
| **Detail level** | Just enough that a customer can plan around it | Every relevant log line, decision tree, and change |
| **Lifecycle** | Created at SEV declaration, closed at all-clear | Drafted day-after, finalised week-after |

A SEV-1 / SEV-2 produces **both** files. Smaller incidents (SEV-3
"informational") may only produce a status-page entry, no
postmortem.

## File naming

`<YYYY-MM-DD>-<short-slug>.md` — UTC date prefix sorts
chronologically; slug is a kebab-case 2-4-word summary.

Examples:
- `2026-07-15-aggregator-vwap-stuck.md`
- `2026-07-22-r1-postgres-replica-lag.md`
- `2026-08-02-binance-ws-disconnect-storm.md`

If two incidents land on the same day, append `.N`:
- `2026-07-15-1-aggregator-vwap-stuck.md`
- `2026-07-15-2-coinbase-rate-limited.md`

## Workflow

1. **Declaration (T+0).** Per [`sev-playbook.md` §3](../sev-playbook.md),
   on-call declares the SEV. As part of declaration:
   - `cp docs/operations/incidents/_template.md docs/operations/incidents/$(date -u +%Y-%m-%d)-<slug>.md`
   - Fill in the **Identification** + **Impact** sections.
   - Open a PR titled `incident: <YYYY-MM-DD> <slug> (SEV-<N>)`.
   - **Squash-merge with `--admin`** — incident posts skip the normal review gate so the public update isn't blocked on a reviewer.
2. **Updates.** Subsequent updates land as commits on the same file. Each update appends to the **Timeline** section. Cadence per the playbook: hourly for SEV-1, daily for SEV-2.
3. **Resolution.** Final entry on the timeline marks the all-clear. Update **Status:** front-matter to `resolved`. Squash-merge.
4. **Postmortem (T+1d).** Per `sev-playbook.md` §6, the postmortem
   lands at `docs/operations/postmortems/<same-date>-<same-slug>.md`.
   The incident file's front-matter `postmortem:` field gains a
   pointer.

## Status-page integration

The status page (vendor TBD — see the hosting-comparison doc) is
the **render** of these markdown files; this directory is the
**source of truth**. If we change vendors, the migration is
"replay the markdown" — all our incident history is portable.

For the v1 launch we expect Instatus or a custom static page;
either way the shape is:
- Incident lifecycle here in markdown
- Status-page entry derived from the markdown's front-matter
- Customer subscriptions handled by the status-page vendor

## Cross-references

- [`sev-playbook.md`](../sev-playbook.md) — full SEV declaration / response process
- [`postmortems/`](../postmortems/) — internal post-resolution analysis
- [`runbooks/sev-status-page-update.md`](../runbooks/sev-status-page-update.md) — binding runbook for cadence + safe-to-publish detail
- [`docs/architecture/status-page-hosting-comparison.md`](../../architecture/status-page-hosting-comparison.md) — vendor-decision rationale
