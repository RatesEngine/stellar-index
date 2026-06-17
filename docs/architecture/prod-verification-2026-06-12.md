---
title: Production API re-verification — 2026-06-12
status: evidence snapshot
last_verified: 2026-06-12
---

# Production API re-verification — 2026-06-12

Re-run of every curl probe from the 2026-05-10 production review
§Appendix B plus fresh production spot-probes, against the live
production API.

- **Base URL:** `https://api.stellarindex.io` (see finding N-5 on
  error-payload host links).
- **Deployed binary:** `v0.5.0-rc.108-65-gb040514d` (built
  2026-06-12T16:48:52Z, commit `b040514d`, `dirty=false`) per
  `/v1/version`.
- **Probe window:** 2026-06-12 22:16–22:27 UTC, 10 s curl timeouts,
  anonymous tier (now `x-ratelimit-limit: 6000`/min).
- **Comparison baseline:** coverage-matrix `Prod` column
  (last verified 2026-05-11 against rc.39).

This document records observations only. `coverage-matrix.md` Prod
cells are NOT yet flipped (that is B2's job, using this as evidence).

---

## 1. Appendix B probe re-runs (R-001 … R-023)

| Probe | Expected (post-fix) | Observed 2026-06-12 | Verdict |
|---|---|---|---|
| R-001 `GET /v1/markets?limit=5` | 200 (was 500 cold-cache) | **200 in 0.14 s**, volume-desc sorted (BTC/USDT first) | **PASS** |
| R-002 `GET /v1/markets?source=binance&limit=3` | 200 | **200 in 0.17 s** | **PASS** |
| R-003 `GET /v1/pools?limit=5` | 200 (no 8 s cold wait) | **200 in 0.15 s**, sdex pools w/ volume + last_price | **PASS** |
| R-004 `GET /v1/pools?source=soroswap&limit=3` | 200 | **200 in 0.08 s**, soroswap pools | **PASS** |
| R-005 `GET /v1/price/batch?asset_ids=USDC-GA5Z…` | non-empty (was silent `data:[]`) | **200, 1 row** `price=1.000000000000, price_type=peg` | **PASS** (note: envelope `flags.stale=true` on the peg row — informational, see N-8) |
| R-005b `GET /v1/price?asset=USDC-GA5Z…&quote=fiat:USD` | 200 with price | 200, `1.000000000000` | **PASS** |
| R-006 F2 supply fields | populate for watched assets (was NULL everywhere) | `/v1/coins/native` is **404 (route dissolved by assets-unification — by design)**; on `/v1/assets/native`: `market_cap_usd=9429715369.12`, `fdv_usd` set, `circulating/total/max_supply=500018068120000000`, `supply_basis=xlm_sdf_reserve_exclusion`. USDC also populates (`market_cap_usd=40594088.52`) | **PASS** (operator gap #97 closed; matrix cells citing `/v1/coins/*` are stale) |
| R-007 `GET /v1/ohlc?base=native&quote=fiat:USD…` high | sane (~spot), not $1.00 | `high=0.1901306240` vs spot 0.1887; `flags.triangulated=true` | **PASS** |
| R-008 ATH consistency | one consistent ATH across surfaces | **STILL INCONSISTENT — see F-A below.** `/v1/assets/native.ath = {usd: 4.7807…, at: 2025-01-18}`; `/v1/changes/coin/XLM.ath_value = 0.29758… (2026-05-30)`; chart-`all` max = 0.7797 (2021-05-16) | **FAIL** |
| R-009 `GET /v1/auth/sep10/challenge?account=G…` | 200 once seed configured | **503** `"SEP-10 not configured"` — unchanged since rc.39 | **FAIL** (known-open operator action; was already "Still open" in the resolution log) |
| R-010 param naming | one convention or accept-both | `/v1/observations` accepts both `asset=` and `base=` (200/200); `/v1/ohlc` accepts both (200/200) | **PASS** (accept-both shipped) |
| R-011 `GET /v1/observations?asset=native&quote=fiat:USD` | empty + `flags.triangulated=true` (documented fix #1271) | 200, `data:[]`, `flags.triangulated=true` | **PASS** (per documented fix semantics) |
| R-012 `GET /v1/history?base=native&quote=fiat:USD…` | still-open (never claimed fixed) | 200, `data:[]`, **`flags.triangulated=false`** (inconsistent with R-011's flag); direct pair `base=native&quote=USDC-G…` returns 1000 rows | **⚠ still open** (documented; flag inconsistency noted) |
| R-013 `/v1/chart` 1y / all truncation | `truncated` + `data_starts_at` fields; honest about retention | `1y×1h = 8,681 pts` back to 2025-06-12; `all×1h = 46,791 pts` back to **2021-02-01**; `truncated=false` (correct — data covers the request) | **PASS** (backfill executed; fields present) |
| R-016 `sep1_status` vs `/v1/issuers` | `/v1/assets/{id}` inlines SEP-1 home_domain | `/v1/assets/USDC-G….home_domain = "centre.io"` inline ✓ (matches `/v1/issuers` `centre.io`/`Circle`); `sep1_status="not_fetched"` (no longer the contradictory `not_applicable`) | **PASS** (R-017 inline also satisfied; `org_name`/`name`/`image` still null inline — minor) |
| R-019 anonymous rate limit | ≥1000/min or documented tiering | **`x-ratelimit-limit: 6000`** on anonymous tier | **PASS** (was 60) |
| Latency bench (30× `/v1/price`) | p95 ≤200 ms, p99 ≤500 ms | fresh-connection (TLS each call): p50=112 ms, p95=475 ms, p99=1198 ms; **keep-alive: p50=82 ms, p95=124 ms, p99=271 ms**; server-side `/v1/status.latency` (5-min window): p50=26 ms, **p95=86 ms**, p99=755 ms | **PASS on p95** (warm-path); see §3 acceptance notes for p99 caveat |

R-014 (default sort) verified inside R-001: volume-desc. R-015,
R-018, R-020 were additive backlog, not re-probed as pass/fail.
R-021 (timeout → 503) untestable — no cold-cache window observed;
every endpoint answered <2 s. R-022 (oracle/latest) re-probed in §2.
R-023 `/v1/methodology` → **200** with machine-readable aggregation
policy — **PASS** (was 404).

## 2. Fresh key production spot probes

| Probe | Expected | Observed | Verdict |
|---|---|---|---|
| `GET /v1/price?asset=native&quote=fiat:USD` | USD quote, closed-bucket timestamp | 200; `price=0.18867…`, `price_type=vwap`, `window_seconds=60`, `observed_at` minute-aligned (closed bucket per ADR-0015), `sources=[bitstamp,coinbase,kraken]` | **PASS** |
| `GET /v1/price/tip?asset=crypto:XLM&quote=fiat:USD` freshness | `observed_at` within ~30 s | **lag 0.0 s** (rolling-window VWAP, `window_seconds=5`, sub-second `observed_at`) | **PASS** |
| `GET /v1/price/tip?asset=native&quote=fiat:USD` freshness | same ≤30 s | **lag 61–113 s** over 10 samples (`observed_at` minute-bucketed, `window_seconds=60`) — the rolling-window path misses the `native`↔`crypto:XLM` alias and falls back to the closed-bucket reader | **FAIL** — see F-B |
| `GET /v1/price/batch?asset_ids=native,USDC-G…,crypto:BTC` | 3 rows | 3 rows (`vwap`/`peg`/`vwap`) | **PASS** |
| `/v1/ohlc` single-bar (default mode) | one bar from raw trades | 200; sane o/h/l/c, `trade_count`, `truncated=false`. Note: legacy `timeframe=`/`granularity=` params are **silently ignored** (spec'd params are `from`/`to`/`interval`) — all five "granularity" calls returned the identical trailing-1h bar | **PASS** (with unknown-param-tolerance caveat, N-7) |
| `/v1/ohlc?interval=` series, 1m/15m/1h/4h/1d | closed CAGG bars per interval | All 5 intervals → 200 with bars for `crypto:XLM/fiat:USD`, `native/USDC-G…`, `crypto:BTC/crypto:USDT` | **PASS** (direct pairs) |
| `/v1/ohlc?base=native&quote=fiat:USD&interval=1h` | bars (XLM/USD is the flagship pair) | **200 but `intervals: []` for every interval** — while `crypto:XLM/fiat:USD` returns full bars. XLM dual-form alias gap in the series reader | **FAIL** — see F-C |
| `GET /v1/history/since-inception?asset=native&quote=fiat:USD` | data reaching back years | 200; daily points from **2021-02-01** → today (`granularity=1d`). Same for `native/USDC-G…` | **PASS** (5+ years; note "inception" for the 1d CAGG is 2021-02-01 on this surface, not 2015 — N-6) |
| `GET /v1/assets?limit=5` pagination | stable paging | 200; `pagination.next` cursor; page 2 via `?cursor=` returns the next 5 distinct assets with a new cursor. (`?offset=` is silently ignored — cursor is the contract) | **PASS** |
| `GET /v1/assets/USDC-GA5Z…` (classic) | code/price/type/issuer/home_domain | `code=USDC`, `issuer=GA5Z…`, `type=classic`, `price_usd=1.0006…`, `home_domain=centre.io`, `market_cap_usd` populated | **PASS** |
| `GET /v1/assets/CAS3J7…OWMA` + `CCW67T…MI75` (soroban) | type/contract | `type=soroban`, `contract_id` echoed, `decimals=7` — but **`code` is null** on soroban assets (and on `native`) | **PASS** (⚠ `code` gap — N-4) |
| `GET /v1/oracle/latest` | per-R-022 partial: 400 steering to `/v1/oracle/streams` | no-args → 400 with explicit pointer; `?asset=native` → 200 (band row, E9 decimals); `/v1/oracle/streams` → 200 full per-(source,asset,quote) snapshot | **PASS** (per documented R-022 resolution) |
| SEP-40 trio `lastprice`/`prices`/`x_last_price` | 200 each | 200 / 200 / 200, `{price, timestamp}` shapes | **PASS** |
| SSE `GET /v1/price/stream?asset=native&quote=fiat:USD` | first event ≤10 s | `:connected` + **3 `price_update` events (300/3600/86400 s windows) within ~1 s** | **PASS** |
| SSE `GET /v1/price/tip/stream?asset=crypto:XLM…` | tip events | `tip_update` events arriving ~5 s cadence — but event payload `observed_at` is **minute-bucketed (lag ~99 s)**, unlike the GET endpoint's rolling-window 0 s | **⚠** (stream path serves the closed-bucket fallback, same alias/fallback family as F-B) |
| 24 h % change presence | per-asset change fields | `/v1/assets/native.change_{1h,24h,7d}_pct` populated; `/v1/changes/coin/XLM` full delta set (h1/h24/d7 + ath) | **PASS** |
| Base+quote volume in USD | volume fields populated | `/v1/vwap?…&window=300s` → `base_volume`, `quote_volume`, `trade_count`, `outliers_filtered`; `volume_24h_usd` on markets/pools/assets/network-stats | **PASS** (⚠ `window` now requires duration units — bare `window=300` is a 400; May probes used `300`. Contract change, N-3) |
| `GET /v1/markets` | volume-sorted markets | PASS (see R-001) | **PASS** |
| `GET /v1/sources` | source registry | 200; **26 sources** (was 21): +cctp, +rozo, +defindex, +soroswap-router, +chainlink, +ecb, +cryptocompare, +coinmarketcap; classes now include `bridge`/`router` | **PASS** |
| `GET /v1/account/usage`, `/v1/account/keys` | auth required | 401 | **SKIPPED(auth)** |
| `GET /v1/healthz`, `/v1/status`, `/v1/version`, `/v1/network/stats`, `/v1/incidents`, `/v1/sac-wrappers`, `/v1/assets/verified`, `/v1/lending/pools`, `/v1/assets/usdc` (slug), `/v1/assets/fiat:EUR` | 200 each | all 200; `network/stats`: `assets_indexed=190,893`, `latest_ledger=63,002,415`, `volume_24h_usd≈$3.08B` | **PASS** |

## 3. Summary

**Counts:** 30 PASS · 4 FAIL · 2 SKIPPED(auth) · 5 ⚠ caveats noted
inline.

### (a) Regressions / failures vs the matrix's ✅ claims

- **F-A (new, R-008-class): `/v1/assets/{id}.ath` is wrong and three
  surfaces disagree.** `/v1/assets/native.ath = $4.7807 (2025-01-18)`
  — ~6× higher than any price in the system's own chart history
  (chart-`all` max = $0.7797 on 2021-05-16; max around 2025-01-18 ≈
  $0.51). `/v1/changes/coin/XLM.ath_value = $0.2976 (2026-05-30)`
  (window-scoped to its own tracking history). The May fix (#1263,
  day-VWAP ATH) either regressed or never covered the
  assets-unification surface. **Blocks F1/F2 metadata evidence.**
- **F-B: `/v1/price/tip` freshness fails for `asset=native`.**
  `observed_at` lag cycles 61–113 s; only the literal
  `asset=crypto:XLM` form hits the rolling-window VWAP (0 s lag,
  5 s window). Root cause: `tipWindowVWAP` queries raw trades on the
  literal asset form — the rc.89 `assetAliases` loop was applied to
  the *fallback* (`readPriceWithAliases`, F-1340) but not to the
  rolling-window path itself
  (`internal/api/v1/price_tip.go::computeTip` → `tipWindowVWAP`).
  The `/v1/price/tip/stream` events are similarly bucket-stale even
  for `crypto:XLM`.
- **F-C: `/v1/ohlc?interval=` series mode returns 0 bars for
  `native/fiat:USD`** while `crypto:XLM/fiat:USD` returns full
  bars — the same XLM dual-form alias gap
  (`internal/api/v1/ohlc_series.go` does not loop `assetAliases`).
  The granularity evidence (1m/15m/1h/4h/1d) currently only
  passes when the consumer spells XLM as `crypto:XLM`.
- **F-D (carry-over): SEP-10 still 503** ("server signing seed isn't
  configured"). Open operator action since 2026-05-10; the matrix's
  L3.12 ✅ remains production-false.
- **N-5 (error-payload links broken):** every problem+json
  `type` URI and 404 body points at error/doc hostnames that did
  **not resolve** at probe time. Error-payload links from the live
  API resolve nowhere; re-point them at the canonical
  `stellarindex.io` domain.

### (b) Previously-⚠/❌ rows now passing

- R-001/R-002/R-003/R-004 — markets/pools fast 200s, no cold-cache 500s observed.
- R-005 / F5.3 — batch stablecoin peg fallback works.
- R-006 / F2.1–F2.6 / F6.5 — supply fields populate end-to-end (XLM + USDC observed); operator gap #97 closed.
- R-007 / S6.4 — OHLC contamination fixed; `/v1/methodology` documents the 4σ filter.
- R-010 — accept-both param naming on observations/ohlc.
- R-013 / S7.2 / F4.2 / F6.4 / S6.1 — history now reaches 2021-02-01 (5+ years, daily), 1y×1h fully served; F4.2's "≥1 year" is now met on this surface.
- R-014 — volume-desc default sort.
- R-016/R-017 — `home_domain` inlined on `/v1/assets/{id}`.
- R-019 / S9.3 / F5.2 — anonymous tier 6000/min (>the 1000/min minimum).
- R-022 (partial-as-designed), R-023 (`/v1/methodology` shipped).
- S9.2 / F3.1 latency — warm-path p95 well under 200 ms (was 246 ms).

### (c) Verification verdict

| Criterion | Verdict | Evidence |
|---|---|---|
| Staleness ≤30 s on tip | **FAIL as-deployed for `asset=native`** (61–113 s); PASS for `asset=crypto:XLM` (0 s). Fix F-B (alias loop in `tipWindowVWAP` + tip stream), or pre-agree the C4 freshness definition pinning the `crypto:XLM` form. Do not present evidence until one of those lands. | §2 rows 2–3 |
| p95 ≤200 ms | **PASS** on warm connections: client keep-alive p95=124 ms; server-side `/v1/status` p95=86 ms. Cold-TLS per-request curl shows 475 ms — k6 (Workstream C1) should be the authoritative evidence. Server-side p99 (755 ms over a 5-min window that included this probe burst) needs the k6 run to confirm ≤500 ms under steady load. | §1 latency row |
| OHLC timeframes/granularities | **PASS for direct pairs** — 1m/15m/1h/4h/1d all serve via `?interval=` (plus 5m/30m/1w per spec) and `/v1/chart` honours all 7 granularities × 6 timeframes. **Blocked for `native/fiat:USD`** by F-C until the alias fix lands. Legacy `timeframe=`/`granularity=` params on `/v1/ohlc` are ignored, not erred (N-7). | §2 OHLC rows |
| Asset metadata (code/price/type/issuer/contract/home_domain) | **PASS for classic** (all six observed on USDC). **⚠ `code` is null for `native` and soroban assets** (N-4) — XLM's code only appears on the slug surface (`/v1/assets/xlm.ticker`). If the customer reads `code` on `/v1/assets/native`, this is a gap. ATH (F-A) taints the wider F1/F2 metadata story. | §2 asset rows |

### Caveat notes (non-blocking, for B2's matrix re-baseline)

- **N-1:** `/v1/coins/*` is 404 — every matrix Prod cell citing
  `/v1/coins/{slug}` must be rewritten against `/v1/assets/{id|slug}`.
- **N-2:** R-012 family — `/v1/history` on the triangulated literal
  pair returns a silent `[]` with `flags.triangulated=false`
  (inconsistent with `/v1/observations`' flagged empty).
- **N-3:** `/v1/vwap` / `/v1/twap` `window` now requires Go-duration
  units (`300s`); bare integers (used by the May probes and possibly
  by old clients) 400. Breaking param change since rc.39 — confirm
  it's documented in the OpenAPI/CHANGELOG.
- **N-4:** `code` null on `native` + soroban asset details.
- **N-6:** "since-inception" daily data starts 2021-02-01, not 2015
  (`prices_1d`-to-2015 exists in the served tier per the SDEX work —
  this API surface doesn't reach it; verify which is intended before
  presenting F6.4 evidence).
- **N-7:** `/v1/ohlc` silently ignores unknown query params
  (`timeframe`/`granularity`) — consider 400-on-unknown-param for the
  verification demo to avoid a consumer "it ignored my granularity"
  surprise.
- **N-8:** `/v1/price/batch` peg rows set envelope `flags.stale=true`
  while the single-asset endpoint does not — flag-semantics
  inconsistency worth one look before the demo.
- `/v1/status.freshness` reports `active_sources 16 / total 22`
  during the probe window — six sources inactive; cross-check against
  the gap detector before claiming full-source liveness.

---

*Probes run from a single external vantage (residential, via
Cloudflare) on 2026-06-12 22:16–22:27 UTC against
`v0.5.0-rc.108-65-gb040514d`. Raw response bodies retained at
`/tmp/prodverify/` on the probing host for the session; the curl
command lines are reproducible from the tables above.*
