---
title: Multi-network assets migration — consolidate /v1/coins into /v1/assets
date: 2026-05-11
status: in progress
scope: v1.0 launch-blocking
related_findings: R-018 (docs/review-2026-05-10.md)
last_verified: 2026-05-11
---

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

### Phase 1.2 — CG + CMC connectors (2nd PR)

- `internal/sources/external/coingecko/catalogue.go` — daily
  refresh worker. Fetches `/coins/list` then `/coins/{id}` for
  top N by market cap. Writes to `verified_currencies_external`
  storage table.
- `internal/sources/external/coingecko/prices.go` — 1-5 min
  refresh worker. Fetches `/simple/price?ids=…&vs_currencies=usd`
  for every catalogue entry. Writes to `aggregator_prices`
  hypertable.
- `internal/sources/external/cmc/` — same shape for CMC.
- Both registered in `external.Registry` as
  `Class:Aggregator, IncludeInVWAP: false`. They still don't
  pollute the VWAP; their current-price snapshots feed the
  `aggregator_avg` price-authority tier only.
- Migration: `verified_currencies_external` + `aggregator_prices`
  tables.

### Phase 1.3 — Per-ticker price worker (3rd PR, biggest piece)

- `internal/aggregate/global.go` — per-ticker VWAP computation.
  Reads trades across all `Class:Exchange` sources where one
  side is the ticker. Bucketizes on `(ticker, quote, bucket)`.
- New CAGG `verified_currency_prices_1m` keyed on
  `(ticker, quote, bucket)`. Retention + auto-refresh policies
  matching `prices_1m`.
- Fallback chain implementation:
  1. Read `verified_currency_prices_1m` for the most-recent
     closed bucket.
  2. If row count below threshold, read `aggregator_prices`
     for the latest tick from each `Class:Aggregator` source
     and average.
  3. If neither has data but the ticker has a `?:BTC` pair,
     triangulate via `BTC_USD × ASSET_BTC`.
- Wire the fallback into a new helper
  `internal/api/v1/global_price.go::computeGlobalPrice`.

### Phase 1.4 — `/v1/assets/{slug}` global view (4th PR)

- `handleAssetGet` dispatches:
  - If `{id}` is a verified slug → global view (handled by new
    `handleGlobalAsset`)
  - Else if `{id}` is a canonical Stellar asset_id → existing
    Stellar-network view (unchanged shape; gains
    `unverified_warning` from Phase 1.1)
- New wire fields on the global view: `ticker`, `slug`,
  `market_cap_usd`, `circulating_supply`, `ath`, `atl`,
  `price_usd`, `price_authority`, `sources[]`, `networks[]`.
- `networks[].stellar.deep_link` points at the canonical
  Stellar-network view.
- Drop `/v1/coins` and `/v1/coins/{slug}`. Add Sunset header
  for two weeks before final removal; document migration in
  the release notes.

### Phase 1.5 — Explorer migration (5th PR)

- `/assets` listing renders verified currencies first with
  global data; unverified Stellar-only assets paginate below.
- `/assets/{slug}` renders global view + networks list +
  per-network deep links.
- Verified badge on the global view; warning banner on
  unverified-collision pages.
- Remove every `/v1/coins` consumer in the explorer.

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
