# W22 — Launch readiness, public-flip

## Scope

Everything that must be true before the public DNS flip and the
first paying customer integration.

In scope:
- `docs/operations/public-flip.md`
- `docs/operations/launch-day-checklist.md`
- `docs/operations/pre-launch-hardening.md`
- `docs/architecture/launch-readiness-backlog.md`
- `docs/launch-task-list.md`
- `docs/operations/customer-demo-script.md`
- `docs/operations/cdn-setup.md`, `cf-pages-setup.md`,
  `explorer-deployment.md`, `status-page-setup.md`
- branding / DNS / SEO posture
- legal: LICENSE, paid-feed redistribution, GDPR, cookie consent
- billing / payment surface
- pricing model + quotas
- documentation entry-point (`docs/getting-started.md`)
- public-flip strategy (NEW repo at v1.0 per memory; no force-push)

## Inputs

- `docs/operations/public-flip.md` — playbook
- `docs/operations/launch-day-checklist.md`
- `08-cgcmc-parity-matrix.md` — Wave 0 gate requires zero high
  gaps
- `09-stellar-coverage-matrix.md` — same
- memory: public-flip strategy (NEW repo) and live-in-development
  phase

## Public-flip dry-run

Walk `docs/operations/public-flip.md` step-by-step against a
staging copy. For each step:

| # | Step (verbatim) | Outcome | Evidence | Status |
| --- | --- | --- | --- | --- |
| _populate from doc_ | | | | |

## Launch-day checklist

Walk `docs/operations/launch-day-checklist.md` against current
R1:

| Item | Status today | Blocker? | Status |
| --- | --- | --- | --- |
| _populate from doc_ | | | |

## Public-flip strategy reconciliation

The memory record says:

> Publish to NEW repo at v1.0; never force-push the private repo

Verify:

- target repository name + organisation
- migration playbook exists
- contributor history preservation strategy
- secret scrubbing pass (W04 + W19 grep) before publication
- LICENSE attribution chain for paid feeds

## Branding / DNS

- `ratesengine.net` apex + `api.ratesengine.net` + `docs.ratesengine.net`
- Cloudflare zone records
- SSL/TLS termination points
- redirects: `_redirects` config
- email DNS (DMARC, SPF, DKIM)
- WHOIS privacy

## Legal posture

| Concern | Status |
| --- | --- |
| LICENSE Apache-2.0 in place | yes |
| Paid CMC feed redistribution licence | _verify_ |
| Polygon Forex licence | _verify_ |
| Frankfurter / ECB licence | _verify_ |
| GDPR data-export path | _verify_ |
| GDPR data-deletion path | _verify_ |
| Cookie consent on explorer | _verify_ |
| Privacy policy + ToS | _verify_ |
| Customer-data residency declaration | _verify_ |

## Pricing model + quotas

| Concern | Status |
| --- | --- |
| Free-tier quota documented | _verify_ |
| Paid-tier quota documented | _verify_ |
| Self-serve signup works | _verify against `internal/auth/signup_tracker.go`_ |
| Self-serve key revoke works | _verify_ |
| Usage visible to user | _verify_ |
| Billing surface live | _verify_ |
| Payment provider integration | _verify (EX-1214 SaaS state)_ |

## Comms readiness

- `deploy/comms/launch-announcement.md` — final draft? blog
  hook? social hook?
- `docs/blog/` — launch post drafted?
- status page seeded with "All systems operational"
- onboarding email finalised

## Documentation entry point

- `docs/getting-started.md` walkable cold by a new developer
- explorer landing page makes the value prop clear
- OpenAPI accessible at a stable URL
- Postman collection up to date

## Adversarial vectors

- E3.1..E3.3 public-flip path
- F2.1 status page reports green during real outage
- G.* compliance / legal

## Cross-workstream dependencies

- W20 owns CG/CMC parity matrix
- W09 owns Stellar-depth matrix execution
- W14 owns alerting readiness
- W17 owns explorer / status-page readiness
- W19 owns billing + secret hygiene
- W23 owns multi-region claim (R2/R3 not required for launch
  but the *claim* must be honest)

## Closure criteria

- Public-flip dry-run table complete
- Launch-day checklist table complete
- Legal posture table complete
- Pricing model + quotas verified
- Branding / DNS verified
- Comms readiness verified
- All Wave 0 findings closed (per remediation plan)
