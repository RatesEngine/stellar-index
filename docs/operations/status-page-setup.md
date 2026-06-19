---
title: Public status page at `status.stellarindex.io`
last_verified: 2026-05-12
status: operator runbook
---

# Public status page setup

> **Moved (2026-06):** the live status page now lives on the main site at
> `https://stellarindex.io/status`. The `status.stellarindex.io` subdomain +
> its `stellarindex-status` CF Pages project still exist but are **redirect-only**
> (`web/status/public/_redirects` 301s every path to `/status`). No DNS change.

The Stellar Index public status page lives at
`https://status.stellarindex.io`. The source lives in this repo at
[`web/status/`](../../web/status/) — a static Next.js export
deployed to Cloudflare Pages on every push to `main`.

F-1211 (codex audit-2026-05-12): this doc previously described an
**Upptime-on-GitHub-Pages** pipeline (and SEV playbook entries
referred to a **cstate** scaffold). Both predecessors were
retired in favour of the in-tree Next.js implementation. The
Upptime fork and the `deploy/status-page/cstate/` directory no
longer exist.

## Architecture (current)

```
internal/incidents/data/      ← canonical incident corpus
  _template.md                  ← required frontmatter shape
  YYYY-MM-DD-<slug>.md          ← one Markdown file per incident

internal/incidents/incidents.go ← go:embed loader the API binary
                                  bakes the corpus into for /v1/incidents

web/status/                    ← Next.js static-export status page
  src/
    app/                       ← /, /incident/[slug]
    lib/incidents.ts           ← build-time loader that reads the
                                 same `internal/incidents/data/*.md`
                                 corpus from the repo root

  next.config.mjs              ← output: 'export'
  public/                      ← static assets, favicon, OG image

deploy:                        ← Cloudflare Pages auto-deploy on push
  trigger:                       push to main
  build:                         `pnpm install && pnpm build`
  output:                        web/status/out
  domain:                        status.stellarindex.io (apex CNAME)
```

The page is **independent of the API by construction** — Cloudflare
Pages is a different provider stack from our Hetzner/AWS/Vultr
origins, so an origin-side outage cannot take the status page down.
The page does not call into the API at runtime; it renders from the
embedded incident corpus committed in the repo plus a build-time
uptime calculation.

## Posting an incident

1. Copy `internal/incidents/data/_template.md` to a new file
   `internal/incidents/data/<YYYY-MM-DD>-<slug>.md` and fill in
   the YAML frontmatter (`title`, `severity`, `status`,
   `started_at`, `affected_components`).
2. Append the customer-facing body (Identification → Impact →
   Timeline → What we did) per the template.
3. Commit + push to `main`. Cloudflare Pages deploys the new
   page within ~2 minutes; after the API binary is also
   redeployed, dashboard webhook subscribers receive the
   `incident.sev1` / `incident.resolved` callbacks via
   `stellarindex-ops emit-incident` (F-1249, codex audit-2026-05-12).

The binding runbook for SEV-1 / SEV-2 updates is
[`runbooks/sev-status-page-update.md`](runbooks/sev-status-page-update.md).

## Component status states

Modelled after Atlassian Statuspage:

- **operational** — green; no active incident.
- **degraded_performance** — partial latency / error-rate impact.
- **partial_outage** — major subsystem down but some surface still
  works.
- **major_outage** — API unavailable.
- **under_maintenance** — scheduled; not an incident.

## Programmatic incident sources

The status page does not poll Prometheus directly — that's the
authoritative source for engineers, not the public-facing signal.
SEV declarations are surfaced via the SEV playbook to the on-call,
who then mirrors the relevant status onto the public page. This
keeps the public page editorial (no false-positive flapping) and
the Prometheus dashboards authoritative (no editorial gate).

## CI / deploy

CI runs `pnpm typecheck`, `pnpm lint`, `pnpm build`, and
`pnpm audit --audit-level high` on every push touching
`web/status/`. The Cloudflare Pages project is configured to
deploy from `main` on push; preview deploys fire automatically
for every PR.
