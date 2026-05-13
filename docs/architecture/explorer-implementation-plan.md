---
title: Showcase site — implementation plan
last_verified: 2026-05-13
status: planning
---

# Showcase site — implementation plan

> **Companion doc:** [`explorer-data-inventory.md`](explorer-data-inventory.md)
> describes WHAT we're building. This doc describes HOW we'll build it
> — stack decisions, phased ticket breakdown, dependencies, risks.

## 1. Stack decisions

### 1.1 Frontend

| Concern | Choice | Why |
|---|---|---|
| Framework | **Next.js 15** (app router, RSC default) | SSR + RSC + ISR + edge runtime in one toolkit; static-export for most routes covers our perf budget; mature ecosystem |
| Language | **TypeScript (strict)** | Type-safety end-to-end with the OpenAPI-generated client |
| Styling | **TailwindCSS** + **shadcn/ui** | Utility-first, zero-runtime CSS, copy-paste components — no design-system maintenance overhead |
| Charts | **TradingView Lightweight Charts** | BSD-licensed, ~30 KB gzipped, has every chart feature we need (OHLC, lines, areas, markers); no licensing cost |
| Data fetching | **TanStack Query (React Query v5)** | Cache, refetch-on-focus, optimistic updates, request dedup — battle-tested |
| API types | **`openapi-typescript`** | Generates `web/explorer/src/api/types.ts` from `openapi/rates-engine.v1.yaml`. Single source of truth. CI check fails if regen drifts. |
| Validation | **Zod** | Runtime validation at the API boundary (defends against contract drift) |
| MDX | **`@next/mdx`** + **`next-mdx-remote`** | Research articles authored as `.mdx` files |
| OG images | **`satori` + `@resvg/resvg-js`** (build-time) for known routes; small Cloudflare Worker for dynamic. | Pure SVG → PNG; no Vercel-specific dep. Build-time covers most routes; the Worker is ~50 lines for the long-tail. |
| Icons | **lucide-react** | Tree-shakeable SVG icons |
| Toasts / notifications | **sonner** | Lightweight, good a11y |
| Date formatting | **date-fns** + native Intl | Tree-shakeable, locale-aware |
| State | URL params + local component state + TanStack Query | No global state library — URL is the source of truth (per design principle §3 of the data-inventory doc) |

### 1.2 Backend (Go monorepo)

No language change. Extensions to the existing stack:

| Component | Addition |
|---|---|
| `internal/api/v1/` | ~80 new handlers (mostly thin wrappers over existing storage) |
| `internal/api/v1/timepin/` | NEW — `as_of_ledger` projection helper used by every handler |
| `internal/aggregate/{tvl,mev,changesummary,routerattribution,defindexexposure}/` | NEW workers |
| `internal/sources/{router_attribution,classic_registry,path_payments}/` | NEW decoders/observers |
| `internal/sources/sdex/` | EXTENDED — capture full offer lifecycle |
| `internal/sources/accounts/` | EXTENDED — issuer-account auth flag tracking |
| `internal/wasm/` | NEW — WASM history ingestion + wasm2wat handler (cgo dep on `libwabt`) |
| `migrations/` | 10 new migrations (0017-0026) |
| `pkg/client/` | Auto-extend with new endpoints (Go SDK stays current) |

### 1.3 Hosting + deployment

