# W11 — API runtime, contracts, streaming, auth

## Scope

Every HTTP route, every middleware, every WS/SSE stream,
every auth gate.

In scope:
- `cmd/ratesengine-api/main.go` (wiring, CORS, trusted proxies,
  rate limit, divergence references)
- `internal/api/v1/` (≈60 handler files; full enumeration below)
- `internal/api/v1/middleware/`
- `internal/api/v1/dashboardauth`, `internal/api/v1/dashboardkeys`
- `internal/api/streaming/` (SSE hub, ring buffer, redis-pub)
- `internal/api/streampublish/`
- `internal/auth/` (apikey postgres + redis, list_keys, signup_tracker,
  store, subject, validators)
- `internal/auth/sep10/`
- `openapi/rates-engine.v1.yaml` (165 KB single file)
- `pkg/client/` (Go client SDK + wire-shape types)

## Inputs

- `inventory/api-route-inventory.md`
- ADR-0018 (API consistency surfaces), ADR-0010, ADR-0020,
  ADR-0025

## Per-handler-file checklist

For every file under `internal/api/v1/`, fill:

| File | Routes | OpenAPI present | Auth gate | Cache | Tests | Status |
| --- | --- | --- | --- | --- | --- | --- |
| `account.go` | | | | | | |
| `assets_coin_extension.go` | | | | | | |
| `assets_f2.go` | | | | | | |
| `assets_global.go` | | | | | | |
| `assets_sep1.go` | | | | | | |
| `assets_verified.go` | | | | | | |
| `assets.go` | | | | | | |
| `auth_sep10.go` | | | | | | |
| `changes.go` | | | | | | |
| `chart.go` | | | | | | |
| `coins_cache.go` (legacy?) | | | | | | |
| `coins.go` (removed per rc.48?) | | | | | | |
| `currencies.go` (removed per rc.48?) | | | | | | |
| `dashboardauth/*` | | | | | | |
| `dashboardkeys/*` | | | | | | |
| `diagnostics_cursors.go` | | | | | | |
| `doc.go` | | | | | | |
| `envelope.go` | data/as_of/flags/pagination contract | | | | | |
| `handler_middleware*.go` | | | | | | |
| `helpers_*` | | | | | | |
| `history.go` | | | | | | |
| `incidents.go` | | | | | | |
| `issuers.go` | | | | | | |
| `known_issuers.go` | | | | | | |
| `known_scams.go` | | | | | | |
| `lending.go` | | | | | | |
| `markets_cache.go` | | | | | | |
| `markets.go` | reads `prices_1m` (rc.45) | | | | | |
| `methodology.go` | | | | | | |
| `middleware/*` | | | | | | |
| `network_stats.go` | | | | | | |
| `observations_stream.go` | | | | | | |
| `observations.go` | | | | | | |
| `ohlc.go` | | | | | | |
| `openapi_examples_test.go` | OpenAPI ↔ runtime drift | | | | | |
| `oracle_sep40.go` | SEP-40 surface | | | | | |
| `oracle.go` | | | | | | |
| `pairs.go` | | | | | | |
| `price_batch.go` | | | | | | |
| `price_stream.go` | SSE | | | | | |
| `price_tip_stream.go` | SSE | | | | | |
| `price_tip.go` | | | | | | |
| `price.go` | last-closed-bucket reader | | | | | |
| `server.go` | route registration | | | | | |

(Additional files exist; complete the list during walk.)

## Per-route audit (per `02-protocol.md` §8)

Use the inventory route list. For each route fill the 10-check
loop:

| Method | Path | OpenAPI | Envelope | Auth | RateLimit | Cache | Pagination | NotFound | LatencyBudget | Tests | RemovedRoute? |

## Removed-route hygiene (rc.48) — **active F-1201..F-1203**

`/v1/coins/*` and `/v1/currencies/*` were removed in commit
`28ac6ac9`. Audit-plan creation pass already found:

- explorer **still calls** these routes from
  `HomeCurrencies.tsx`, `sitemap.ts`, `HomeTryAPI.tsx`,
  `embed/currency/[ticker]/page.tsx`, and
  `assets/[slug]/AssetConverter.tsx` (F-1201)
- `examples/curl/04-coins.sh` + README (F-1202)
- live R1 **still serves 200** for these routes at audit kick-off,
  pre-deploy of rc.48 binary (F-1203)

Verify:

| Surface | State expected | State today | Status |
| --- | --- | --- | --- |
| `internal/api/v1/coins.go` | removed | _verify (file may still exist)_ | |
| `internal/api/v1/currencies.go` | removed | | |
| `internal/api/v1/coins_cache.go` | removed | _but file present in tree at audit time — verify_ | |
| `internal/api/v1/coins_cache_test.go` | removed | _verify_ | |
| `openapi/rates-engine.v1.yaml` | `/v1/coins/*` + `/v1/currencies/*` paths absent | | |
| `pkg/client/endpoints.go` | corresponding methods removed | | |
| `examples/curl/` | scripts referencing them removed | | |
| `examples/postman/` | requests referencing them removed | | |
| `web/explorer/` | components referencing them removed (per #46) | | |
| live R1 | 404 fast-path (NOT 200 + slow lookup) | EV-1205 shows 200s still served | finding pending |

## SSE / streaming sub-audit

- `internal/api/streaming/hub.go` — multiplexing
- `internal/api/streaming/ring.go` — ring buffer (size? overflow?)
- `internal/api/streaming/event.go` — event format
- `internal/api/streaming/handler.go` — SSE write loop
- `internal/api/streaming/redispub/*` — Redis pub/sub
- `internal/api/streampublish/publisher.go` — aggregator-side publish
- slow-consumer disconnect threshold
- Last-Event-ID handling on reconnect

## Auth sub-audit

- `internal/auth/apikey.go` + Postgres store + Redis cache
- revoke invalidation flow
- `internal/auth/sep10/*` — challenge, verify, JWT issuance
- audience + expiry + replay defence
- `internal/auth/subject.go` — identity derivation precedence
- `internal/auth/validators.go` — input validation
- `internal/auth/signup_tracker.go` — anti-abuse

## OpenAPI vs handlers (R04 reconciliation)

Two-way reconciliation:

1. Every route in `internal/api/v1/server.go` is in OpenAPI.
2. Every OpenAPI operation has a registered handler.

Captured in `04-reconciliation.md` R04.

## pkg/client/ audit

- `client.go`, `endpoints.go`, `types.go`, `errors.go`, `doc.go`
- per-endpoint test in `endpoints_test.go` (58 KB single file)
- example coverage in `example_test.go` (49 KB)
- asset detail test coverage
- error types match RFC 7807 envelope

## Adversarial vectors

- B1.* authentication / authorization
- B2.* rate-limit bypass
- B3.* cache poisoning
- B4.* pagination / cursor abuse
- B5.* streaming / SSE
- B6.* removed-route abuse

## Cross-workstream dependencies

- W09 owns Redis cache + cursor stability
- W14 owns api-5xx / api-down / api-latency runbooks
- W19 owns full auth + secret + billing audit
- W17 owns explorer consumer side

## Closure criteria

- Every handler-file row complete
- Per-route 10-check loop complete for every route
- OpenAPI ↔ handler reconciliation R04 complete
- Removed-route hygiene table complete
- SSE + Auth sub-audit complete
- pkg/client coverage proven
