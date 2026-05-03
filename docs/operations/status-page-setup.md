---
title: Public status page at `status.ratesengine.net` (L4.11)
last_verified: 2026-05-03
status: operator runbook
---

# Public status page setup

Operator runbook for closing **L4.11 / Task #73** in the
launch-readiness backlog. **Decision: Upptime on GitHub Pages.**

## Why Upptime

The hard rule: a status page MUST be hosted independently of the
infra it reports on. Same-rack hosting defeats the purpose during
an outage. GitHub Pages is independent of our origin (Hetzner FSN1,
AWS us-east-1, Vultr Singapore) — that constraint binds the choice.

Within the GitHub-Pages-hosted set, the choice was between cstate
(static, manual incident posting) and **Upptime** (GitHub
Actions-driven, auto-monitored). Upptime wins on:

- **Automatic uptime monitoring.** GitHub Actions probes every 5
  minutes; failed probes auto-create issues; recovered endpoints
  auto-close them. The on-call doesn't have to remember "post
  to the status page" during an incident — the page updates
  itself.
- **Same-stance fit.** Incident lifecycle is GitHub issues; the
  workflow is the project's existing review/escalation tooling.
- **Same independence guarantee.** Hosted on GitHub Pages,
  operationally independent of our origin.

We can graduate to a custom solution post-launch if customer
feedback wants tighter brand integration — the URL stays
`status.ratesengine.net`, only the backend swaps.

**Tradeoff to know about:** Upptime probes from GitHub's IPs
only. If our service is reachable from GitHub's runners but
unreachable from real customers (e.g. EU-only outage), Upptime
won't catch it. Our own Prometheus alerts on the API path catch
real outages; Upptime is the *public-facing* signal, not the
authoritative one.

## Step-by-step

```
0. Pre-reqs
   - GitHub org "RatesEngine" (already exists; same org as
     the rates-engine repo).
   - DNS for ratesengine.net under operator control
     (Cloudflare per cdn-setup.md).
   - A GitHub PAT with repo + workflow scope, stored as
     repo secret GH_PAT in the new status repo. (Upptime's
     workflows commit response-time data back to the repo;
     the default GITHUB_TOKEN can't trigger downstream
     workflows on its own commits.)

1. Fork the Upptime template
   - Go to https://github.com/upptime/upptime
   - "Use this template" → Create a new repo
   - Owner: RatesEngine
   - Name: ratesengine-status
   - Visibility: Public

2. Configure .upptimerc.yml
   See the canonical example at upptime/upptime#readme.
   Minimum config for Rates Engine:

       owner: RatesEngine
       repo: ratesengine-status

       sites:
         - name: API (api.ratesengine.net)
           url: https://api.ratesengine.net/v1/healthz
           expectedStatusCodes: [200]
           assignees: [ash]
         - name: API readiness
           url: https://api.ratesengine.net/v1/readyz
           expectedStatusCodes: [200]
         - name: SSE (price tip stream)
           url: https://api.ratesengine.net/v1/price/tip/stream?base=native&quote=fiat:USD
           expectedStatusCodes: [200]
           # SSE keeps the connection open; Upptime closes after
           # status-line + first event. 5s timeout is enough.
           maxResponseTime: 5000
         - name: Documentation (docs.ratesengine.net)
           url: https://docs.ratesengine.net
           expectedStatusCodes: [200]
         # r1/r2/r3 health if exposed publicly:
         - name: API origin r1 (FSN1)
           url: https://api-r1.ratesengine.net/v1/healthz
           expectedStatusCodes: [200]
         - name: API origin r2 (us-east-1)
           url: https://api-r2.ratesengine.net/v1/healthz
           expectedStatusCodes: [200]
         - name: API origin r3 (Singapore)
           url: https://api-r3.ratesengine.net/v1/healthz
           expectedStatusCodes: [200]

       status-website:
         cname: status.ratesengine.net
         baseUrl: /
         name: Rates Engine
         introTitle: "Live status of the Rates Engine API + ingest layers"
         introMessage: |
           Probe results from GitHub-hosted runners every 5
           minutes. Authoritative incident reporting lives on
           the GitHub Issues tab of this repo.
         logoUrl: https://docs.ratesengine.net/logo.png  # if applicable

       assignees:
         - ash

       # Optional: post incident updates to a Slack webhook.
       # If set, Upptime alerts on every status change. Skip
       # if Slack notifications come from our own alerting
       # stack (Alertmanager); double-noise is worse than one
       # source.
       #
       # notifications:
       #   - type: slack
       #     url: $SLACK_WEBHOOK   # set as repo secret

3. Configure repo secrets (Settings → Secrets → Actions)
   - GH_PAT: a fine-grained PAT with `contents: write` +
     `actions: write` on this repo only. Used by Upptime's
     workflows to commit response-time history.

4. Trigger first run
   - Actions tab → "Uptime CI" → Run workflow.
   - First run takes ~5 min: probes every site, generates
     history JSON, pushes to gh-pages branch.

5. DNS
   - In Cloudflare, add CNAME:
       Name: status
       Target: ratesengine.github.io   (default GitHub Pages hostname)
       Proxy status: Proxied (Cloudflare in front gives us TLS
                              + DDoS posture)
   - Wait ~5 min for cert provisioning.
   - Verify https://status.ratesengine.net renders and shows
     all sites.

6. Smoke test
   - Force a probe failure: temporarily change one site's URL
     in `.upptimerc.yml` to a 404 endpoint.
   - Wait for the next 5-min probe cycle.
   - Expect: a new GitHub issue auto-opens describing the
     failure; the status page shows the affected component
     as "down".
   - Revert the URL; on the next probe cycle the issue
     auto-closes and the page returns to "all systems
     operational".
```

