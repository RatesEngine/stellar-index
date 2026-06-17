---
title: Multi-network assets migration — consolidate /v1/coins into /v1/assets
date: 2026-05-11
status: superseded
superseded_by: docs/architecture/stellar-focus-refactor-plan.md (2026-06-16)
scope: v1.0 launch-blocking
related_findings: R-018 (docs/review-2026-05-10.md)
last_verified: 2026-06-16
---

> **SUPERSEDED (2026-06-16).** The cross-chain / multi-network asset model
> described below (the `networks[]` identity, per-network drill-down routes,
> and browse-by-blockchain surfaces) was **removed** in the Stellar-focus
> refactor — see `docs/architecture/stellar-focus-refactor-plan.md`. The
> `/v1/coins → /v1/assets` consolidation itself stands; only the cross-chain
> presentation was reverted. Non-Stellar coins that remain (BTC/ETH/…) are
> pricing-reference only (`reference_only`), kept solely for pricing-reference
> use in the divergence cross-check, and are not browseable entities. This doc is
> retained as a historical record of the (now-reverted) multi-network design.

# Multi-network assets migration

## Why

Today the API exposes three overlapping concepts:

- `/v1/currencies` — fiat catalogue (ISO 4217 + USD-pegged stablecoins
  acting as fiat proxies). Used by the explorer's currencies pages.
- `/v1/coins` — Stellar-canonical asset with 24h stats, change %,
  ATH, sparklines, top markets, friendly-slug routing.
- `/v1/assets` — Stellar-canonical asset by canonical asset_id with
  SEP-1 overlay + F2 supply fields. More raw than `/v1/coins`.

`/v1/coins` and `/v1/assets` describe the **same thing** (a Stellar-
issued asset) with overlapping but different field sets. R-018 in
the 2026-05-10 review flagged this; consumers picked one or the
other and got partial data.

This document is the canonical plan for consolidating both into
`/v1/assets` with a richer wire shape that treats multi-network
assets as first-class.

## Product model

**Currencies are the headline.** A "currency" is the cross-chain
concept (USDC, USDT, BTC, ETH, XLM, AQUA). It has a single global
identity:

- `ticker` (e.g. "USDC")
- `slug` (e.g. "usdc")
- `name` (e.g. "USD Coin")
- aggregated `market_cap_usd`, `circulating_supply`, `ath`, `atl`
  (cross-chain)

**Networks are sub-entries.** A currency lists every network it's
issued on. For Stellar entries we surface our native indexing
(price, volume, supply via on-chain ledger sums). For non-Stellar
entries we surface external metadata (contract address, name) and
link out — until we light up indexing on that chain.

**Drill-down is canonical.** Clicking the Stellar row of a global
view lands on the existing `/v1/assets/{canonical_asset_id}` page
with the Stellar-network-specific data.

```
/v1/assets/usdc                       (global view)
  ├── ticker: USDC
  ├── name: USD Coin
  ├── market_cap_usd: 35_000_000_000
  ├── price_usd: 1.0001
  ├── price_authority: "vwap_native"
  ├── sources: [coinbase, binance, kraken, sdex, soroswap]
  └── networks:
        - { network: "stellar",  data_quality: "indexed",  asset_id: "USDC-GA5Z…",
            stellar_price_usd: 1.0013, stellar_volume_24h_usd: 1_002_890,
            deep_link: "/v1/assets/USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN" }
        - { network: "ethereum", data_quality: "external", contract: "0xa0b8…",
            external_link: "https://etherscan.io/token/0xa0b8…" }
        - { network: "solana",   data_quality: "external", contract: "EPjFW…",
            external_link: "https://solscan.io/token/EPjFW…" }

/v1/assets/USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN
  (Stellar-network view — existing endpoint, extended with the fields
  /v1/coins/{slug} used to carry: change %, sparklines, top_markets, etc.)
```

## Price authority

