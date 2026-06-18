---
title: Showcase site — data, IA, and API plan
last_verified: 2026-05-04
status: planning
---

# Showcase site — data, IA, and API plan

**Codename:** none yet — referred to as "the showcase" in this doc.
**Domain:** `stellarindex.io` for the site (live on Cloudflare
Pages). `docs.stellarindex.io` serves the auto-generated API
reference. The user dashboard is in-site at `stellarindex.io/account`
(the standalone `app.stellarindex.io` SPA was retired 2026-06-17 —
see [`docs/operations/cf-pages-setup.md`](../operations/cf-pages-setup.md)).

This doc is the **single planning artefact** for the user-facing
explorer. It captures: who it's for, the design principles, the URL
scheme, every page, every panel, every API call (existing or new),
every schema addition, the build order, and the performance budget.

It supersedes the v0 sketch from earlier in this file's history.

> **Status:** planning. Nothing here is binding until split into
> implementation tickets. The doc itself becomes the contract for
> those tickets.

---

## 1. Mission

Two goals, weighted equally:

1. **Showcase what the API can do.** Every visible piece of data is
   backed by a single, copy-pasteable public API call. The site
   doubles as the most thorough living tutorial for the API — every
   panel exposes its underlying request via a `<>` reveal.
2. **Be genuinely useful.** Stellar users tracking holdings,
   researchers investigating incidents, internal team debugging,
   and curious public alike should each find the site answers
   their real questions — not a demo facade.

