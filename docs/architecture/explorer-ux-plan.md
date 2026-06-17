---
title: Stellar Index — explorer UX system (design of record)
last_verified: 2026-06-12
status: Accepted design; phased build
related:
  - docs/adr/0035-factory-anchored-contract-gating.md
  - docs/adr/0033-completeness-verification-model.md
  - docs/protocols/README.md
---

# Stellar Index — explorer UX system

The design of record for stellarindex.io: **the ultimate tool for
exploring the Stellar network and for asset pricing inside and outside
Stellar.** This document decides the full logical system — information
architecture, canonical pages, navigation, cross-linking model, trust
surfaces, and build phasing.

## 1. The system in one idea

Stellar Index is **an index in the literal sense**: every entity on the
network has exactly one canonical page, every page is built from
verified data, and every fact on every page links to the API call that
produced it. Three lenses over one dataset:

1. **Prices** — what is anything worth? (Stellar assets + global
   crypto/fiat/RWA we already ingest: CEX feeds, 516 Chainlink feeds,
   ECB FX, RedStone RWA.)
2. **Protocols** — what is happening in Stellar DeFi? (Per-protocol
   deep dives backed by our per-source tables + the ADR-0035 verified
   contract registries.)
3. **Network** — what happened on chain? (Ledgers, transactions,
   accounts, contracts, events — classic + Soroban, backed by the
   certified lake.)

Bound together by a fourth, brand-defining lens:

4. **Coverage** — why should you believe it? (ADR-0033 completeness
   verdicts, ADR-0035 verification pages, methodology — rendered as
   product, not buried docs.)

### The three differentiators every screen leans on

- **Protocol-aware attribution.** We are the only Stellar tool that can
  say "this contract IS a Blend V2 pool, deployed by this factory at
  this ledger, and here is every event it ever emitted, decoded." The
  ADR-0035 registry is the hinge between the generic Network lens and
  the Protocols lens.
- **Provable completeness.** Every panel carries a coverage badge
  backed by completeness_snapshots — not a marketing claim, an audit
  verdict. No other explorer can render "this table is verified
  complete from ledger X."
- **Independent cross-venue pricing.** VWAP across CEX + SDEX + AMMs
  with confidence scoring, oracle cross-checks, and OHLC back to 2015 —
  for Stellar assets AND the global assets we already price.

## 2. Personas → primary jobs

| Persona | Primary jobs | Primary lens |
|---|---|---|
| Trader / analyst | price, charts, markets, divergence (CEX vs DEX), liquidations | Prices, Markets |
| DeFi user | pool/vault health, reserves, flows, liquidation risk, bridge volumes | Protocols |
| Developer / integrator | API per panel, contract pages, event streams, webhooks | Network, every panel's API affordance |
| Issuer / asset team | their asset page, supply, markets, verified badge | Prices |
| Protocol team | their protocol page, coverage proof, contract registry confirmation | Protocols, Coverage |
| Researcher | network stats, full-history OHLC, protocol comparisons | all |

## 3. Information architecture

### Top navigation (five sections + omnibox)

```
[Stellar Index]  Prices  Markets  Protocols  Network  Coverage     [⌘K Search] [API] [●status]
```

### Universal search (⌘K omnibox)

Type-detection routing — the front door of the whole product:

| Input shape | Routes to |
|---|---|
| `G...` (56 chars) | /account/{g} |
| `C...` (56 chars) | /contract/{c} — protocol-attributed if in registry |
| 64-hex | /tx/{hash} |
| integer | /ledger/{seq} |
| asset code / name / ticker | asset results (grouped: verified first, then by class) |
| `XLM/USD`-shaped | /pair/{base}/{quote} |
| protocol name | /protocols/{name} |
| anything else | grouped full-text results |

### Canonical entity pages (one URL per thing, everything cross-links)

