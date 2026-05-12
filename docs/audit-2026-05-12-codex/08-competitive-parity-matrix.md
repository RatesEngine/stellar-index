# Competitive Parity Matrix

This matrix is a launch-quality gate against the market-data surfaces
customers expect from CoinGecko and CoinMarketCap.

Statuses: `covered`, `partial`, `gap`, `non_goal`, `not_applicable`.

Every row must cite evidence. A `gap` on a launch-claimed surface is a
finding; a high-impact parity gap blocks public launch.

## Asset Directory And Identity

| ID | Surface | Expected Evidence | Status | Finding |
| --- | --- | --- | --- | --- |
| CP-A01 | Paginated asset list | `/v1/assets`, OpenAPI, frontend route | | |
| CP-A02 | Per-asset detail | `/v1/assets/{id-or-slug}`, web detail page | | |
| CP-A03 | Slug, ticker, canonical ID lookup | parser, redirects, collision handling | | |
| CP-A04 | Cross-network identity | `networks[]`, USDC-style mapping | | |
| CP-A05 | External IDs | CoinGecko/CMC IDs where claimed | | |
| CP-A06 | Categories/classes | crypto, stablecoin, fiat, RWA/DeFi if claimed | | |
| CP-A07 | Verified/trusted issuer signal | verified list, badge, API field | | |
| CP-A08 | Search | partial ticker/name search behavior | | |
| CP-A09 | Logos/images | metadata source, CDN/cache behavior | | |
| CP-A10 | Project links/descriptions | SEP-1/docs/API/web source | | |
| CP-A11 | Warning/compliance/scam flags | warning fields, known scam surfaces | | |

## Price, Market Data, And History

| ID | Surface | Expected Evidence | Status | Finding |
| --- | --- | --- | --- | --- |
| CP-B01 | Spot price | `/v1/price`, cache, store, OpenAPI | | |
| CP-B02 | Batch/multiple quote pricing | `/v1/price/batch`, limits, errors | | |
| CP-B03 | Latest/tip observations | `/v1/price/tip`, source freshness | | |
| CP-B04 | OHLC/VWAP/TWAP/chart | `/v1/chart`, `/v1/ohlc`, `/v1/vwap`, `/v1/twap` | | |
| CP-B05 | Market cap/current and historical | asset detail, chart market-cap mode | | |
| CP-B06 | Fully diluted market cap | supply fields and formulas | | |
| CP-B07 | 24h/7d/30d/1y change | API/web fields and tests | | |
| CP-B08 | ATH/ATL and dates | API/web fields and history query | | |
| CP-B09 | Sparkline | API/web list field | | |
| CP-B10 | Top markets per asset | markets endpoint, source attribution | | |
| CP-B11 | Venue volume split | CEX/DEX/oracle/source contribution | | |
| CP-B12 | Bid/ask or order book | SDEX/orderbook support or explicit non-goal | | |
| CP-B13 | Trades feed | `/v1/trades` or observations endpoint | | |
| CP-B14 | Stale/confidence/divergence flags | API flags and aggregation policy | | |
| CP-B15 | Bulk export | CSV/JSON/export support or non-goal | | |

## Coverage Breadth

| ID | Surface | Expected Evidence | Status | Finding |
| --- | --- | --- | --- | --- |
| CP-C01 | Tracked asset count | live/API/store count and docs truth | | |
| CP-C02 | Tracked exchange/source count | `/v1/sources`, adapters, docs | | |
| CP-C03 | Fiat conversion count | currency seed and FX adapters | | |
| CP-C04 | CEX breadth | Binance/Coinbase/Kraken/Bitstamp adapters | | |
| CP-C05 | Aggregator references | CoinGecko/CMC/CryptoCompare source policy | | |
| CP-C06 | Cross-chain coverage | explicit support or product-positioning non-goal | | |
| CP-C07 | Long-tail discovery | discovery worker and catalogue behavior | | |

## Real-Time And Developer Experience

| ID | Surface | Expected Evidence | Status | Finding |
| --- | --- | --- | --- | --- |
| CP-D01 | SSE/WebSocket price streaming | `/v1/price/stream`, reconnect behavior | | |
| CP-D02 | Trade/observation stream | `/v1/observations/stream` | | |
| CP-D03 | OpenAPI | spec matches handlers | | |
| CP-D04 | Postman/curl examples | examples match current API | | |
| CP-D05 | SDK coverage | Go client plus TS/Python/Rust/Java status | | |
| CP-D06 | Interactive docs/explorer | web/docs entry point | | |
| CP-D07 | Sandbox/testnet endpoint | support or non-goal | | |
| CP-D08 | Webhooks | exposed webhook product vs internal-only code | | |
| CP-D09 | Rate-limit headers | middleware behavior and docs | | |
| CP-D10 | Error envelope | RFC7807/problem+json consistency | | |

## Trust, Commercial, And Operations

| ID | Surface | Expected Evidence | Status | Finding |
| --- | --- | --- | --- | --- |
| CP-E01 | Public methodology | endpoint/page/docs truth | | |
| CP-E02 | Source contribution disclosure | table, API, UI | | |
| CP-E03 | Outlier/stablecoin/divergence disclosure | docs and API flags | | |
| CP-E04 | Status page and incidents | web/status, incident archive | | |
| CP-E05 | SLA measurement | SLA probe, history, docs | | |
| CP-E06 | Reproducible build | CI, Docker, release artifacts | | |
| CP-E07 | Free/paid tiers and quotas | pricing page, auth/rate limit config | | |
| CP-E08 | Self-serve keys and usage | dashboard/API/auth lifecycle | | |
| CP-E09 | Billing/payment | platform/billing, Stripe webhook, docs | | |
| CP-E10 | DDoS/proxy posture | Cloudflare/Caddy/trusted proxy evidence | | |
