# Public status page — `status.ratesengine.net`

Static-site scaffold for the public status page committed to in
the proposal §"Incident Detection and Response" + Freighter RFP
F3.5 / F3.6 SLA targets.

## What ships from this repo

- [`cstate/config.toml`](cstate/config.toml) — Hugo / cstate
  site configuration (title, base URL, theme params).
- [`cstate/data/systems.yml`](cstate/data/systems.yml) — the
  public component list. Each entry is a customer-facing
  service surface; updates to component status flip the badge
  on the index page.
- [`cstate/content/issues/_TEMPLATE.md`](cstate/content/issues/_TEMPLATE.md)
  — the front-matter + body template every new incident post
  copies from. Per-incident files land alongside it as
  `YYYY-MM-DD-<slug>.md`.

## What does NOT ship from this repo

The hosting target. cstate produces a static `public/`
directory; you can host that anywhere — GitHub Pages, Vercel,
Netlify, Cloudflare Pages, S3 + CloudFront, etc. The choice is
operator preference + cost; this repo is the source-of-truth
for content + config, not the hosting layer.

The DNS record. `status.ratesengine.net` → `<host>` lives in
the operator's DNS, separate from the API's record so the
status page can serve even when the API is down (which is
exactly when customers need it).

## Hosting recommendations

For launch, the simplest credible option is **Cloudflare Pages**
+ this repo's `deploy/status-page/cstate/` as the build root:

1. Connect the repo to Cloudflare Pages.
2. Build command: `hugo --minify` from this directory.
3. Output directory: `public`.
4. Bind `status.ratesengine.net` to the project.

Cloudflare Pages serves from CF's edge for free, runs the build
on every push, and survives any plausible regional outage of
ours. The Statuspage.io commercial route is also acceptable but
costs $79+/month for a paid tier and ties incidents to a
proprietary platform.

## Theme

`cstate` is the chosen theme. It's an off-the-shelf Hugo theme
focused on incident-status pages — no custom CSS / JS to
maintain. Source: <https://github.com/cstate/cstate>. Pin a tag
in the operator's local clone or git-submodule the theme into
`themes/cstate/` at deploy time.

## Operator workflows

- **Routine pre-flight:** `hugo --gc -d public` from this
  directory. Inspect `public/index.html` in a browser. Push.
- **Posting an incident:** see
  [`docs/operations/runbooks/sev-status-page-update.md`](../../docs/operations/runbooks/sev-status-page-update.md).
  The runbook is the binding source of truth for the update
  cadence (hourly during SEV-1, daily during SEV-2 — matches
  Freighter F3.5 / F3.6).
- **Editing the component list:** edit `cstate/data/systems.yml`
  directly. Every entry corresponds to a customer-facing service
  surface; keep the list narrow so visitors can pinpoint
  which segment is affected during partial degradation.

## Why a status page, not just metrics

Metrics + alerts (Prometheus + AlertManager + Slack/Discord —
see `configs/ansible/roles/prometheus/`) cover **operator-side**
visibility. The status page covers **customer-side** visibility:

- Customers who got a 503 want to know whether to retry, fail
  over to a different price source, or wait.
- A static HTML page survives outages that take the API down,
  including ones taking down our Prometheus stack — that's the
  whole point.
- The Freighter SLA's "status updates within X minutes of
  detection" is enforceable via this page; without it the
  language has nowhere to land.

## Pre-launch checklist

- [ ] Hugo + cstate theme installed (operator workstation or CI).
- [ ] Hosting target chosen + connected (Cloudflare Pages
      recommended).
- [ ] DNS for `status.ratesengine.net` → hosting target.
- [ ] First test rebuild succeeds; index page renders with all
      components green.
- [ ] `docs/operations/sev-playbook.md` references the live URL
      (it does, post-PR).
- [ ] `docs/operations/runbooks/sev-status-page-update.md`
      verified by the on-call rota in a tabletop.