The headline `price_usd` on the global view is computed by us with
a three-tier fallback. Every response carries `price_authority`
and `sources[]` so consumers know which tier produced the served
value and can downgrade trust on (2) and (3) vs (1).

1. **`vwap_native`** — VWAP across every `Class:Exchange` trade in
   our pipeline tagged with the ticker. For USDC that's
   Coinbase + Binance + Kraken + Bitstamp (CEX) + every Stellar
   DEX trade that touches a USDC asset_id. Same VWAP infra we
   already use for Stellar pairs, extended to bucket on
   `(ticker, quote)` instead of `(asset_id, quote)`. Wins when
   we have ≥ N trades in the window (configurable).

2. **`aggregator_avg`** — simple mean across `Class:Aggregator`
   sources (CoinGecko + CMC) at the latest tick. The "we
   aggregate the aggregators" framing: we never serve a black-box
   CG number; we compute the average across the trusted
   aggregators and attribute the inputs. Used when (1) is too
   thin. Aggregator-class sources still don't contribute to VWAP
   (avoids double-counting); their current-price snapshots feed
   this tier only.

3. **`triangulated`** — derived via bridge currency.
   `ASSET_USD ≈ ASSET_BTC × BTC_USD` when no direct USD-quoted
   trade exists. Same triangulation infrastructure used for
   Stellar pairs today (`internal/aggregate/triangulate.go`),
   extended to per-ticker.

## Verified-currency catalogue

Friendly slugs (`/v1/assets/usdc`) only resolve for currencies in
a **verified catalogue**. Two seed sources:

- **Hand-curated YAML** (`configs/verified_currencies.yaml`) —
  initial seed of ~30 known currencies (USDC, USDT, BTC, ETH,
  XLM, AQUA, yXLM, SHX, EURC, PYUSD, …). Every entry includes:
  the ticker, slug, name, optional CG/CMC IDs, and a `networks`
  map with per-network asset identifiers (Stellar `asset_id`,
  Ethereum contract address, etc.).
- **CoinGecko augmentation** (Phase 1.2) — daily refresh fetches
  CG's top-N currencies by market cap and merges into the
  catalogue. Hand-curated entries take precedence on conflict —
  we trust our verified-issuer mapping for Stellar over CG's
  reported asset_id.

