# Live Site QA Review - 2026-05-13

## Scope

This review treats Rates Engine as:

1. A public market-intelligence explorer in the CoinGecko /
   CoinMarketCap category.
2. A deeper Stellar-specific product for developers, analysts, and
   advanced consumers.
3. A public proof surface for the API, diagnostics, reliability, and
   provenance claims made in the repo.

The review covered the repo intent, the live public site, live API
spot checks, status/docs surfaces, desktop and mobile browser behavior,
and focused user journeys.

## QA plan executed

1. Read the product and repo intent from `README.md`, `CLAUDE.md`,
   `web/explorer/README.md`, and
   `docs/architecture/explorer-data-inventory.md`.
2. Mapped the explorer route set and the API endpoints each route
   depends on.
3. Probed the live production surfaces on 2026-05-13:
   - explorer pages on `ratesengine.net`
   - public status page
   - public docs site
   - selected API endpoints for status, network stats, assets,
     markets, pools, sources, oracle streams, diagnostics, and USDC
4. Ran headless Chromium checks against desktop and mobile viewports
   for:
   - `/`
   - `/assets/`
   - `/markets/`
   - `/networks/`
   - `/sources/`
   - `/dexes/`
   - `/oracles/`
   - `/diagnostics/`
   - `/pricing/`
   - `/signup/`
   - `/assets/usdc/`
5. Ran focused interaction checks:
   - desktop search for `USDC`
   - mobile nav drawer
   - BTC market filtering
   - rendered USDC network drilldown hrefs
6. Ran local TypeScript checks:
   - `pnpm --dir web/explorer typecheck`
   - `pnpm --dir web/status typecheck`
   - `pnpm --dir web/dashboard typecheck`

All three typecheck commands passed.

## Product assessment

### What is already strong

- The product direction is coherent. The explorer is not a generic
  crypto homepage; it is a Stellar-first market data product with
  stronger provenance, diagnostics, source visibility, and operator
  transparency than a normal price site.
- The `<>` request reveal is the right differentiator. It turns the
  UI into live API documentation and gives developers immediate trust
  in what each panel means.
- The asset, market, source, oracle, diagnostics, status, and docs
  surfaces collectively tell a much richer story than "token chart +
  market cap."
- The verified-currency catalogue and the global asset view are a
  good bridge between mainstream consumer expectations and Stellar's
  issuer / asset-ID complexity.
- Search, mobile navigation, docs, public status, and the market
  filter interaction all worked in the focused browser pass.

### Where the product currently misses the mark

- The explorer homepage says "every currency, asset, and market,"
  while the actual depth is still asymmetric:
  Stellar gets native deep dive; other networks currently appear
  mostly as identity / external-reference context. That is a valid
  product strategy, but the homepage copy reads broader than the
  implementation.
- During a live degraded state, the explorer itself gives users very
  little explicit context. The public status page is good, but the
  primary explorer experience mostly reduces degradation to a small
  status indicator in navigation. A blockchain consumer or developer
  looking at stale / frozen / partial data needs clearer in-context
  explanation.
- Some live copy still reads like implementation staging text rather
  than product text. Examples:
  - `/markets/` says the heatmap, sub-tables, and live tape land in
    later passes.
  - `/diagnostics/` says several panels will arrive "as endpoints
    ship."
  This is honest, but it also broadcasts "unfinished product" on the
  public site. If the site is live, the copy should distinguish
  "coming later" product roadmap from "this page is incomplete."

## Findings

### F-01 - Production is live-degraded right now

Severity: High

The production status API reported:

- `overall = degraded`
- 8 active incidents
- 1 page-level incident:
  `ratesengine_anomaly_freeze_sustained`

The public status page rendered "Degraded performance" and an active
incident section in the browser pass.

Why this matters:

- The product's main promise is trusted pricing and diagnostics.
  A sustained anomaly freeze is exactly the kind of state a consumer
  or developer needs to understand immediately.
- The status page is doing its job, but the primary explorer surfaces
  do not make the degraded state prominent enough.

Recommended action:

1. Add an explicit degraded-state banner or inline callout on the
   explorer when `/v1/status.overall !== "ok"`.
2. Link directly to the status page and, when available, summarize the
   highest-severity active incident.
3. Treat anomaly-freeze states as a product-visible trust signal, not
   only an ops signal.

### F-02 - DEX pools are timing out, and the UI handles failure poorly

Severity: High

The live request:

```text
GET /v1/pools?limit=5&order_by=volume_24h_usd_desc
```

returned:

```text
503 Pools query timed out
```

The error detail says the underlying trades-hypertable scan did not
return within 8 seconds.

`/dexes/` depends directly on `/v1/pools`. In the browser pass it
remained visibly loading or drifted toward an empty-state-like user
experience. In source, `DexesView` has no `q.isError` rendering path;
after an API failure, it can present "No pools matched." as if the
request succeeded and returned zero rows.

Relevant code:

- `web/explorer/src/app/dexes/DexesView.tsx:72`
- `web/explorer/src/app/dexes/DexesView.tsx:199`
- `web/explorer/src/app/dexes/DexesView.tsx:206`

Why this matters:

- DEX depth is one of the product's main Stellar-specific promises.
- A 503 is acceptable during operational trouble; presenting it as an
  empty result is not.

Recommended action:

1. Fix the backend query path or add a precomputed / cached pools
   listing suited for the public explorer.
2. Add explicit DEX pools error UI with retry and the returned problem
   detail.
3. Add browser coverage asserting that 5xx responses do not become
   "No pools matched."

### F-03 - Explorer session detection is structurally CORS-incompatible

Severity: High

Every sampled explorer page emitted browser console failures for:

```text
GET https://api.ratesengine.net/v1/account/me
```

The browser fetch is credentialed:

- `web/explorer/src/api/hooks.ts:160`
- `web/explorer/src/api/hooks.ts:165`

But the API CORS middleware explicitly states it does not set
`Access-Control-Allow-Credentials: true`:

- `internal/api/v1/middleware/cors.go:14`
- `internal/api/v1/middleware/cors.go:43`

The live response to an origin-scoped `/v1/account/me` probe included:

- `Access-Control-Allow-Origin: https://ratesengine.net`
- no `Access-Control-Allow-Credentials`

That exactly matches the browser error.

Why this matters:

- Signed-out visitors get repeated console noise.
- More importantly, cross-origin cookie session detection cannot work
  as implemented. The explorer's `useMe()` hook and the API's CORS
  contract disagree at the protocol level.

Recommended action:

Choose one architecture and make it consistent:

1. If cookie auth on `ratesengine.net` is intended, support
   credentialed CORS for the specific explorer origins and update
   tests/config accordingly.
2. If credentialed cross-origin CORS is intentionally forbidden,
   stop using `credentials: "include"` from the explorer and redesign
   the account/session flow around a same-origin app shell or token
   handoff.

### F-04 - Verified-asset Stellar drilldown leaks API paths into site routing

Severity: Medium

The live USDC global asset response includes:

```text
deep_link = /v1/assets/USDC-GA5Z...
```

On `/assets/usdc/`, the frontend renders that value directly through
`next/link`:

- `web/explorer/src/app/assets/[slug]/NetworksPanel.tsx:123`
- `web/explorer/src/app/assets/[slug]/NetworksPanel.tsx:141`

The focused browser check found:

```text
/v1/assets/USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN/
```

as a rendered same-origin href. Next.js then attempts to prefetch an
RSC/static route for that API-looking path, producing preflight and
fetch noise in the browser console.

Why this matters:

- The asset page surfaces as "works, but noisy and brittle."
- The rendered link target is an API path, not an explorer route, so
  the interaction model is muddled even before prefetch failures.

Recommended action:

1. Do not feed API `deep_link` values directly into `next/link`.
2. For explorer navigation, build the explorer route explicitly:
   `/assets/{slug}/{network}` or the intended per-network page.
3. If the API deep link is meant to be user-visible, render it as an
   external API link, not as an internal site route.

## Lower-priority product gaps

These are not "broken" in the same way as the findings above, but they
matter for a consumer/developer-facing market product.

1. The degraded-state explanation should live in the main product, not
   only on the separate status domain.
2. Asset detail pages would benefit from a more explicit "why trust
   this price?" summary: source mix, stale/freeze/divergence state,
   and whether the displayed price is native, triangulated, or
   authority-referenced.
3. Consumers will expect comparison and watchlist workflows sooner
   than later. The current site is good for investigation; it is less
   sticky for repeat consumer use.
4. "Global asset" vs "Stellar issuer asset" is powerful but cognitively
   dense. The current cross-chain view is useful, but it needs very
   crisp wording so users understand when they are seeing a catalogue
   identity versus Stellar-specific market data.

## Evidence captured during execution

Live browser route checks:

- Explorer desktop/mobile route rendering on 11 routes.
- Public status page rendered degraded state and incidents.
- Public docs page rendered normally.
- Search for `USDC` worked and exposed a verified cue.
- Mobile navigation drawer exposed expected primary actions.
- BTC filtering on `/markets/` worked.
- USDC page rendered one broken API-looking same-origin drilldown href.

Live API probes:

- `/v1/status`
- `/v1/network/stats`
- `/v1/assets?limit=5&include=sparkline`
- `/v1/markets?limit=5&include=sparkline`
- `/v1/diagnostics/cursors`
- `/v1/assets/usdc`
- `/v1/pools?limit=5&order_by=volume_24h_usd_desc`
- `/v1/sources?include=stats,sparkline`
- `/v1/oracle/streams`

## Recommended next pass

1. Treat F-02, F-03, and F-04 as immediate product-quality fixes.
2. Decide whether the explorer should surface degraded operational
   status as a banner, inline callout, or both.
3. After fixes, rerun the same browser and API pass and add automated
   coverage for:
   - credentialed session detection
   - DEX pools error handling
   - network-panel link targets
   - visible degraded-state messaging
