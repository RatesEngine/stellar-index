---
# Copy this file to YYYY-MM-DD-<slug>.md and fill in the
# placeholders. Front-matter fields:
#
# title       — short customer-facing headline. Avoid jargon.
#               Bad: "TimescaleDB primary lag spike"
#               Good: "Pricing API returning stale prices"
#
# date        — RFC 3339 UTC of the incident-start time. cstate
#               sorts and ranks by this.
#
# resolved    — false until resolved; flip to true and add a
#               final ## Resolved section to the body.
#
# severity    — one of: down | disrupted | notice
#               "down"      → ≥ 1 component completely unavailable
#               "disrupted" → degraded but partially serving
#               "notice"    → maintenance / advisory; no impact yet
#
# affected    — list of `name:` values from data/systems.yml
#               that are degraded. cstate flips those component
#               banners; visitors see the per-component scope.
#
# section     — keep "issue" so cstate routes the page correctly.

title: "<short customer-facing headline>"
date: 2026-MM-DDTHH:MMZ
resolved: false
severity: disrupted
affected:
  - "Pricing API"
section: issue

# Skip rendering this placeholder file — it's only here as a
# copy-paste source. Hugo's leading-underscore filename
# convention doesn't exclude content from .Site.RegularPages
# (which is what cstate's incidents listing iterates), so we
# use the explicit _build directive.
_build:
  render: never
  list: never
---

<!--
Body is markdown. Each timestamped paragraph is one customer-
visible update — push these as they happen, not as a single
post-mortem at the end.

Format every update as:

## Investigating — 2026-MM-DD HH:MM UTC
or
## Identified — …
or
## Monitoring — …
or
## Resolved — …

The four words above match the conventional incident lifecycle
(matches statuspage.io / Atlassian conventions); cstate doesn't
parse them but customers used to other status pages do.

Per docs/operations/sev-playbook.md §2 the post cadence is:
  SEV-1 → status update at detect, then hourly
  SEV-2 → status update at detect, then daily
-->

## Investigating — 2026-MM-DD HH:MM UTC

We're seeing <symptom from the customer's POV>. Engineers are
investigating. Next update in <SEV-1: 1h | SEV-2: 24h>.

## Identified — 2026-MM-DD HH:MM UTC

Root cause is <one sentence; safe-to-publish detail level>.
Mitigation is <action being taken>; ETA <timestamp>.

## Monitoring — 2026-MM-DD HH:MM UTC

Mitigation deployed. Watching <signal — error rate, latency,
ingest lag, …> for <duration> before declaring resolved.

## Resolved — 2026-MM-DD HH:MM UTC

<Symptom> is back to normal as of <timestamp>. Total impact:
<duration> of <degraded behaviour>. A full post-mortem will be
published at <link or "internal-only" / "the next monthly
service report"> by <date>.