Any asset_id NOT mapped by the catalogue is reachable only by
full canonical asset_id (`/v1/assets/USDC-GA5Z…`). Friendly slugs
are never auto-generated from observed ticker codes — that's the
attack surface for ticker-collision phishing (see "Unverified
asset warning" below).

## Unverified asset warning

When a user navigates to `/v1/assets/{some_asset_id}` for an
asset whose code matches a verified ticker but whose issuer is
NOT the verified issuer (e.g. someone issues their own
`USDC-G_DIFFERENT…`), the response attaches:

```json
{
  "data": { ...normal asset detail... },
  "flags": { "unverified_ticker_collision": true },
  "unverified_warning": {
    "verified_slug": "usdc",
    "verified_asset_id": "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN",
    "verified_name": "USD Coin",
    "verified_issuer": "Circle (centre.io)",
    "note": "Exercise caution — this asset uses the ticker 'USDC' but is not the verified USDC on Stellar. The verified USDC on Stellar is issued by Circle: USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN."
  }
}
```

The explorer renders the warning as a prominent banner with a
deep-link to the verified asset.

This is independent of the existing scam-issuer registry
(`docs/operations/scam-issuers.md` / stellar.expert directory) —
that signal flags issuers known to be scams; this one flags
ticker collisions regardless of intent. Both can fire on the
same asset; both surface in the response.

## Phases

### Phase 1.1 — Verified-currency catalogue + unverified warning (1st PR)

**Files:**
- `configs/verified_currencies.yaml` — seed (~30 currencies)
- `internal/currency/verified.go` — loader, exposes `LookupByTicker(slug)`,
  `LookupByStellarAssetID(asset_id)`, `FindCollisionsByCode(code)`
- `internal/api/v1/assets.go` — wire the unverified-warning
  attachment into `handleAssetGet`
- `internal/api/v1/assets.go` — `AssetDetail.UnverifiedWarning *UnverifiedWarning`
- OpenAPI + pkg/client + explorer types in lockstep
- Tests: catalogue parses + lookups work + collision detection
  attaches warning

**Out of scope (Phase 1.1):** no slug-routing change, no global
view, no price computation. This is the foundation + the
anti-confusion warning that ships immediately.

### Phase 1.2 — CG + CMC connectors, catalogue-driven (shipped 2026-05-11)

The plan as originally written assumed two new tables
(`verified_currencies_external`, `aggregator_prices`) and a fresh
poller package per aggregator. Discovery during implementation:

- The `oracle_updates` hypertable (migration 0003) already stores
  aggregator-class observations — CG and CMC pollers have been
  emitting `canonical.OracleUpdate` records since 2026-04-24.
  Schema-identical to the proposed `aggregator_prices`.
- `internal/sources/external/coingecko/` and `coinmarketcap/`
  packages already exist with working pollers.

What 1.2 actually delivers:

1. **Catalogue-driven ticker mapping.** CG's hardcoded `tickerToID`
   map (13 entries) is now overridable via
   `Poller.TickerToID`, set at indexer startup from
   `currency.Catalogue.CoinGeckoIDs()`. Adding a verified currency
   with a `coingecko_id` in `internal/currency/data/seed.yaml`
   automatically extends the poll set. The package default stays
   as a fallback for tests + any caller that doesn't pass the
   override.
2. **Catalogue-driven aggregator pair set.** The indexer's
   `defaultAggregatorPairs` hardcoded crypto list is now derived
   from the catalogue at startup
   (`aggregatorPairsFromCatalogue`). Falls back to the hardcoded
   list when no catalogue is wired.
3. **Storage reader.**
   `*timescale.Store.LatestAggregatorPricesForPair(ctx, base, quote, sources)`
   returns the most-recent aggregator-class observation per source
   for a given pair. The Phase 1.3 `aggregator_avg` tier consumes
   this directly; the storage layer doesn't repeat
   `external.Registry`'s class filter.
4. **No new migrations.** `oracle_updates` covers the price-storage
   role.

**Deferred to a later phase: CoinGecko catalogue augmentation
worker** (the `/coins/list` + top-N market-cap refresh, separate
trust surface for "known unverified" currencies). The hand-curated
26-currency seed is sufficient for v1; augmentation lands when
either (a) a customer's coverage requirement exceeds the seed, or
(b) the Phase 1.4 global view starts surfacing 404s for popular
non-Stellar tickers. Tracked in the launch task list under §G15
Phase 1.2 follow-up.

### Phase 1.3 — Three-tier global-price fallback chain

Split into 1.3a (shipped) and 1.3b (deferred):

**1.3a — fallback orchestration (shipped 2026-05-11)**

- `internal/aggregate/global.go::ComputeGlobalPrice` walks the
  three tiers in order:
  1. `vwap_native` — `GlobalPriceReader.LatestVWAP` reads the
     existing `prices_1m` CAGG for the (base, quote) pair. Wins
     when `trade_count >= VWAPMinTradeCount` (default 5,
     matches the existing reduced-redundancy threshold).
  2. `aggregator_avg` — reads via the Phase 1.2
     `Store.LatestAggregatorPricesForPair` reader across
     `external.AggregatorSources()`. Averages the fresh
     observations (< `MaxAggregatorAge`, default 10m). Cross-
     decimal scaling normalises CG-style 8dp observations to the
     14dp common scale before averaging.
  3. `triangulated` — reads the existing Redis triangulation
     looker (`TriangulatedPriceLooker.LookupTriangulated`),
     same Redis-cached implied VWAPs `/v1/price` already
     consults.
- Result type (`GlobalPriceResult`) carries `Price`, `Authority`
  (`vwap_native` / `aggregator_avg` / `triangulated`), `Sources`,
  `AsOf`, and `TradeCount` for transparency.
- Storage errors short-circuit lower tiers — a transient VWAP
  failure must not silently degrade to aggregator. Only "no
  rows" / `ok=false` triggers the next tier.

**1.3b — cross-chain ticker-bucketed VWAP CAGG (deferred)**

The plan's `verified_currency_prices_1m` CAGG bucketed on
`(ticker, quote, bucket)` would normalise Stellar SDEX USDC/XLM
trades + CEX USDC/USD trades into one mixed-quote VWAP. That
requires per-trade FX-anchor multiplication (convert XLM-quoted
trades to USD via the prevailing XLM/USD rate at trade time) — a
distinct algorithm from our existing per-pair VWAP, and
operationally only meaningful when we have non-Stellar-chain
trade data to actually aggregate across.

For today's deployment (Stellar trades + off-chain CEX/FX path
only), tier 1's per-pair VWAP is functionally equivalent — the
"cross-chain" aggregation reduces to whatever's already in
`prices_1m` for the specific (base, quote) requested. 1.3b lands
when one of: (a) we ingest non-Stellar-chain trades (Ethereum
USDC, etc.), (b) Phase 1.4's global view exposes a price-quality
gap the per-pair tier-1 can't bridge, or (c) a customer
explicitly requires a cross-network VWAP surface.

