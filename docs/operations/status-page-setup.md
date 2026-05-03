---
title: Public status page at `status.ratesengine.net` (L4.11)
last_verified: 2026-05-03
status: operator runbook
---

# Public status page setup

Operator runbook for closing **L4.11 / Task #73** in the
launch-readiness backlog. Decision: **cstate on GitHub Pages**.
The architectural rationale is in [the prior session
summary](../architecture/launch-readiness-backlog.md) — short
version:

- A status page MUST be hosted independently of the infra it
  reports on. Same-rack hosting defeats the purpose during an
  outage.
- GitHub Pages is independent of our origin (Hetzner FSN1, AWS
  us-east-1, Vultr Singapore) and free.
- Incident posting via `git push` matches the project's
  "everything reviewable" stance and is scriptable for
  on-call use.
- We can graduate to Statuspage.io later without changing the
  customer-facing URL.

## Step-by-step

```
0. Pre-reqs
   - GitHub org (the same one hosting the rates-engine repo).
   - DNS for ratesengine.net under operator control (Cloudflare
     per cdn-setup.md).

1. Create the status repo
   - Name: ratesengine-status
   - Visibility: Public (the page is public; the repo is too)
   - Initialise: empty (cstate will scaffold)

2. Scaffold from cstate
   - Local clone:
       git clone git@github.com:RatesEngine/ratesengine-status.git
       cd ratesengine-status
   - Initialise with cstate's hugo template:
       git submodule add https://github.com/cstate/cstate themes/cstate
       cp -r themes/cstate/exampleSite/* .
   - Edit config.toml:
       title = "Rates Engine Status"
       baseurl = "https://status.ratesengine.net"
       [params]
         description = "Live status of the Rates Engine API + ingest layers."
         brandName = "Rates Engine"

3. Define components (= surfaces an outage page would mention)
   Create one file per component under `content/issues/.gitkeep`
   alternatives, then list components in config.toml [[params.systems]]:

       [[params.systems]]
         name = "API (api.ratesengine.net)"
         category = "core"
       [[params.systems]]
         name = "Ingest pipeline"
         category = "core"
       [[params.systems]]
         name = "Aggregator"
         category = "core"
       [[params.systems]]
         name = "Multi-region replication (R1/R2/R3)"
         category = "core"
       [[params.systems]]
         name = "SSE streams"
         category = "core"
       [[params.systems]]
         name = "Documentation (docs.ratesengine.net)"
         category = "secondary"

4. Wire GitHub Pages publishing
   - Add .github/workflows/deploy.yml:

       name: deploy
       on:
         push:
           branches: [main]
       jobs:
         build:
           runs-on: ubuntu-latest
           steps:
             - uses: actions/checkout@v4
               with: { submodules: true }
             - uses: peaceiris/actions-hugo@v3
               with: { hugo-version: latest, extended: true }
             - run: hugo --minify
             - uses: peaceiris/actions-gh-pages@v4
               with:
                 github_token: ${{ secrets.GITHUB_TOKEN }}
                 publish_dir: ./public
                 cname: status.ratesengine.net

5. DNS
   - In Cloudflare, add CNAME:
       Name: status
       Target: ratesengine.github.io   (default GitHub Pages hostname)
       Proxy status: Proxied (Cloudflare in front gives us TLS + DDoS)
   - Wait ~5 min for cert provisioning.

6. Smoke test
   - First commit: a "v1 launch" maintenance entry under
     content/issues/2026-05-03-launch.md scheduled in advance.
   - Verify https://status.ratesengine.net renders and the
     entry is visible.
```

## Posting an incident

```sh
cd ratesengine-status
DATE=$(date -u +%Y-%m-%d-%H%M)
cat > content/issues/${DATE}-degraded-api.md <<'EOF'
---
title: "Degraded API performance"
date: 2026-05-03T14:23:00Z
resolved: false
informational: false
severity: down            # down | disrupted | notice
affected: ["API (api.ratesengine.net)"]
---

Investigating: API p95 latency elevated since 14:20 UTC.
Likely R1 origin under load; failover to R2 in progress.
EOF

git add content/issues/${DATE}-degraded-api.md
git commit -m "incident: API degraded $DATE"
git push
```

The Pages deploy runs immediately; subscribers refreshing the
page see the update within ~30 seconds.

To resolve:

```sh
# Edit the same file:
# - Set `resolved: true`
# - Append a "Resolved: <UTC time>" line + post-mortem summary
git commit -am "incident: $DATE resolved"
git push
```

## Subscriptions

cstate has no built-in subscriptions. Pair with one of:

- **RSS** — cstate emits a feed at `/issues/index.xml`. Customers
  subscribe via any RSS reader. Zero work on our side.
- **Email** — point a Mailchimp / Buttondown form at the RSS
  feed. ~30 min setup, $0–$10/mo.
- **Slack/Discord webhooks** — post manually as part of the
  incident-response runbook (we already do this for SEV
  channels).

For v1 launch, ship with RSS only; add email if the first
customer asks.

## Verification (pre-launch checklist)

- [ ] `https://status.ratesengine.net` renders and shows
      "All systems operational" with the six components
      listed above.
- [ ] TLS cert is valid (Cloudflare-issued).
- [ ] RSS feed at `/issues/index.xml` validates as well-formed.
- [ ] Test incident posted + resolved cleanly during a dev-time
      drill.
- [ ] On-call runbook (`docs/operations/sev-playbook.md`)
      includes the "post a status update" step.

## Cross-references

- Backlog row: L4.11 in [launch-readiness-backlog.md](../architecture/launch-readiness-backlog.md)
- SEV escalation procedure: [sev-playbook.md](sev-playbook.md)
- cstate upstream: <https://github.com/cstate/cstate>