If a feature only serves goal 1 (showy) or only serves goal 2
(useful but doesn't expose API capability), it's a v2 candidate.
v1 ships features that hit both.

---

## 2. Audiences

| Audience | Primary needs | Where they live on the site |
|---|---|---|
| **Stellar users** (token holders, traders) | "What's my asset doing? Is it healthy? Where's the best price?" | `/coins/*`, `/pairs/*`, `/markets`, `/anchors/*`, watchlist |
| **Researchers / journalists** | "What happened during this incident? Show me the data, let me cite it." | `/research/*` (blog), deep-link URLs into every view, `/tx/*`, `/contracts/*` |
| **Builders** (integrators, oracle consumers) | "What does the API return? How do I call it? Is it reliable?" | `<>` reveals everywhere, `/docs`, `/sources/*`, `/diagnostics` |
| **Internal team + auditors** | "Is the system healthy? Did we miss anything?" | `/diagnostics`, `/anomalies`, `/divergences`, `/sources/*` |
| **General public** | "Show me the cool stuff." | `/`, `/coins/*`, top-movers widgets, live trade tape |

Same site, no mode toggle. Information density rises gracefully —
every page shows the answer above the fold, with progressive
disclosure into the deep data underneath.

---

## 3. Design principles

These are the rules every design decision must answer to.

1. **Panels = API queries, 1:1.** No panel transforms or joins data
   beyond what the underlying endpoint returns. If the UI wants
   something the API can't shape, it becomes a gap-list entry — not
   a client-side join. This keeps the `<>` reveal honest and forces
   API completeness.
2. **`<>` reveals the request.** Every panel has a button (top-right
   of each card) that shows the exact `curl` (or `wscat` for SSE)
   that produced the data, with copy-to-clipboard. Doubles as live
   API documentation.
3. **URL state is config.** Every selection — `as_of_ledger`,
   selected sources, chart range, overlays, compared assets, sort
   order, filters — lives in the URL query string. Reload-safe,
   share-safe, deep-linkable. Required for the blog-post-with-links
   pattern (§14) and the time machine (§13).
4. **Anonymous-readable.** Free-tier rate limit covers the showcase.
   Authenticated views (account, keys, usage) live behind sign-in
   on `/account`. Everything else is open.
5. **No mode toggle.** Internal-vs-external split is a UX trap.
   Same site for everyone; deep data is one click below the
   surface, not behind a flag.
6. **Mobile-first.** Single-column collapse, touch-friendly chart
   controls, swipe-up `<>` sheet on mobile.
7. **Performance is a feature.** LCP < 1.5s on 3G; JS bundle
   < 100 KB gzipped per route; static-render where possible (§24).
8. **Server-shaped, not client-joined.** If a panel needs three
   things, we add an endpoint that returns the three things shaped
   for the panel. One round trip, one cache key.
9. **Sortable by default.** Every list is sortable on every column,
   reflected in the URL.
10. **Open Graph cards everywhere.** Every page renders a rich OG
    image showing its current state for Twitter/Slack/Discord
    previews.

---

## 4. URL scheme

CoinGecko-inspired, base-asset-centric. **Quote is a sub-route**;
when omitted, defaults to `usd`.

```
/                              Landing
/coins                         Asset directory (paginated, sortable, filterable)
/coins/stellar                 XLM, default quote (USD)
/coins/stellar/eur             XLM/EUR
/coins/stellar/brl             XLM/BRL
/coins/aqua                    AQUA Soroban token, default quote
/coins/aqua/usd                ↑ explicit
/coins/usdc                    USDC (resolves to dominant issuer)
/coins/usdc-circle             USDC (Circle issuer specifically — disambiguation)
/coins/CADBC...                Soroban contract by C-strkey (no slug yet)

/pairs                         All pairs (sortable grid: base × quote heatmap)
/pairs/stellar/usdc            Per-venue breakdown for XLM/USDC (Soroswap vs Phoenix vs SDEX)

/markets                       Same as /pairs but the canonical "explore the market" entry
/markets/sdex                  All SDEX-only pairs
/markets/soroswap              Soroswap-only

/sources                       Source directory (sortable: class, reliability, weight)
/sources/{name}                Source detail (binance, kraken, sdex, soroswap, …)

/protocols                     Protocol directory (Soroswap, Phoenix, Aquarius, Reflector, …)
/protocols/{slug}              Protocol detail (TVL, pairs, WASM history, contracts)

/contracts/{C-strkey}          Contract detail (WASM versions, decoder claim, events)

/oracles                       Oracle directory (Reflector trio, Redstone, Band)
/oracles/{name}                Oracle detail (feeds, freshness, divergence vs our VWAP)

/issuers/{G-strkey}            Issuer detail (assets, auth flags, anchor SEP-1)
/anchors/{home_domain}         Anchor detail (issuers grouped by domain)

/tx/{tx_hash}                  One transaction's effects: trades, events, state changes
/accounts/{G-strkey}           Account activity: trades, contracts invoked, asset flow
/path-payments                 Network-wide path-payment heatmap

/anomalies                     Freeze + anomaly timeline
/divergences                   Cross-reference (Chainlink/CoinGecko/oracle vs our VWAP)
/mev                           Suspicious-pattern detector (sandwiches, oracle deviations, etc.)

/network                       Stellar macro pulse (TVL aggregate, contract deploys, fee market)
/diagnostics                   System health (decoder coverage, archive completeness, cursors)

/research                      Articles + post-mortems (MDX rendered from posts/*.md)
/research/{slug}               One post

/docs                          Embedded API reference (auto-generated from OpenAPI)
/account                       Authenticated: keys, usage, settings (sign-in gated)

/search                        Universal search results page (Etherscan-style)
                               Accepts: asset slug, ticker, contract id, tx hash, G-account,
                               anchor domain, Soroban function name. Top-of-every-page bar.
```

### URL state parameters (used across many routes)

| Param | Type | Notes |
|---|---|---|
| `as_of_ledger` | `uint32` | Time machine pin; every endpoint accepts. Default = live. |
| `as_of` | `ISO 8601` | Resolved to ledger by `/v1/ledgers/at?ts=…`. |
| `from` / `to` | `ISO 8601` | Range bounds for charts. |
| `granularity` | `1m\|15m\|1h\|4h\|1d\|1w\|1mo` | CAGG bucket. |
| `timeframe` | `1h\|24h\|7d\|30d\|1y\|all` | Rolling-window shorthand. |
| `sources` | csv | Restrict to subset (`?sources=binance,sdex`). |
| `compare` | csv | Multi-asset overlay (`?compare=stellar,aqua,blnd`). |
| `sort` | `<col>:asc\|desc` | Tables. |
| `q` | string | Search query. |
| `quote` | slug | Override default quote where applicable. |
| `tab` | string | Active tab on detail pages. |
| `panel` | string | Anchored sub-view (`#confidence-card`). |

---

## 5. Site map (visual)

```
ROOT
├── /                              Landing — pulse + top movers + tape + new listings
├── /coins                         Asset directory
│   └── /coins/{slug}              Asset detail (default USD quote)
│       └── /coins/{slug}/{quote}  Same page, different quote
├── /pairs                         All pairs (heatmap grid)
│   └── /pairs/{base}/{quote}      Per-venue breakdown
├── /markets                       Markets directory (synonym of /pairs)
│   └── /markets/{venue}           Per-venue market list (sdex, soroswap, phoenix, …)
├── /sources                       Source directory
│   └── /sources/{name}            Source detail
├── /protocols                     Protocol directory
│   └── /protocols/{slug}          Protocol detail (TVL, pairs, WASM history)
├── /contracts/{C-strkey}          Contract detail
├── /oracles                       Oracle directory
│   └── /oracles/{name}            Oracle detail
├── /issuers/{G-strkey}            Issuer detail
├── /anchors/{home_domain}         Anchor (issuers grouped by SEP-1 home_domain)
├── /tx/{tx_hash}                  Single tx effects
├── /accounts/{G-strkey}           Account activity
├── /path-payments                 Network-wide path-payment heatmap
├── /anomalies                     Freeze + anomaly timeline
├── /divergences                   Cross-reference monitor
├── /mev                           Suspicious-pattern detector
├── /network                       Macro pulse
├── /diagnostics                   System health
├── /research                      Articles index
│   └── /research/{slug}           One article
├── /docs                          OpenAPI reference (Redocly)
├── /account                       Authenticated: keys + usage
└── /search                        Universal search
```

---

## 6. Cross-cutting view primitives

These compose throughout the site. Specify them once, use everywhere.

### 6.1 Multi-window delta strip

Wherever a number appears, it's accompanied by colour-coded multi-window deltas:

```
$0.1234   1h: +0.5%  ·  24h: +3.2%  ·  7d: −1.1%  ·  30d: +18.4%
```

Powered by `GET /v1/changes/{entity_type}/{id}` (§9.6).

### 6.2 Sparkline

Inline 7-day mini-chart next to every entity in every list. Server-rendered SVG, ~1 KB each. Cached per-entity for 5 min. Endpoint: `GET /v1/sparkline/{entity_type}/{id}?window=7d`.

### 6.3 Direction pill

Arrow + % chip; colour graduated:

```
↗ +1-5%      light green
↗↗ +5-20%    green
↗↗↗ >+20%    bright green
↘ same scale, red
→ <±0.5%     grey
```

### 6.4 Streak indicator

```
↗ 14 days up     green
ATH 2h ago       gold
ATL 3 days ago   red
new pair         purple (last 24h)
```

### 6.5 Rank-change badge

```
▲ 2     moved up 2 spots since yesterday
▼ 1     moved down 1
—       unchanged
NEW     entered the leaderboard
```

### 6.6 Acceleration arrow

Used sparingly on hero metrics. Combines first + second derivative:

```
↗↗   accelerating up      (+ +)
↗→   steady up            (+ flat)
↗↘   decelerating up      (+ −)
↘↘   accelerating down    (− −)
↘→   steady down          (− flat)
↘↗   recovering           (− +)
```

### 6.7 Source contribution donut

For any aggregated price: shows the % weight of each contributing source. Hover a wedge → see that source's last trade ledger and current price contribution. Endpoint: `GET /v1/price/{base}/{quote}/sources`.

### 6.8 Confidence decomposition card

Hero number with factor breakdown:

```
Confidence: 87/100
  ✓ 6 sources      (target: ≥3)
  ✓ σ = 0.3%       (target: <2%)
  ✓ Divergence 0.4% vs Chainlink   (target: <2%)
  ✓ Baseline fresh: 4 min ago
  ✓ Depth: $2.4M
```

Already returned by `/v1/price` as `confidence_factors`.

### 6.9 TVL chart with annotations

Every protocol/asset chart can overlay event markers (WASM upgrades, anomaly events, large flows). Markers are clickable → link to the underlying tx or contract.

### 6.10 The `<>` reveal

Top-right of every card. Opens a tray showing:

```
GET https://api.stellarindex.io/v1/price/native/fiat:USD?as_of_ledger=62405155
Authorization: not required (anonymous tier)

Response (200):
{ "data": { ... }, "as_of": "...", "sources": [...] }
```

With "copy as cURL", "copy URL", and "open in API explorer" buttons.

---

## 7. Page inventory

Every page lists its panels. Each panel has: name, what it shows, the API call (existing or new), and status (`✅ exposed today` / `⚠️ partial` / `❌ new`).

### §7.1 Landing (`/`)

The above-the-fold sells the system. Below: macro context.

| Row | Panel | Shows | API call | Status |
|---|---|---|---|---|
| 1 | **Pulse banner** | "X assets · Y trades/sec · Z sources live · ledger N · lag T" | `GET /v1/diagnostics/pulse` | ❌ new |
| 1 | **Live trade tape** | Streaming list of every trade (source, pair, price, size) | `GET /v1/observations/stream?asset=*` | ❌ new (wildcard) |
| 2 | **Total Stellar TVL** | Aggregate with sparkline + multi-window deltas | `GET /v1/tvl?range=30d` | ❌ new |
| 2 | **Network volume 24h** | Total cross-protocol volume + delta | `GET /v1/network/volume?window=24h` | ❌ new |
| 2 | **Active sources** | Live count + freshest-source widget | `GET /v1/sources?live=true` | ⚠️ partial |
| 3 | **Top movers (24h gainers)** | Top 5 assets + delta + sparkline | `GET /v1/coins?sort=delta_24h:desc&limit=5` | ❌ new (sort + delta cols) |
| 3 | **Top movers (24h losers)** | Symmetric to gainers | Same endpoint, ascending | ❌ new |
| 3 | **Top volume 24h** | Top 5 pairs by USD volume | `GET /v1/pairs?sort=volume_24h_usd:desc&limit=5` | ❌ new (sort) |
| 4 | **Top protocols by TVL** | Leaderboard with rank changes | `GET /v1/protocols?sort=tvl:desc` | ❌ new |
| 4 | **TVL flow Sankey (30d)** | Money moving between protocols | `GET /v1/tvl/flow?from=…&to=…` | ❌ new |
| 5 | **Live anomaly banner** | "Frozen now: 2 pairs (PHOENIX/USDC, AQUA/USD)" | `GET /v1/anomalies?status=firing` | ❌ new |
| 5 | **Recent freeze events** | Last N freeze decisions, one-line each | `GET /v1/anomalies?since=24h&limit=10` | ❌ new |
| 6 | **New listings feed** | SEP-41 contracts discovered last 24h | `GET /v1/discovered?since=24h&limit=20` | ❌ new |
| 6 | **Recent WASM upgrades** | Contract upgrades last 7d | `GET /v1/contracts/wasm-upgrades?since=7d` | ❌ new |
| 7 | **Stellar weather strip** | Composite health indicators | `GET /v1/network/health` | ❌ new |

### §7.2 Coin directory (`/coins`)

CoinGecko-style table with full sort + filter.

| Panel | Shows | API call |
|---|---|---|
| **Asset table** | rank, slug, name, ticker, price, Δ1h, Δ24h, Δ7d, Δ30d, market cap, volume 24h, supply, sparkline 7d | `GET /v1/coins?sort=…&filter=…&cursor=…&limit=100` |
| **Filter rail** | type (native/classic/soroban/fiat), source class, anchor verified, has-volume-24h, has-soroswap-pair | Query params on the same endpoint |
| **Search bar** | Type-ahead by ticker, code, contract id, anchor domain | `GET /v1/search?q=…&type=coin` |
| **View toggle** | Table / Grid / Tree-map by market cap | Frontend-only |

The `/v1/coins` endpoint is the **registry-aware superset** of `/v1/assets` — it's `/v1/assets` joined with `classic_assets`, `discovered_assets`, and the `change_summary_5m` rollup so the table renders in one round-trip.

### §7.3 Coin detail (`/coins/{slug}` or `/coins/{slug}/{quote}`)

Multi-tab. Tabs are first-class URL state (`?tab=chart`).

#### Overview tab

| Panel | Shows | API call | Status |
|---|---|---|---|
| **Hero price card** | Current price, multi-window delta strip, sparkline 24h, frozen/stale/divergence badges | `GET /v1/price?asset={slug}&quote={quote}` + `GET /v1/changes/coin/{slug}` | ⚠️ |
| **Asset metadata** | Name, description, anchor org/domain, image, supply (circ/total/max), market cap, age | `GET /v1/coins/{slug}/metadata` | ⚠️ |
| **24h stats card** | Volume, trades, high/low, source count, dominant pair | `GET /v1/coins/{slug}/stats?window=24h` | ❌ new |
| **Source contribution donut** | Per-source % weight in current VWAP | `GET /v1/price/{slug}/{quote}/sources` | ❌ new |
| **Confidence card** | Score + factor decomposition | (already in `/v1/price`) | ✅ |
| **Watchlist toggle** | Add/remove from local watchlist | Frontend-only |

#### Chart tab

| Panel | Shows | API call | Status |
|---|---|---|---|
| **TradingView chart** | OHLC, granularity (1m/15m/1h/4h/1d/1w/1mo), timeframe (1h/24h/7d/30d/1y/all), price-type (vwap/twap), volume bars | `GET /v1/chart?asset={slug}&quote={quote}&timeframe=…&granularity=…&price_type=…` | ⚠️ TWAP deferred |
| **Per-source overlay toggle** | Layer trades from one source | `GET /v1/history?...&sources={name}&from=…&to=…` | ❌ source filter on history |
| **Volatility band overlay** | 1h/4h/24h volatility envelope | `GET /v1/volatility?...` | ❌ new |
| **Annotation markers** | WASM upgrades, anomaly events, big flows | `GET /v1/coins/{slug}/events?from=…&to=…` | ❌ new |
| **Multi-asset overlay** | Compare against other assets normalized | `?compare=stellar,blnd,aqua` (URL state) | ❌ requires compose |

#### Markets tab

| Panel | Shows | API call | Status |
|---|---|---|---|
| **All pairs for this asset** | pair, source, last price, 24h volume, last trade, depth | `GET /v1/pairs?base={slug}` | ⚠️ filter missing |
| **Cross-quote comparison** | This asset vs USD, EUR, BRL, BTC side-by-side | Multi-call fan-out | ✅ |
| **Cross-protocol comparison** | This asset on Soroswap vs Phoenix vs SDEX | `GET /v1/coins/{slug}/protocols` | ❌ new |

#### History tab

| Panel | Shows | API call | Status |
|---|---|---|---|
| **Since-inception chart** | All-time price | `GET /v1/history/since-inception?asset={slug}&quote={quote}&granularity=1d` | ✅ |
| **Trade list** | Paginated raw trades | `GET /v1/history?...` | ✅ |
| **Trustline growth** (classic) | Adds/removes per day | `GET /v1/coins/{slug}/trustlines/history` | ❌ new |
| **Holder count over time** | Net unique holders | `GET /v1/coins/{slug}/holders/history` | ❌ new |

#### Supply tab

| Panel | Shows | API call | Status |
|---|---|---|---|
| **Supply chart** | Circulating / total / max over time | `GET /v1/coins/{slug}/supply/history?granularity=1d&from=…&to=…` | ❌ new |
| **Supply breakdown** | Algorithm components (e.g. SDF reserves, locked, claimable, LP) | `GET /v1/coins/{slug}/supply/breakdown` | ❌ new |
| **Issuance events** (Soroban) | Mint/burn/clawback timeline | `GET /v1/coins/{slug}/sep41-events?from=…&to=…&kind=…` | ❌ new |

#### Issuer tab (classic only)

| Panel | Shows | API call | Status |
|---|---|---|---|
| **Issuer card** | G-strkey, home_domain, SEP-1 metadata, age, auth flags | `GET /v1/issuers/{G-strkey}` | ❌ new |
| **Anchor link** | Link to `/anchors/{home_domain}` | derived | — |
| **Other assets by this issuer** | List | `GET /v1/issuers/{G-strkey}/assets` | ❌ new |
| **Auth flag history** | Changes to `auth_required`, `auth_revocable`, etc. over time | `GET /v1/issuers/{G-strkey}/auth-history` | ❌ new |

#### Liquidity tab

| Panel | Shows | API call | Status |
|---|---|---|---|
| **SDEX order book** | Current bids/asks ladder | `GET /v1/orderbook?base={slug}&quote={quote}&depth=20` | ❌ new |
| **Slippage simulator** | "Selling X right now would cost N% slippage" | `GET /v1/slippage?base=…&quote=…&size=…` | ❌ new |
| **Bid-ask spread chart** | Spread over time | `GET /v1/spread?base=…&quote=…&from=…&to=…&granularity=…` | ❌ new |

### §7.4 Pair detail (`/pairs/{base}/{quote}`)

Per-venue deep-dive — the "researcher" page where the **aggregated** view in §7.3 isn't enough.

| Panel | Shows | API call | Status |
|---|---|---|---|
| **Per-venue chart matrix** | Same pair on every venue, stacked | `GET /v1/pairs/{base}/{quote}/venues` | ❌ new |
| **Venue spread chart** | Spread between venues over time (arbitrage signal) | `GET /v1/pairs/{base}/{quote}/spread?from=…&to=…` | ❌ new |
| **Per-venue order flow** | Which venue gets which trades | Derived from `/v1/history?...&sources=...` | ⚠️ |
| **Liquidity migration view** | Liquidity moving between venues over time | `GET /v1/pairs/{base}/{quote}/liquidity-flow?from=…&to=…` | ❌ new |
| **Live tape** | Streaming trades for this pair only | `GET /v1/observations/stream?asset={base}&quote={quote}` | ✅ |
| **VWAP / TWAP / outlier-filtered** | Computed on user-selected window | `GET /v1/vwap`, `/v1/twap` | ✅ |

### §7.5 Markets directory (`/markets` and `/markets/{venue}`)

| Panel | Shows | API call |
|---|---|---|
| **Markets table** | pair, last price, 24h vol, 24h trades, source count | `GET /v1/markets?sort=…&cursor=…` |
| **Heatmap grid** | base × quote, cell = 24h % change, colour-coded | `GET /v1/markets/heatmap?from=…&to=…` |
| **Per-venue table** (`/markets/{venue}`) | Same shape, filtered to venue | `GET /v1/markets?venue={name}` |

### §7.6 Source directory (`/sources`)

| Panel | Shows | API call |
|---|---|---|
| **Source table** | name, class, subclass, weight, vwap-included, paid, backfill-safe, **24h trades observed**, **last decoded ledger**, **decode error rate**, **mean lag**, **uptime 30d** | `GET /v1/sources?include=health` |
| **Class filter** | exchange / aggregator / oracle / authority_sanity | Query param |

### §7.7 Source detail (`/sources/{name}`)

| Panel | Shows | API call | Status |
|---|---|---|---|
| **Source metadata card** | Class, weight, vwap inclusion, BackfillSafe, paid, contracts (for Soroban sources) | `GET /v1/sources/{name}` | ⚠️ |
| **Decoder health** | Decode errors 24h, orphans 24h, unmatched-hits 24h, last decoded ledger | `GET /v1/sources/{name}/health` | ❌ new |
| **Recent contributions** | Last 100 trades from this source | `GET /v1/observations?source={name}` | ⚠️ source-only filter |
| **Source race chart** | When this source publishes vs others (latency profile) | `GET /v1/sources/{name}/race?pair=…&from=…&to=…` | ❌ new |
| **Reliability scoreboard** | Rolling 30d uptime, mean lag, error rate | `GET /v1/sources/{name}/reliability?window=30d` | ❌ new |
| **VWAP weight history** | This source's % share of contributing pairs over time | `GET /v1/sources/{name}/weight-history?pair=…&from=…&to=…` | ❌ new |
| **WASM history** (Soroban only) | Every WASM hash this contract has run + first/last ledger | `GET /v1/sources/{name}/wasm-history` | ❌ new |

### §7.8 Protocol directory (`/protocols`)

The **scoreboard** is the centerpiece.

| Panel | Shows | API call |
|---|---|---|
| **Protocol scoreboard** | name, kind, **TVL with multi-window deltas + sparkline**, volume 24h, pair count, status badge (Surging/Stable/Cooling), rank change | `GET /v1/protocols?sort=tvl:desc&include=changes` |
| **TVL leaderboard chart** | Stacked area: TVL share by protocol over time | `GET /v1/protocols/tvl-share?from=…&to=…&granularity=…` |
| **Acceleration leaderboard** | Protocols whose growth is speeding up vs last week | `GET /v1/protocols?sort=acceleration:desc` |

Status-badge rules (transparent, hover to see):
- **🔥 Surging**: Δ7d > +10% AND Δ24h > 0
- **↗ Growing**: Δ30d > +5% AND Δ7d ≥ 0
- **↔ Stable**: |Δ30d| < 5%
- **⚠ Cooling**: Δ7d < −5% OR Δ30d < −5%
- **🆘 Declining**: Δ7d < −15% AND Δ24h < 0

### §7.9 Protocol detail (`/protocols/{slug}`)

| Panel | Shows | API call | Status |
|---|---|---|---|
| **Overview card** | Description, github link, audit doc reference, current factory WASM hash, decoder coverage status | `GET /v1/protocols/{slug}` | ❌ new |
| **TVL chart with annotations** | TVL over time with WASM-upgrade markers + big flow markers | `GET /v1/protocols/{slug}/tvl/history?from=…&to=…&granularity=…` | ❌ new |
| **TVL rank-change history** | "This protocol's rank over time" line chart | `GET /v1/protocols/{slug}/rank-history?from=…&to=…` | ❌ new |
| **All contracts** | Each contract under protocol, role, current WASM hash, deploy ledger | `GET /v1/protocols/{slug}/contracts` | ❌ new |
| **All pairs** (DEX) | Every pair, token0/token1, current reserves, 24h volume | `GET /v1/protocols/{slug}/pairs` | ❌ new |
| **Pair-creation cadence** | Bar chart: new pairs per day | `GET /v1/protocols/{slug}/pair-cadence?from=…&to=…` | ❌ new |
| **WASM history timeline** | Across-contract upgrade timeline | `GET /v1/protocols/{slug}/wasm-history` | ❌ new |
| **Volume / TVL ratio chart** | Capital efficiency over time | `GET /v1/protocols/{slug}/efficiency?from=…&to=…` | ❌ new |
| **LP yield (DEX)** | Pool-by-pool fee revenue / TVL APR | `GET /v1/protocols/{slug}/yields` | ❌ new |
| **Router attribution** (router protocols) | "Of $X moved last 24h, 62% went through Soroswap's own AMM, 18% routed to Phoenix, 12% to Aquarius, 8% to SDEX" | `GET /v1/protocols/{slug}/router-attribution?window=24h` | ❌ new (see §7.9.1) |
| **Routed-in share** (underlying-venue protocols) | "62% of Phoenix volume last week came in via the Soroswap router" | `GET /v1/protocols/{slug}/routed-in?window=7d` | ❌ new (see §7.9.1) |
| **Aggregator exposure** (aggregator protocols) | DeFindex-style: "$4.2M deployed in Blend supply, $1.1M Blend borrow, $800k Aquarius LP" | `GET /v1/protocols/{slug}/exposure?window=24h` | ❌ new (see §7.9.1) |
| **Per-vault exposure** (aggregator protocols) | One row per vault contract — capital allocation per underlying protocol | `GET /v1/protocols/{slug}/vaults` | ❌ new (see §7.9.1) |

### §7.9.1 Router + aggregator attribution (cross-cutting)

Soroswap and DeFindex are both **routers / aggregators**: their on-chain entry points (`SoroswapRouter`, DeFindex vault contracts) wrap underlying liquidity venues. The interesting question is "where does the money actually land?" — and from the underlying venue's POV, "how much of my volume is routed-in vs direct?"

This is a **cross-cutting attribution layer**, not a per-protocol panel. It powers four places on the site:

1. **Soroswap protocol page** — router-attribution donut: how much of Soroswap-routed volume hits Soroswap pairs vs Phoenix vs Aquarius vs SDEX.
2. **DeFindex (and any aggregator) protocol page** — exposure chart: where is the deposited capital deployed underneath, per underlying protocol.
3. **Underlying venues' protocol pages** (Phoenix, Aquarius, SDEX, Blend) — routed-in share: what % of my flow is direct vs via a router/aggregator.
4. **Per-pair page** (§7.4) — "this trade hit Phoenix via Soroswap router" badge on each row of the live tape.

#### How attribution works

When a tx invokes a known router/aggregator contract, every trade or state-change event emitted by the **same tx batch** is attributed to that router. Mechanism:

- Maintain a `routers` registry: `(contract_id, name, kind, protocol_slug)`. Seeded from known router/vault contracts; auto-discovery extends it (see decoder hook below).
- Dispatcher's `ContractCallDecoder` fires on any invocation of a router contract. The dispatcher pushes the `routed_via` tag into a per-tx context.
- Every trade inserted from the same tx batch gets a `routed_via` column populated.
- For aggregator vaults (DeFindex), additional layer: when a vault invokes an underlying protocol (e.g. Blend's `submit`), the resulting Blend state-change is tagged with `routed_via=defindex` AND captures the specific vault contract id for vault-level breakdown.

Attribution is **post-hoc and additive**: a Phoenix swap remains a Phoenix trade in the trades hypertable (so Phoenix volume is unchanged); the `routed_via` column is just an extra dimension you can group by.

#### What about multi-hop routes?

A single tx can route through multiple pools (`A → Soroswap pair X → Phoenix pool Y → Aquarius pool Z`). Each leg emits its own underlying event; **all legs share the same `routed_via` tag** because they're all in the same tx batch. The router's tx → 3 trades, all tagged `routed_via=soroswap-router`.

#### What about nested routers?

A DeFindex vault could deposit into Blend, which itself triggers an internal swap. The first router observed on the call stack wins (`routed_via=defindex`); the inner Blend interaction is captured as a separate dimension via the aggregator-specific tracking on `aggregator_exposures` (§9.9). No need to support arbitrary nesting at v1.

### §7.10 Contract detail (`/contracts/{C-strkey}`)

The atom. Every other page links here.

| Panel | Shows | API call | Status |
|---|---|---|---|
| **Overview** | C-strkey, deploy ledger, current WASM hash, protocol membership, decoder claim | `GET /v1/contracts/{id}` | ❌ new |
| **WASM version timeline** | One row per `(wasm_hash, first_ledger, last_ledger)`; bytecode size, WAT preview link | `GET /v1/contracts/{id}/wasm-history` | ❌ new |
| **WASM bytecode viewer** | Per-version: download `.wasm`, hex preview, hash | `GET /v1/contracts/{id}/wasm/{hash}` | ❌ new |
| **WAT viewer** | Per-version: `wasm2wat` output, syntax-highlighted, **diff against previous version** | `GET /v1/contracts/{id}/wasm/{hash}/wat` (generated on demand, cached by hash) | ❌ new |
| **Storage transitions** | Per-ledger LedgerEntry diff (ContractData / ContractCode) | `GET /v1/contracts/{id}/storage-transitions?from=…&to=…` | ❌ new |
| **Recent events** | Last N Soroban events from this contract | `GET /v1/contracts/{id}/events?limit=50&from=…&to=…` | ❌ new |
| **Recent invocations** | Last N InvokeContract calls (function name, args) | `GET /v1/contracts/{id}/invocations?limit=50&from=…&to=…` | ❌ new |
| **Resource usage** | Per-tx Soroban resource fee histogram | `GET /v1/contracts/{id}/resources?from=…&to=…` | ❌ new |

### §7.11 Oracle directory (`/oracles`)

| Panel | Shows | API call |
|---|---|---|
| **Oracle table** | name, kind, contract id, feed count, last update ledger, **freshness lag**, divergence vs our VWAP (if applicable) | `GET /v1/oracles` |

### §7.12 Oracle detail (`/oracles/{name}`)

| Panel | Shows | API call | Status |
|---|---|---|---|
| **Feed table** | per-feed: asset, last price, decimals, last update, lag, observer | `GET /v1/oracle/latest?source={name}` | ✅ |
| **Per-feed history chart** | Single feed over time | `GET /v1/oracle/prices?asset={asset}&records=200` | ✅ |
| **Cross-pair (Reflector-CEX)** | x_last_price for arbitrary base/quote | `GET /v1/oracle/x_last_price?asset=…&quote=…` | ✅ |
| **Divergence vs our VWAP** | Plot oracle alongside our VWAP, show delta % | `GET /v1/divergences?reference={name}&asset=…` | ❌ new |

### §7.13 Issuer detail (`/issuers/{G-strkey}`)

| Panel | Shows | API call |
|---|---|---|
| **Issuer card** | G-strkey, creation ledger, home_domain, SEP-1 metadata (org name, description, conditions, KYC) | `GET /v1/issuers/{G-strkey}` |
| **Auth flags** | `auth_required`, `auth_revocable`, `auth_immutable`, `auth_clawback_enabled` (current + history) | `GET /v1/issuers/{G-strkey}/auth-history` |
| **Issued assets** | All assets ever issued by this G-account | `GET /v1/issuers/{G-strkey}/assets` |
| **Anchor link** | If `home_domain` set, link to `/anchors/{domain}` | derived |
| **Reputation badges** | SEP-1 verified, age, audit status (parsed from SEP-1), home_domain SSL status | derived from `/v1/issuers` |

### §7.14 Anchor detail (`/anchors/{home_domain}`)

| Panel | Shows | API call |
|---|---|---|
| **Anchor card** | Domain, organization name + description, contact, regulatory info, all SEP-1 fields | `GET /v1/anchors/{domain}` |
| **All issuers under this domain** | List | `GET /v1/anchors/{domain}/issuers` |
| **All assets** | Aggregated across issuers | `GET /v1/anchors/{domain}/assets` |
| **Total trustlines** | Sum across assets | derived |
| **24h flow** | Sum of payment-volumes across issuers' assets | `GET /v1/anchors/{domain}/flow?window=24h` |
| **Reputation strip** | SEP-1 freshness, SSL, last verification | derived |

### §7.15 Tx detail (`/tx/{tx_hash}`)

| Panel | Shows | API call |
|---|---|---|
| **Tx header** | Hash, ledger, ts, source account, fee, status (success/fail), op count | `GET /v1/tx/{hash}` |
| **Op-by-op breakdown** | Each op decoded (type, args, effects) | (in tx response) |
| **Trades generated** | Trades this tx produced (with link to each pair page) | `GET /v1/tx/{hash}/trades` |
| **Soroban events emitted** | All events from any contract this tx invoked | `GET /v1/tx/{hash}/events` |
| **State changes** | LedgerEntry diff this tx caused | `GET /v1/tx/{hash}/changes` |
| **Visualization** | If path-payment: route diagram (USDC → XLM → AQUA) | derived from ops |

### §7.16 Account detail (`/accounts/{G-strkey}`)

| Panel | Shows | API call |
|---|---|---|
| **Account card** | G-strkey, creation ledger, sequence, home_domain (if set), inflation dest, signers | `GET /v1/accounts/{G-strkey}` |
| **Trustlines** | All trustlines + balances + auth status | `GET /v1/accounts/{G-strkey}/trustlines` |
| **Recent activity** | Trades involving this account, contracts invoked | `GET /v1/accounts/{G-strkey}/activity?limit=…` |
| **Asset flow chart** | Inflows/outflows per asset over time | `GET /v1/accounts/{G-strkey}/flow?from=…&to=…` |
| **Watchlist link** | Add to `?watchlist=` URL param | frontend |

### §7.17 Path-payments (`/path-payments`)

| Panel | Shows | API call |
|---|---|---|
| **Network heatmap** | Most-traversed asset paths (Sankey or chord diagram) | `GET /v1/path-payments/heatmap?from=…&to=…` |
| **Recent path payments** | Last N with full path visualization | `GET /v1/path-payments/recent?limit=50` |
| **Path success rate** | % of path-payment ops that succeed vs fail | `GET /v1/path-payments/success-rate?from=…&to=…` |

### §7.18 Anomalies (`/anomalies`)

| Panel | Shows | API call |
|---|---|---|
| **Currently firing** | Active freezes, full breakdown | `GET /v1/anomalies?status=firing` |
| **Freeze timeline** | Last N events: ts, asset, quote, reason, duration, frozen value, recovered_at | `GET /v1/anomalies?since=…&kind=…&limit=…` |
| **Per-asset rate** | Last N days, count of freezes per asset | `GET /v1/anomalies/by-asset?window=…` |
| **Per-reason breakdown** | single_source / divergence / outlier_storm / manual | `GET /v1/anomalies/by-reason?window=…` |
| **Calendar heatmap** | Daily anomaly count, GitHub-style grid | derived |

### §7.19 Divergences (`/divergences`)

| Panel | Shows | API call |
|---|---|---|
| **Divergence monitor** | Per (asset, reference): our VWAP, ref price, delta %, status | `GET /v1/divergences` |
| **Per-reference detail** | Filter by Chainlink / CoinGecko / Reflector / Redstone / Band | `GET /v1/divergences?reference=…` |
| **Historical chart** | One pair, one reference, delta % over time | `GET /v1/divergences/{asset}/{quote}/{reference}/history?from=…&to=…` |

### §7.20 MEV / suspicious-pattern detector (`/mev`)

Auto-flagged pattern feed.

| Panel | Shows | API call |
|---|---|---|
| **Recent flagged events** | Sandwich attacks, oracle deviations, liquidation cascades, wash trading | `GET /v1/mev?since=…&kind=…&limit=…` |
| **Per-kind tallies** | Counts last 7d / 30d per pattern | `GET /v1/mev/tally?window=…` |
| **Flagged event detail** | Per event: tx hashes, accounts, profit estimate (if applicable), full timeline | `GET /v1/mev/{event_id}` |

Pattern detection lives in a new aggregator-side worker; results persisted to `mev_events` (§9.5).

### §7.21 Network / macro pulse (`/network`)

| Panel | Shows | API call |
|---|---|---|
| **Total Stellar TVL** | Aggregate across protocols, multi-window deltas | `GET /v1/network/tvl` |
| **Total network volume** | All sources, all pairs | `GET /v1/network/volume?window=…` |
| **Soroban activity index** | Composite: deploys/day + upgrades/day + invocations/day | `GET /v1/network/soroban-activity` |
| **Network freeze rate** | Freezes per day | `GET /v1/network/freeze-rate?window=…` |
| **Source diversity (Shannon entropy)** | How decentralized is our pricing | `GET /v1/network/source-diversity` |
| **Stablecoin peg health strip** | USDC/USD, EURC/EUR, MXNe/MXN, etc. deviations | `GET /v1/network/peg-health` |
| **Operations per ledger** | Throughput chart | `GET /v1/network/ops-per-ledger?from=…&to=…` |
| **Fee market history** | Base fee + Soroban inclusion fee over time | `GET /v1/network/fee-market?from=…&to=…` |
| **Active address count** | Daily uniques | `GET /v1/network/active-addresses?from=…&to=…` |
| **New contract deploys** | Daily contract-creation count | `GET /v1/network/new-contracts?from=…&to=…` |

### §7.22 Diagnostics (`/diagnostics`)

Public — no PII. Demonstrates operational rigour.

| Panel | Shows | API call |
|---|---|---|
| **Pulse banner** | Last ledger, lag vs network tip, ingest rate | `GET /v1/diagnostics/pulse` |
| **Decoder coverage table** | Per-source: events seen 24h, decode errors, orphans, last-output ledger | `GET /v1/diagnostics/decoders` |
| **Archive completeness** | Cross-anchor archive %, per region | `GET /v1/diagnostics/archive-completeness` |
| **Cross-region health** | All-regions-serve-same-rate check (ADR-0015) | `GET /v1/diagnostics/cross-region` |
| **Backfill cursor table** | Per-cursor: source, sub_source, last_ledger, lag | `GET /v1/diagnostics/cursors` |
| **WASM-decoder coverage** | For every WASM hash: which decoder claims it | `GET /v1/diagnostics/wasm-coverage` |
| **SLO burn-rate** | Multi-window burn rates per ADR-0009 | `GET /v1/diagnostics/slo` |

### §7.23 Research / blog (`/research`)

| Panel | Shows | Source |
|---|---|---|
| **Article index** | Tagged, sorted by date | `posts/*.md` (MDX) |
| **Article (`/research/{slug}`)** | MDX with embedded `<RatesLink>` and `<RatesPanel>` components that deep-link into the live site | Static-rendered |

Authoring flow: researcher writes prose, drops in primitives:
- `<RatesLink coin="stellar" quote="usdc" asOf={50123456}>oracle deviation here</RatesLink>` → renders an inline link with hover-preview of the chart at that moment.
- `<RatesPanel type="multi-source-overlay" pair="aqua/usdc" range="2026-03-14T12:00..18:00Z" />` → embeds a live, frozen-state panel.
- `<TxLink hash="abc..." />` → tx pill.

Publishing = git commit + push. CI rebuilds the site.

### §7.24 Account (`/account`)

| Panel | Shows | API call |
|---|---|---|
| **Sign-in** | SEP-10 challenge → token (Freighter / Albedo / Lobstr wallets) | `GET /v1/auth/sep10/challenge` + `POST /v1/auth/sep10/token` |
| **Account info** | account_id, tier, rate limit, member-since | `GET /v1/account/me` |
| **Usage chart** | Requests/day, 429 count | `GET /v1/account/usage` (live trailing-30-day counters from the Redis-backed `usage.Counter` the rate-limit middleware writes; 429 attribution lands when the rate-limit middleware emits a separate counter). F-1259 (codex audit-2026-05-12). |
| **API keys** | List with label, created-at, last-used; create/revoke | `GET /v1/account/keys` (list missing) + `POST` (works) + `DELETE` (missing) |

### §7.25 Universal search (`/search`)

| Panel | Shows | API call |
|---|---|---|
| **Top-of-every-page bar** | Type-ahead with categorized results: Coins / Issuers / Anchors / Contracts / Tx / Accounts / Articles | `GET /v1/search?q=…&types=…&limit=…` |
| **Results page** | Full results, paginated, per-type tabs | `GET /v1/search?q=…&type=…&cursor=…` |

Backed by trigram + tsvector indexes on `coins.slug`, `coins.code`, `coins.name`, `issuers.home_domain`, `contracts.id`, `tx.hash`, `accounts.id`. Fuzzy match for typos.

---

## 8. Time machine

Every endpoint accepts `as_of_ledger=N` (or `as_of=ISO`, resolved to ledger). Default = live tip.

### 8.1 What "as of" means

For each endpoint shape:
- **Point-in-time** (`/v1/price`): the closed-bucket VWAP whose window contains ledger N.
- **Range** (`/v1/history?from=…&to=…&as_of_ledger=N`): the data as it WAS at N — so `to` is clamped to N if `to > N`. (Bigger ranges with `as_of_ledger` in the past simulate "what we knew then.")
- **Lists** (`/v1/coins`, `/v1/protocols`): only entities that existed by ledger N (filter on `first_seen_ledger ≤ N`).
- **Live tip** (`/v1/price/tip`): meaningful only when N is recent; for historical N, returns the closed-bucket containing N (not the tip).
- **WASM detail** (`/v1/contracts/{id}/wasm/{hash}`): hash-keyed, no time dependency.

### 8.2 Implementation

A **single helper** `pinTime(ctx, asOfLedger)` in `internal/api/v1/timepin/` that every handler routes through. Centralizes the projection logic. New endpoints get it for free; existing endpoints add one line.

CAGG reads use `time_bucket()` aligned to `as_of_ledger`'s wall-clock; trades hypertable filters on `ledger ≤ N`; cursor lookups + registry queries filter on `first_seen_ledger ≤ N`.

### 8.3 UX

- **Top-right global widget on every page** — current "as of" indicator. Live = no badge. Historical = orange "🕓 Viewing as of ledger 50,123,456 · 2025-09-12T13:42Z" with a "Back to live" button.
- **Ledger picker** — modal accepting ledger #, ISO date, or "N hours/days ago." Resolves to ledger via `/v1/ledgers/at?ts=…`.
- **URL-shareable** — every page's URL carries `as_of_ledger` so links Just Work.
- **Frozen mode styling** — historical pages get a subtle off-tone (warm yellow tint) so you can never confuse historical with live. Charts grey out the future.

### 8.4 Cost

The audit work isn't compute — it's **discipline**. Every new endpoint must accept `as_of_ledger`. Code review enforces it. The single helper makes it cheap; without that discipline you end up with half-broken historical state.

---

## 9. Schema additions

Six new tables. Migrations 0017-0022.

### 9.1 `wasm_versions` + `contract_wasm_history`

```sql
-- 0017
CREATE TABLE wasm_versions (
    wasm_hash         char(64)    PRIMARY KEY,
    bytecode          bytea       NOT NULL,
    bytecode_size     integer     NOT NULL,
    first_seen_at     timestamptz NOT NULL DEFAULT now(),
    first_seen_ledger integer     NOT NULL
);

CREATE TABLE contract_wasm_history (
    contract_id   text     NOT NULL,
    first_ledger  integer  NOT NULL,
    last_ledger   integer,                            -- NULL = current
    wasm_hash     char(64) NOT NULL REFERENCES wasm_versions(wasm_hash),
    PRIMARY KEY (contract_id, first_ledger)
);

CREATE INDEX contract_wasm_history_current_idx
    ON contract_wasm_history (contract_id) WHERE last_ledger IS NULL;
```

Bytecode inline (50-500 KB; ~50 contracts × ~5 versions ≈ 50 MB total).
Backfilled from existing `wasm-history` JSONL on r1.

### 9.2 `freeze_events`

```sql
-- 0018
CREATE TABLE freeze_events (
    asset_id      text         NOT NULL,
    quote_id      text         NOT NULL,
    frozen_at     timestamptz  NOT NULL,
    frozen_at_ledger integer   NOT NULL,
    reason        text         NOT NULL CHECK (reason IN
                                              ('single_source','divergence',
                                               'outlier_storm','manual')),
    frozen_value  numeric      NOT NULL,
    recovered_at  timestamptz,
    recovered_at_ledger integer,
    detail        jsonb,
    PRIMARY KEY (asset_id, quote_id, frozen_at)
);

SELECT create_hypertable('freeze_events', 'frozen_at',
                         chunk_time_interval => INTERVAL '30 days',
                         if_not_exists => TRUE);

CREATE INDEX freeze_events_status_idx
    ON freeze_events (frozen_at DESC) WHERE recovered_at IS NULL;
```

### 9.3 `divergence_observations`

```sql
-- 0019
CREATE TABLE divergence_observations (
    asset_id      text         NOT NULL,
    quote_id      text         NOT NULL,
    reference     text         NOT NULL CHECK (reference IN
                                              ('chainlink','coingecko',
                                               'reflector-cex','reflector-fx',
                                               'reflector-dex','redstone','band')),
    observed_at   timestamptz  NOT NULL,
    observed_at_ledger integer NOT NULL,
    our_price     numeric      NOT NULL,
    ref_price     numeric      NOT NULL,
    delta_pct     numeric      NOT NULL,
    status        text         NOT NULL CHECK (status IN ('clear','firing')),
    PRIMARY KEY (asset_id, quote_id, reference, observed_at)
);

SELECT create_hypertable('divergence_observations', 'observed_at',
                         chunk_time_interval => INTERVAL '7 days',
                         if_not_exists => TRUE);
```

### 9.4 `decoder_stats_5m`

```sql
-- 0020
CREATE TABLE decoder_stats_5m (
    bucket          timestamptz NOT NULL,
    source          text        NOT NULL,
    events_seen     bigint      NOT NULL DEFAULT 0,
    decode_errors   bigint      NOT NULL DEFAULT 0,
    orphan_events   bigint      NOT NULL DEFAULT 0,
    last_ledger     integer,
    PRIMARY KEY (bucket, source)
);

SELECT create_hypertable('decoder_stats_5m', 'bucket',
                         chunk_time_interval => INTERVAL '7 days',
                         if_not_exists => TRUE);
```

5-minute rollup flushed by aggregator from `dispatcher.Stats()`.

### 9.5 `tvl_observations` + `mev_events`

```sql
-- 0021
CREATE TABLE tvl_observations (
    protocol_slug   text        NOT NULL,
    observed_at     timestamptz NOT NULL,
    observed_at_ledger integer  NOT NULL,
    tvl_usd         numeric     NOT NULL,
    pool_count      integer     NOT NULL,
    breakdown       jsonb,                 -- per-pool TVL detail
    PRIMARY KEY (protocol_slug, observed_at)
);

SELECT create_hypertable('tvl_observations', 'observed_at',
                         chunk_time_interval => INTERVAL '7 days',
                         if_not_exists => TRUE);

CREATE TABLE mev_events (
    event_id        uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    detected_at     timestamptz  NOT NULL,
    detected_at_ledger integer   NOT NULL,
    kind            text         NOT NULL CHECK (kind IN
                                                ('sandwich','oracle_deviation',
                                                 'liquidation_cascade','wash_trade')),
    asset_id        text,
    quote_id        text,
    tx_hashes       text[]       NOT NULL,
    accounts        text[],
    detail          jsonb        NOT NULL,
    profit_usd      numeric
);

CREATE INDEX mev_events_detected_idx
    ON mev_events (detected_at DESC);
CREATE INDEX mev_events_kind_idx
    ON mev_events (kind, detected_at DESC);
```

### 9.6 `change_summary_5m`

```sql
-- 0022
CREATE TABLE change_summary_5m (
    entity_type   text         NOT NULL CHECK (entity_type IN
                                              ('coin','protocol','pair','source')),
    entity_id     text         NOT NULL,
    refreshed_at  timestamptz  NOT NULL,
    current_value numeric      NOT NULL,
    h1_value      numeric,    h1_delta_pct  numeric,
    h24_value     numeric,    h24_delta_pct numeric,
    d7_value      numeric,    d7_delta_pct  numeric,
    d30_value     numeric,    d30_delta_pct numeric,
    ath_value     numeric,    ath_at        timestamptz,
    atl_value     numeric,    atl_at        timestamptz,
    streak_direction text,    streak_days   integer,
    acceleration  text,                                     -- 'increasing' | 'flat' | 'decreasing'
    PRIMARY KEY (entity_type, entity_id)
);
```

Refreshed by an aggregator job every 5 min. Powers every multi-window delta strip on the site. **One endpoint, used everywhere.**

### 9.7 `classic_assets`, `issuers`, `anchors`

```sql
-- 0023
CREATE TABLE classic_assets (
    asset_id          text         PRIMARY KEY,    -- "{code}-{issuer}"
    code              text         NOT NULL,
    issuer_g_strkey   text         NOT NULL,
    slug              text         UNIQUE,         -- lowercased code, disambiguated if needed
    first_seen_at     timestamptz  NOT NULL,
    first_seen_ledger integer      NOT NULL,
    last_seen_at      timestamptz  NOT NULL,
    last_seen_ledger  integer      NOT NULL,
    observation_count bigint       NOT NULL DEFAULT 0
);

CREATE INDEX classic_assets_issuer_idx ON classic_assets (issuer_g_strkey);

CREATE TABLE issuers (
    g_strkey            text         PRIMARY KEY,
    home_domain         text,
    auth_required       boolean,
    auth_revocable      boolean,
    auth_immutable      boolean,
    auth_clawback       boolean,
    sep1_resolved_at    timestamptz,
    sep1_payload        jsonb,
    creation_ledger     integer
);

CREATE INDEX issuers_home_domain_idx ON issuers (home_domain);

CREATE TABLE anchors (
    home_domain        text         PRIMARY KEY,
    org_name           text,
    description        text,
    contact_email      text,
    sep1_payload       jsonb,
    sep1_resolved_at   timestamptz,
    sep1_resolved_status text       NOT NULL DEFAULT 'pending'
                                    CHECK (sep1_resolved_status IN ('pending','ok','fetch_failed','parse_failed','tls_failed'))
);
```

### 9.8 `classic_asset_stats_5m`

```sql
-- 0024
CREATE TABLE classic_asset_stats_5m (
    bucket            timestamptz NOT NULL,
    asset_id          text        NOT NULL,
    trustline_count   bigint,
    outstanding_supply numeric,
    volume_24h_usd    numeric,
    last_trade_ledger integer,
    PRIMARY KEY (bucket, asset_id)
);

SELECT create_hypertable('classic_asset_stats_5m', 'bucket',
                         chunk_time_interval => INTERVAL '7 days',
                         if_not_exists => TRUE);
```

### 9.9 `routers` + `routed_via` + `aggregator_exposures`

Powers §7.9.1 — router/aggregator attribution.

```sql
-- 0025
CREATE TABLE routers (
    contract_id    text         PRIMARY KEY,    -- C-strkey
    name           text         NOT NULL,       -- "soroswap-router-v1", "defindex-vault-{...}"
    kind           text         NOT NULL CHECK (kind IN ('router','aggregator-vault')),
    protocol_slug  text         NOT NULL,       -- "soroswap" / "defindex" / …
    added_at       timestamptz  NOT NULL DEFAULT now(),
    auto_discovered boolean     NOT NULL DEFAULT false,
    notes          text
);

CREATE INDEX routers_protocol_idx ON routers (protocol_slug);

-- Add the attribution column to the existing trades hypertable.
ALTER TABLE trades ADD COLUMN routed_via text;
CREATE INDEX trades_routed_via_idx ON trades (routed_via)
    WHERE routed_via IS NOT NULL;

-- Aggregator-vault exposure: capital allocation per (vault, underlying protocol).
-- Distinct from routed_via tagging — vaults hold capital persistently, not just
-- per-tx. Refreshed on every state-changing interaction the vault makes with
-- an underlying protocol.
CREATE TABLE aggregator_exposures (
    vault_contract_id  text         NOT NULL,
    underlying_protocol text         NOT NULL,    -- "blend", "aquarius", "soroswap", …
    observed_at        timestamptz  NOT NULL,
    observed_at_ledger integer      NOT NULL,
    exposure_usd       numeric      NOT NULL,
    detail             jsonb,                     -- e.g. {"blend_supply": ..., "blend_borrow": ...}
    PRIMARY KEY (vault_contract_id, underlying_protocol, observed_at)
);

SELECT create_hypertable('aggregator_exposures', 'observed_at',
                         chunk_time_interval => INTERVAL '7 days',
                         if_not_exists => TRUE);

CREATE INDEX aggregator_exposures_vault_idx
    ON aggregator_exposures (vault_contract_id, observed_at DESC);
CREATE INDEX aggregator_exposures_protocol_idx
    ON aggregator_exposures (underlying_protocol, observed_at DESC);
```

**Backfill:** mechanical. For each known router contract, walk the
trades hypertable and the dispatcher's contract-call observations,
tag any same-tx trades. For DeFindex, walk Blend / Aquarius / etc.
state-change events from any known vault contract and write
`aggregator_exposures` rows.

---

## 10. API endpoint additions (full list)

Grouped by responsibility. Each carries `as_of_ledger` unless noted.

### 10.1 Coin / asset

| Endpoint | Returns | Powered by |
|---|---|---|
| `GET /v1/coins` | Registry-aware super-table (slug, name, ticker, price, deltas, sparkline, market cap, supply) | join of `assets`, `classic_assets`, `discovered_assets`, `change_summary_5m` |
| `GET /v1/coins/{slug}` | Single coin canonical record | `assets` + `coin_metadata` view |
| `GET /v1/coins/{slug}/metadata` | SEP-1 + image + description | `coin_metadata` |
| `GET /v1/coins/{slug}/stats?window=24h` | High/low/volume/trades/source-count | `prices_*` CAGGs |
| `GET /v1/coins/{slug}/protocols` | Per-protocol pair list | `pairs` join `protocols` |
| `GET /v1/coins/{slug}/supply/history` | Time-series circulating/total/max | `asset_supply_history` |
| `GET /v1/coins/{slug}/supply/breakdown` | Algorithm components | classic-supply / sep41-supply readers |
| `GET /v1/coins/{slug}/sep41-events` | Mint/burn/clawback timeline | `sep41_supply_events` |
| `GET /v1/coins/{slug}/trustlines/history` | Adds/removes/day | `trustline_observations` rollup |
| `GET /v1/coins/{slug}/holders/history` | Holder count over time | rollup |
| `GET /v1/coins/{slug}/events` | Annotation events for charts | union (WASM upgrades, freezes, big flows) |

### 10.2 Pair / market

| Endpoint | Returns |
|---|---|
| `GET /v1/pairs` | Sortable pair list with deltas |
| `GET /v1/pairs/{base}/{quote}` | Pair detail summary |
| `GET /v1/pairs/{base}/{quote}/venues` | Per-venue breakdown |
| `GET /v1/pairs/{base}/{quote}/spread?from=…&to=…` | Cross-venue spread |
| `GET /v1/pairs/{base}/{quote}/liquidity-flow?from=…&to=…` | Liquidity migration |
| `GET /v1/markets` | Same as `/v1/pairs` aliased |
| `GET /v1/markets/heatmap?from=…&to=…` | base × quote heatmap matrix |
| `GET /v1/orderbook?base=…&quote=…&depth=20` | SDEX bids/asks ladder |
| `GET /v1/spread?base=…&quote=…&from=…&to=…&granularity=…` | Spread chart |
| `GET /v1/slippage?base=…&quote=…&size=…` | Slippage simulator |

### 10.3 Aggregation transparency

| Endpoint | Returns |
|---|---|
| `GET /v1/price/{base}/{quote}/sources` | Per-source % weight + volume in current VWAP |
| `GET /v1/price/{base}/{quote}/why` | Full math: trades used, outliers excluded, weights, confidence |

### 10.4 Sources

| Endpoint | Returns |
|---|---|
| `GET /v1/sources?include=health` | Source list with health metadata |
| `GET /v1/sources/{name}` | Source detail |
| `GET /v1/sources/{name}/health` | Decoder errors / orphans / unmatched |
| `GET /v1/sources/{name}/race?pair=…&from=…&to=…` | Per-source latency profile |
| `GET /v1/sources/{name}/reliability?window=30d` | Uptime / lag / error rate |
| `GET /v1/sources/{name}/weight-history?pair=…&from=…&to=…` | VWAP weight share over time |
| `GET /v1/sources/{name}/wasm-history` | (Soroban only) |

### 10.5 Protocols

| Endpoint | Returns |
|---|---|
| `GET /v1/protocols` | Scoreboard with deltas + status badge |
| `GET /v1/protocols/{slug}` | Protocol overview |
| `GET /v1/protocols/{slug}/contracts` | All contracts |
| `GET /v1/protocols/{slug}/pairs` | All pairs |
| `GET /v1/protocols/{slug}/tvl/history` | TVL over time |
| `GET /v1/protocols/{slug}/rank-history` | Rank-change timeline |
| `GET /v1/protocols/{slug}/wasm-history` | WASM upgrade timeline |
| `GET /v1/protocols/{slug}/pair-cadence` | New-pair frequency |
| `GET /v1/protocols/{slug}/efficiency` | Volume/TVL ratio |
| `GET /v1/protocols/{slug}/yields` | LP yields |
| `GET /v1/protocols/tvl-share?from=…&to=…&granularity=…` | Stacked-area share |
| `GET /v1/protocols/{slug}/router-attribution?window=…` | (router protocols) — share of router-driven volume by underlying venue |
| `GET /v1/protocols/{slug}/routed-in?window=…` | (any protocol) — share of own volume that arrived via a router/aggregator |
| `GET /v1/protocols/{slug}/exposure?window=…` | (aggregator protocols) — capital deployed per underlying protocol |
| `GET /v1/protocols/{slug}/vaults` | (aggregator protocols) — vault list with per-vault exposure summary |
| `GET /v1/protocols/{slug}/vaults/{contract_id}` | One vault's detail + history |
| `GET /v1/protocols/{slug}/vaults/{contract_id}/exposure?from=…&to=…` | Per-vault exposure over time |
| `GET /v1/routers` | All registered router/aggregator contracts |
| `GET /v1/routers/{contract_id}` | Detail for one router/vault contract |

### 10.6 Contracts

| Endpoint | Returns |
|---|---|
| `GET /v1/contracts/{id}` | Contract overview |
| `GET /v1/contracts/{id}/wasm-history` | Version timeline |
| `GET /v1/contracts/{id}/wasm/{hash}` | Bytecode (binary) + metadata |
| `GET /v1/contracts/{id}/wasm/{hash}/wat` | WAT (generated on demand, cached) |
| `GET /v1/contracts/{id}/wasm/{hash}/diff/{prev_hash}` | Side-by-side WAT diff |
| `GET /v1/contracts/{id}/storage-transitions?from=…&to=…` | LedgerEntry diff |
| `GET /v1/contracts/{id}/events?from=…&to=…&limit=…` | Soroban event firehose |
| `GET /v1/contracts/{id}/invocations?from=…&to=…&limit=…` | InvokeContract calls |
| `GET /v1/contracts/{id}/resources?from=…&to=…` | Resource fee histogram |
| `GET /v1/contracts/wasm-upgrades?since=…` | Network-wide recent upgrades |

### 10.7 Oracles

| Endpoint | Returns |
|---|---|
| `GET /v1/oracles` | Oracle directory |
| `GET /v1/oracles/{name}` | Oracle detail (feeds + freshness) |

(Already-existing `/v1/oracle/lastprice` etc. retained for SEP-40 compat.)

### 10.8 Issuers / anchors

| Endpoint | Returns |
|---|---|
| `GET /v1/issuers/{G-strkey}` | Issuer card |
| `GET /v1/issuers/{G-strkey}/assets` | Their assets |
| `GET /v1/issuers/{G-strkey}/auth-history` | Auth flag changes |
| `GET /v1/anchors/{home_domain}` | Anchor card |
| `GET /v1/anchors/{home_domain}/issuers` | Issuers under domain |
| `GET /v1/anchors/{home_domain}/assets` | All assets |
| `GET /v1/anchors/{home_domain}/flow?window=24h` | Aggregate flow |

### 10.9 Stellar primitives

| Endpoint | Returns |
|---|---|
| `GET /v1/tx/{hash}` | Tx header + ops |
| `GET /v1/tx/{hash}/trades` | Trades from this tx |
| `GET /v1/tx/{hash}/events` | Soroban events emitted |
| `GET /v1/tx/{hash}/changes` | LedgerEntry diff |
| `GET /v1/accounts/{G-strkey}` | Account header |
| `GET /v1/accounts/{G-strkey}/trustlines` | Trustlines + balances |
| `GET /v1/accounts/{G-strkey}/activity?limit=…` | Recent trades + invocations |
| `GET /v1/accounts/{G-strkey}/flow?from=…&to=…` | Per-asset flow chart |
| `GET /v1/path-payments/heatmap?from=…&to=…` | Network heatmap |
| `GET /v1/path-payments/recent?limit=50` | Recent ops |
| `GET /v1/path-payments/success-rate?from=…&to=…` | Success/failure ratio |
| `GET /v1/ledgers/at?ts=…` | ISO → ledger resolver |

### 10.10 Anomalies / divergences / MEV

| Endpoint | Returns |
|---|---|
| `GET /v1/anomalies?status=…&since=…&kind=…&limit=…` | Freeze events |
| `GET /v1/anomalies/{event_id}` | Single freeze event detail |
| `GET /v1/anomalies/by-asset?window=…` | Per-asset rate |
| `GET /v1/anomalies/by-reason?window=…` | Per-reason breakdown |
| `GET /v1/divergences` | Current divergence state |
| `GET /v1/divergences/{asset}/{quote}/{reference}/history?from=…&to=…` | Historical delta |
| `GET /v1/mev?since=…&kind=…&limit=…` | Flagged events feed |
| `GET /v1/mev/{event_id}` | Single event detail |
| `GET /v1/mev/tally?window=…` | Per-kind tallies |

### 10.11 TVL / volatility / changes

| Endpoint | Returns |
|---|---|
| `GET /v1/tvl?range=30d` | Aggregate TVL with deltas |
| `GET /v1/tvl/flow?from=…&to=…` | Sankey data |
| `GET /v1/volatility?base=…&quote=…&window=24h&from=…&to=…` | Volatility chart |
| `GET /v1/changes/{entity_type}/{id}` | Multi-window delta strip data |
| `GET /v1/sparkline/{entity_type}/{id}?window=7d` | Server-rendered SVG |

### 10.12 Network / diagnostics

| Endpoint | Returns |
|---|---|
| `GET /v1/diagnostics/pulse` | One-shot system state |
| `GET /v1/diagnostics/decoders` | Per-source health |
| `GET /v1/diagnostics/archive-completeness` | Cross-anchor archive % |
| `GET /v1/diagnostics/cross-region` | All-regions-same-rate check |
| `GET /v1/diagnostics/cursors` | Backfill positions |
| `GET /v1/diagnostics/wasm-coverage` | WASM hash → decoder map |
| `GET /v1/diagnostics/slo` | Multi-window burn rates |
| `GET /v1/network/tvl` | Aggregate Stellar TVL |
| `GET /v1/network/volume?window=24h` | Total cross-protocol volume |
| `GET /v1/network/soroban-activity` | Composite activity index |
| `GET /v1/network/freeze-rate?window=…` | Freezes/day |
| `GET /v1/network/source-diversity` | Shannon entropy |
| `GET /v1/network/peg-health` | Stablecoin deviations |
| `GET /v1/network/ops-per-ledger?from=…&to=…` | Throughput |
| `GET /v1/network/fee-market?from=…&to=…` | Fee history |
| `GET /v1/network/active-addresses?from=…&to=…` | Daily uniques |
| `GET /v1/network/new-contracts?from=…&to=…` | Contract creations/day |
| `GET /v1/network/health` | Composite weather strip |

### 10.13 Search

| Endpoint | Returns |
|---|---|
| `GET /v1/search?q=…&types=…&limit=…` | Cross-type results |

### 10.14 Stream additions

| Endpoint | Notes |
|---|---|
| `GET /v1/observations/stream?asset=*` | Wildcard firehose for `/` tape |
| `Last-Event-ID` replay on tip + observations streams | Per-connection ring buffer |

### 10.15 Embeds

| Endpoint | Returns |
|---|---|
| `GET /embed/coin/{slug}/{quote}` | Static iframe-safe page (subset of coin detail) |
| `GET /embed/chart?...` | Chart-only iframe |
| `GET /embed/og/coin/{slug}.png` | Open Graph card |
| `GET /embed/og/research/{slug}.png` | Article OG card |

---

## 11. Decoder + writer extensions

What the Go side has to add. Roughly grouped by feature.

### 11.1 Classic-asset registry (§7.13/§7.14)

- New `internal/sources/classic_registry/` observer:
  - Hooks on every trade in the `trades` hypertable (post-insert trigger or aggregator-side worker).
  - Upserts `classic_assets` row per `(code, issuer)`.
  - On observing a new issuer, enqueues SEP-1 fetch.
- New SEP-1 fetcher worker — wraps existing `internal/metadata`. Rate-limits outbound HTTPS (10 RPS), respects robots.txt + `X-RateLimit-*`, retries with backoff.
- New observer extends `accounts` decoder to capture issuer-account auth flag changes → write to `issuers`.

### 11.2 TVL aggregation (§7.8 / §7.9 / §7.21)

- New aggregator-side worker `internal/aggregate/tvl/`:
  - Every aggregator tick (1 min): for each known protocol, sum `reserve_a × price_a + reserve_b × price_b` over its pools.
  - Writes one row per (protocol, tick) to `tvl_observations`.
  - Uses `lp_reserve_observations` + latest `prices_1m` CAGG.

### 11.3 Persistence wiring (currently Redis-only)

- **Freeze events**: `internal/aggregate/freeze` already detects + emits the flag; add a sink that writes to `freeze_events` on every state transition (clear→firing, firing→clear).
- **Divergence observations**: `internal/divergence/worker.go` already computes; add a sink writing each comparison to `divergence_observations`.
- **Decoder stats**: aggregator periodically reads `dispatcher.Stats()` via a metrics hook + writes to `decoder_stats_5m`.
- **TVL**: above.

### 11.4 MEV detection (§7.20)

- New worker `internal/aggregate/mev/`:
  - Sandwich detector: walks `trades` hypertable for matching pre/post-trade pairs in same tx batch with reversal.
  - Oracle-deviation detector: cross-references `oracle_updates` with `prices_1m`, flags >Nσ events.
  - Liquidation cascade: clusters Blend auction events temporally, correlates with price moves.
  - Writes detected events to `mev_events` with full provenance.
- One pass at start for backfill, then incremental on new ledgers.

### 11.5 WASM history materialization

- New `internal/wasm/` package:
  - Watches `UploadContractWasm` ops + `ContractCode` LedgerEntry creates → upserts `wasm_versions`.
  - Watches `ContractData` LedgerEntry changes touching the executable instance → upserts `contract_wasm_history`.
- One-shot backfill from existing `wasm-history` JSONL on r1.
- API handler `GET /v1/contracts/{id}/wasm/{hash}/wat` uses cgo binding to `wabt` (libwasm + wasm2wat). Cache by hash, indefinite TTL (immutable). LRU in-process.

### 11.6 Change-summary rollup (§9.6)

- New aggregator-side worker that, every 5 min, walks every `(entity_type, entity_id)` and computes the multi-window deltas + ATH/ATL + streak + acceleration. Writes to `change_summary_5m`.
- O(N) per refresh where N = entity count (~10k assets + 10 protocols + 200 pairs + 30 sources). Trivial cost.

### 11.7 Search index

- Add tsvector column to `coins`, `issuers`, `contracts`. GIN index. `pg_trgm` for fuzzy matching on tickers + names.
- Search endpoint dispatches across types, merges, ranks by trgm similarity + recency.

### 11.8 SEP-1 rate-limiting

- The only outbound HTTPS we add. Per `home_domain`: max 1 RPS, retry with exponential backoff, weekly stale-cache acceptance. Background worker runs nightly + on-demand for newly-discovered issuers.

### 11.9 Router + aggregator attribution (§7.9.1)

- New `internal/sources/router_attribution/` observer:
  - Hooks the dispatcher's `ContractCallDecoder` for every contract in the `routers` registry. (Same plumbing the Band oracle decoder uses today — no new dispatcher seam.)
  - When a router invocation is observed, pushes `routed_via=<router_name>` into a tx-scoped context that lives across the rest of the tx batch.
  - Trades inserted by other decoders within the same tx batch read the context + populate `trades.routed_via`.
- **Router seed**: ship the registry pre-populated with known router contracts at v1:
  - `SoroswapRouter` (current + historical WASM versions discovered via `wasm-history`).
  - `DefindexFactory` + a curated list of vault contracts derived from factory `new_vault` events.
  - Extends as new routers come online — auto-discovery heuristic flags candidates (contracts that frequently invoke ≥2 underlying-protocol contracts in single tx batches), an operator promotes confirmed ones into the registry.
- **DeFindex aggregator-exposure tracker**: separate from the per-tx routed-via tagging. A periodic worker that, for each known DeFindex vault contract, queries its on-chain state (vault token holdings, deposited amounts in underlying protocols) and writes `aggregator_exposures` rows. Frequency: same as TVL ticker (1 min).
- **Backfill**: walk historical txs that invoked any known router contract, tag the trades they generated. Walk Blend / Aquarius / etc. state-change events from any known vault, write `aggregator_exposures` rows.

**Gotchas:**
- Router contract WASM versioning: a router can be upgraded; function names / signatures may change. Per-WASM-hash decoder audit (the existing CLAUDE.md "Soroban DeFi contracts upgrade in place" rule) applies. Gate backfill against unaudited router WASM.
- DeFindex vault discovery is open-ended; ship at v1 with a curated allowlist, add auto-discovery heuristic in a follow-up.
- "Routing through a router that itself routes through another router" (nested routers) — at v1, attribute to the outermost router only. Don't try to support arbitrary nesting until we see it in the wild.

---

## 12. Forensic / incident article system

Covered in §7.23. Mechanic in detail:

### 12.1 Article authoring

```markdown
---
title: The Reflector / Blend incident of 2025-03-14
date: 2025-03-14
ledger_range: [50115000, 50120000]
tags: [mev, oracle, blend, reflector, post-mortem]
authors: [@ash, @reviewer]
---

On March 14, 2025 at <RatesLink coin="aqua" quote="usdc" asOf={50115423}>
12:42 UTC (ledger 50,115,423)
</RatesLink>, the Reflector-DEX oracle reported an AQUA price 18% above
our independent VWAP.

<RatesPanel
  type="multi-source-overlay"
  pair="aqua/usdc"
  sources={["reflector-dex", "soroswap", "phoenix", "sdex"]}
  range={[50115000, 50120000]}
  highlight={50115423}
/>

This deviation triggered <RatesLink anomaly="freeze-2025-03-14T12:42">
our freeze on AQUA/USDC
</RatesLink> within a single closed bucket…
```

### 12.2 Build pipeline

- Posts live in `posts/*.md` in this repo (or a sibling content repo).
- Next.js MDX renderer compiles each post to a static page.
- `<RatesLink>` and `<RatesPanel>` resolve to `/coins/...`, `/contracts/...`, `/anomalies/...` URLs with the right `as_of_ledger` baked in.
- Hover-preview shows a thumbnail of the linked panel; click navigates with full URL state.
- Articles are SEO-friendly + sharable. OG cards render the headline metric.

### 12.3 Why this beats a custom incident page

- Reusability: each post is prose + links. No new UI per incident.
- Verifiability: every claim has a `<>` reveal showing the API call.
- Linkable: any external article (Twitter, blog, audit report) can use the same `RatesLink` pattern via plain URLs.
- Time machine integration is automatic — post body uses `as_of_ledger` URLs that just work.

---

## 13. URL state — reference

```
/coins/stellar/usdc?as_of_ledger=50115423&tab=chart&granularity=1m&timeframe=1h&sources=binance,kraken&compare=stellar,aqua&panel=confidence-card
```

Decomposition:
- `coin = stellar`, `quote = usdc` from path
- `as_of_ledger = 50115423` — time machine
- `tab = chart` — active tab
- `granularity = 1m`, `timeframe = 1h` — chart settings
- `sources = binance,kraken` — overlay restriction
- `compare = stellar,aqua` — multi-asset overlay
- `panel = confidence-card` — anchored sub-view

Frontend principle: every reactive setting reads from + writes to URL query params. Use `useSearchParams` + `router.replace` everywhere. No local state for anything URL-encodable.

---

## 14. Comparison & overlay tools

### 14.1 Multi-asset overlay (`?compare=…`)

Available on every coin chart. Each asset is added as a layer normalized to `% change from window start`. Up to 5 assets. Chart legend shows colour, current %, 24h delta.

### 14.2 Multi-source overlay (`?sources=…`)

On pair charts. Each source becomes its own line. Toggle all-on / all-off. Useful for spotting which source led / lagged.

### 14.3 Multi-protocol overlay

On pair detail pages. Same pair on Soroswap vs Phoenix vs Aquarius vs SDEX. Stacked or overlaid view.

### 14.4 Quadrant chart

`/coins?view=quadrant`. x = price 24h%, y = volume 24h%. Each asset = dot. Top-right = healthy momentum; bottom-right = price up but no volume (suspicious). Brushing selects a subset; click a dot to navigate.

### 14.5 Calendar heatmap

GitHub-style grid. Used on:
- Asset pages — daily trade count.
- Protocol pages — daily new pairs / WASM upgrades.
- Anomaly page — daily freeze count.

---

## 15. Universal search

Top-of-every-page bar. Categorized type-ahead; results page at `/search?q=…`.

### 15.1 Result categories

- **Coins** (asset slug, ticker, name, code-issuer)
- **Issuers** (G-strkey, home_domain)
- **Anchors** (home_domain, org name)
- **Contracts** (C-strkey, protocol-known names)
- **Transactions** (tx hash; usually direct match)
- **Accounts** (G-strkey)
- **Articles** (research post titles + tags)

### 15.2 Ranking

- Exact match at top
- Trigram similarity for fuzzy matches
- Recency boost (recently active assets / contracts > dormant)
- Popularity boost (by 24h volume / view count if we track)

### 15.3 Cmd-K

Keyboard shortcut opens search modal. Every page.

---

## 16. Embeds + integrations

### 16.1 Iframe embeds

Every chart, donut, metric has a "Share / Embed" button. Generates:

```html
<iframe src="https://stellarindex.io/embed/chart?asset=native&quote=fiat:USD&granularity=1h&timeframe=24h"
        width="800" height="400" frameborder="0"></iframe>
```

URL-state-encoded so the embedded view is exactly what the embedder configured.

### 16.2 Open Graph

Every page: rich OG image.

- `/coins/stellar/usdc` → image showing current price, sparkline, 24h delta.
- `/research/reflector-blend-2025-03-14` → image of the headline metric.
- Generated by an `embed/og/*` worker; cached for 5 min.

### 16.3 Wallet portfolio

`/account` with sign-in: Stellar wallet → see your portfolio valued through our prices. Read-only. Shows balances, total USD value, 24h change. Uses our existing price endpoints + the user's account state from `/v1/accounts/{G}`.

### 16.4 SEP-40 oracle reader demo

A panel on `/oracles` shows live-streaming output of a reference SEP-40 oracle reader hitting our `/v1/oracle/lastprice` endpoint. Code visible in a `<>` reveal so integrators can copy.

---

## 17. Build order

Independent shipping units. Each unblocks the next.

| Phase | Work | Outcome |
|---|---|---|
| **0. Pre-work** | Migrations 0017-0024 + backfills | Tables exist + populated from history |
| **1. High-priority API endpoints** | Source breakdown, supply history, supply breakdown, volatility, change-summary, sparkline, diagnostics | Most panels can render; thin wrappers over data we already have |
| **2. Persistence wiring** | Freeze events sink, divergence sink, decoder-stats flush, TVL writer, MEV worker | Currently-Redis-only state lands in postgres |
| **3. Classic-asset registry** | Observer + SEP-1 worker + endpoints | Issuer/anchor pages work; classic asset coverage explodes |
| **4. WASM materialization** | wasm_versions + contract_wasm_history + WAT endpoint | Contract pages work with full history |
| **5. Time machine** | `pinTime` helper + every endpoint takes `as_of_ledger` | URL state is fully time-shiftable |
| **6. Frontend scaffold** | Next.js app, layout, design system primitives (§6) | Empty pages, shared components |
| **7. Landing + coin directory + coin detail** | The most-visited pages | Site is "browsable" |
| **8. Pair / market / source / oracle pages** | Mid-tier pages | Most surfaces live |
| **9. Protocol + contract explorer** | Depends on phase 4 | WASM time machine surfaces |
| **10. Issuer / anchor / tx / account pages** | Depends on phase 3 | Stellar primitives surface |
| **11. Anomaly / divergence / MEV / network / diagnostics** | Operator-friendly views | Internal team can ditch the CLI |
| **12. Research + embeds + sign-in** | Articles, OG, iframe, wallet integration | Marketing-ready |
| **13. Polish + perf budget** | Bundle splits, ISR tuning, mobile pass | Launch quality |

Phases 1-5 are backend-only. Phases 6+ are frontend (with backend additions discovered along the way).

Realistic: phases 0-5 ≈ 2-3 weeks of backend work, phases 6-13 ≈ 4-6 weeks of frontend. ~6-9 weeks total to a polished v1.

---

## 18. Performance budget

- **LCP < 1.5s** on 3G mobile, p95.
- **JS bundle < 100 KB gzipped per route** (per Next.js app router).
- **API p95 < 200 ms** for any endpoint (already covered by k6 SLA proof).
- **Static generation + ISR** for everything except SSE + authenticated + time-machine views.
- **Lightweight Charts** (~30 KB gzipped) over full TradingView library (~2 MB).
- **CDN absorbs anonymous traffic** per `cdn-setup.md`. Authenticated routes bypass.
- **Per-page bundle splits** — `/coins/*` doesn't ship `/protocols/*` JS.
- **Skeleton states everywhere** — no layout shift; every panel reserves its area.
- **Image optimization** — Next.js Image component; AVIF/WebP fallbacks.
- **Font subsetting** — single weight + italic variant; Latin + Latin-Extended only.
- **No third-party trackers** at v1.

---

## 19. Cross-cutting concerns

### 19.1 Caching

Every public endpoint uses the cache-control matrix from ADR-0018 / `cdn-setup.md`. New endpoints follow the same pattern:

| Surface | Cache header |
|---|---|
| Closed-bucket VWAPs, history, OHLC, since-inception | `public, max-age=60, s-maxage=300` |
| Tip prices | `public, max-age=1` |
| Asset / coin / protocol catalogues | `public, max-age=60, s-maxage=300` |
| Account, auth, embed-OG | `private, no-store` |
| SSE streams | `no-store` |
| WASM bytecode (immutable) | `public, max-age=31536000, immutable` |

### 19.2 Rate limits

- Anonymous: 60 req/min — fine for normal browse.
- Authenticated: 1000 req/min.
- Embeds: same anonymous limit, so an embedder needs to use cached iframes (Cache-Control). OG image cache covers most of this.

### 19.3 Auth

Only `/account/*` is auth-required. SEP-10 challenge → JWT. Wallet integrations: Freighter, Albedo, Lobstr at v1.

### 19.4 i18n

English-only at v1. URL pattern is `i18n-ready` (no `/en/` prefix today, can add via redirect later). Number formatting is Intl-aware client-side.

### 19.5 Accessibility

- WCAG 2.1 AA target.
- Every chart has a screen-reader-friendly fallback table.
- Colour-blind palettes on heatmaps + multi-line charts.
- Keyboard nav on every interactive element.

### 19.6 Per-region consistency

All closed-bucket endpoints already return identical results across regions (ADR-0015). Tip + raw surfaces are explicitly per-region. UI badges this in the freshness indicator on tip-derived widgets.

---

## 20. Open questions

To answer before frontend scaffolding starts:

1. **Hosting:** static + CDN with ISR (Vercel/Netlify), or self-hosted Next.js on r1? Static covers everything except SSE which clients hit directly.
2. **Wallet UX:** Freighter only at v1 vs Freighter + Albedo + Lobstr? Freighter has 80%+ market share.
3. **Repo layout:** monorepo (`web/explorer/`) or separate repo? Monorepo simpler for typed API client sharing.
4. **MDX content repo:** in-tree (`posts/*.md`) or sibling repo?
5. **Brand:** colour palette, logo, typography — needs a brief design pass.
6. **Embeds:** allow arbitrary domains, or whitelist? Whitelist for v1.
7. **Slug ownership:** when two issuers issue assets with the same code (USDC-Circle vs USDC-Anchor), which gets the bare `/coins/usdc`? Volume-weighted dominant by default; admin-overridable in `classic_assets.slug`.
8. **MEV detection thresholds:** sandwich detection has a high false-positive rate. Curated allowlist of known MEV bots vs algorithmic? Probably algorithmic with a confidence score.
9. **OG image generator:** Vercel `@vercel/og` (best DX), Satori, or pure server-side Canvas? Decide once we're picking the framework.
10. **`as_of_ledger` UX:** is the off-tone styling enough to prevent confusion, or do we also disable certain panels (live tape, "currently firing")? Probably disable.

---

## 21. Cross-references

- API source-of-truth: [`openapi/stellar-index.v1.yaml`](../../openapi/stellar-index.v1.yaml)
- Per-surface consistency policy: [ADR-0018](../adr/0018-api-consistency-surfaces.md)
- Closed-bucket cross-region rule: [ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md)
- Chart timeframe × granularity: [ADR-0020](../adr/0020-chart-api-contract.md)
- Anomaly + freeze policy: [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md)
- Aggregation policy chain: [`aggregation-plan.md`](aggregation-plan.md)
- Coverage matrix (requirement × delivery): [`coverage-matrix.md`](coverage-matrix.md)
- CDN setup: [`../operations/cdn-setup.md`](../operations/cdn-setup.md)
- Latency budget: [ADR-0009](../adr/0009-latency-budget.md)
- SLA proof procedure: [`../operations/sla-proof-procedure.md`](../operations/sla-proof-procedure.md)