```
/asset/{id}                    unified: Stellar assets, global crypto, fiat, RWA
/pair/{base}/{quote}           canonical pair (VWAP + per-venue breakdown)
/protocols                     directory
/protocols/{name}              protocol deep dive
/protocols/{name}/{instance}   pool / vault / market / feed instance
/network                       network pulse
/ledger/{seq}  /tx/{hash}  /op/{id}
/account/{g_strkey}
/contract/{c_strkey}           THE HINGE (see §6)
/source/{venue}                CEX / on-chain venue page
/oracles  /oracles/{name}      oracle hub + per-oracle feed boards
/coverage                      trust center
/coverage/{source}             per-source verdict + verification page
```

Migration from current routes: /assets/* → /asset/* (redirects),
/dexes + /lending + /exchanges fold into /protocols + /source,
/markets/[pair] → /pair, /research stays (rendered docs), /convert,
/widgets, /embed, /divergences, /anomalies stay as Prices subsections.

## 4. Lens 1 — Prices

**/prices (assets index).** One table, one namespace, faceted: network
(Stellar / off-chain / multi-chain), class (crypto / stablecoin / fiat /
RWA), verified-only toggle, venue coverage, 24h volume/change. Stellar
assets and BTC/ETH/EUR/BENJI live in the SAME table — the point is
"price anything," not "Stellar only."

**/asset/{id} (the money page).** Tabs:
- **Overview** — price (closed-bucket, per ADR-0015/0018 contract),
  confidence score with "why this price?" expander (contributing
  sources, class policy, freeze/anomaly state, exact bucket), supply
  (circulating/total/max from the three-algorithm pipeline), identity
  (verified-catalogue badge, SEP-1 metadata, issuer link, networks[]
  for multi-chain assets).
- **Charts** — OHLC/TWAP/VWAP, granularity 1m→1d, **history to 2015**
  for majors (headline capability), compare overlay (vs another asset,
  vs an oracle feed).
- **Markets** — every venue trading it: SDEX books, AMM pools (links
  into /protocols/{name}/{instance}), CEX listings; per-venue price,
  volume, share-of-VWAP.
- **On-chain** — Stellar-specific: issuer, trustlines, SAC wrapper,
  holders-derived stats, transfers (/contracts/{id}/transfers exists),
  supply observer detail.
- **Oracles** — what every oracle says vs our VWAP: Reflector (3
  contracts), RedStone, Band, Chainlink — price, freshness, divergence,
  with the divergence-alert state.
- **API** — every endpoint that serves this asset, copy-paste curl.

**/pair/{base}/{quote}.** The canonical market view: VWAP line +
per-venue candles, venue share, trades tape (live SSE), triangulation
path when indirect ("XLM→USDC→EUR via …" — the convert engine).

**/convert/{from}/{to}** (exists) — uses the same triangulation; show
the path and the bucket timestamp, never an in-progress price.

**/oracles** — the oracle terminal: all feeds × all oracles, freshness
heatmap, divergence-vs-VWAP board. Unique: nobody renders Reflector vs
RedStone vs Band vs Chainlink side by side.

## 5. Lens 2 — Protocols (the new pillar)

**/protocols (directory).** Card per protocol: coverage badge (verified
complete since ledger X / verified-within-window / enumerated-pending),
24h events + volume, instance count (from protocol_contracts), event
types decoded (the EVERY-event list). Sectioned: DEX/AMM (SDEX,
Soroswap, Aquarius, Phoenix, Comet), Lending (Blend), Yield (DeFindex),
Bridges (CCTP, Rozo), Oracles (Reflector, RedStone, Band), Token layer
(SEP-41 transfers/supply).

**/protocols/{name} (deep-dive template).** Consistent skeleton, with
protocol-specific panels where the data is unique:

- **Header**: identity, verified factories (the ADR-0035 trust roots,
  with the multi-factory story rendered: "2 factories, 27 pools, all
  lake-verified"), genesis ledger, coverage badge → /coverage/{name}.
- **KPIs**: 24h/7d volume, events/day, active instances, protocol-TVL
  where derivable (AMM reserves from sync events; Blend supplied/borrowed).
- **Instances table**: pools/pairs/vaults/markets with per-instance
  volume, reserves, last activity → instance pages.
- **Events explorer**: filterable, live-tailing decoded event tape
  (kind, instance, amounts, tx link) — our per-source tables rendered.
- **Coverage tab**: the docs/protocols verification page rendered as
  product + the live completeness verdict.

Protocol-specific signature panels (the reasons to visit):
- **Blend** — the liquidation terminal: live auctions (new/fill/delete
  with fill-percent curves), bad-debt ledger, per-pool
  supplied/borrowed/collateral flows, emissions. Liquidation data is
  high-value and nobody else surfaces it.
- **Soroswap / Aquarius / Phoenix / Comet** — pools with reserves,
  swap flow, fee/skim events; pair pages link to /pair canonical views.
- **DeFindex** — vaults: deposits/withdrawals, strategy flows
  (vault→strategy mapping once the team confirms fan-out), rebalances,
  fee events.
- **CCTP / Rozo** — bridge flow dashboard: USDC in/out of Stellar over
  time, large transfers, source/destination domains. "Money entering
  and leaving Stellar" is a headline chart.
- **SDEX** — classic DEX: top books, trade tape since 2015.
- **Oracles** — per-feed boards: update cadence, freshness SLA,
  deviation vs our VWAP (links into the oracle terminal).
- **SEP-41 / token layer** — transfer volume by asset, mint/burn
  (supply changes) feed.

**/protocols/{name}/{instance}** — one pool/vault/market: reserves +
composition over time, event history, linked contract page, linked
canonical pair, "verified member" provenance (which factory deployed
it, at which ledger).

## 6. Lens 3 — Network (the explorer pillar)

**/network** — pulse: ledger tip + close-time sparkline, tx + event
throughput, events/min by protocol (stacked), DEX volume now, bridge
net-flow today, fees. (network/stats + ledger/stream exist.)

**Entity pages:**
- **/ledger/{seq}** — header, txs, ops, events; protocol-attributed
  event summary ("3 Soroswap swaps, 1 Blend repay…").
- **/tx/{hash}** — ops, events (decoded where a protocol decoder
  matched, raw XDR otherwise), fee, source account.
- **/account/{g}** — balances/trustlines, tx history, DeFi positions
  view (Blend positions, LP shares, vault shares — derived from our
  per-protocol tables; a unique panel).
- **/contract/{c} — THE HINGE.** Three states:
  1. **Protocol-attributed** (in protocol_contracts / known registry):
     "This is a **Blend V2 pool**, deployed by factory CCZD… at ledger
     51,499,915 — verified" + the protocol instance panel embedded +
     link to /protocols/blend/{instance}.
  2. **Recognized, unattributed**: shape known to the recognition
     audit but not protocol-gated; show raw decoded events.
  3. **Unknown**: raw events + invocations from the lake.
  This page converts our gating work into visible product value.

**Honest phasing**: PG served tier holds the recent window; the CH lake
holds everything to genesis. Phase N1 ships point-lookups (exact
ledger/tx/contract → CH point query is cheap) + recent-window browsing;
Phase N2 ships history-scale browse/filter (needs CH-backed API read
path + pagination design). Never fake it: ranges not yet servable say
so, with the coverage story explaining what exists in the lake.

## 7. Lens 4 — Coverage (trust center)

**/coverage** — the dashboard version of ADR-0033: per-source verdict
table (substrate continuity ✓ / recognition ✓ / projection reconcile ✓,
verified-to watermark, last run), the lake substrate stats (ledgers
contiguous + hash-chained to genesis), incident history.

**/coverage/{source}** — the per-protocol verification page (already
written in docs/protocols/) rendered as product, plus live verdicts and
the "for the protocol team" confirmation CTA.

**Badge system (global pattern).** Every data panel in the product
carries one of: 🟢 *verified complete* (reconciled vs lake) · 🔵
*verified within window* (retention-scoped) · 🟡 *enumerated, pending
verification* · ⚪ *best-effort* (external venue data). Badges link to
/coverage/{source}. This is the brand made visible.

## 8. Global UX patterns

- **API-transparency everywhere** (existing pattern, kept universal):
  every panel has "view API call" → copyable curl + link to docs →
  signup CTA. The explorer IS the API demo; this is the developer
  funnel.
- **Live where it's real**: SSE tickers on price/tip, trades tape,
  ledger tip; everything else closed-bucket with explicit timestamps.
  Never render an in-progress bucket (ADR-0015).
- **Export everything**: CSV/JSON on every table; embed/widget on every
  chart (extends existing /widgets + /embed).
- **Watchlist** (localStorage, no account): pin assets/pairs/pools to a
  /me strip on home. Account-backed alerts come with the platform
  dashboard later.
- **Consistent badges**: verified asset (catalogue), coverage tier,
  frozen/anomaly price state, stale oracle.
- Desktop-first pro tool; mobile gets search, asset pages, and the
  pulse. Dark mode default for the terminal feel; light retained.

**Home page = mission control**: omnibox; network pulse strip; price
board (XLM + majors + movers, confidence-aware); protocol leaderboard
(24h volume/events); bridge net-flow today; trust strip (sources
verified, coverage %, last audit run); recent blog/changelog.

## 9. API surface to build (the real backend work)

Pricing endpoints largely exist. New surface, additive (v1):

```
/v1/protocols                          directory + KPIs + coverage tier
/v1/protocols/{name}                   detail + factories + instances summary
/v1/protocols/{name}/instances         pools/vaults/markets + per-instance stats
/v1/protocols/{name}/events            filterable decoded events (+ SSE stream)
/v1/protocols/{name}/stats             timeseries (volume, events, TVL-ish)
/v1/contracts/{id}                     attribution + identity (registry-backed)
/v1/contracts/{id}/events              decoded events for one contract
/v1/accounts/{g}/positions             cross-protocol DeFi positions
/v1/ledgers/{seq}  /v1/txs/{hash}      network explorer point lookups (CH-backed)
/v1/coverage                           completeness verdicts (public read)
/v1/coverage/{source}                  per-source verdict + registry
/v1/oracles/compare?asset=…            cross-oracle board
```

Backed by: existing per-source PG tables (blend_*, phoenix_*,
defindex_*, trades, oracle_updates, soroswap_*, cctp/rozo_events,
sep41_*), protocol_contracts, completeness_snapshots, and CH point
queries for network lookups. Aggregations get materialized rollup
tables (per-protocol hourly stats) — not live OLAP against the lake.

## 10. Build phases

| Phase | Scope | Backend dependency |
|---|---|---|
| **P1 — IA + Prices unification** | nav restructure, omnibox, /asset + /pair canonicalization (redirects), oracle terminal, badge system v1 (static tiers) | none beyond existing API |
| **P2 — Protocols pillar** | /protocols directory + deep-dive template; ship Blend (liquidation terminal) + Soroswap + SDEX first, then Phoenix/Aquarius/Comet/DeFindex/CCTP/Rozo/oracles/SEP-41 | /v1/protocols* + rollup tables |
| **P3 — Coverage center** | /coverage dashboards, verification pages as product, live badges | /v1/coverage (read path over completeness_snapshots) |
| **P4 — Network explorer (point)** | /contract (the hinge), /tx, /ledger, /account + positions | /v1/contracts/{id}, CH point-lookup read path |
| **P5 — Network explorer (history)** | full-history browse/filter, account history | CH-backed paginated reads, rollups |
| **P6 — Power features** | watchlists→alerts, compare tooling, embeds v2, CSV/bulk export | platform accounts integration |

Sequencing rationale: P1 is pure frontend on the existing API. P2 is
the differentiator and converts the ADR-0035 work into product. P3 is
cheap (data exists) and brand-defining. P4 unlocks "explorer" claims
honestly; P5 is the scale step; P6 monetizes attention.

## 11. Decisions log (so we don't re-litigate)

1. One unified asset namespace (Stellar + global) — no separate
   "currencies" world; facets do the work.
2. Canonical entity URLs; /contract is the protocol-attribution hinge.
3. Coverage badges on every panel, backed by real verdicts — never a
   static "trusted" sticker.
4. Closed-bucket-only price rendering everywhere (ADR-0015), explicit
   timestamps, confidence always visible.
5. Protocol pages follow one template + per-protocol signature panel.
6. Network explorer phased point-lookups-first; no fake full-history
   browsing before the CH read path exists.
7. Desktop-first, dark-default; API-transparency stays universal.
8. /research (rendered ADRs/architecture) stays — it is part of the
   trust story.
