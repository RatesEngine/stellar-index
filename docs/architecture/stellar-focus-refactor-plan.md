# Stellar-focus refactor plan

> **Status:** Proposed. No code changes yet.
> **Date:** 2026-06-16
> **Author:** cold-audit synthesis (4 parallel auditors: API+wire+SDK,
> currency+aggregate+storage, explorer UI, docs+branding).
> **Goal:** bring the product's *presentation* fully back to "the protocol
> explorer for **the Stellar network**," removing the cross-chain /
> multi-blockchain explorer surface that crept in via R-018 — **without**
> touching the core pricing pipeline.

---

## 1. Executive summary

The drift is **real but shallow and entirely in the application/presentation
layer.** Four independent audits converged on the same conclusion:

- There is **one** root-cause epicentre: the **R-018 multi-network-asset
  model** (`docs/architecture/multi-network-assets-migration.md`, 2026-05-11),
  which models a verified currency as "this ticker, on these N blockchains."
- That model is **mostly deferred / unbuilt.** The only *shipped* artifacts
  are (a) an embedded YAML catalogue with non-Stellar coins + `networks[]`
  arrays, (b) Go projection code that serves them, (c) the public wire shape
  (`GlobalAssetView.networks[]`, `PerNetworkAssetView`), and (d) the explorer
  UI that browses them ( `/networks`, `/assets/{slug}/{network}`, network
  dropdowns, "Blockchain" nav).
- **Nothing in the core pricing pipeline is drift.** CoinGecko / CMC /
  Chainlink reference prices, CEX + FX feeds, the oracle feeds, VWAP / TWAP /
  OHLC / triangulation math, the divergence/anomaly cross-check, and the
  CCTP/Rozo bridge **Stellar-leg** decoders are all in-scope and stay.

### The single most important fact (data safety)

**This refactor requires NO database migration and touches NO stored data.**
The entire cross-chain identity model lives in an embedded YAML
(`internal/currency/data/seed.yaml`) plus Go projection code. There are
**zero** multi-network / cross-chain columns or tables in any migration
(`0001`–`0064`). The `trades`, `soroban_events`, supply, and completeness
tables — everything the operator spent months building up — are untouched.
This satisfies the standing "be very careful around anything destructive"
constraint by construction: the blast radius is YAML + Go + TS + OpenAPI, not
data.

### The one structural decision

We are **pre-v1** (`v0.5.0-rc.*`; no `v1.0.0` tag) with **no production
consumer traffic**. A breaking `pkg/client` / OpenAPI change is *cheap now*
and *expensive after launch*. **Recommendation: do the clean full collapse
now** — remove the `networks[]` / `PerNetworkAssetView` wire shapes outright
rather than the conservative "keep-but-re-document" path. (The conservative
path is documented in §6 as the fallback if we decide to freeze the v1 wire
shape early.)

---

## 2. What is PROTECTED (pricing scope — do NOT cut)

Per the standing guardrail, the following are core pricing scope and
**must survive untouched**. All four auditors confirmed none of these are
drift:

| Area | Why it stays |
|---|---|
| `internal/aggregate/*` (VWAP/TWAP/OHLC/triangulate/outliers/stablecoin/global) | The pricing engine. Explicitly core pricing scope. |
| `internal/divergence/*` + CoinGecko/CMC/Chainlink **reference** prices | Cross-check / divergence / anomaly-detection signals (incl. non-Stellar reference pairs BTC/USD, ETH/USD). |
| `internal/currency` `coingecko_id` / `coinmarketcap_id` fields + `CoinGeckoIDs()` / `CoinMarketCapIDs()` | Load-bearing for `aggregatorPairsFromCatalogue` + the CG/CMC pollers' reference pair set. |
| BTC / ETH (and any other actual reference-pair) ticker→CG-id entries | The divergence layer needs a reference price for the non-Stellar reference pairs. **Keep the mapping, drop the browseable identity.** |
| CEX feeds (`internal/sources/external/*`) + FX pollers | Primary pricing inputs. |
| Oracle feeds (reflector / band / redstone) | Stellar-side oracle ingest. |
| CCTP / Rozo decoders (`internal/sources/cctp`, `…/rozo`) | These index the **Stellar leg** of bridge protocols; the cross-chain attributes are properties of the Stellar events. On-mission. |
| Fiat currency entries + FX-rate path | Stellar pricing quote-units (XLM/EUR, USDC→USD proxy). |
| Stablecoin→fiat proxy (`internal/aggregate/stablecoin.go`) | Aggregator policy, Stellar-relevant. |
| Asset-class taxonomy (fiat / crypto / stablecoin) | Useful for Stellar assets too; keep the enum, drop the "rank fiat against crypto market-cap" framing. |
| `/v1/network/stats` + `NetworkStats` | "Network" = **the Stellar network** (24h vol, markets, ledger). Single-network aggregate, correctly named. |
| Chainlink config, ADR-0028 RWA RedStone feeds, oracle-manipulation-defense docs | Pricing inputs / case studies. |
| The "classic/native + Soroban" framing | That **is** Stellar. Tighten wording only (see §5 Tier 3). |

