# Public status page — `status.ratesengine.net`

The status page is a custom Next.js static-export app at
[`web/status/`](../../web/status). It replaces the previous
cstate (Hugo) implementation that was vendored under this
directory — see git history if you need the old version.

## What ships from the codebase

- [`web/status/src/app/page.tsx`](../../web/status/src/app/page.tsx)
  is the single page. It polls `/v1/status` every 30 s and
  renders:
    - Overall status banner (ok / degraded / down)
    - Per-service heartbeats (api, indexer, aggregator) with
      "last seen X ago" timestamps
    - Request-latency strip (p50 / p95 / p99 over 5 min,
      coloured against RFP targets 50 / 200 / 500 ms)
    - Ingest freshness (last aggregator tick + active source
      count)
    - Active incidents — sourced from Alertmanager via
      `/v1/status`; severity-coloured, runbook-linked
    - Public endpoint matrix grouped by surface (Health,
      Pricing, Catalogue, Oracle, Auth)
    - Incident history — currently empty; past incidents will
      land here once the auto-posting pipeline ships

The endpoint list in `page.tsx` is intentionally curated —
operator-only surfaces (`/metrics`, `/v1/diagnostics/*`) are
excluded so the page focuses on what customers actually consume.

## Hosting

Cloudflare Pages project `ratesengine-status` with custom
domain `status.ratesengine.net`. Bootstrap config managed by
[`scripts/ops/cf-pages-bootstrap.sh`](../../scripts/ops/cf-pages-bootstrap.sh):
- Root: `web/status`
- Build command: `pnpm install --frozen-lockfile && pnpm build`
- Output: `out`
- Env: `NEXT_PUBLIC_API_BASE_URL=https://api.ratesengine.net`,
  `NODE_VERSION=20`, `PNPM_VERSION=10`

## Why not cstate / Statuspage.io

cstate v6 broke our v5 config shape, and the v5 vendored copy
hit a chain of Hugo-template gotchas (pagination config,
date-format params, `_TEMPLATE.md` rendering as a real
incident). Maintaining a Hugo theme + a dozen cstate-specific
config knobs to render "ok/not ok + latency" wasn't a good
trade vs ~400 lines of TSX that read directly from
`/v1/status` and look polished by default.

Statuspage.io ($79+/month, ties incidents to a proprietary
platform) was rejected on the same make-vs-buy axis — the
data already exists in our Prometheus + Alertmanager pipeline.