| Concern | Choice | Notes |
|---|---|---|
| **Build target** | **`output: 'export'`** in `next.config.mjs` | Static HTML + JS bundle; ~5 MB total. No SSR, no edge runtime, no vendor lock-in. |
| **v1 hosting** | **Cloudflare Pages** | Same vendor as the API CDN; git-push deploys with per-PR previews; free tier covers v1 traffic. Same operational story as `cdn-setup.md`. |
| **v1 fallback** | rsync → r1 nginx → Cloudflare CDN | If we want full self-hosted: `next build` produces `out/`, rsync to r1, nginx serves under Cloudflare. Same TLS termination as the API. |
| **API origin** | `api.ratesengine.net` (existing, behind Cloudflare per `cdn-setup.md`) | No change |
| **Showcase domain** | `ratesengine.net` (the explorer / showcase). Customer dashboard lives at the separate `app.ratesengine.net`; see [`web/dashboard/README.md`](../../web/dashboard/README.md). | CNAME → Cloudflare Pages OR A → r1; Cloudflare proxied (orange cloud) either way. Single-vendor CDN story. |
| **Dynamic routes** | Client-side rendering (CSR) for long-tail (`/contracts/{id}`, `/tx/{hash}`, `/accounts/{G}`); pre-rendered via `generateStaticParams` for high-traffic (top-N coins, all protocols, all sources) | TanStack Query handles client-side fetch + cache; SEO targets the pre-rendered set; long-tail is JS-rendered (Google handles it) |
| **OG images** | Build-time generation via `satori` + `@resvg/resvg-js` for pre-rendered routes; small Cloudflare Worker (~50 lines) for dynamic | `@vercel/og` is a Vercel runtime; Satori is the underlying library and runs anywhere. |
| **Build env** | Node 20 LTS, pnpm 10 | Faster + smaller node_modules than npm |
| **CI** | GitHub Actions — same workflow as the Go monorepo, new job for `web/explorer/` | `pnpm install --frozen-lockfile`, `pnpm typecheck`, `pnpm lint`, `pnpm build`. Cloudflare Pages auto-deploys on push to main. |
| **Why not Vercel** | Brand fit + vendor consolidation | The "we run our own everything" pitch is the differentiator. Vercel adds a vendor; static export to our existing Cloudflare CDN doesn't. Reliability difference is invisible at our request volume. |

### 1.4 Repo layout

Monorepo. New top-level directory:

```
web/
└── showcase/
    ├── package.json
    ├── pnpm-lock.yaml
    ├── next.config.mjs
    ├── tsconfig.json
    ├── tailwind.config.ts
    ├── postcss.config.mjs
    ├── .eslintrc.json
    ├── .prettierrc
    ├── README.md
    ├── public/
    └── src/
        ├── app/                       # Next.js routes
        │   ├── layout.tsx
        │   ├── page.tsx               # /
        │   ├── globals.css
        │   ├── coins/
        │   ├── pairs/
        │   ├── markets/
        │   ├── dexes/
        │   ├── lending/
        │   ├── aggregators/
        │   ├── oracles/
        │   ├── sources/
        │   ├── contracts/
        │   ├── tx/
        │   ├── accounts/
        │   ├── issuers/
        │   ├── anchors/
        │   ├── network/
        │   ├── diagnostics/
        │   ├── anomalies/
        │   ├── divergences/
        │   ├── mev/
        │   ├── research/
        │   ├── docs/
        │   ├── account/
        │   └── search/
        ├── components/
        │   ├── ui/                    # shadcn copies
        │   ├── primitives/            # delta strip, sparkline, direction pill, …
        │   ├── panels/                # composed cards
        │   ├── reveal/                # <> mechanism
        │   ├── charts/                # Lightweight Charts wrappers
        │   ├── nav/                   # navbar, search, footer
        │   └── mdx/                   # RatesLink, RatesPanel, TxLink primitives
        ├── api/
        │   ├── client.ts              # fetch wrapper
        │   ├── types.ts               # generated from OpenAPI (committed)
        │   └── hooks.ts               # TanStack Query hooks
        ├── lib/
        │   ├── url-state.ts           # query-param helpers
        │   ├── time-pin.ts            # as_of_ledger helpers
        │   ├── format.ts              # number/date formatters
        │   └── slugs.ts               # asset-slug resolution
        └── posts/                     # MDX research articles
```

Why monorepo: shared CI, atomic commits across stack boundaries (e.g. "add `/v1/foo` endpoint + UI in one PR"), shared CHANGELOG, shared issue tracker. Cost: contributors pull more files. Acceptable.

### 1.5 Authoring conventions

- Server components by default; `'use client'` only where interactivity (charts, streams, forms) demands it.
- Every interactive selection writes to URL (`router.replace`).
- Every panel exports a `getRequestExample()` for the `<>` reveal.
- Every page component reads `searchParams` (server) or `useSearchParams` (client) for `as_of_ledger`.
- Tailwind classes only; no inline `style={}`.
- All numbers formatted via `lib/format.ts` (Intl-aware locale).

