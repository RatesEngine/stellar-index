---
title: Getting started with Rates Engine
last_verified: 2026-05-03
status: living doc
---

# Getting started

Rates Engine is a public, aggregated, real-time and historical
pricing API for every asset on the Stellar network — native XLM,
classic credit assets, and SEP-41 Soroban tokens. This page walks
you from zero to your first authenticated request in under five
minutes.

> **Hosted endpoint:** `https://api.ratesengine.net` (post-launch)
> **Reference docs:** [`docs.ratesengine.net`](https://docs.ratesengine.net)
> **Status page:** [`status.ratesengine.net`](https://status.ratesengine.net)

## Quick start

```sh
# Current XLM/USD price — no auth required for the free tier.
curl -fsSL https://api.ratesengine.net/v1/price?asset=native&quote=fiat:USD

# 24-hour OHLC for XLM:
curl -fsSL "https://api.ratesengine.net/v1/ohlc?base=native&quote=fiat:USD&from=$(date -u -v-24H +%Y-%m-%dT%H:%M:%SZ)"

# Last-24h trade history for an asset:
curl -fsSL "https://api.ratesengine.net/v1/history?base=native&quote=fiat:USD&from=$(date -u -v-24H +%Y-%m-%dT%H:%M:%SZ)"
```

Every JSON response carries the same envelope:

```json
{
  "data":    { "...": "..." },
  "as_of":   "2026-04-28T12:00:00Z",
  "sources": ["sdex", "soroswap", "binance"],
  "flags": {
    "stale": false,
    "reduced_redundancy": false,
    "triangulated": false,
    "divergence_warning": false
  }
}
```

The `flags` block is the operational quality signal:

| Flag | Meaning |
|---|---|
| `stale` | Response degraded below the surface's documented contract (see [ADR-0018](adr/0018-api-consistency-surfaces.md) for the per-surface baseline) |
| `reduced_redundancy` | Cross-region archive completeness is degraded ([ADR-0017](adr/0017-archive-completeness-invariants.md)) |
| `triangulated` | Reserved for a future triangulated public-serving path; the current Timescale-backed API leaves this false in normal operation |
| `divergence_warning` | Anomaly detection or cross-reference observed a meaningful divergence; treat with caution |
| `frozen` | Anomaly detection refused to publish the new bucket; this response carries the previous bucket's last-known-good value ([ADR-0019](adr/0019-anomaly-detection-and-freeze-policy.md)) |
| `single_source` | Only one source contributed; combined with `frozen` this is the manipulation signature |

## Authentication

The free tier supports anonymous requests at a low rate limit
(60 req/min). Authenticated tiers unlock higher limits + access
to private surfaces (`/v1/observations`, `/v1/account/*`).

```sh
# Issue an API key (returns the raw key once — store it):
curl -fsSL -X POST https://api.ratesengine.net/v1/account/keys \
     -H "Authorization: Bearer $YOUR_SEP10_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"label":"my laptop"}'

# Use the key on subsequent requests:
curl -fsSL https://api.ratesengine.net/v1/account/me \
     -H "Authorization: Bearer rate_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
```

API keys are scoped to a single Stellar account (G-strkey), proven
via SEP-10 Web Auth at issuance time. The account's signature
authorises the key. Rotation is available via `POST /v1/account/keys`;
revocation is not shipped yet in this snapshot.

## Rate limits

| Tier | Anonymous | Authenticated |
|---|---|---|
| Requests / minute | 60 | 1 000 |

Rate-limit headers on every response:

```
X-RateLimit-Limit:     1000
X-RateLimit-Remaining: 987
```

Exceeded limits return `429 Too Many Requests` with `Retry-After`.
Operators on a Postgres outage may see the rate-limit middleware
fail open; the `ratesengine_ratelimit_fail_open_total` counter
ticks during such windows (operators alert on a sustained spike).

## Endpoint families

| Family | URL prefix | Surface (per [ADR-0018](adr/0018-api-consistency-surfaces.md)) |
|---|---|---|
| Asset catalogue | `/v1/assets`, `/v1/assets/{id}`, `/v1/assets/{id}/metadata` | closed-bucket |
| Current price | `/v1/price`, `/v1/price/batch` | closed-bucket |
| Tip price | `/v1/price/tip` | tip (no cross-region consistency) |
| Observations | `/v1/observations` | per-source raw |
| History | `/v1/history`, `/v1/history/since-inception` | closed-bucket |
| Aggregates | `/v1/ohlc`, `/v1/vwap`, `/v1/twap` | closed-bucket |
| Markets / pairs | `/v1/markets`, `/v1/pairs` | closed-bucket |
| Oracle (SEP-40) | `/v1/oracle/lastprice`, `/v1/oracle/x_last_price`, `/v1/oracle/prices`, `/v1/oracle/latest` | closed-bucket |
| Sources | `/v1/sources` | closed-bucket |
| Account | `/v1/account/me`, `/v1/account/usage`, `/v1/account/keys` | private |

The three consistency surfaces are not interchangeable. **Query
parameters never shift the surface** — pick the URL that matches
your needs (see [ADR-0018](adr/0018-api-consistency-surfaces.md)).

## SDKs

| Language | Package | Status |
|---|---|---|
| Go | `github.com/RatesEngine/rates-engine/pkg/client` | v0.x — public-launch hardening |
| TypeScript | (planned) | — |
| Python | (planned) | — |

The Go client is a thin layer over the v1 REST API:

```go
import "github.com/RatesEngine/rates-engine/pkg/client"

c := client.New(client.Options{
    BaseURL: "https://api.ratesengine.net",
    APIKey:  "rek_xxxxxxxx...", // optional; anonymous tier works without
})
env, err := c.Price(ctx, client.PriceQuery{
    Asset: "native",
    Quote: "fiat:USD", // optional; defaults to "fiat:USD" server-side
})
if err != nil {
    // *client.APIError carries Status, Title, Detail, RequestID
    log.Fatal(err)
}
fmt.Printf("%s = %s %s (as of %s)\n",
    env.Data.AssetID, env.Data.Price, env.Data.Quote, env.AsOf)
```

The SDK returns the full `Envelope[T]` shape so consumers can read
`env.Flags.Stale`, `env.Flags.DivergenceWarning`, etc. alongside
`env.Data`. See [`pkg/client/doc.go`](../pkg/client/doc.go) for the
full surface — typed methods today: `Price`, `Assets`, `Asset`,
`AssetMetadata`, `HistorySinceInception`, `Me`, `Usage`,
`CreateKey`. Bulk + chart + market-discovery surfaces (`PriceBatch`,
`PriceTip`, `OHLC`, `History`, `Sources`, `Markets`, `Pair`) ship
in the same package as PR queue lands.

## Errors

Errors follow [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html)
(`application/problem+json`):

```json
{
  "type":    "https://api.ratesengine.net/errors/invalid-asset-id",
  "title":   "Invalid asset identifier",
  "status":  400,
  "detail":  "asset_id must match: native | <code>-<G-issuer> | <C-contract> | fiat:<CODE>",
  "instance": "/v1/assets/banana",
  "request_id": "abc123def456"
}
```

`request_id` echoes the `X-Request-ID` response header — include it
in support requests so the on-rotation engineer can correlate
without parsing logs.

## Operational integration

For oracle / on-chain integrations:

- **SEP-40 passthrough** — `/v1/oracle/lastprice` and friends
  return the same data shape Reflector / Redstone / Band oracles
  use on-chain. Use these when you want a drop-in replacement for
  an on-chain oracle's `lastprice()` call without contract
  invocations on every read.
- **SSE streams** — `/v1/price/tip/stream`, `/v1/observations/stream`,
  `/v1/price/stream` push closed-bucket events as they ship.
  Reconnect with `Last-Event-ID` to resume; heartbeats every 15 s.
- **Bulk lookup** — `/v1/price/batch` (GET, ≤ 100 assets) or
  POST (≤ 1000) for portfolios.

## Self-hosting

Rates Engine is Apache-2.0; the full stack runs locally with one
command:

```sh
git clone git@github.com:RatesEngine/rates-engine.git
cd rates-engine
make dev    # docker-compose: TimescaleDB + Redis + MinIO + API
```

Production deployment is documented in
[`docs/operations/archival-node-bringup.md`](operations/archival-node-bringup.md).
The tier-1 deployment runs three geographically-separated archival
nodes per [ADR-0004](adr/0004-tier1-validator-aspiration.md).

## Help

- **Documentation:** [`docs/`](.) in this repo, or the rendered
  reference at [`docs.ratesengine.net`](https://docs.ratesengine.net).
- **Issues:** open one at
  [github.com/RatesEngine/rates-engine/issues](https://github.com/RatesEngine/rates-engine/issues).
- **Security:** `security@ratesengine.net` (do not open a public
  issue for security findings — see [SECURITY.md](../SECURITY.md)).

## What changed recently

See [CHANGELOG.md](../CHANGELOG.md) for the per-release detail or
[GitHub Releases](https://github.com/RatesEngine/rates-engine/releases)
for the operator-facing summaries.