---

## 3. The drift inventory (by layer)

Everything below serves the cross-chain *explorer* model and is **not** needed
for pricing. Grouped by artifact, with the auditor's verdict.

### 3a. Catalogue data + types (`internal/currency`) — the upstream root
- `data/seed.yaml` — ~14 non-Stellar coins as first-class browseable entries
  (BTC, ETH, SOL, BNB, XRP, ADA, DOGE, AVAX, POL, DOT, LINK, UNI, AAVE, WBTC),
  each with `networks:` (non-Stellar chains) + `external_link` to
  etherscan/solscan. **Split:** keep BTC/ETH (+ any genuine reference pair) as
  a *pricing-only* ticker→CG/CMC-id mapping; drop the `networks[]` arrays and
  the browse-only coins (DOGE/ADA/DOT/UNI/AAVE/WBTC/POL/SOL/BNB/XRP/AVAX).
- `verified.go` `NetworkEntry` + `Networks []NetworkEntry` — the cross-chain
  identity type. Collapse to Stellar-only issuance (code/issuer/asset_id).
- `verified.go` `AssetClass` comment "comparable market cap for ranking
  against crypto" — restate purpose as Stellar-asset classification.
- Fiat M2 market-cap surfacing (`seed.yaml` `circulating_supply` for fiats) —
  the "$42T, ranks #1" CMC-clone projection. Keep the YAML data (harmless),
  stop projecting it as a comparable market cap.
- `doc.go` package doc — reword to "Stellar verified-asset catalogue."

### 3b. Market-cap cache (`internal/currency/marketcap`) — **confirmed presentation-only**
- Whole package (`cache.go`, `refresher.go`) polls CoinGecko market-cap for
  non-Stellar coins to fill the CMC-style `market_cap_usd` column. **Verified:
  consumed ONLY by `internal/api/v1` presentation (assets.go, assets_global.go,
  diagnostics_ingestion.go, server.go); never by `divergence`/`aggregate`.**
  Remove the package + its refresher goroutine + the ~5 read sites.

### 3c. API layer (`internal/api/v1`)
- `assets_global.go` — `GlobalAssetView.Networks[]`, `NetworkView`,
  `PerNetworkAssetView`, `networkViewsFromCatalogue`, `handleAssetByNetwork`,
  `attachCryptoMarketCaps`.
- `assets.go` — `externalNetworks` allowlist (ethereum/solana/polygon/base/
  arbitrum/tron/bitcoin/bsc/avalanche/xrpl), `handleAssetListExternalNetwork`,
  `projectCatalogueForNetwork` (emits `asset_id="ethereum:0x…"`),
  `asset_class=blockchain` chip, `filterCatalogueByNetwork`, the `network=`
  query param + slug-dispatch network plumbing.
- `markets.go` — `expandSlugToAssetIDs` already Stellar+CEX-only in behaviour;
  only the "cross-chain Markets tab" **comments** are drift (reword, keep fn).
- `oracle.go`, `price.go`, `network_stats.go`, `envelope.go`, `coins.go` —
  **clean** ("per-network" there = `native`↔`crypto:XLM` canonical-alias, not
  multi-chain).