---

## 2. Phasing

13 phases. Each is shippable independently. Backend phases can run in parallel with frontend phases starting from phase 5.

```
Backend                                 Frontend
─────────────────────────────────────  ─────────────────────────────────────
Phase 0  Stack setup
Phase 1  Schema + backfills
Phase 2  Persistence wiring             ┐
Phase 3  Aggregator workers             ├─ Phase 6 Frontend scaffold (parallel)
Phase 4  New decoders                   ┘
Phase 5  API endpoints (thin)
Phase 6  API endpoints (non-trivial)    Phase 7  Coin + pair + market pages
                                        Phase 8  Section directories + detail
                                        Phase 9  Contract + WASM
                                        Phase 10 Issuer / anchor / tx / account
                                        Phase 11 Macro + diagnostics + research
                                        Phase 12 Embeds + sign-in
                                        Phase 13 Polish + perf + launch
```

Approximate effort:
- Backend: 3-5 weeks (parallelizable across 1-2 engineers).
- Frontend: 4-6 weeks for full panel set + polish.
- Total wall: ~6-9 weeks to a polished v1, less if we parallelize.

---

## 3. Phase-by-phase ticket breakdown

Each phase lists tickets with **acceptance criteria** (AC), **dependencies** (Dep), and **estimate** (S/M/L = 0.5 day / 2 days / 1 week).

### Phase 0 — Stack setup (S)

| # | Ticket | AC | Est |
|---|---|---|---|
| 0.1 | Bootstrap `web/explorer/` with Next.js 15 + TS + Tailwind + shadcn | `pnpm install && pnpm dev` runs, `/` returns 200 with "Hello, ratesengine" | S |
| 0.2 | Add `make web-dev` / `make web-build` / `make web-typecheck` / `make web-lint` targets | All targets work; `make verify` includes web checks | S |
| 0.3 | Set up `openapi-typescript` generation | `pnpm generate:api` produces `src/api/types.ts`; CI fails if regen drifts | S |
| 0.4 | Add `web/explorer/` job to CI workflow | Typecheck + lint + build all pass on green branch | S |
| 0.5 | README explaining stack, how to run, how to deploy | Renders on GitHub | S |

**Phase 0 deliverable**: an empty Next.js app at `web/explorer/` that builds + lints + has the API types generated and CI green. Foundation for everything else.

### Phase 1 — Schema + backfills (M)

All 10 migrations + their backfills. Each is its own commit/PR.

| # | Ticket | AC | Est |
|---|---|---|---|
| 1.1 | Migration 0017: `wasm_versions` + `contract_wasm_history` | Up + down; integration test populates a row | S |
| 1.2 | Backfill `wasm_versions` from r1's existing `wasm-history` JSONL | r1 query: `SELECT count(*) FROM wasm_versions` returns ≥ all known WASM hashes | M |
| 1.3 | Migration 0018: `freeze_events` | Up + down, hypertable created | S |
| 1.4 | Migration 0019: `divergence_observations` | Same | S |
| 1.5 | Migration 0020: `decoder_stats_5m` | Same | S |
| 1.6 | Migration 0021: `tvl_observations` + `mev_events` | Same | S |
| 1.7 | Migration 0022: `change_summary_5m` | Same | S |
| 1.8 | Migration 0023: `classic_assets` + `issuers` + `anchors` | Same | S |
| 1.9 | Backfill `classic_assets` from existing `trades` hypertable | r1 query confirms ≥ count of distinct (code, issuer) pairs in trades | S |
| 1.10 | Migration 0024: `classic_asset_stats_5m` | Same | S |
| 1.11 | Migration 0025: `routers` + `trades.routed_via` + `aggregator_exposures` | Same; `trades` ALTER doesn't lock | S |
| 1.12 | Seed `routers` with Soroswap router + DeFindex factory contracts | Rows present in r1 after deploy | S |
| 1.13 | Migration 0026: `price_source_contributions` + `sdex_offer_events` | Same | S |

**Phase 1 deliverable**: every new table exists on r1 and is populated where backfill was straightforward. No code reads from them yet.

### Phase 2 — Persistence wiring (M)

