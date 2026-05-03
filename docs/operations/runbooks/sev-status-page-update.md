---
title: SEV status-page update
last_verified: 2026-05-03
status: living doc
related:
  - docs/operations/sev-playbook.md
  - deploy/status-page/README.md
  - deploy/status-page/cstate/content/issues/_TEMPLATE.md
---

# SEV status-page update

The customer-facing companion to the on-call escalation flow.
Every SEV that meets the visibility threshold below MUST be
posted to `status.ratesengine.net`; this runbook is the binding
how-to.

## When to post

Post a status-page update for any incident that:

- a customer might notice from the outside (HTTP 5xx rate spike,
  latency SLO breach, ingest lag visible on `flags.stale`,
  prolonged data-freshness gap, etc.), AND
- has fired a SEV-1 or SEV-2 per [`sev-playbook.md`](../sev-playbook.md).

Operator-internal incidents (e.g. a build failure, a non-
production drift event, an internal monitoring blip that didn't
affect serving) MUST NOT post — the page exists to tell
customers about THEIR experience, not ours.

## Update cadence

Per Freighter F3.5 / F3.6 + the SEV playbook §2:

| Severity | First update | Subsequent updates |
|---|---|---|
| SEV-1 | At detection (≤ 15 min from SEV start) | Every hour |
| SEV-2 | At detection (≤ 30 min from SEV start) | Every 24 h |

If a SEV-2 escalates to SEV-1, switch to the hourly cadence at
the escalation timestamp; don't wait for the next hour boundary.

## Steps

### 1 — Open a fresh incident file

```sh
cd deploy/status-page/cstate/content/issues
cp _TEMPLATE.md "$(date -u +%Y-%m-%d)-<slug>.md"
$EDITOR "$(ls -t *-<slug>.md | head -1)"
```

`<slug>` is a short lowercase hyphen-separated identifier
(`pricing-api-stale`, `cex-ingest-down`). It appears in the URL
so make it informative; the title is what the customer reads.

### 2 — Fill the front-matter

Match the template precisely. The five fields the page renders
from are `title`, `date`, `resolved`, `severity`, `affected`:

```yaml
title: "Pricing API returning stale prices"
date: 2026-05-02T18:00Z
resolved: false
severity: disrupted
affected:
  - "Pricing API"
section: issue
```

`affected:` values MUST match `name:` strings in
[`deploy/status-page/cstate/data/systems.yml`](../../../deploy/status-page/cstate/data/systems.yml)
exactly — the cstate render fails an entry that doesn't.

### 3 — Write the first update body

Use the template's `## Investigating — <UTC>` heading. One
short paragraph from the customer's POV — what they're seeing,
that we're investigating, when the next update lands.

> **Bad:** "Aggregator is throwing PgError 53300 'too many
> connections' on the secondary; primary failover initiated."
>
> **Good:** "We're seeing elevated error rates on the pricing
> API; some requests are returning 5xx. Engineers are
> investigating; next update in 1 hour."

The status page is a **public** surface. Don't leak internal
infrastructure detail; don't speculate about cause until the
"Identified" stage; don't blame upstream providers by name in
the early updates.

### 4 — Push

`git add` + `git commit` + `git push` against the status-page
branch / repo per the `deploy/status-page/README.md` hosting
notes. Cloudflare Pages (or whichever host) rebuilds within
1–2 min; the update is then live.

CI rebuild status is visible at the host's deploy dashboard;
if the build fails, the previous version stays up — the static
contract is preserved.

### 5 — Post follow-up updates per cadence

Each update is a NEW heading inside the SAME file. Don't open
a second file for the same incident. The lifecycle headings:

- `## Investigating — <UTC>` — initial post; cause unknown.
- `## Identified — <UTC>` — cause known; mitigation underway.
- `## Monitoring — <UTC>` — fix deployed; watching for
  recurrence.
- `## Resolved — <UTC>` — back to normal.

Customers get a clean timeline they can scroll without us
needing a ticket-tracker on the public surface.

### 6 — Resolve

When the incident clears:

1. Add a `## Resolved — <UTC>` body section.
2. Flip `resolved: true` in the front-matter.
3. (Optional) Add the post-mortem link or commit-deadline to
   the resolved-section body.

cstate moves the file out of the active incidents list and into
the history view. Resolved incidents stay visible per the
default theme retention (~30 days on the index, indefinitely
deeper-linked).

## What if the operator workstation is down too

If you can't reach git from your laptop (e.g. you're on the
phone with the customer + your VPN died), the on-call playbook
has a fallback: edit the markdown directly through the host's
web interface (Cloudflare Pages → repository → edit on GitHub
in browser → commit). It's slower but works from any device
with a browser.

The DNS for `status.ratesengine.net` lives at the registrar
(Cloudflare) — separate from the API DNS — so the status
page itself doesn't depend on our infrastructure being
reachable. That's the whole point.

## Don't

- **Don't** delete an incident file even after resolution.
  History is part of the trust signal; "incident never happened"
  reads worse than "incident happened, we recovered cleanly".
- **Don't** post a new incident for the same root cause that
  recurred within 24 h — append to the existing file with a
  fresh `## Recurrence — <UTC>` heading instead.
- **Don't** blame upstream providers by name in early updates.
  "Stellar mainnet validator congestion" is fine after you've
  confirmed it; "AWS US-East is down" is fine after the AWS
  status page has confirmed it. Speculation backfires.
- **Don't** post operator-internal events. The page is for
  customer-visible impact only.
- **Don't** auto-resolve via a script. A human reads the
  resolved-state signals and presses the button. Auto-resolve
  has bitten every status-page-running team that's tried it.