### 3d. Public SDK (`pkg/client/types.go`) — **SemVer-major**
- `GlobalAssetView` + `NetworkView` (`Networks []NetworkView`, `Contract`,
  `ExternalLink`), `VerifiedCurrencyListItem` (`NetworkCount`, `Networks`),
  `PerNetworkAssetView` (wholly removable).

### 3e. OpenAPI (`openapi/stellar-index.v1.yaml`) — contract-breaking
- `network` query param (10-chain enum) on `/assets`; `/assets/{asset_id}/{network}`
  path (enum even lists cardano/dogecoin/polkadot); `GlobalAssetView` +
  `NetworkView` + `PerNetworkAssetView` + `VerifiedCurrencyListItem` schemas'
  cross-chain fields. (CoinGecko/CMC refs in divergence/methodology context = keep.)

### 3f. Explorer UI (`web/explorer`) — static export, needs CDN `_redirects`
- **Routes to delete:** `/networks`, `/assets/[slug]/[network]` (merge its
  Stellar deep-dive content back into `/assets/[slug]`).
- **Components:** delete `NetworksPanel.tsx`; remove `AssetsTable` network
  `<select>` + `network` plumbing; remove `VerifiedCurrenciesStrip` "N nets"
  badge + "cross-chain" wording; slim `catalogue.ts` `NetworkEntry`.
- **Nav/footer/search:** rename "Blockchain" dropdown/column; remove "Networks"
  item + search result; fix "every connected network" copy.
- **SEO:** remove `/networks` from `sitemap.ts`; reword cross-chain metadata.
- **Keep:** home stat strip, world-currencies fiat strip, top-assets/movers/
  markets, all price/chart/markets panels, fiat embed widget, verified badge,
  asset-class filter (all pricing).

### 3g. Docs / positioning
- `multi-network-assets-migration.md` — mark superseded; remove "until we
  light up indexing on that chain"; reframe `networks[]` as identity/reference
  anchoring, not a multi-chain-indexing roadmap.
- CLAUDE.md "comprehensive blockchain explorer" → "comprehensive **Stellar**
  explorer (classic/native + Soroban)" (match README's clearer phrasing).
- `explorer-ux-plan.md`, `coins-to-assets-migration.md` — track the re-scope
  (keep all "price anything" pricing language).
- **Do NOT touch** ADRs 0036/0037/0038 (immutable accept-only; their
  framing is Stellar-correct anyway).

---

## 4. Cross-cutting constraints (bind every PR below)

1. **Data-safe by construction.** No migration, no data migration. If any PR
   in this plan proposes a schema change, it is out of scope — flag it.
2. **Wire-breaking = three artifacts in lockstep.** Server handler +
   `openapi/stellar-index.v1.yaml` + `pkg/client/types.go` change together;
   OpenAPI is source of truth (`make docs-api` + contract tests enforce it).
3. **One `pkg/client` SemVer-major bump.** Bundle ALL SDK removals into a
   single major bump (ADR-0005), don't drip breaks.
4. **Client-before-server ordering** (`feedback_api_change_deploy_order`): the
   explorer auto-deploys faster than API releases. Remove/guard the explorer
   surfaces **before or with** the API removal, never after — else the live
   site 404s on fields the API stopped sending.
5. **Static-export redirects.** The explorer is a Cloudflare Pages static
   export with no server redirects — every deleted route needs a `_redirects`
   entry (`/networks → /`, `/assets/:slug/:network → /assets/:slug`).
6. **Commit-merge-repeat** (`feedback_commit_cadence`): one logical unit per
   commit on `main`, build + tests green each step, push sparingly.

---

## 5. Prioritized, merge-as-you-go work plan

Ordered so each unit builds + tests green on its own, lands, then the next
starts. Earlier tiers are zero-risk (no wire break); the wire break is
deliberately sequenced after the UI stops consuming the fields.

### Tier 0 — pre-flight verification (no code change)
- **T0.1** ✅ (done in this session) Confirm `marketcap` is presentation-only
  (it is) and the module is pre-v1 (it is).
- **T0.2** Re-grep `vc.Networks` / `s.marketCaps` consumers at refactor time
  to catch any new read site before deleting.

