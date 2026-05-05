---
title: "[SEV-N] one-line customer-facing summary — YYYY-MM-DD"
date: YYYY-MM-DD
severity: SEV-1 | SEV-2 | SEV-3
status: investigating | identified | monitoring | resolved
started_at: YYYY-MM-DDTHH:MM:SSZ
resolved_at:                                 # leave empty until resolved
affected_components:                         # one or more — must match status-page component names
  - api
  - indexer
  - aggregator
  - storage
postmortem:                                  # leave empty until the postmortem is written
---

<!--
This is a CUSTOMER-FACING incident post. Tone, detail level, and
cadence per docs/operations/sev-playbook.md §5.

Read the README before filling this in:
  docs/operations/incidents/README.md
-->

# [SEV-N] One-line customer-facing summary

## Identification

What customers experience right now. Plain English; no jargon.

> Example: Some `/v1/price` queries are returning 503 for assets
> in the SHIB / DOGE family. XLM, BTC, and ETH pricing remains
> unaffected.

## Impact

Concrete scope of impact. Use bullets:

- **Endpoints affected:** `/v1/price?asset=...`
- **Customers affected:** estimated %; if subscriber-tier specific, say which
- **Data correctness:** "stale data being served" vs "no data" vs
  "wrong data" — these are different stories for the customer
- **Workaround:** if any (e.g. "use `/v1/price/tip` instead")

## Timeline

All times UTC. Append entries as the incident progresses; never edit
prior entries (treat as append-only event log).

| Time (UTC) | Update |
|-----------|--------|
| HH:MM | **Investigating.** First sentence the customer reads. What we know, what we're looking at. |
| HH:MM | (subsequent update — what changed) |
| HH:MM | **Identified.** Cause located. ETA to fix if known. |
| HH:MM | **Monitoring.** Fix deployed. Watching for full recovery. |
| HH:MM | **Resolved.** All-clear. |

## What we did

For SEV-1 / SEV-2: a one-paragraph summary of the actual fix
that landed. Replaces speculation with facts. Customer-facing
detail level.

> Example: We rolled back `ratesengine-aggregator` to v0.3.1
> (the version preceding v0.3.2's stablecoin-proxy change).
> Pricing for SHIB / DOGE recovered within 60 seconds of the
> restart. Postmortem with full root cause + remediation will
> follow within 24 hours.

## Postmortem

Once the postmortem lands at
`docs/operations/postmortems/<same-date>-<same-slug>.md`, link
it here AND update the front-matter `postmortem:` field to point
at it.