Convert Redis-only state to durable.

| # | Ticket | AC | Est |
|---|---|---|---|
| 2.1 | Freeze-event sink: `internal/aggregate/freeze` writes to `freeze_events` on every clear→firing / firing→clear transition | Integration test triggers a freeze, row appears | M |
| 2.2 | Divergence-observation sink: `internal/divergence/worker.go` writes per-comparison row | Test triggers a comparison, row appears | M |
| 2.3 | Decoder-stats flush: aggregator periodic worker reads `dispatcher.Stats()` snapshot, writes 5m rollup | Row count grows monotonically; no double-counting | M |
| 2.4 | Per-source contribution persister: aggregator on every closed-bucket flush writes contribution shares to `price_source_contributions` | Row per (asset, quote, bucket) with source-share JSON | M |

**Phase 2 deliverable**: 4 new sinks. Visible in postgres + queryable via psql. No new endpoints yet.

### Phase 3 — Aggregator workers (L)

| # | Ticket | AC | Est |
|---|---|---|---|
| 3.1 | TVL writer: per-tick TVL computation per protocol, writes `tvl_observations` | r1: `SELECT * FROM tvl_observations LIMIT 10` shows fresh rows | M |
| 3.2 | Change-summary rollup: every 5 min computes multi-window deltas + ATH/ATL + streak + acceleration for every entity | `change_summary_5m` row count = entity count; values match psql spot-checks | M |
| 3.3 | Peg-health computer: stablecoin deviations; cached + served by `/v1/network/peg-health` | Endpoint returns USD-pegged deviations within 1% accuracy | M |
| 3.4 | Source-diversity (Shannon entropy) computer | Endpoint returns sane entropy values | S |
| 3.5 | DeFindex exposure ticker: per-vault on-chain state queries → `aggregator_exposures` rows | Row per (vault, underlying) per tick | L |
| 3.6 | MEV detector — sandwich pattern | Detected events written to `mev_events`; false-positive rate <5% on a known-good test set | L |
| 3.7 | MEV detector — oracle deviation | Same | M |
| 3.8 | MEV detector — liquidation cascade | Same | M |

**Phase 3 deliverable**: workers running on r1 + populating their tables. Spot-check via psql.

### Phase 4 — New decoders (L)

| # | Ticket | AC | Est |
|---|---|---|---|
| 4.1 | `internal/sources/classic_registry/` observer — auto-upsert `classic_assets` + `issuers` from trades + ChangeTrust | r1: every trade source produces registry rows; coverage = 100% of trades | M |
| 4.2 | SEP-1 fetcher worker — rate-limited HTTPS fetch + parse + cache to `issuers.sep1_payload` / `anchors` | All known issuers with home_domain have a fresh `sep1_resolved_at` within a week | M |
| 4.3 | `internal/sources/router_attribution/` — `ContractCallDecoder` hook for `routers` registry, tags same-tx trades | A test tx via Soroswap router shows `routed_via='soroswap-router-v1'` in trades | M |
| 4.4 | `internal/wasm/` — `UploadContractWasm` op + `ContractCode` LedgerEntry observer | Watching live ledger flow: any new contract upload appears in `wasm_versions` within 1 ledger | M |
| 4.5 | `internal/sources/path_payments/` — decode path-payment ops + reconstruct asset routes | Test tx with multi-hop path appears with full `route` array | M |
| 4.6 | `internal/sources/sdex` extension: capture full offer lifecycle (creates, updates, deletes), not just fills | New `sdex_offer_events` table populated; net-state derivable per pair | L |
| 4.7 | `internal/sources/accounts` extension: issuer auth flag + home_domain change tracker | Auth flag changes appear in `issuers` history | M |
| 4.8 | `internal/sources/network_meta/` — fee-market + active-address counters | `network_meta_5m` rollup populated | M |

**Phase 4 deliverable**: every new decoder wired into the dispatcher; r1 ingesting all the new dimensions.

### Phase 5 — API endpoints (thin wrappers) (M)

Most endpoints from §10 of the data-inventory doc. Each is a thin handler over an existing query.

