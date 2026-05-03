---
title: API Design — Rates Engine v1
last_verified: 2026-05-03
status: ratified — `openapi/rates-engine.v1.yaml` is the binding contract; this doc records design intent
---

# API Design — Rates Engine v1

**Owner:** @ash.
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
   `X-RateLimit-Remaining`, and `Retry-After` on 429s.
8. **Server time is authoritative.** Every response includes
   `as_of` (RFC 3339, UTC, millisecond precision).
9. **Errors follow RFC 9457 Problem Details.** Not our own
   error envelope.
10. **Cacheability is explicit.** `Cache-Control` is set per
    endpoint; historical endpoints are the CDN-friendly surfaces.

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
| GET | `/v1/version` | Version, build date, full commit SHA, dirty flag, Go runtime version. | — |
| GET | `/v1/sources` | Source catalogue + class metadata (`exchange` / `aggregator` / `oracle` / `authority_sanity` + `include_in_vwap`). Optional `?class=` filter. | — |

### 5.2 Asset catalog

| Method | Path | Purpose | RFP |
| ------ | ---- | ------- | --- |
| GET | `/v1/assets` | List every indexed asset (paginated). The `?type=classic,soroban`, `?code=USDC`, `?issuer=G…` filter params are reserved in the OpenAPI spec but the handler currently ignores them and returns the unfiltered page. | F1.*, S1.1-3 |
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
  "volume_24h_usd": "42185923.44",        // aggregator (shipped — null until snapshot exists)
  "market_cap_usd": "25394841030.00",     // supply derivation when a snapshot exists
  "fdv_usd": null,                        // supply derivation
  "circulating_supply": "25394841030.00", // supply derivation
  "total_supply": "25394841030.00",       // supply derivation
  "max_supply": null                      // supply derivation (null if uncapped or no snapshot)
}
```

### 5.4 Historical price + OHLC

| Method | Path | Purpose | RFP |
| ------ | ---- | ------- | --- |
| GET | `/v1/history?base=&quote=&from=&to=&limit=` | Raw per-trade history for a pair in a time window. | S6.1 |
| GET | `/v1/ohlc?base=&quote=&from=&to=` | Single OHLC bar for a pair over one window. | S6.1, F1 |
| GET | `/v1/history/since-inception?asset=&quote=&granularity=1d` | Closed-bucket historical VWAP series from the CAGG ladder. | S6.*, S7.* |

Query params:

- `/v1/history`: `base`, `quote`, `from`, `to`, optional `limit`, optional opaque `cursor`.
- `/v1/history/since-inception`: `asset`, `quote`, optional `granularity` (`1m | 15m | 1h | 4h | 1d | 1w | 1mo`).
- `/v1/ohlc`: `base`, `quote`, `from`, `to`. Defaults to the last hour when omitted.

Response `data` for `/v1/history`:

```json
[
  {
    "source": "sdex",
    "ledger": 57211344,
    "tx_hash": "abc123...",
    "op_index": 0,
    "ts": "2026-04-22T14:29:12Z",
    "base_asset": "native",
    "quote_asset": "fiat:USD",
    "base_amount": "10000000",
    "quote_amount": "1241800",
    "price": "0.1241800000"
  }
]
```

Response `data` for `/v1/history/since-inception`:

```json
{
  "asset_id": "native",
  "quote": "fiat:USD",
  "price_type": "vwap",
  "granularity": "1d",
  "points": [
    { "t": "2026-04-21T00:00:00Z", "p": "0.12390", "v_usd": "42185923.44" },
    { "t": "2026-04-22T00:00:00Z", "p": "0.12421", "v_usd": "51031844.18" }
  ]
}
```

OHLC `/v1/ohlc`:

```json
{
  "from": "2026-04-22T14:00:00Z",
  "to": "2026-04-22T15:00:00Z",
  "open": "0.1241200000",
  "high": "0.1243800000",
  "low": "0.1240100000",
  "close": "0.1242100000",
  "base_volume": "184022210",
  "quote_volume": "22851218",
  "trade_count": 184,
  "truncated": false
}
```
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
| GET | `/v1/account/usage?from=&to=` | Usage-summary placeholder; currently returns `[]` until the counter store lands. |
| POST | `/v1/account/keys` | Create a new API key (rotate). |

Self-service signup flow lives at `https://ratesengine.net/signup`;
the API returns the generated key once.

---

## 6. Authentication

Two runtime-auth classes are shipped in this snapshot:

| Class | Auth | Default limit | Who |
| ---- | ---- | ------------- | --- |
| Anonymous | none | 60 rpm per IP | Public/demo callers |
| Authenticated | `Authorization: Bearer rek_<64-hex>` or SEP-10 JWT | 1000 rpm per subject/key | Wallet, SDK, and operator clients |

API keys are:

- Generated server-side via CSPRNG; the plaintext is shown **once**
  at creation.
- Stored in Redis under a SHA-256-derived key; the plaintext itself is
  not recoverable from the stored record.
- Prefixed `rek_` (short for "Rates Engine key") for reverse-grep.
- Scopes are reserved in the record model, but scope enforcement is not
  wired on runtime endpoints in this snapshot.

SEP-10 (Stellar keypair auth) is shipped as the current wallet-facing
auth bootstrap. Clients obtain a challenge, sign it, exchange it for a
JWT, and then use that bearer token on authenticated routes.

mTLS for internal service-to-service only (see [HA plan §6](../architecture/ha-plan.md#6-security-posture)).

---

## 7. Rate limiting

- **Algorithm:** token bucket.
- **Storage:** Redis (per-key/per-subject + per-IP).
- **Window:** per-minute.
- **Scope:** authenticated callers use the authenticated subject/key
  bucket; anonymous callers use the IP bucket. `X-Forwarded-For` only
  influences the anonymous IP identity when the immediate peer is in
  `api.trusted_proxy_cidrs`.
- **Response when limit hit:** 429 with
  `Retry-After` header and an RFC 9457 problem payload.

Response headers on every successful request:

```
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 987
```

---

## 8. Caching

### 8.1 HTTP cache headers

| Endpoint class | `Cache-Control` |
| -------------- | --------------- |
| `/v1/healthz`, `/v1/readyz`, `/v1/version`, `/metrics` | `no-store` |
| `/v1/account/*`, `/v1/auth/sep10/*` | `private, no-store` |
| `/v1/price/tip`, `/v1/observations*` | `private, no-cache, must-revalidate` |
| `/v1/price`, `/v1/price/batch`, `/v1/assets*` | `public, max-age=30, s-maxage=60` |
| `/v1/history*`, `/v1/ohlc`, `/v1/vwap`, `/v1/twap`, `/v1/markets`, `/v1/pairs`, `/v1/sources`, `/v1/oracle/*` | `public, max-age=60, s-maxage=300` |

The current stack does **not** emit `ETag`, `Server-Timing`,
`X-Correlation-ID`, or conditional-GET 304 responses in this
snapshot.

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
clients get `instance` + `request_id` to open a support ticket.

---

## 12. Observability surface (exposed)

- `X-Request-ID` header on every response. Clients may set it; if the
  value is absent or rejected as unsafe/oversize, the server mints a
  fresh 32-character hex token.
- The current stack does not expose `Server-Timing` or
  `X-Correlation-ID` headers in this snapshot.

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

## 15. Open questions — closed

Each was a "close by Week 4 design review" item; all settled:

1. **GraphQL layer?** Out of v1 scope. Tracked as L7.5 in the
   launch-readiness backlog (`⏳ post-launch`); if a customer
   insists, a thin wrapper (gqlgen or hasura) ships then.
2. **WebSockets vs SSE?** SSE. Implemented as `/v1/price/stream`,
   `/v1/price/tip/stream`, `/v1/observations/stream`. We have
   nothing client→server in the streaming use case.
3. **Asset image hosting?** Proxy via `internal/metadata` —
   `stellar.toml`'s `image` field surfaces as `asset.image_url`
   on `/v1/assets/{id}` after SEP-1 verification. We don't
   re-host issuer images.
4. **Webhook callbacks?** Not in v1. Customers who want push
   use SSE.
5. **gRPC?** No. The serving footprint is HTTP-native; no
   partner has asked, and adding a binary protocol earns no
   measurable win on top of the SSE + REST shape.

---

## 16. OpenAPI spec

Lives at [openapi/rates-engine.v1.yaml](../../openapi/rates-engine.v1.yaml).
Source-of-truth for the wire contract — every documented path
is implemented in `internal/api/v1/` (the price/batch,
price/stream, history/since-inception, account/*, and SEP-40
passthrough paths that were on the lint's `planned_regex`
allow-list have all shipped). The 1:1 invariant is enforced by
`scripts/ci/lint-docs.sh` §2 (OpenAPI ↔ handler).