### Phase 1.4 — `/v1/assets/{slug}` global view

Split into 1.4a (shipped) and 1.4b (deferred):

**1.4a — slug dispatch + GlobalAssetView (shipped 2026-05-11)**

- `handleAssetGet` dispatches on the path parameter:
  - Verified-currency slug (`usdc`, `eurc`, `aqua`, …) →
    `handleGlobalAsset`, returning the new `GlobalAssetView`
    wire shape (catalogue identity + `price_usd` /
    `price_authority` / `price_sources` from
    `ComputeGlobalPrice` + `networks[]` with Stellar
    `deep_link` + non-Stellar `contract` / `external_link`).
  - Canonical Stellar asset_id (`native`, `CODE-G…`, `C…`,
    `fiat:CODE`) → existing per-Stellar-asset handler.
- Production wiring binds `aggregate.GlobalPriceReader` to
  `*timescale.Store` + the existing Redis triangulated looker,
  with `external.AggregatorSources()` as the tier-2 source list.
- Deferred fields on the global view (`market_cap_usd`,
  `circulating_supply`, `ath`, `atl`, `change_24h_pct`) are
  not populated yet — those need either Phase 1.2's deferred
  CG catalogue-augmentation worker (for non-Stellar tickers)
  or an operator-wired supply pipeline (for Stellar-anchored
  ones). Schema is forward-compatible: adding these fields
  later is additive.
- `/v1/coins` + `/v1/coins/{slug}` mark themselves deprecated
  via the `Deprecation: true` + `Link: rel="successor-version"`
  headers per RFC 9745 / 8288. No `Sunset` header yet (we
  don't have a firm removal date — Phase 1.5's explorer
  migration sets the schedule).

**1.4b — `/v1/coins` deletion (deferred to after Phase 1.5)**

Actual deletion of the `/v1/coins` and `/v1/coins/{slug}`
routes waits for the explorer to migrate to
`/v1/assets/{slug}`. Per the operator's API+frontend deploy-
order rule (the explorer auto-deploys faster than the API
binary), removing the route before the explorer migration
would break the live explorer. Phase 1.5 lands the explorer
change; 1.4b removes the routes + adds the `Sunset` header
to any operator-pinned stale-version proxy lifetimes.

### Phase 1.5 — Explorer migration (5th PR)

- `/assets` listing renders verified currencies first with
  global data; unverified Stellar-only assets paginate below.
- `/assets/{slug}` renders global view + networks list +
  per-network deep links.
