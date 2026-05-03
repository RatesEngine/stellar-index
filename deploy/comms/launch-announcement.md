<!--
Public launch announcement. Send T-0 (immediately after the
cut completes per launch-day-checklist.md §T-0 step 6).

Channels:
  - Email to the RFP contacts (Stellar + Freighter)
  - Slack: #rates-engine-public
  - Project handle (Twitter / Mastodon / wherever applicable)
  - Customer Slack/Discord presence if applicable

Tone: factual, short, surfaces-first. The reader should be
able to make their first request in under 60 seconds.

Subject line: "Rates Engine — public launch (api.ratesengine.net live)"
-->

# Rates Engine — public launch

The Rates Engine is now live at **{{api_url}}** as of
{{utc_time}}.

## What this is

A Stellar-network pricing API. Aggregated VWAP / TWAP / OHLC
across on-chain DEXs (Soroswap, Aquarius, Phoenix, Comet,
SDEX), CEX feeds (Binance, Coinbase, Kraken, Bitstamp),
oracle networks (Reflector, Redstone, Band), and FX anchors
(ExchangeRatesApi, Polygon Forex). Source code is public at
<https://github.com/RatesEngine/rates-engine>.

## First request

```sh
curl 'https://api.ratesengine.net/v1/price?base=native&quote=fiat:USD' | jq .
```

API documentation: <https://docs.ratesengine.net>.
Getting-started walkthrough: <https://docs.ratesengine.net/getting-started>.
Status: <https://status.ratesengine.net>.

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
- SLA targets per the Freighter RFP: p95 ≤ 200 ms, p99 ≤
  500 ms, ≥ 99.9 % availability, ≤ 30 s freshness.
  Continuous evidence trail via the SLA probe;
  see <https://docs.ratesengine.net/sla>.

## How to get a key

{{onboarding_instructions}}

(Anonymous tier is rate-limited at 60 rpm/IP — fine for
exploration; an API key bumps you to 1000 rpm.)

## Reaching us

- Bug reports / feature requests: <https://github.com/RatesEngine/rates-engine/issues>
- Security: `security@ratesengine.net`
- Operational status: <https://status.ratesengine.net>

— {{your_name}}