| # | Ticket group | Endpoints | Est |
|---|---|---|---|
| 5.1 | Asset endpoints (originally planned as `/v1/coins`; unified into `/v1/assets` per [coins-to-assets-migration.md](coins-to-assets-migration.md)) | `/v1/assets`, `/v1/assets/{id}`, `/v1/assets/{id}/{metadata,stats,supply/history,supply/breakdown,sep41-events,trustlines/history,holders/history,events,protocols}` | M |
| 5.2 | Pair / market endpoints | `/v1/pairs`, `/v1/pairs/{base}/{quote}/{venues,spread,liquidity-flow}`, `/v1/markets/heatmap` | M |
| 5.3 | Aggregation transparency | `/v1/price/{base}/{quote}/sources`, `/v1/price/{base}/{quote}/why` | M |
| 5.4 | Source endpoints | `/v1/sources?include=health`, `/v1/sources/{name}/{health,race,reliability,weight-history,wasm-history}` | M |
| 5.5 | Protocol endpoints (kind-aware) | `/v1/protocols?kind=...`, detail + sub-routes (router-attribution, exposure, vaults, etc.) | M |
| 5.6 | Issuer / anchor endpoints | `/v1/issuers/*`, `/v1/anchors/*` | M |
| 5.7 | Anomaly / divergence / MEV endpoints | `/v1/anomalies`, `/v1/divergences`, `/v1/mev` | M |
| 5.8 | Network / diagnostics endpoints | `/v1/network/*`, `/v1/diagnostics/*` | M |
| 5.9 | Sparkline + change-summary endpoints | `/v1/sparkline/*`, `/v1/changes/*`, `/v1/tvl*`, `/v1/volatility` | M |

Each ticket: handler + integration test + OpenAPI spec update + Go SDK extension. Order doesn't matter; pick by user priority.

**Phase 5 deliverable**: ~70 of the ~80 new endpoints live + queryable via curl.

### Phase 6 — API endpoints (non-trivial) (L)

The 10ish endpoints that need real compute or a new dependency.

| # | Ticket | Notes | Est |
|---|---|---|---|
| 6.1 | `wasm2wat` integration: cgo binding to `libwabt`; `GET /v1/contracts/{id}/wasm/{hash}/wat` returns disassembly | Cache by hash; immutable; LRU in-process | L |
| 6.2 | WAT diff endpoint: `GET /v1/contracts/{id}/wasm/{hash}/diff/{prev_hash}` returns side-by-side hunks | Pretty diff output; max 5 MB response | M |
| 6.3 | SDEX order book snapshot: `GET /v1/orderbook` from `sdex_offer_events` net state | Bids + asks ladders, 20 levels each | M |
| 6.4 | Slippage simulator: `GET /v1/slippage` merges SDEX depth + AMM reserves | Returns slippage % for a given size | M |
| 6.5 | Path-payment heatmap: `GET /v1/path-payments/heatmap` aggregates routes | Sankey-ready data | M |
| 6.6 | Universal search: `GET /v1/search` cross-type with tsvector + trgm | Sub-100ms p95 for typical query | M |
| 6.7 | Time-pin helper + every endpoint accepts `as_of_ledger` | All Phase 5 endpoints retro-fitted | M |
| 6.8 | Wildcard observations stream: `/v1/observations/stream?asset=*` | Hub-driven firehose; load-tested at 100 trades/sec | M |
| 6.9 | Last-Event-ID replay on tip + observations streams | Per-connection ring buffer | M |
| 6.10 | OG image generation: build-time (Satori → resvg PNG) for pre-rendered routes; Cloudflare Worker for the long-tail | Worker code shipped alongside the showcase repo; deploys to Cloudflare via `wrangler` | M |

**Phase 6 deliverable**: every endpoint listed in the planning doc § 10 lives. Backend complete.

### Phase 7 — Frontend scaffold (M)

Now the showcase starts taking shape. Can run parallel to phases 5-6 (endpoints can be mocked initially via TanStack Query stubs).

