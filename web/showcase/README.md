# Rates Engine — showcase site

Public explorer for the Rates Engine API. Lives at
`app.ratesengine.net` post-launch.

This is the **scaffold** that lands as Phase 0 of the
[implementation plan](../../docs/architecture/showcase-site-implementation-plan.md).
Everything visible today is a stub; real panels arrive starting in
Phase 7.

## Stack

- [Next.js 15](https://nextjs.org) — app router, RSC default
- TypeScript (strict)
- [TailwindCSS](https://tailwindcss.com) + [shadcn/ui](https://ui.shadcn.com)
- [TradingView Lightweight Charts](https://tradingview.github.io/lightweight-charts/)
- [TanStack Query v5](https://tanstack.com/query)
- [openapi-typescript](https://github.com/openapi-ts/openapi-typescript) — types generated from `../../openapi/rates-engine.v1.yaml`
- [Zod](https://zod.dev) — runtime validation at the API boundary
- [satori](https://github.com/vercel/satori) + [@resvg/resvg-js](https://github.com/yisibl/resvg-js) — Open Graph card generation (build-time + Cloudflare Worker for long-tail)
- [lucide-react](https://lucide.dev) — icons
- MDX via `@next/mdx` (Phase 12 — research blog)

**Static export only** (`output: 'export'` in `next.config.mjs`).
Deployed to Cloudflare Pages at v1; rsync → r1 nginx behind Cloudflare
CDN as the fallback. Same vendor story as `api.ratesengine.net`.

## Run locally

```sh
cd web/showcase
pnpm install
pnpm dev
# http://localhost:3000
```

## Common tasks

```sh
pnpm dev                # next dev (HMR)
pnpm build              # next build (production)
pnpm typecheck          # tsc --noEmit
pnpm lint               # next lint
pnpm format             # prettier --write
pnpm generate:api       # regenerate src/api/types.ts from OpenAPI
```

`make web-dev`, `make web-build`, `make web-typecheck`, `make web-lint`
all hit the same scripts from the repo root.

## Layout

```
src/
├── app/                 Next.js routes
├── components/
│   ├── ui/              shadcn copies
│   ├── primitives/      MultiWindowDelta, Sparkline, DirectionPill, …
│   ├── panels/          Composed cards
│   ├── reveal/          The <> mechanism
│   ├── charts/          Lightweight Charts wrappers
│   ├── nav/             Navbar, search, footer
│   └── mdx/             RatesLink, RatesPanel, TxLink (Phase 12)
├── api/
│   ├── client.ts        Fetch wrapper
│   ├── types.ts         Generated; DO NOT EDIT
│   └── hooks.ts         TanStack Query hooks (Phase 7)
├── lib/
│   ├── url-state.ts     Query-param helpers
│   ├── time-pin.ts      as_of_ledger helpers
│   ├── format.ts        Number/date formatters
│   └── slugs.ts         Asset-slug resolution
└── posts/               MDX research articles (Phase 12)
```

## Design principles

(See [`showcase-site-data-inventory.md` §3](../../docs/architecture/showcase-site-data-inventory.md)
for the canonical list. Summary:)

1. **Panels = API queries, 1:1.** No client-side joins.
2. **`<>` reveals the request.** Every panel exposes its underlying call.
3. **URL state is config.** Every selection lives in query params.
4. **Server-shaped, not client-joined.** New endpoint > N round-trips.
5. **Mobile-first, performance budget.** LCP < 1.5s on 3G, 100 KB JS per route.

## API source-of-truth

The OpenAPI spec at `../../openapi/rates-engine.v1.yaml` is the
contract. `pnpm generate:api` regenerates `src/api/types.ts`; CI
fails if the regen would produce a diff. **Never hand-edit
`src/api/types.ts`.**

## Deployment

Static export only — `next build` produces an `out/` directory of
HTML + JS + CSS, no Node runtime needed at the edge.

**v1 target: Cloudflare Pages.** Connect the repo, set the build
output dir to `web/showcase/out`, set the build command to
`pnpm --filter @ratesengine/showcase build`. Cloudflare auto-deploys
on every push to `main` + creates per-PR previews. Free tier covers
unmetered requests on Pages-served static assets.

**v1 fallback: rsync → r1.** `make web-build` produces `out/`; rsync
that to `/var/www/showcase/` on r1; Cloudflare proxies `app.ratesengine.net`
→ r1 nginx → static files. Same TLS termination + CDN as the API.

Dynamic routes (`/coins/{slug}`, `/contracts/{id}`, `/tx/{hash}`,
`/accounts/{G}`) use **client-side rendering**: the build emits a
shell, the page hydrates and fetches data from `api.ratesengine.net`
via TanStack Query. High-traffic routes (top-N coins, all protocols,
all sources) get pre-rendered via `generateStaticParams` for SEO;
the long-tail is JS-rendered (Google handles it fine in 2026).

Why not Vercel: brand fit + vendor consolidation. Static export to
our existing Cloudflare CDN is one vendor; Vercel is two. Reliability
difference is invisible at our traffic volume — the showcase is a
thin shell over `api.ratesengine.net`, which is the actual reliability
question.