### Tier 1 — cut the non-Stellar browse surface (YAML + Go projection; **no wire break, no SDK break, no data**)
- **T1.1** Remove `internal/currency/marketcap/` package + refresher goroutine
  (`cmd/stellarindex-api/main.go`) + the read sites in `assets.go` /
  `assets_global.go` / `diagnostics_ingestion.go` / `server.go`. Drop the
  `market_cap_usd` surfacing for non-Stellar coins + fiat-M2 market-cap.
- **T1.2** Remove `externalNetworks` + `handleAssetListExternalNetwork` +
  `projectCatalogueForNetwork` + `asset_class=blockchain` + `filterCatalogueByNetwork`
  + the `network=` param handling in `assets.go`. (Route still exists in
  OpenAPI until Tier 4 — but returns Stellar-only.)
- **T1.3** Strip non-divergence non-Stellar coins + all `networks[]` arrays
  from `seed.yaml`; keep BTC/ETH (+ genuine reference pairs) as ticker→CG/CMC
  mappings. Extract the reference-price map into a pricing-only structure so
  the catalogue becomes a pure Stellar trust registry.

### Tier 2 — remove the explorer cross-chain UI (ships fast via Pages; do BEFORE the wire break)
- **T2.1** Delete `/networks` route + nav/footer/search/sitemap links; add
  `_redirects`.
- **T2.2** Delete `/assets/[slug]/[network]` route; merge Stellar deep-dive
  (issuer/SDEX markets/supply) into `/assets/[slug]`; add `_redirects`.
- **T2.3** Delete `NetworksPanel.tsx`; remove `AssetsTable` network dropdown +
  `network` plumbing; remove `VerifiedCurrenciesStrip` "N nets" badge; slim
  `catalogue.ts`.
- **T2.4** Reword "Blockchain" nav/footer → "Explore"/"Stellar"; copy fixes.

### Tier 3 — collapse the identity model + wire shape (**SemVer-major; lockstep**)
- **T3.1** Replace `NetworkEntry`/`Networks[]` in `verified.go` with
  Stellar-only issuance identity (code/issuer/asset_id).
- **T3.2** Remove `GlobalAssetView.networks[]`, `NetworkView`,
  `PerNetworkAssetView`, `handleAssetByNetwork` from `assets_global.go`; trim
  `VerifiedCurrencyListItem.networks/network_count`.
- **T3.3** OpenAPI: remove the `network` param, `/assets/{slug}/{network}`
  path, and the cross-chain schema fields; `make docs-api`; fix contract tests.
- **T3.4** `pkg/client/types.go`: remove the mirrored types; **one major
  bump.** Regenerate `web/explorer/src/api/types.ts`.

### Tier 4 — docs / positioning sweep
- **T4.1** Reword CLAUDE.md / README / migration docs per §3g; mark
  `multi-network-assets-migration.md` superseded; CHANGELOG entry.

### Optional follow-up
- Consider a short ADR ("Stellar-only explorer scope; reference-price assets
  are pricing-only, not browseable entities") to make the boundary durable and
  prevent re-drift.

---

## 6. Fallback: conservative re-scope (if we choose to freeze the v1 wire shape now)

If we decide **not** to break the v1 API/SDK (e.g. an external consumer is
about to integrate), the docs auditor's lower-risk path is:

- Keep `PerNetworkAssetView` / `networks[]` in the wire, but **re-document**
  the non-Stellar `contract`/`external_link`/`data_quality` fields as
  "verified cross-chain *identity* + reference-price anchor only — Stellar
  Index does not index these chains."
- Still do Tiers 1 + 2 (remove the browse-by-network routes, market-cap
  surfacing, and explorer cross-chain UI) — those don't break the contract.
- Skip Tier 3.

This keeps the contract stable at the cost of leaving a (now-documented,
mostly-unpopulated) cross-chain shape in the public API. **Given pre-v1 +
no consumers, the full collapse (§5) is recommended over this.**

---

## 7. Bottom line

The product is already ~95% Stellar-positioned. The drift is the R-018
cross-chain *asset-explorer* model, and it is shallow: embedded YAML + Go
projection + a wire shape + UI — **no database, no pricing math, no data
loss.** Execute Tiers 1→4 (or the §6 fallback) and the explorer is fully,
consistently "the protocol explorer for the Stellar network," with the
core pricing pipeline (including non-Stellar *reference* prices)
entirely intact.