| # | Ticket | AC | Est |
|---|---|---|---|
| 7.1 | Top-level layout: navbar (kind-grouped), footer, search bar, theme toggle | Works on mobile + desktop; keyboard-navigable | M |
| 7.2 | Design system primitives in `components/primitives/`: `MultiWindowDelta`, `Sparkline`, `DirectionPill`, `StreakIndicator`, `AccelerationArrow`, `RankBadge` | Storybook-style page at `/dev/primitives` (gated to local-dev only) | M |
| 7.3 | `<>` reveal mechanism: every panel renders its API call inside a tray | `Cmd-/` toggles all reveals on the page | M |
| 7.4 | TanStack Query setup with global error/retry policies | All hooks use shared client | S |
| 7.5 | URL state helpers: `useUrlParam`, `useUrlAsOfLedger`, etc. | Roundtrip survives page navigation | M |
| 7.6 | Time-machine widget (top-right, every page) | "Live" badge in normal mode; "Viewing as of …" in pinned mode; off-tone styling | M |
| 7.7 | Sign-in stub: SEP-10 challenge → JWT (Freighter-only at v1) | Logged-in state shown in nav | M |

**Phase 7 deliverable**: empty pages with shared chrome, design primitives, working URL state, time-machine widget.

### Phase 8 — Coin + pair + market pages (L)

| # | Ticket | AC | Est |
|---|---|---|---|
| 8.1 | `/coins` directory: sortable + filterable table with sparklines + delta strips | All sorts work; URL state preserved on refresh | M |
| 8.2 | `/coins/{slug}` overview tab: hero card, metadata, 24h stats, source donut, confidence card | All panels render real data; `<>` reveal on each | M |
| 8.3 | `/coins/{slug}` chart tab: TradingView Lightweight Charts wrapper with timeframe + granularity + price-type selectors | All combinations work; URL state | L |
| 8.4 | `/coins/{slug}` markets tab: pairs across venues, cross-quote comparison | | M |
| 8.5 | `/coins/{slug}` history tab: since-inception chart + raw-trades pagination | | M |
| 8.6 | `/coins/{slug}` supply tab: chart + breakdown + SEP-41 events | | M |
| 8.7 | `/coins/{slug}` issuer tab (classic only): issuer card, auth-flag history, sister assets | | M |
| 8.8 | `/coins/{slug}` liquidity tab: order book, slippage simulator, spread chart | | M |
| 8.9 | `/pairs/{base}/{quote}` per-venue page: chart matrix, spread chart, liquidity migration, live tape | | L |
| 8.10 | `/markets` directory: heatmap + sortable table | | M |

**Phase 8 deliverable**: customer-facing core. Anyone can browse any asset and explore it deeply.

### Phase 9 — Section directories + detail pages (M)

Each kind has its own directory + detail page set, with kind-appropriate scoreboard columns.

| # | Ticket | AC | Est |
|---|---|---|---|
| 9.1 | `/dexes` directory: AMM + order-book scoreboard with TVL, volume, pair count, status badge | | M |
| 9.2 | `/dexes/{slug}` detail: TVL chart, contracts, pairs, WASM history, pair cadence, efficiency, yields, router attribution (Soroswap) | | L |
| 9.3 | `/lending` directory: Blend + future. Scoreboard with TVL, utilization, borrow APR, liquidations 24h, backstop coverage | | M |
| 9.4 | `/lending/{slug}` detail: per-pool TVL + utilization + APYs, auctions panel, backstop coverage, oracle dependency link | | L |
| 9.5 | `/aggregators` directory: scoreboard with AUM, vault count, top-3 underlying exposures, 7d net flow | | M |
| 9.6 | `/aggregators/{slug}` detail: vaults list + per-vault exposure + history | | L |
| 9.7 | `/oracles` directory: feed count + freshness + divergence | | M |
| 9.8 | `/oracles/{name}` detail: feeds, per-feed history, cross-pair, divergence chart | | M |

**Phase 9 deliverable**: every protocol kind navigable; researcher-grade detail.

### Phase 10 — Contract / WASM time machine (L)