- Verified badge on the global view; warning banner on
  unverified-collision pages.
- Remove every `/v1/coins` consumer in the explorer.

## Design extension — everything-is-an-asset (operator 2026-05-11)

The original plan treated `/v1/currencies` (fiat + USD-pegged
stablecoins acting as fiat proxies) and `/v1/assets` (Stellar-
canonical assets) as two separate surfaces. Operator decision
2026-05-11 supersedes that: **`/v1/currencies` dissolves into
`/v1/assets`**. Every entity served by the API is an *asset* with a
class tag.

### Asset class taxonomy

Each asset carries an `asset_class` field. Initial classes:

- `fiat` — sovereign currencies (USD, EUR, GBP, JPY, MXN, BRL, …).
  Replace `/v1/currencies/us-dollar` with `/v1/assets/us-dollar`.
- `stablecoin` — fiat-pegged crypto (USDC, USDT, EURC, PYUSD,
  MXNe, …). Distinct from `fiat` so the explorer can render the
  peg relationship + depeg-risk surface differently from a pure
  fiat rate.
- `crypto` — non-pegged crypto (BTC, ETH, XLM, AQUA, SOL, …).
- (future) `stock`, `metal`, `fund`, `commodity` — same shape;
  added when ingestion lights up.

Class is operator-curated in the verified-currency catalogue (one
extra field per `verified_currencies.yaml` entry, default
`crypto`). Phase 1.5 explorer migration consumes the class to
render category sections (Fiat / Stablecoins / Crypto / …) on the
landing page.

### Routing implications

- `/v1/currencies` and `/v1/currencies/{ticker}` follow the same
  deprecation pattern as `/v1/coins` (Deprecation + Link headers
  in Phase 1.4a-ish; deletion after the explorer migrates).
- `/v1/assets/{slug}` resolves every class: `usdc` (stablecoin)
  → GlobalAssetView; `us-dollar` (fiat) → GlobalAssetView with
  `asset_class: "fiat"` and an empty-or-FX-only `networks[]`
  (fiat doesn't have on-chain issuances per se).
- The fiat-overlay registry (`internal/canonical/asset_fiat.go`)
  remains the source of truth for which fiat codes are
  allow-listed; the verified-currency catalogue *references* fiat
  by ISO code rather than restating it.

### Scope this phase

Capturing only — no implementation yet. The class field gets
added to `internal/currency/data/seed.yaml` + the Catalogue type
in a future commit (likely bundled with Phase 1.4b's `/v1/coins`
deletion + a parallel `/v1/currencies` deprecation). Phase 1.5
consumes it. Recording here so the next implementation pass has
the design intent.

## Open questions / decisions deferred

- **N for VWAP threshold** — how many trades does a ticker need
  in the window before `vwap_native` wins over `aggregator_avg`?
  Default 5 (matches our existing reduced-redundancy threshold);
  revisit after live data.
- **External-link domains per network** — Etherscan vs
  Blockscout vs Etherscan-fork is operator policy; default to
  Etherscan for ETH, Solscan for SOL, etc., overridable via
  config.
- **Aggregator-class freshness threshold** — at what staleness
  do we stop trusting an aggregator's last tick? Default 10 min
  (within their own update cadence); flag stale beyond that.

## Estimate

~3 weeks of focused work for Phase 1.1 → 1.5. Phase 1.1 is the
foundation and ships within a single session; Phase 1.2-1.5 each
take 2-5 days depending on backfill volume.

## Cross-references

- R-018 in `docs/review-2026-05-10.md`
- ADR-0007 (aggregation policy + cache-key contract)
- ADR-0011 (supply derivation — per-network supply queries hook
  here)
- ADR-0019 (anomaly + freeze policy — applies to global VWAP too)
- ADR-0026 (stablecoin-fiat proxy — the late-binding pattern;
  global view is the same kind of late binding extended to
  per-ticker)
