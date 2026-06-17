<!--
Public launch announcement. Send T-0 (immediately after the
cut completes per launch-day-checklist.md §T-0 step 6).

Channels:
  - Email to the launch contacts (Stellar + Freighter)
  - Slack: #stellar-index-public
  - Project handle (Twitter / Mastodon / wherever applicable)
  - Customer Slack/Discord presence if applicable

Tone: factual, short, surfaces-first. The reader should be
able to make their first request in under 60 seconds.

Subject line: "Stellar Index — public launch (api.stellarindex.io live)"
-->

# Stellar Index — public launch

The Stellar Index is now live at **{{api_url}}** as of
{{utc_time}}.

## What this is

A Stellar-network pricing API. Aggregated VWAP / TWAP / OHLC
across on-chain DEXs (Soroswap, Aquarius, Phoenix, Comet,
SDEX), CEX feeds (Binance, Coinbase, Kraken, Bitstamp),
oracle networks (Reflector, Redstone, Band), and FX anchors
(ExchangeRatesApi, Polygon Forex). Source code is public at
<https://github.com/StellarIndex/stellar-index>.

## First request

```sh
curl 'https://api.stellarindex.io/v1/price?base=native&quote=fiat:USD' | jq .
```

API documentation: <https://docs.stellarindex.io>.
Getting-started walkthrough: <https://docs.stellarindex.io/getting-started>.
Status: <https://status.stellarindex.io>.

## What's covered today

- All four core surfaces: `/v1/price` (closed-bucket VWAP),
  `/v1/price/tip` (rolling), `/v1/observations` (per-source
  raw), `/v1/history/since-inception`.
- Streaming companions: `/v1/price/stream` (closed-bucket SSE),
  `/v1/price/tip/stream`, `/v1/observations/stream`.
- Asset metadata, supply, market cap, FDV, 24h volume on
  `/v1/assets/{id}`.
- Multi-region (FSN1, us-east-1, Singapore) — every region
  serves byte-identical closed-bucket values per ADR-0015.
- SLA targets: p95 ≤ 200 ms, p99 ≤
  500 ms, ≥ 99.9 % availability, ≤ 30 s freshness.
  Continuous evidence trail via the SLA probe;
  see <https://docs.stellarindex.io/sla>.

## How to get a key

{{onboarding_instructions}}

(Anonymous tier is rate-limited at 60 rpm/IP — fine for
exploration; an API key bumps you to 1000 rpm.)

## Reaching us

- Bug reports / feature requests: <https://github.com/StellarIndex/stellar-index/issues>
- Security: `security@stellarindex.io`
- Operational status: <https://status.stellarindex.io>

— {{your_name}}