| # | Ticket | AC | Est |
|---|---|---|---|
| 10.1 | `/contracts/{id}` overview + WASM version timeline | | M |
| 10.2 | WASM bytecode viewer: download + hex preview | | M |
| 10.3 | WAT viewer with syntax highlighting | Use `monaco-editor` lazy-loaded | M |
| 10.4 | WAT diff viewer (side-by-side) | Custom hunk renderer or `react-diff-view` | M |
| 10.5 | Storage transitions panel | | M |
| 10.6 | Recent events firehose | | M |
| 10.7 | Recent invocations | | M |
| 10.8 | Resource fee histogram | | S |
| 10.9 | `/sources/{name}` page (kind-aware: Soroban sources show WASM history) | | M |

**Phase 10 deliverable**: protocol/contract explorer — the "WASM time machine" surface.

### Phase 11 — Issuer / anchor / Stellar primitive views (M)

| # | Ticket | AC | Est |
|---|---|---|---|
| 11.1 | `/issuers/{G-strkey}` page | | M |
| 11.2 | `/anchors/{home_domain}` page | | M |
| 11.3 | `/tx/{hash}` page with op-by-op breakdown + path-payment route diagram | | M |
| 11.4 | `/accounts/{G-strkey}` page with activity + flow chart | | M |
| 11.5 | `/path-payments` heatmap | | M |
| 11.6 | Universal search results page `/search` | | M |

**Phase 11 deliverable**: full Stellar-primitive coverage; researchers can drill from any asset to any tx to any account.

### Phase 12 — Macro + diagnostics + research blog + embeds (M)

| # | Ticket | AC | Est |
|---|---|---|---|
| 12.1 | `/network` macro pulse | All composite indicators live | M |
| 12.2 | `/diagnostics` system health page | | M |
| 12.3 | `/anomalies` freeze timeline + calendar heatmap | | M |
| 12.4 | `/divergences` cross-reference monitor | | M |
| 12.5 | `/mev` flagged-events feed | | M |
| 12.6 | `/research` MDX blog with `RatesLink`, `RatesPanel`, `TxLink` primitives | First post published as a smoke test | L |
| 12.7 | Iframe embeds (`/embed/chart`, `/embed/coin/...`) | | M |
| 12.8 | OG image generation for every page | OG cards visible in Twitter/Slack/Discord previews | M |
| 12.9 | `/account` sign-in + keys + usage | Freighter wallet integration | L |
| 12.10 | `/docs` embedded API reference (Redocly) | | S |

**Phase 12 deliverable**: launch-shape site. All routes reachable; all primitives wired.

### Phase 13 — Polish + perf + launch (M)

| # | Ticket | AC | Est |
|---|---|---|---|
| 13.1 | Mobile pass: every page passes Lighthouse mobile ≥ 90 | | M |
| 13.2 | Bundle analysis: every route < 100 KB gzipped JS | `next-bundle-analyzer` clean | M |
| 13.3 | LCP < 1.5s on 3G simulation across all routes | | M |
| 13.4 | A11y pass: WCAG 2.1 AA across all routes | axe-core CI pass | M |
| 13.5 | Skeleton states everywhere (no layout shift) | CLS < 0.1 | M |
| 13.6 | Error boundaries on every route | Graceful fallback UI for any 500 | S |
| 13.7 | Status page integration (link to status.ratesengine.net) | | S |
| 13.8 | Robots.txt + sitemap | | S |
| 13.9 | Analytics (privacy-respecting — Plausible or simple-analytics) | | S |
| 13.10 | Security pass: CSP headers, no inline scripts, all third-party deps reviewed | | M |
| 13.11 | First post-mortem article published as launch artefact | | M |

**Phase 13 deliverable**: launch-quality site. Ready to publish.

---

## 4. Dependencies + critical path

```
Phase 0 ──┐
          ├─ Phase 1 ──┐
          │            ├─ Phase 2 ──┐
          │            │            ├─ Phase 3 ──┐
          │            └─ Phase 4 ──┤            ├─ Phase 5 ──┐
          │                         │            │            ├─ Phase 6 ──┐
          │                         │            └────────────┘            │
          └─ Phase 7 ────────────────────────────────────────────┐         │
                                                                 ├─ Phase 8 ─┐
                                                                 │           ├── 9, 10, 11
                                                                 │           │
                                                                 └───────────┴── 12
                                                                                  │
                                                                                  └── 13
```

