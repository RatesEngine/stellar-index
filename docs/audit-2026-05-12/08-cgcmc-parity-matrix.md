# CoinGecko / CoinMarketCap Parity Matrix

This is the parity audit checklist. Each row is one feature that
CG or CMC ships and that consumers expect any rates aggregator to
ship. Mark each:

- `covered` — we ship it; cite proof
- `partial` — we ship some of it; specify the gap
- `gap` — we don't ship it; needs a finding
- `non-goal` — explicit product decision; cite the decision
- `n/a` — feature is structurally impossible for our scope

A `gap` row of severity `high` is launch-blocking. A `partial`
row needs a finding describing the gap precisely.

## Rationale for the matrix

CG/CMC are the consumer's mental model for "what an asset price
API returns." Anything we don't match becomes an integration
friction point. The matrix is intentionally aggressive — non-goals
must be deliberate, not accidental.

## A. Asset directory + identity

| # | Feature (CG/CMC) | Our surface | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| A.1 | Asset list with paginated cursor | `/v1/assets` (and `/v1/assets/verified`) | covered | server.go:722,727; live R1 200 | none |
| A.2 | Per-asset detail page | `/v1/assets/{id-or-slug}` (dual shape) | covered | server.go:728; live EV-1209 | none |
| A.3 | Slug + ticker dual lookup | `/v1/assets/{slug}` GlobalAssetView vs `/v1/assets/{asset_id}` AssetDetail | covered | EV-1209 USDC=GlobalAssetView; EV-1208 native=AssetDetail | none |
| A.4 | Cross-chain identity (USDC across chains) | GlobalAssetView `networks[]` | covered | EV-1209 USDC `network_count: 6` | none |
| A.5 | CG ID + CMC ID cross-reference per asset | `coingecko_id`, `coinmarketcap_id` fields on verified assets | covered | EV-1209 (`coingecko_id:"usd-coin", coinmarketcap_id:"3408"`) | none |
| A.6 | Asset categorisation (stablecoin, governance, etc.) | `class` field (crypto/stablecoin/fiat) | partial | EV-1209 (`class:"stablecoin"`); no governance/RWA/L1 sub-class | F-CGCMC-A006 |
| A.7 | Verified / trusted-issuer signal | `/v1/assets/verified`, verified-badge UI | covered | EV-1209 (`verified_issuer:"Circle (centre.io)"`) | none |
| A.8 | Search by partial ticker / name | `web/explorer/src/components/nav/SearchModal.tsx` (uses `/v1/currencies` — DEAD) | gap | F-1201/F-WEB-1001 | F-CGCMC-A008 |
| A.9 | Asset logo / image URL | not in `/v1/assets` envelope | gap | grep `logo\|image_url` envelopes | F-CGCMC-A009 |
| A.10 | Description / project links / whitepaper URL | not in envelope | gap | same | F-CGCMC-A010 |
| A.11 | Audit / compliance flags (e.g. CMC's "warning") | `/v1/assets/{id}` warning on unverified collisions per R-018, but no compliance flag | partial | `internal/api/v1/assets_global.go` collision warning | F-CGCMC-A011 |
| A.12 | Asset platform / parent chain | implicit via networks[] | covered | EV-1209 networks each have `network` field | none |
| A.13 | Asset categories (DeFi, Layer-1, RWA, etc.) | not in envelope | gap | grep `category\|categories` | F-CGCMC-A013 |

## B. Price + market data

| # | Feature | Our surface | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| B.1 | Spot price `asset/quote` | `/v1/price?asset=...&quote=...` | covered | server.go:743; live R1 200 | none |
| B.2 | Multiple-quote pricing | `/v1/price/batch` (GET + POST) | covered | server.go:770,774 | F-1236 (POST not in OpenAPI) |
| B.3 | Multi-asset batch price | same | covered | server.go:770,774 | F-1236 |
| B.4 | Price tip / latest observation | `/v1/price/tip`, `/v1/price/tip/stream` | covered | server.go:748,753 | none |
| B.5 | OHLC chart (multiple windows) | `/v1/chart`, `/v1/ohlc` | covered | server.go:786,789; 7 CAGGs (1m/15m/1h/4h/1d/1w/1mo) | none |
| B.6 | Market cap (current) | `/v1/assets/{id}` market_cap field | covered | rc.42 fix; assets_f2.go | none |
| B.7 | Fully-diluted market cap | not in envelope | gap | grep `fdv\|fully_diluted` returns nothing | F-CGCMC-B007 |
| B.8 | Market cap chart over time | `/v1/chart?price_type=market_cap` (rc.46) | covered | rc.46 commit `d91bbdc5`; chart.go | none |
| B.9 | 24h volume | `/v1/markets`, `/v1/assets/{id}` | covered | EV-1210 markets envelope `volume_24h_usd` | none |
| B.10 | Price change % (1h/24h/7d/30d/1y) | rc.47 AssetDetail extension `change_percent_1h/24h/7d/30d/1y` | covered (partial) | assets_coin_extension.go | needs verify all windows |
| B.11 | All-time high + date + % from ATH | rc.47 AssetDetail extension | covered | rc.47 commit | none |
| B.12 | All-time low + date + % from ATL | rc.47 AssetDetail extension | covered | rc.47 commit | none |
| B.13 | Sparkline 7d on asset list | rc.47 AssetDetail extension | covered | rc.47 commit | none |
| B.14 | Top markets per asset (where it trades) | rc.47 AssetDetail extension | covered | rc.47 commit | none |
| B.15 | Per-pair last-trade timestamp | `/v1/markets` | covered | EV-1210 (`last_trade_at`) | none |
| B.16 | Volume by venue (CEX vs DEX vs aggregator split) | `/v1/sources?include=stats` + `price_source_contributions` table (migration 0026) | partial | server.go:828; explorer `/sources` page | F-CGCMC-B016 (no per-asset venue split) |
| B.17 | Bid/ask spread | not in any envelope | gap | grep `bid\|ask\|spread` API returns nothing | F-CGCMC-B017 |
| B.18 | Volatility / standard-deviation | `internal/aggregate/baseline` (multi-window per migration 0007/0008) | partial | not exposed via API, only used internally | F-CGCMC-B018 |
| B.19 | Confidence score on price | ADR-0019, `/v1/price` `flags.confidence` | covered | `internal/aggregate/confidence/`, envelope.go | none |
| B.20 | Divergence warning vs reference | ADR-0019, `divergence_warning` flag | covered | `internal/divergence/worker.go`; envelope.go | F-1230 (no depeg test) |
| B.21 | Stale flag | `/v1/price` flags | covered | EV-1208 (`flags.stale`) | none |
| B.22 | Reduced-redundancy flag | `/v1/price` flags | covered | EV-1208 (`flags.reduced_redundancy`) | none |
| B.23 | Triangulated flag | `/v1/price` flags | covered | EV-1210 (`flags.triangulated`) | none |
| B.24 | Trades feed | `/v1/observations`, `/v1/observations/stream` | covered | server.go:758,762 | none |
| B.25 | Order-book snapshot | not exposed | gap | classic SDEX has offer book in migration 0026 but no API surface | F-CGCMC-B025 |

## C. Coverage breadth

| # | Feature | Our surface | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| C.1 | Number of tracked assets | discovered_assets table (migration 0006) + verified currency catalogue | partial | `internal/sources/canonical/discovery/`; not surfaced as a count | F-CGCMC-C001 |
| C.2 | Number of tracked exchanges | external + on-chain sources via registry | covered | `/v1/sources` | none |
| C.3 | Number of fiat conversions supported | ECB + Frankfurter + Polygon + ExchangeRatesAPI | covered | `internal/sources/{forex,frankfurter,external/{ecb,polygonforex,exchangeratesapi}}` | **F-1212** ECB STOPPED on R1 |
| C.4 | Major CEX coverage (Binance, Coinbase, Kraken, Bitstamp) | 4 CEX adapters | covered | `internal/sources/external/{binance,coinbase,kraken,bitstamp}/` | none |
| C.5 | Aggregator/oracle coverage (Coingecko, CMC, Cryptocompare) | 3 adapters (ClassAggregator → divergence-only) | covered | `internal/sources/external/{coingecko,coinmarketcap,cryptocompare}/` | none |
| C.6 | Cross-chain (non-Stellar) coverage | partial via CG/CMC pull (only used as divergence reference) | partial | aggregator policy excludes ClassAggregator from VWAP | F-CGCMC-C006 |
| C.7 | Stablecoin coverage matrix | catalogue + class field + 9 stablecoins in `aggregate/stablecoin.go:24-37` | covered | code grep | none |
| C.8 | Fiat coverage matrix | `internal/currency/data/seed.yaml` (hand-curated) + circulation_data.csv | partial | `//go:embed` confirmed | F-CGCMC-C008 (no per-fiat coverage doc) |
| C.9 | Token decimals correctness | per-asset decimals via SEP-1 / SAC; uniform 10^8 for off-chain | covered | per CLAUDE.md + `internal/scval/scval.go:231-242` | none |
| C.10 | Long-tail asset support (tokens with low volume) | discovery worker + `/v1/assets` includes unverified | covered | `/v1/assets` lists by network (rc.46) | none |

## D. History + reproducibility

| # | Feature | Our surface | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| D.1 | Historical price (custom range) | `/v1/history` | covered | server.go:777 | none |
| D.2 | Since-inception history | `/v1/history/since-inception` | covered | server.go:782 | none |
| D.3 | Daily snapshots (close, open, high, low) | continuous aggregates `prices_1d` | covered | EV-1232 (7 CAGGs) | none |
| D.4 | Volume history per pair | implicit in CAGGs | partial | `prices_1m` carries volume, but no dedicated volume-only endpoint | F-CGCMC-D004 |
| D.5 | Bulk historical export (CSV / JSON) | none | gap | no `/v1/export` route | F-CGCMC-D005 |

## E. Streaming / real-time

| # | Feature | Our surface | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| E.1 | WebSocket price stream | not offered (we use SSE) | non-goal | SSE chosen per ADR; explicitly | none |
| E.2 | SSE price stream | `/v1/price/stream`, `/v1/price/tip/stream` | covered | server.go:767,753 | F-1225 (Cache-Control gap) |
| E.3 | Trade stream | `/v1/observations/stream` | covered | server.go:762 | none |
| E.4 | Order-book stream | not offered | gap | tied to B.25 | F-CGCMC-E004 |

## F. Developer experience

| # | Feature | Our surface | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| F.1 | OpenAPI spec | `openapi/rates-engine.v1.yaml` (165 KB single file, 54 paths) | covered | EV-1225 | F-1211, F-1236 |
| F.2 | Postman collection | `examples/postman/rates-engine.postman_collection.json` | covered | inventory | needs J56 gen-drift verify |
| F.3 | Curl examples | `examples/curl/{01-10}*.sh` | covered | inventory | F-1202 (`04-coins.sh` dead) |
| F.4 | Go client SDK | `pkg/client/{client,endpoints,types,errors,doc}.go` (35KB endpoints, 34KB types) | covered | inventory | none |
| F.5 | TypeScript client SDK | none — explorer hand-rolls in `src/api/` | gap | grep `pkg/client-ts` returns nothing | F-CGCMC-F005 |
| F.6 | Python client SDK | none | gap | — | F-CGCMC-F006 |
| F.7 | Rust / Java SDKs | none | gap | — | F-CGCMC-F007 |
| F.8 | API documentation site | `docs/reference/api/` + explorer | covered (partial) | inventory | needs J55 gen-drift verify |
| F.9 | Interactive API explorer | not in repo | gap | no Swagger UI / Stoplight scaffold | F-CGCMC-F009 |
| F.10 | Sandbox / testnet endpoints | none | gap | no testnet binary surface | F-CGCMC-F010 |
| F.11 | Webhook subscriptions | `internal/notify/webhook.go` exists but no public subscribe API | gap | grep `webhooks` in server.go shows only `/v1/webhooks/stripe` (incoming, not outgoing) | F-CGCMC-F011 |
| F.12 | Rate-limit headers (X-RateLimit-*) | _verify_ | needs_evidence | grep `RateLimit` in middleware | needs follow-up |
| F.13 | RFC 7807 error envelope | `internal/api/v1/envelope.go` (`writeProblem`) | covered | EV-1208 (live RFC 7807 response) | F-1235 (dashboard handlers bypass) |
| F.14 | Versioned API surface (v1) | `/v1/*` prefix on every route | covered | server.go grep | none |
| F.15 | API key self-service (signup, list, revoke) | `/v1/signup` + `/v1/account/keys` + `/v1/dashboard/keys` | covered | server.go:850-853, 877+ | F-1232 (signup IP throttle) |
| F.16 | SEP-10 auth as alternative to API key | `/v1/auth/sep10/{challenge,token}` | covered | server.go:877-878 | F-1224 (replay) |

## G. Trust + transparency

| # | Feature | Our surface | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| G.1 | Public methodology page | `/v1/methodology` | covered | server.go:835; live R1 200 | none |
| G.2 | Per-source contribution disclosure | `/v1/sources` + `price_source_contributions` table | covered | server.go:828; migration 0026 | none |
| G.3 | Outlier-handling disclosure | docs + `internal/aggregate/outliers.go` | covered | code + docs/architecture/aggregation-plan.md | none |
| G.4 | Stablecoin proxy disclosure | ADR-0026 + `/v1/methodology` | covered | adr/0026 | F-1230 (no depeg test) |
| G.5 | Cross-region determinism claim | ADR-0015 + cross-region tools | partial | only R1; tools refuse `<2 regions` | F-1234 |
| G.6 | Public status page | `web/status/src/app/page.tsx` | covered | inventory | F-1245 (no wrangler.toml) |
| G.7 | Public incidents archive | `internal/incidents/data/*.md` + `/v1/incidents.atom` + status page | covered | server.go:697,698; `//go:embed data/*.md` | none |
| G.8 | SLA documentation + measurement | `docs/operations/sla-probe.md` + sla-probe binary | **broken** | F-1221 (textfile not written) + F-1219 (rule not loaded) + F-1223 (probe calls dead routes) | F-1221, F-1223, F-1219 |
| G.9 | Open-source repo (Apache-2.0) | `LICENSE` | covered | inventory | none |
| G.10 | Public ADRs | `docs/adr/` (26 ADRs, 0012 missing) | covered | inventory/adr-inventory.md | T-1206 (0012 mystery) |
| G.11 | Reproducible build | `docker/*.Dockerfile` + `make verify` | covered | W03 | none |
| G.12 | Audit log of admin actions | `internal/platform/audit.go` (interface only) | partial | no `Update`/`Delete` exposed; F-1240 Stripe path doesn't write | F-1240 |

## H. Pricing model + commercial

| # | Feature | Our surface | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| H.1 | Free tier with documented quotas | API tier in platform schema (migration 0027); `internal/api/v1/middleware/ratelimit*` | covered (partial) | tier schema present; no public quota doc | F-CGCMC-H001 |
| H.2 | Paid tier(s) with documented quotas | tier schema + Stripe webhook (`/v1/webhooks/stripe`) | covered (partial) | server.go:854; F-1227, F-1231 | F-CGCMC-H002 |
| H.3 | Self-serve signup | `/v1/signup` | covered | server.go:853 | F-1232 |
| H.4 | Self-serve key management | `/v1/account/keys` (GET/POST/DELETE) + `/v1/dashboard/keys` | covered | server.go:850-853, 877+ | none |
| H.5 | Usage metering visible to user | `/v1/account/usage` | covered | server.go:849; `internal/usage/counter.go` | none |
| H.6 | Billing surface (invoices, payment) | Stripe webhook only — no invoice list endpoint | partial | F-1231 | F-CGCMC-H006 |

## I. Frontend surface (consumer parity)

| # | Feature | Our surface | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| I.1 | Asset detail page (web, deep-linkable) | `web/explorer/src/app/assets/[slug]/page.tsx` | covered | inventory; live | F-1201 (AssetConverter dead-route call) |
| I.2 | Markets page | `web/explorer/src/app/markets/[pair]/` | covered | inventory | F-1215 (slow) |
| I.3 | Search bar (asset / market / pair) | `web/explorer/src/components/nav/SearchModal.tsx` | broken | calls dead `/v1/currencies` | F-1201 / F-WEB-1001 |
| I.4 | Verified-currency badge | rc.4x explorer | covered | EV-1209 verified_issuer field | none |
| I.5 | Sparklines on asset list | rc.4x explorer (`lightweight-charts` dep) | covered | dependency-inventory + AssetDetail extension | none |
| I.6 | OG image / social preview per asset | `satori` + `@resvg/resvg-js` deps + presumably `app/.../opengraph-image.tsx` | needs_evidence | grep web/explorer/src for `opengraph-image` | F-CGCMC-I006 |
| I.7 | Sitemap + canonical URLs + robots | `web/explorer/src/app/sitemap.ts` (broken — calls /v1/currencies) + server.go:900 `/robots.txt` | partial | F-1201 — sitemap broken | F-1201 |
| I.8 | Mobile-first responsive | tailwindcss + Next | covered | dep | none |
| I.9 | Chart drag-zoom + tooltip | `lightweight-charts` dep | covered | dep | none |

## J. Operational parity

| # | Feature | Our surface | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- |
| J.1 | 99.9%+ uptime track record | sla-probe history | **broken** | F-1221 SLA evidence chain | F-1221 |
| J.2 | Multi-region failover | ADR-0008/0015/0016 (R1 only today) | partial | docs only; R2/R3 not deployed | F-1234 |
| J.3 | DDoS protection | Cloudflare (per Caddy trusted-proxy config) | covered | `configs/caddy/Caddyfile.api:41-66` | none |
| J.4 | API key rate-limit fairness across users | ratelimit identity precedence (per-key > per-IP) | covered | `internal/ratelimit/`, middleware | F-1232 (signup IP throttle weak) |
| J.5 | Public uptime page with history | `web/status` | partial | live page exists; data depends on F-1221 SLA evidence | F-1221 |
| J.6 | Maintenance window communication | `deploy/comms/maintenance-window.md` | covered | template exists | none |

## Roll-up

| Section | Total rows | Covered | Partial | Gap | Non-goal | Broken | Blank |
| --- | --- | --- | --- | --- | --- | --- | --- |
| A | 13 | 8 | 2 | 3 | 0 | 0 | 0 |
| B | 25 | 18 | 4 | 3 | 0 | 0 | 0 |
| C | 10 | 7 | 3 | 0 | 0 | 0 | 0 |
| D | 5 | 3 | 1 | 1 | 0 | 0 | 0 |
| E | 4 | 2 | 0 | 1 | 1 | 0 | 0 |
| F | 16 | 8 | 0 | 7 | 0 | 0 | 1 (F.12 needs_evidence) |
| G | 12 | 9 | 2 | 0 | 0 | 1 | 0 |
| H | 6 | 4 | 2 | 0 | 0 | 0 | 0 |
| I | 9 | 6 | 1 | 0 | 0 | 0 | 2 (I.6 + I.7 partial) |
| J | 6 | 3 | 2 | 0 | 0 | 1 | 0 |
| **Total** | **106** | **68** | **17** | **15** | **1** | **2** | **3** |

**Coverage rate: 64% covered + 16% partial = 80% baseline parity.**

The 15 `gap` rows + 2 `broken` rows + 3 needs-evidence cells
total 20 line items requiring follow-up. Of those, the broken
rows (G.8, J.1) and the dead-route gaps (A.8, I.3, I.7) are
**Wave 0 pre-flip blockers** — they tie back to F-1201, F-1219,
F-1221, F-1223 in the master register.

The 7 SDK gaps in F.5–F.11 are deliberate launch-scope choices
(Go SDK ships first; TS/Python/Rust/Java are Wave 2+).
