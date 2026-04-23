---
title: API Design — Rates Engine v1
last_verified: 2026-04-22
status: draft — ratified at Week 4 design review
---

# API Design — Rates Engine v1

**Owner:** @ash.
**Ratification target:** end of Week 4.
**Binds:** `openapi/rates-engine.v1.yaml` (source of truth for the
wire contract), `internal/api/` (Go implementation),
`pkg/client/` (Go SDK), `docs/reference/api/` (generated reference).

This doc is the *design intent*; the OpenAPI file is *the contract*.
Where the two diverge, the OpenAPI file wins and this doc gets updated.

---

## 1. Principles

1. **REST first, GraphQL later.** Every endpoint is plain
   `GET/POST` JSON. GraphQL, if ever, wraps REST; it never replaces
   it.
2. **All amounts are strings in JSON.** Never JSON numbers — IEEE
   754 precision loss above 2^53 breaks i128 (ADR-0003).
3. **Every response has an envelope.** Fields always present; client
   code never branches on "is this key present?"
4. **Versioned by URL prefix.** `/v1/...`. Breaking changes bump the
   integer.
5. **One canonical asset identifier format.** A single `asset_id`
   grammar used across every endpoint (§3).
6. **Pagination is cursor-based.** No `offset`/`limit`; no deep-paging
   perf cliff.
7. **Rate limits expressed in headers.** `X-RateLimit-Limit`,
   `X-RateLimit-Remaining`, `X-RateLimit-Reset`.
8. **Server time is authoritative.** Every response includes
   `as_of` (RFC 3339, UTC, millisecond precision).
9. **Errors follow RFC 9457 Problem Details.** Not our own
   error envelope.
10. **Cacheability is explicit.** `Cache-Control`, `ETag`, and
    `Last-Modified` set per endpoint; historical endpoints go
    through CDN.

---

## 2. Base URL + versioning

- Production: `https://api.ratesengine.net/v1`
- Staging: `https://api.staging.ratesengine.net/v1`
- Self-hosted: `http://<host>:3000/v1`

Versioning rules:

- `/v1` frozen on launch. Only additive, backwards-compatible
  changes: new optional query params, new response fields, new
  endpoints.
- Breaking change → `/v2`, parallel-run with `/v1` for 6 months
  minimum, 12 months for heavily-used endpoints.
- `/v1` endpoints marked `deprecated: true` in OpenAPI as soon as a
  `/v2` equivalent lands.

---

## 3. Asset identifier grammar

One format everywhere. The `asset_id` query parameter accepts any of:

| Form | Example | Meaning |
| ---- | ------- | ------- |
| `native` | `native` | XLM (Stellar lumens) |
| `<code>-<issuer>` | `USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN` | Classic asset, code + G-address issuer |
| `<code>:<issuer>` | `USDC:GA5Z…KZVN` | Alias for the above (accepted on input; output normalises to `-`) |
| `<contract_id>` | `CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA` | SAC or any C-prefixed Soroban contract |

**Canonical form (what we emit):** always `<code>-<issuer>` for
classic, always `CAS3J7…` for Soroban. `native` stays `native`.

Strkey validation per SEP-23. 56-char G-prefixed for issuers;
56-char C-prefixed for contracts.

---

## 4. Response envelope

Every 2xx JSON response follows this shape:

```json
{
  "data": { ... },
  "as_of": "2026-04-22T14:30:15.842Z",
  "sources": ["soroswap", "aquarius", "reflector-dex"],
  "flags": {
    "stale": false,
    "reduced_redundancy": false,
    "triangulated": false,
    "divergence_warning": false
  },
  "pagination": { "next": "opaque-cursor" }
}
```

Field semantics:

- `data`: endpoint-specific payload. Always present.
- `as_of`: when the server produced the payload. Millisecond
  precision.
- `sources`: ordered list of contributing sources (DEXes, oracles,
  CEXes). Empty when source breakdown is not meaningful.