Critical path: 0 → 1 → 4 → 5 → 7 → 8 → 13. Roughly 6 weeks if a single engineer drives.

Parallelization opportunities:
- Phase 2 + 3 + 4 can run together once Phase 1 lands (they're independent observers/workers).
- Phase 7 (frontend scaffold) starts the moment Phase 0 lands; mock data unblocks panels until real endpoints land in Phase 5.
- Phases 8-12 can run in any order once Phase 7 lands; pick by priority.

---

## 5. Risk register

| # | Risk | Mitigation |
|---|---|---|
| R1 | **wasm2wat cgo dep** introduces a new toolchain requirement (libwabt). Pure-Go binary preferred. | Investigate `wabt-go` (cgo) vs WASM-based wasm2wat (pure Go via `wazero`?). Decision before Phase 6.1. Fallback: spin out as a sidecar service. |
| R2 | **MEV false-positive rate** kills credibility | Per-pattern tuning + visible confidence score on each flagged event + allowlist for known operators. Don't promote to top-of-page until p95 false-positive rate < 5%. |
| R3 | **DeFindex vault discovery** requires ongoing maintenance | v1 ships with curated allowlist + factory-event auto-add. Auto-discovery heuristic is follow-up. |
| R4 | **SEP-1 fetch outbound HTTPS** is the only egress dependency | Per-domain rate limit + weekly stale acceptance. Background worker with retries. Cache hit ≥ 90%. |
| R5 | **Time-machine discipline** (every endpoint must accept `as_of_ledger`) | Code-review enforcement + lint rule that fails CI if a new handler doesn't import the timepin helper. |
| R6 | **SDEX offer lifecycle** is the largest single ingest extension | Spike + design note before starting Phase 4.6. Estimate may grow. |
| R7 | **Cloudflare Pages limits** (build minutes, request count) if traffic spikes | Cloudflare Pages free tier is generous (500 builds/mo, unmetered requests on Pages-served static assets). Worst case: switch to rsync → r1 nginx → Cloudflare proxied — zero code change, same `out/` artefact. |
| R8 | **Schema drift** between `web/explorer/src/api/types.ts` and OpenAPI spec | CI check: regen and `git diff --exit-code`. |
| R9 | **Frontend bundle creep** as we add panels | `next-bundle-analyzer` in CI; per-route budget (100 KB) enforced. |
| R10 | **Time-machine UX confusion** ("am I looking at live or historical?") | Off-tone background + persistent "as of" badge + disable live-tape panels in pinned mode. |

---

## 6. Open questions resolved

From the data-inventory doc §20, here's where each lands:

1. **Hosting:** Cloudflare Pages (static export) for v1 — same vendor as our API CDN; rsync-to-r1 as fallback (1.3 above).
2. **Wallet UX:** Freighter only at v1; Albedo + Lobstr in Phase 12.9 follow-up.
3. **Repo layout:** monorepo (1.4 above).
4. **MDX content repo:** in-tree at `web/explorer/src/posts/` (1.4 above).
5. **Brand:** punted to a designer pass during Phase 7. Default Tailwind palette + a single accent colour at scaffold time.
6. **Embeds:** allow arbitrary domains at v1 (Phase 12.7); whitelist if abused.
7. **Slug ownership:** volume-weighted dominant (already in §9.7 of data-inventory doc).
8. **MEV detection thresholds:** algorithmic with visible confidence score; tuned per pattern.
9. **OG generator:** Satori + resvg at build time for pre-rendered routes; tiny Cloudflare Worker for the long-tail (1.1 above).
10. **`as_of_ledger` UX:** off-tone styling + disable live panels.

---

## 7. Cross-references

- Data inventory (the WHAT): [`explorer-data-inventory.md`](explorer-data-inventory.md)
- API source-of-truth: [`../../openapi/rates-engine.v1.yaml`](../../openapi/rates-engine.v1.yaml)
- Coverage matrix (RFP × delivery): [`coverage-matrix.md`](coverage-matrix.md)
- Aggregation policy: [`aggregation-plan.md`](aggregation-plan.md)
- ADRs: [`../adr/`](../adr/)
- CDN setup: [`../operations/cdn-setup.md`](../operations/cdn-setup.md)
