---
title: Status page hosting comparison
last_verified: 2026-04-30
status: decision-support (Task #73)
related:
  - docs/operations/sev-playbook.md §5.1 (Public communication)
  - docs/operations/public-flip.md §"Post-flip" (DNS cutover)
---

# Status page hosting comparison

Decision-support doc — pick a hosting option from §Recommendation
below and the implementation under Task #73 is then straightforward
(half-day per the matrix).

## What we're picking

A vendor / self-hosted option for `https://status.ratesengine.net`
that satisfies the requirements in
[`sev-playbook.md §5.1`](../operations/sev-playbook.md#51-status-page):

- **5 states** (Operational / Degraded performance / Partial
  outage / Major outage / Under maintenance).
- **Update templates** the playbook §5.2 already pins (SEV-1
  initial, mid-incident, resolved).
- **Public**, no auth, low-latency from anywhere.
- **Wired to PagerDuty** (or wherever the incident response
  starts) so on-call doesn't have to context-switch to a
  separate UI.
- **CNAME-able** to `status.ratesengine.net` so it inherits the
  DNS cutover documented in
  [`public-flip.md §Post-flip`](../operations/public-flip.md).

## Options surveyed

Three categories, six concrete options:

### Hosted SaaS

| Option | Cost (entry tier) | Pages-per-incident UX | Pros | Cons |
|---|---|---|---|---|
| **Atlassian Statuspage** | $29/mo "Hobby" or $79/mo "Starter" | Best-in-class; the de-facto standard | Mature, beautiful, lots of integrations (PagerDuty, Slack, etc.), email/SMS subscriber list, custom domain on every paid tier | Atlassian acquired and de-emphasised; pricing creep over time; ties us to Atlassian's product strategy |
| **BetterStack (Better Uptime)** | $25/mo "Solo" or $89/mo "Business" | Modern; very fast UI | Bundled status page + uptime monitoring; cleaner UI than Statuspage; Slack/PagerDuty/etc. integrations; CNAME on every paid tier | Smaller ecosystem than Statuspage; bundled monitoring may duplicate our existing Prometheus stack |
| **Instatus** | Free tier (1 status page, 25k subscribers) | Modern; very minimal | **Free tier is genuinely usable** for our scale; CNAME on free tier; Slack + webhook integrations; clean UI | Newer vendor (smaller bus factor); bring-your-own incident-management (no built-in PagerDuty replacement) — but we have PagerDuty already so this is fine |

### Open-source self-hosted

| Option | Cost (infra only) | Pages-per-incident UX | Pros | Cons |
|---|---|---|---|---|
| **Cachet** | ~$5/mo (smallest VPS) | Clean | OSS (BSD); PHP + MySQL; widely deployed | Slow upstream development; PHP stack is a different operational footprint than the rest of the system (Go + Postgres) |
| **cstate** | ~free (static hosting) | Minimal Hugo theme | Static-only — no DB; Hugo theme; deploys to GitHub Pages or S3; configs as YAML files in git | No live-update UI — incident creation is "edit YAML, push to repo, wait for redeploy"; OK for low-cadence outages, painful in the middle of a SEV-1 |
| **Gatus** | ~$5/mo (smallest VPS) | Functional | Go-native; YAML-config; bundled health-checking; matches our stack | More monitoring-tool than status-page; less polished public-facing UI than the SaaS options |

## Decision matrix

Weighted against our specific needs:

| Criterion | Weight | Statuspage | BetterStack | Instatus | Cachet | cstate | Gatus |
|---|---:|---:|---:|---:|---:|---:|---:|
| Time-to-first-incident response (no waiting on a CI deploy) | high | 5 | 5 | 5 | 4 | **1** | 4 |
| Public UX (anonymous user lands, gets answer in 2 s) | high | 5 | 5 | 5 | 4 | 5 | 3 |
| PagerDuty / webhook integration | high | 5 | 5 | 4 | 3 | 1 | 3 |
| Cost at our scale (~3 incidents/quarter) | medium | 2 | 3 | **5** | 5 | 5 | 5 |
| CNAME to `status.ratesengine.net` on chosen tier | high | 5 | 5 | 5 | 5 | 5 | 5 |
| Stack-fit (Go + Postgres + MinIO; no new langs / DBs) | low | n/a hosted | n/a hosted | n/a hosted | 1 (PHP) | 5 (static) | 5 (Go) |
| Open-source freedom (no vendor lock) | medium | 1 | 1 | 1 | 5 | 5 | 5 |
| Bus factor (vendor still around in 2 years) | medium | 5 | 4 | 3 | 4 | 4 | 4 |
| **Total (rough, illustrative)** | | **28** | **28** | **28** | **31** | **26** | **29** |

The numeric scores are illustrative — they're roughly even, which
means **the decision is dominated by the qualitative tradeoffs
rather than scoring**.

## Recommendation: **Instatus**

Three reasons:

1. **Free tier covers us at v1.0 launch volume.** Instatus's free
   tier supports 1 status page, 25k email subscribers, custom
   domain, and Slack integration. Our launch volume is well below
   that — we'd have headroom to grow without paying.

2. **Modern UI matches the rest of the project's polish.** Both
   Statuspage and BetterStack qualify on UI; Instatus matches and
   keeps us off the "default Statuspage skin" that signals
   "generic enterprise SaaS" to engineering customers.

3. **Bring-your-own incident-management is the right shape for
   us.** We have PagerDuty, we have on-call rotation, we have the
   SEV playbook templated. We don't need the status-page vendor
   to own incident lifecycle — just the public surface. Instatus
   does just that.

**Trade-off accepted:** Instatus is a smaller vendor than
Statuspage. If they go away, the migration cost is real (custom
domain re-CNAME, subscriber list export). Mitigated by:
- Choosing CSV-exportable subscriber list (Instatus supports).
- Keeping incident posts as markdown in our own repo
  (`docs/operations/incidents/<YYYY-MM-DD>-<short>.md`) so the
  source of truth is git, with the status-page entry being a
  rendering of it. Migration becomes "switch the vendor; replay
  the markdown."

## Fallback recommendation: **Cachet (self-hosted)**

If the project explicitly wants to avoid SaaS lock entirely,
Cachet is the most-mature OSS option. Pick it over Statuspage
only if:
- The team has bandwidth for a PHP service in the operational
  surface (someone needs to be on-call for it).
- The "incident creation" flow can tolerate a self-hosted
  service occasionally being unavailable during the very
  incident the status page is meant to communicate.

The second risk is the killer. **Cachet is not the
recommendation** for an exchange-grade pricing API; the status
page's job is to be available *when our main system isn't*, and
self-hosted on the same operational footprint as the main
system creates a correlation that defeats the purpose.

## NOT recommended: **cstate**

cstate ships a beautiful static page, but the operational flow
("edit YAML, push, wait for CI, then refresh") is wrong for the
SEV-1 path our playbook §5.2 templates. SEV-1 timelines are
5/30/24 hours. A "git push + Pages deploy" round-trip takes
2-5 minutes per status update — fine for routine, painful for
the 5-minute initial-acknowledge window.

## Implementation outline (post-decision)

This becomes ~half a day once a vendor is chosen. Steps assuming
Instatus:

1. Sign up; pick the free tier.
2. Configure the 5 states from sev-playbook.md §5.1 as
   Instatus components (or "subsystems" in Instatus's vocab):
   API, Indexer, Aggregator, Storage (Timescale + Redis +
   MinIO), Status Page itself.
3. Wire PagerDuty integration so SEV-1/2 acknowledgement in
   PagerDuty creates an Instatus incident with the playbook §5.2
   "Initial" template pre-filled.
4. Add CNAME `status.ratesengine.net → ratesengine.instatus.com`
   in our DNS config (post-public-flip per
   `docs/operations/public-flip.md §Post-flip`).
5. Test by deliberately creating a "Maintenance" entry from
   Instatus's UI and confirming the public page renders.
6. Add `docs/operations/incidents/` git directory + a one-liner
   in sev-playbook.md §5.1 explaining "incident posts live as
   markdown in this directory; the status page is a rendering."

## Open questions for the implementer

1. **Webhook posting from PagerDuty → Instatus**: Instatus
   supports PagerDuty webhook out-of-the-box but the mapping
   between PagerDuty severities (SEV-1/2/3) and Instatus
   states (Operational / Degraded / Partial / Major /
   Maintenance) needs a one-time config table. Recommend:
   PagerDuty SEV-1 → Instatus "Major outage"; SEV-2 → "Partial
   outage"; SEV-3 → "Degraded performance".

2. **Subscriber list policy**: Instatus supports email + RSS +
   Slack subscribers. Public RSS is fine; email subscriber list
   is GDPR-relevant — should we collect emails from launch, or
   defer until customer-base demand surfaces? Recommend defer
   until customers ask — RSS + Slack are zero-PII.

3. **History retention**: Instatus keeps incident history forever
   on paid tier; free tier may have limits. Confirm before
   commit; if limited, mirror the markdown in our repo as the
   long-term record.

4. **Maintenance windows**: should we pre-schedule maintenance
   announcements for our planned deploys? Recommend yes for
   anything where the operator silences alerts (per the SEV-1
   drill scenario's spike-test variant) — public-facing
   "scheduled maintenance" gives customers warning that's
   asymmetric with our internal alert silence.

5. **Status page itself going down**: Instatus's own uptime is
   a vendor commitment, not ours. Document in the runbook that
   if status.ratesengine.net is unreachable, post to
   `#rates-engine-public` Slack channel as the fallback
   communication channel.

## Cost summary

If the recommendation lands:
- **$0/month** at launch (Instatus free tier).
- **DNS cost**: $0 (DNS already managed elsewhere; just adds a CNAME).
- **Engineering time**: ~half a day to set up + integrate.
- **Ongoing**: zero unless we cross 25k email subscribers (~10×
  Freighter's wallet count today).

## When to revisit

Revisit the vendor choice if:
- Customer count crosses 1k (would push us toward paid tier
  anyway, at which point Statuspage's deeper integrations may
  be worth the price difference).
- A SEV-1 highlights an Instatus reliability issue we couldn't
  work around.
- Atlassian sells Statuspage to a more committed owner (the
  vendor-lock concern flips).

Otherwise, the choice is sticky — switching status pages
mid-life is operationally expensive and signals instability to
customers.
