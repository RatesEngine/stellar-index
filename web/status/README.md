# web/status — redirect-only stub

**The status page moved onto the main site at
[`stellarindex.io/status`](https://stellarindex.io/status)** (one site,
unified nav). Its source now lives in the explorer at
`web/explorer/src/app/status/` (page + `incident/[slug]` postmortems),
with the incident loader at `web/explorer/src/lib/incidents.ts`.

This directory is what remains: a **redirect-only** Cloudflare Pages
project (`stellarindex-status`, still bound to `status.stellarindex.io`).
`public/_redirects` 301s every path to
`https://stellarindex.io/status/:splat` (incident deep-links preserved).
The tiny Next page here is just the build artifact the redirect rides on
— it's shadowed by the edge redirect and rarely rendered.

Deploy is unchanged: `.github/workflows/status-page.yml` builds + publishes
this project, keeping the subdomain (and its TLS cert) resolving. No DNS
change was needed to move the page.

To retire the subdomain entirely later, delete the `stellarindex-status`
CF Pages project + the `status.stellarindex.io` DNS record; nothing else
depends on it.