- `flags`: advisory quality markers. See
  [HA plan §9](../architecture/ha-plan.md#9-degradation-modes-what-we-promise-under-failure).
- `pagination`: present only on list-returning endpoints. `next` is
  opaque; clients pass it verbatim to the same URL's `cursor=`.

Errors use RFC 9457, not this envelope (§11).

---

## 5. Endpoint catalogue

Grouped by RFP requirement. Numbered IDs (A.1, B.2) map back to the
[coverage matrix](../architecture/coverage-matrix.md).

### 5.1 Health & metadata

| Method | Path | Purpose | RFP |
| ------ | ---- | ------- | --- |
| GET | `/v1/healthz` | Shallow: process up. Returns `200 {"status":"ok"}`. | — |
| GET | `/v1/readyz` | Deep: Timescale + Redis + captive-core reachable. | F3.* |
| GET | `/v1/version` | Build SHA, version, build date. | — |

### 5.2 Asset catalog

| Method | Path | Purpose | RFP |
| ------ | ---- | ------- | --- |
| GET | `/v1/assets` | List every indexed asset (paginated). `?type=classic,soroban` `?code=USDC` `?issuer=G…` | F1.*, S1.1-3 |
| GET | `/v1/assets/{asset_id}` | Asset detail with metadata: code, type, issuer, contract_id, home_domain, decimals, image (optional). | F1.*, S1.1-3 |
| GET | `/v1/assets/{asset_id}/metadata` | Same as above + SEP-1 `[CURRENCIES]` fields (description, image, orgName). | F1.6 |

Response `data` for `/v1/assets/{asset_id}`:

```json
{
  "asset_id": "USDC-GA5Z...KZVN",
  "type": "classic",
  "code": "USDC",
  "issuer": "GA5Z...KZVN",
  "contract_id": "CBIE...LI4F",         // the SAC bridge, if one exists
  "home_domain": "circle.com",
  "decimals": 7,
  "sep1_status": "verified"             // or "missing" / "failed"
}
```

For Soroban-native tokens:

```json
{
  "asset_id": "CAS3J7...OWMA",
  "type": "soroban",
  "code": "XLM",                         // from SAC.symbol()
  "decimals": 7,
  "contract_id": "CAS3J7...OWMA",
  "issuer": null,
  "home_domain": null,
  "sep1_status": "not_applicable"
}
```

### 5.3 Current price

| Method | Path | Purpose | RFP |
| ------ | ---- | ------- | --- |
| GET | `/v1/price?asset_id=&quote=USD` | Current aggregated price. | F1.2, S5.1-4 |
| GET | `/v1/price/batch?asset_ids=A,B,C&quote=USD` | Up to 100 assets in one call. | F5.3 |
| POST | `/v1/price/batch` | Same, body-form; up to 1000 assets. | F5.3 |
| GET | `/v1/price/stream?asset_id=…&quote=USD` | Server-Sent Events. | S5.3 |

Response `data` for `/v1/price`:

```json
{
  "asset_id": "USDC-GA5Z...KZVN",
  "quote": "USD",
  "price": "1.0003",                      // string!
  "price_type": "vwap",                   // one of: vwap, twap, last_trade
  "window_seconds": 300,                  // VWAP window or TWAP window
  "observed_at": "2026-04-22T14:30:12.182Z", // close-time of underlying
                                             // last_trade, or aggregation-
                                             // window end for vwap/twap
  "change_24h_pct": "-0.02",              // aggregator (not yet wired)
  "volume_24h_usd": "42185923.44",        // aggregator (not yet wired)
  "market_cap_usd": "25394841030.00",     // supply derivation (null if unknown)
  "fdv_usd": null,                        // supply derivation
  "circulating_supply": "25394841030.00", // supply derivation
  "total_supply": "25394841030.00",       // supply derivation
  "max_supply": null                      // supply derivation (null if uncapped)
}
```

### 5.4 Historical price + OHLC

| Method | Path | Purpose | RFP |
| ------ | ---- | ------- | --- |
| GET | `/v1/history?asset_id=&quote=&timeframe=1h&granularity=1m` | TWAP/VWAP series. | S6.*, S7.* |
| GET | `/v1/ohlc?asset_id=&quote=&timeframe=24h&granularity=15m` | OHLC candles. | S6.1, F1 |
| GET | `/v1/history/since-inception?asset_id=&quote=&granularity=1d` | Alias for all-time with 1d default. | S6.1, F6.4 |

Query params:

- `timeframe`: `1h | 24h | 1w | 1mo | 1y | all`.
- `granularity`: `1m | 15m | 1h | 4h | 1d | 1w | 1mo`.
- `from`, `to`: optional RFC 3339 timestamps (override `timeframe`).
- `price_type`: `vwap | twap` (default `vwap`).

Response `data` for `/v1/history`:

```json
{
  "asset_id": "XLM-native",
  "quote": "USD",
  "price_type": "vwap",
  "granularity": "1m",
  "points": [
    { "t": "2026-04-22T14:29:00Z", "p": "0.12418", "v_usd": "4281.22" },
    { "t": "2026-04-22T14:30:00Z", "p": "0.12421", "v_usd": "5103.18" }
  ]
}
```

OHLC `/v1/ohlc`:

```json
{
  "asset_id": "XLM-native",
  "quote": "USD",
  "granularity": "15m",
  "candles": [
    {
      "t_open":  "2026-04-22T14:15:00Z",
      "t_close": "2026-04-22T14:30:00Z",
      "o": "0.12412", "h": "0.12438", "l": "0.12401", "c": "0.12421",
      "v_base": "1840222.10", "v_usd": "228512.18"
    }
  ]
}
```

Timeframe × granularity validity is enforced server-side and matches
the table in the Freighter RFP. Invalid combinations → 400 with
`type=https://api.ratesengine.net/errors/invalid-granularity`.

### 5.5 Streaming (SSE)

| Method | Path | Purpose | RFP |
| ------ | ---- | ------- | --- |
| GET | `/v1/price/stream` | SSE channel per asset. | S5.3 |
| GET | `/v1/trades/stream` | Raw trade firehose (authenticated only). | — |

Event shape on `/v1/price/stream`:

```
event: price_update
id: 01HFZTV7JJGYYM4HGBHS3RAKQG
data: {"asset_id":"XLM-native","price":"0.12421","as_of":"2026-04-22T14:30:00.123Z","flags":{...}}
```

`id` is a ULID; resume by passing `Last-Event-ID` header.

### 5.6 Markets (liquidity + venue breakdown)

| Method | Path | Purpose | RFP |
| ------ | ---- | ------- | --- |
| GET | `/v1/markets?asset_id=` | Which venues list this asset; current quote, 24h vol, last trade. | S3.* |
| GET | `/v1/pairs?base=&quote=` | List of direct markets for a pair. | — |

### 5.7 Oracle passthrough (SEP-40)

Others can consume *our* prices via a SEP-40-shaped read surface.

| Method | Path | Purpose | RFP |
| ------ | ---- | ------- | --- |
| GET | `/v1/oracle/lastprice?asset=` | SEP-40 `lastprice` equivalent. | S2.6 |
| GET | `/v1/oracle/prices?asset=&records=N` | SEP-40 `prices`. | S2.6 |
| GET | `/v1/oracle/x_last_price?base=&quote=` | SEP-40 `x_last_price`. | S2.6 |

Decimals and resolution exposed via `/v1/oracle/decimals` and
`/v1/oracle/resolution`.

### 5.8 Admin / self-service (authenticated)

| Method | Path | Purpose |
| ------ | ---- | ------- |
| GET | `/v1/account/me` | API key holder info + quota status. |
| GET | `/v1/account/usage?from=&to=` | Daily usage summary. |
| POST | `/v1/account/keys` | Create a new API key (rotate). |

Self-service signup flow lives at `https://ratesengine.net/signup`;
the API returns the generated key once.

---

## 6. Authentication

Three tiers:

| Tier | Auth | Limit | Who |
| ---- | ---- | ----- | --- |
| Anonymous | none | 60 rpm per IP | Public demo, curious browsers |
| API-key | `Authorization: Bearer rek_<32-char-base58>` | 1000 rpm per key | Default wallet/SDK clients |
| Partner | `Authorization: Bearer rek_<…>` + allowlisted | 10 000 rpm per key | Negotiated — Freighter, LOBSTR, … |

API keys are:

- Generated server-side via CSPRNG; the plaintext is shown **once**
  at creation.
- Stored in Postgres as Argon2id hash.
- Prefixed `rek_` (short for "Rates Engine key") for reverse-grep.
- Scoped: read-only is default; write scopes only exist for
  self-service endpoints (`/account/*`).

SEP-10 (Stellar keypair auth) is **deferred to v1.1**. Tempting but
adds a full challenge-response round-trip to every request unless we
mint a session token; easier to layer in once the basic system is
live.

mTLS for internal service-to-service only (see [HA plan §6](../architecture/ha-plan.md#6-security-posture)).

---

## 7. Rate limiting

- **Algorithm:** token bucket.
- **Storage:** Redis (per-key + per-IP).
- **Window:** per-minute; bucket refills at `limit/60` per second.
- **Burst:** `limit × 1.5` burst capacity per bucket.
- **Scope:** rate limits are per API key (if auth present) or per IP
  (otherwise). X-Forwarded-For is honoured behind HAProxy.
- **Response when limit hit:** 429 with
  `Retry-After` header and an RFC 9457 problem payload.

Response headers on every successful request:

```
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 987
X-RateLimit-Reset: 2026-04-22T14:31:00Z
```

Abuse protection (above-and-beyond rate limits):

- Circuit breaker per client: 5 consecutive 5xx responses → 30 s
  cooldown.
- Request-size limits: 10 KB for `GET` query string, 1 MB for POST
  batches.
- SSE subscription caps: max 50 active streams per key.

---

## 8. Caching

### 8.1 HTTP cache headers

| Endpoint class | `Cache-Control` | Server-side cache TTL |
| -------------- | --------------- | --------------------- |
| `/v1/healthz`, `/v1/version` | `max-age=60, public` | — |
| `/v1/assets`, `/v1/assets/{id}` | `max-age=300, public` | 5 min (Redis) |
| `/v1/price`, `/v1/price/batch` | `no-cache, no-store` | 1-5 s (Redis) |
| `/v1/history` (short timeframes) | `max-age=60, public` | 60 s (Redis) |
| `/v1/history` (long timeframes / `all`) | `max-age=3600, public, immutable` | CDN (Cloudflare) + 1 h Redis |
| `/v1/ohlc` (closed candles) | `max-age=604800, public, immutable` | CDN indefinite |
| `/v1/ohlc` (open candle) | `max-age=5, public` | 5 s (Redis) |

### 8.2 ETag / conditional GET

All cacheable responses emit `ETag: W/"<sha256 of body>"`. Clients
revalidate with `If-None-Match`; server returns 304 without a body.

### 8.3 Why "closed candles are immutable"

A 15-minute candle for `14:15:00-14:30:00` cannot change after
`14:30:05` (5-second settle for late ledgers). We mark the candle
immutable and let CDN pin it for a week. This is the single biggest
cache win and makes the p95 ≤ 200 ms SLA comfortable for historical
endpoints.

---

## 9. Pagination

Cursor-based, opaque to the client.

- Request: `?cursor=<opaque>&limit=<1..500>`.
- Response envelope: `pagination.next` (null if no more results).
- Cursor encodes `(primary_sort_field, primary_key)` signed by an
  HMAC over a server-only secret; prevents clients from mutating
  the cursor to skip ahead.

Default `limit=100`; hard max `limit=500`.

---

## 10. Content negotiation

- `Accept: application/json` — default, what every endpoint emits.
- `Accept: text/event-stream` — only for `/v1/*/stream` endpoints.
- No XML, no protobuf, no msgpack at v1. Revisit if a partner
  asks and has latency numbers that justify.
- `Accept-Encoding: gzip, br` honoured by HAProxy.

---

## 11. Errors (RFC 9457)

Every 4xx/5xx returns:

```
Content-Type: application/problem+json

{
  "type": "https://api.ratesengine.net/errors/rate-limit-exceeded",
  "title": "Rate limit exceeded",
  "status": 429,
  "detail": "You have exceeded your 1000 req/min quota. Try again in 12 seconds.",
  "instance": "/v1/price?asset_id=...",
  "retry_after": 12
}
```

Error types are URL-stable; each `type` URL resolves to a live HTML
page explaining the error. Custom fields are snake_case.

Standard `status` codes:

| Code | Meaning | Example |
| ---- | ------- | ------- |
| 400 | Invalid input | Bad `asset_id` strkey |
| 401 | Missing auth | API key required |
| 403 | Forbidden | Trying to hit admin endpoint without scope |
| 404 | Not found | Unknown `asset_id` |
| 409 | Conflict | Creating a duplicate API key |
| 422 | Semantic error | Invalid timeframe×granularity combo |
| 429 | Rate-limited | |
| 500 | Server bug | |
| 502 | Upstream failed | Timescale primary-replica failover in progress |
| 503 | Service degraded | All indexers down; prices too stale to serve |

We **never** return 500 with a stack trace. Stack traces go to logs;
clients get `instance` + a correlation ID to open a support ticket.

---

## 12. Observability surface (exposed)

- `Server-Timing` header on every response: `cache;dur=1, db;dur=12, compute;dur=2`.
- `X-Request-ID` header (UUIDv7). Clients may set it; if absent we
  generate. Returned as-is.
- `X-Correlation-ID` for chained requests — same across a client's
  request burst when they pass one.

---

## 13. Versioning discipline

Rules enforced in CI (`scripts/ci/lint-openapi.sh`):

- No `required: true` field added to an existing response schema
  (add-only optional).
- No enum value *removed* from a response schema.
- No path removed from `/v1` without a matching `/v2` and a
  migration guide in the release notes.
- `deprecated: true` precedes removal by at least 6 months.

Breaking changes land in a `v2/` spec alongside `v1/`. Both served
concurrently. `/v3` only when we can retire `/v1`.

---

## 14. SDK

- **Go:** `pkg/client/` in this repo. Published as
  `github.com/RatesEngine/rates-engine/pkg/client` (same module
  path as the server; tag with `client/v0.x.y` SemVer).
- **TypeScript:** auto-generated from OpenAPI into
  `https://github.com/RatesEngine/rates-engine-js`. Published to
  npm. Owner: community, with review by @ash for launch version.
- **Python:** post-launch, community-owned.

All SDKs have:

- Strong typing on every request/response (generated from OpenAPI).
- Auto-retry with exponential backoff on 429 / 502 / 503.
- SSE client with `Last-Event-ID` resume support.

---

## 15. Open questions (close by Week 4)

1. **GraphQL layer?** The RFP allows "REST or GraphQL"; we ship REST
   V1. If a customer insists on GraphQL we build a thin wrapper
   (gqlgen or hasura) post-launch.
2. **WebSockets vs SSE?** SSE is simpler operationally; WebSockets
   allow client→server. We have nothing client→server in the
   streaming use case, so SSE wins. Revisit if we add a client-
   pushed "heartbeat" model.
3. **Asset image hosting?** Do we proxy `stellar.toml`'s `image` URL
   or re-host? Probably proxy-then-cache to avoid being a CDN for
   random issuer images.
4. **Webhook callbacks?** Not in V1 scope. Customers who want push
   use SSE.
5. **gRPC?** No. The serving footprint is HTTP-native; gRPC adds a
   binary protocol with no partner asking for it.

---

## 16. OpenAPI spec

Lives at [openapi/rates-engine.v1.yaml](../../openapi/rates-engine.v1.yaml).
Source-of-truth for the wire contract; every implemented handler
has an entry. Remaining paths (price/batch, price/stream,
history/since-inception, account/*, SEP-40 passthrough) are on
the lint's planned_regex allow-list per
[delivery-plan.md](../discovery/delivery-plan.md).