## Manual incident posting (when needed)

Auto-monitoring catches downtime that's reachable-from-GitHub.
For incidents Upptime can't see (correctness bugs, regional
outages from non-GitHub viewpoints, policy decisions like
maintenance windows), open a GitHub issue manually with the
Upptime-recognised labels:

```sh
gh issue create \
  --repo RatesEngine/ratesengine-status \
  --title "Investigating: degraded VWAP accuracy on USDC pairs" \
  --label "incident,API (api.ratesengine.net)" \
  --body "Investigating reports of a ~0.5% drift on USDC-quoted
VWAP since 14:20 UTC. Likely a stablecoin depeg signal not yet
folded into the aggregator's confidence factor. Updates here."
```

The site-name label MUST match a name in `.upptimerc.yml`'s
`sites:` list — that's how Upptime ties the issue to a
component. Resolving the issue (closing it) flips the
component back to "operational" on the page.

## Subscriptions

Upptime ships with:

- **RSS feed** at `/feed.xml` — zero config.
- **History JSON** at `/api/<site>.json` — for any third-party
  dashboard that wants raw data.
- **Slack notification** support via the (commented) `notifications:`
  block above.

For v1 launch, ship with RSS only; add Slack if the first
customer asks. Email subscriptions are not built-in; pair
with Mailchimp / Buttondown pointing at the RSS feed if needed
post-launch.

## Verification (pre-launch checklist)

- [ ] `https://status.ratesengine.net` renders and shows
      all sites.
- [ ] TLS cert is valid (Cloudflare-issued).
- [ ] First probe cycle completed; status page shows
      "all systems operational" (or accurate state).
- [ ] RSS feed at `/feed.xml` validates as well-formed.
- [ ] Test incident: forced a probe failure, confirmed an
      issue auto-opened, reverted, confirmed it auto-closed.
- [ ] Manual incident-posting workflow recorded in
      [`sev-playbook.md`](sev-playbook.md) (post a status
      update step).
- [ ] L4.11 in [`launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md)
      flipped 🟡 → ✅.

## Cross-references

- Backlog row: L4.11 in [launch-readiness-backlog.md](../architecture/launch-readiness-backlog.md)
- SEV escalation procedure: [sev-playbook.md](sev-playbook.md)
- Upptime upstream: <https://github.com/upptime/upptime>
- Upptime examples gallery: <https://upptime.js.org/>
