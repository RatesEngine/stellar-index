# W17 — Web frontends (explorer, dashboard, status)

## Scope

Every page, every API call, every static export of every web
frontend.

In scope:
- `web/explorer/` — Next.js 15 static-export consumer surface
- `web/dashboard/` — admin SPA
- `web/status/` — public status page
- shared TypeScript types vs Go wire shape

Out of scope:
- Cloudflare Pages live state (EX-1206)
- Resolved node_modules (EX-1211)
- Build artifacts under `out/` (EX-1212)

## Inputs

- `inventory/webfrontend-inventory.md`
- `docs/architecture/explorer-data-inventory.md`
- `docs/architecture/explorer-implementation-plan.md`
- `docs/operations/explorer-deployment.md`
- `.github/workflows/explorer-deploy.yml`,
  `docs-deploy.yml`, `status-page.yml`

## Per-page checklist (explorer)

For every page in `web/explorer/src/app/`:

| Page route | API calls | Dead links to removed routes | SEO meta + canonical | Mobile responsive | Tests | Status |
| --- | --- | --- | --- | --- | --- | --- |
| _populate from inventory_ | | | | | | |

Special attention to recent rc.4x explorer changes:
- `/assets/[slug]` renders for fiat + cross-chain currencies
  (rc.45 / `dd91cd63`)
- explorer migrated every `/v1/coins` consumer to `/v1/assets`
  (rc.47 / `8452716b`) — **but second-pass audit found
  active `/v1/currencies` callers remaining; see F-1201**
- explorer deleted `/currencies/*` pages (`a219a7f7`)
- `_redirects` consolidation under Cloudflare cap
- version surface (footer badge + meta tag) per rc.45

### Removed-route hygiene (F-1201)

Confirmed during plan creation: explorer still calls
`/v1/currencies` from these source files:

- `web/explorer/src/app/HomeCurrencies.tsx`
- `web/explorer/src/app/sitemap.ts`
- `web/explorer/src/app/HomeTryAPI.tsx`
- `web/explorer/src/app/embed/currency/[ticker]/page.tsx`
- `web/explorer/src/app/assets/[slug]/AssetConverter.tsx`

Once the rc.48 binary deploys to R1, every page that mounts
these components will start failing. This is a Wave 0
pre-flip blocker.

## Per-page checklist (dashboard)

- auth: dashboardauth + dashboardkeys flows
- usage display path (consumes `internal/usage/counter`)
- billing display path
- key list/create/revoke flows
- list of pages from inventory

## Per-page checklist (status)

- data source for each "system OK / degraded" tile
- incidents linkage to `docs/operations/incidents/`
- maintenance window publication
- public RSS / atom feed?

## Build hygiene

For each frontend:

| Check | Explorer | Dashboard | Status |
| --- | --- | --- | --- |
| `next.config.mjs` — output: 'export' or 'standalone'? | | | |
| `wrangler.toml` — Cloudflare Pages config valid | | | |
| `_headers` / `_redirects` present? | | | |
| `tsconfig.json` strict mode | | | |
| `tailwind.config.ts` | | | |
| pnpm-lock.yaml audited (W04) | | | |
| Build under `make web-build` / `make dashboard-build` / `make status-build` | | | |
| Static-export constraints (e.g. `dynamic = 'force-static'` on sitemap/robots) | | | |

## API shape ↔ frontend type drift

If the frontends use shared types from `pkg/client` via codegen,
verify codegen is fresh. If they use hand-rolled types, find
every spot where they could drift (e.g. `data.flags.stale`
expected but not in API response).

## SEO + social

- sitemap.xml exists + lists every verified-asset page
- robots.txt allows public crawl
- OpenGraph metadata per asset page
- canonical URLs avoid duplicate-content issues (case-fold dedup
  per rc.4x bug fix `e9620f26`)
- favicon set
- preload critical assets

## Accessibility

- semantic HTML (heading levels, landmarks)
- keyboard navigation
- ARIA where needed
- contrast ratios (AA)
- skip-to-content

## Adversarial vectors

- F1.* wrong-data on user-facing page
- F2.* reputational (status page green during real outage)
- B6.1/B6.2 explorer making dead-route requests

## Cross-workstream dependencies

- W03 owns `explorer-deploy.yml`, `docs-deploy.yml`, `status-page.yml`
- W04 owns pnpm-lock audit
- W11 owns API surface that the frontends consume
- W19 owns dashboard auth surface

## Closure criteria

- Every page in every frontend has a row
- Build hygiene table complete
- API shape ↔ frontend type drift assessed
- SEO + accessibility evaluated
- Removed-route reference check (no `/v1/coins/*` or
  `/v1/currencies/*` left in explorer code, examples, or
  static-export output)
