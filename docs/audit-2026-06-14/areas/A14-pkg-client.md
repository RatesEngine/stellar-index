# A14 — Public SDK (`pkg/client`)

Read-only audit of the SemVer-stable public Go client SDK + wire-shape
types. Scope: `pkg/` (all `.go`, incl. tests). Audit dimensions: D1
(correctness — request building, error handling, retry, wire-shape
fidelity vs server JSON), D2 (i128/amounts as strings per ADR-0003),
SemVer stability (ADR-0005), D3 (secrets / TLS / base-URL handling),
plus drift between SDK wire types and the server's actual responses.

**Files read (10 / 10 in scope):**
`client.go`, `types.go`, `endpoints.go`, `errors.go`, `doc.go`,
`client_test.go`, `endpoints_test.go`, `errors_test.go`,
`asset_detail_test.go`, `example_test.go`.

**Cross-referenced (out of scope, read for drift verification):**
`internal/api/v1/envelope.go`, `internal/api/v1/account.go`,
`internal/api/v1/price.go` / `vwap.go` / `ohlc*.go` (struct-tag grep),
`internal/api/v1/server.go` (route registration),
`openapi/stellar-index.v1.yaml` (endpoint coverage).

`go vet ./pkg/client/` clean. `go test ./pkg/client/` passes.

---

## Findings

| ID | Sev | Dim | File:Line | Summary |
|----|-----|-----|-----------|---------|
| A14-01 | High | D1 / SemVer | types.go:16 | `Envelope.Pagination` is a value type `Pagination` (not `*Pagination`) with `omitempty`; server uses `*Pagination`. `omitempty` is a no-op on a struct value, so SDK re-encode emits `"pagination":{}` where the server omits it. Re-emit/round-trip drift. |
| A14-02 | Med | D1 / SemVer | types.go:805,829,757 | `VerifiedCurrencyListItem`, `GlobalAssetView`, `PerNetworkAssetView` types exist with godoc links to `[Client.AssetsVerified]` / `[Client.AssetByNetwork]`, but **those methods do not exist**. Dangling godoc refs + un-callable types. Server routes (`GET /v1/assets/verified`, `GET /v1/assets/{asset_id}/{network}`) and OpenAPI both exist → coverage gap, not just a doc typo. |
| A14-03 | Med | D1 (doc) | doc.go:81-82 | `# Coverage` doc lists `Coins, Coin, ..., Currencies, Currency` as SDK methods. Those methods were removed (see types.go:435, 680). Stale doc claims "every endpoint has a typed method" and "35 methods as of 2026-05-09" while also omitting the 4 explorer surfaces that DO exist server-side. |
| A14-04 | Low | D1 / SemVer | types.go:411-416 | SDK `Account.CreatedAt` has no `omitempty`; server (`account.go:43`) has `created_at,omitempty`. On re-encode of an API-key Account without a created_at, SDK emits `"created_at":"0001-01-01T00:00:00Z"`. Minor re-emit drift. |
| A14-05 | Low | D1 | example_test.go:219,904 | Two examples put `"identifier":"GA..."` in the `Account` JSON, but the SDK `Account` struct has no `Identifier` field (server `Account` doesn't either — it's `subject.Identifier`, internal). The field is silently dropped on decode; examples model a non-existent wire field. Cosmetic, but misleads SDK readers. |
| A14-06 | Low | D3 | client.go:65-88 | `New()` never validates `BaseURL`. A malformed/relative BaseURL (e.g. `"api.example.com"` with no scheme) is only caught per-request inside `doJSON` via `url.Parse`, which accepts schemeless/relative URLs without error → requests silently go to the wrong place. No constructor-time guard. |
| A14-07 | Low | SemVer | client.go:28 | `userAgent = "stellarindex-go-sdk/0.1.0"` is hand-pinned and stale vs the repo's `v0.5.0-rc.108` tag line. Comment says "bump in tandem with the SDK tag"; it has drifted. Server telemetry mis-attributes SDK version. |
| A14-08 | Low | D1 | errors.go:110-115 | Non-JSON error bodies only land in `Detail` when `len(body) <= 256`. A 257-byte plain-text proxy error (e.g. a verbose nginx 502) is silently discarded → `APIError` carries status only, no diagnostic text. Intentional cap, but the truncation (rather than truncate-to-256) loses all context on slightly-oversized bodies. |
| A14-09 | Info | SemVer | endpoints.go:551-561 | Dangling/misplaced doc comment: a `CoinsOptions` doc block (describing `Limit`/`Issuer`/`Cursor`) sits immediately above `type IssuersOptions` with no `CoinsOptions` type beneath it (Coins removed). Leftover from the Coins deletion; the comment now describes a non-existent type. |
| A14-10 | Info | D1 | errors.go:24, doc.go | SDK has **no retry logic** at all — `RetryAfter` is parsed and exposed but never acted on; no backoff, no idempotent-GET retry. This is a deliberate "caller owns retry" design (documented), noted only so the audit record is explicit that "retry" = surface-only, by design. |

---

## CORRECT (verified sound — no action)

- **D2 / ADR-0003 (i128 as strings) — FULLY COMPLIANT.** Every
  amount/price/supply/volume field on every wire type is `string` or
  `*string`, never a numeric Go type:
  - `PriceSnapshot.Price`, `TradeRow.{BaseAmount,QuoteAmount,Price}`,
    `OHLCBar.{Open,High,Low,Close,BaseVolume,QuoteVolume}`,
    `VWAPResult.{Price,BaseVolume,QuoteVolume}`, `TWAPResult.Price`,
    `HistoryPoint.{P,VolumeUSD}` — all strings.
  - `AssetDetail.{CirculatingSupply,TotalSupply,MaxSupply,PriceUSD,
    MarketCapUSD,FDVUSD,VolumeUSD24h,FixedNumber,MaxNumber}` —
    `*string`.
  - `NetworkStats.Volume24hUSD`, `Market.Volume24hUSD`,
    `Pool.{Volume24hUSD,LastPrice}`, `GlobalAssetView.{PriceUSD,
    CirculatingSupply,MarketCapUSD}`, `VerifiedCurrencyListItem.{
    CirculatingSupply,MarketCapUSD}` — `*string`/`string`.
  - The only `float64` fields are `MethodologyOutlierFilter.DefaultSigma`
    (a config sigma, not a token amount), `StatusLatency.P*Ms` (millisecond
    latency), and `ChangeSummary` percentage/value deltas — none of these
    are i128 token amounts, so float is acceptable.
  - `asset_detail_test.go` explicitly pins string-decode + null→nil and
    omitempty-on-reencode behaviour for the supply/F2 fields.

- **D3 (secrets / TLS / base URL) — sound.** No `InsecureSkipVerify`,
  no `TLSClientConfig` override, no hardcoded `http://` in non-test
  source (only `https://api.stellarindex.io` default). Uses the
  standard library transport (TLS verification on by default). API key
  is only ever sent as `Authorization: Bearer` and only when non-empty
  (`client.go:119`, test-pinned `TestNoAuthHeaderWhenAPIKeyEmpty`).
  `KeyCreated.Plaintext` is documented as the once-only secret; no
  logging of it anywhere. Response body capped at 16 MiB
  (`maxResponseBytes`) so a hostile server can't OOM the caller.

- **D1 — request building is correct.** `url.PathEscape` on every
  path-param method (`Asset`, `AssetMetadata`, `Issuer`, `RevokeKey`,
  `ChangeSummary`) — test-pinned. Trailing-slash stripped on BaseURL.
  Zero-valued optional query params correctly omitted (window_seconds,
  from/to, limit, cursor, class, outlier_sigma) — each has a dedicated
  "omits zero" test. `From/To` formatted as RFC3339-UTC (avoids the
  zero-time `0001-01-01` foot-gun, test-pinned). Context propagated via
  `http.NewRequestWithContext`; cancellation test present.

- **D1 — error handling is correct.** `APIError` implements `error`,
  is matched via `errors.As` (test-pinned), parses RFC 9457
  problem+json into all fields (`Type/Title/Detail/Instance/RequestID`),
  falls back to status-only on non-JSON, treats any `*json*`
  content-type as a problem candidate. `parseRetryAfter` handles both
  delta-seconds and HTTP-date forms and clamps past dates to 0 (never
  negative) — comprehensively test-pinned (`TestParseAPIError_RetryAfter`).
  `Is{NotFound,Unauthorized,Forbidden,RateLimited,ServerError}`
  predicates all polarity-tested.

- **Wire-shape fidelity (spot-checked vs server) — matches.**
  `Envelope.{Data,AsOf,Sources,Flags}` field tags identical to
  `internal/api/v1.Envelope`. `Flags` struct is field-for-field
  identical to the server (incl. `frozen`, `single_source`,
  `unverified_ticker_collision` omitempty). `Problem`/`problemJSON`
  matches `internal/api/v1.Problem`. `PriceSnapshot` matches
  `price.go` (`asset_id/quote/price/price_type/observed_at/
  window_seconds`). `VWAPResult`/`OHLCBar` volume fields are strings
  matching `vwap.go`/`ohlc`. Unknown-field tolerance (Go's default
  decoder) means additive server fields are non-breaking, as the
  package docs claim.

- **PriceBatch GET/POST routing** (`endpoints.go:123-160`) correctly
  mirrors the server's 100/1000 caps, rejects >1000 client-side rather
  than silently chunking (documented rationale: chunking would mask the
  batch-wide `flags.stale` OR). Both branches test-pinned.

- **SemVer hygiene (general):** clean exported surface — every exported
  type has a doc comment; `internal/` server constants are deliberately
  re-declared (`priceBatchGETMax/POSTMax`) with a documented rationale
  rather than imported, preserving the `internal/`-boundary +
  consumer-decoupling. `doc.go` correctly documents the v0.x
  break-allowed / v1.0 binding contract.

---

## Notes for follow-up (gaps, not bugs — per audit instruction)

- **Explorer wire shapes are partially in the SDK but unreachable.**
  The R-018 multi-network surfaces (`GlobalAssetView`,
  `PerNetworkAssetView`, `VerifiedCurrencyListItem`, `NetworkView`)
  have types but no methods (A14-02). The SSE streaming surfaces and
  SEP-40 oracle passthrough are documented as deliberately-excluded
  in `doc.go` (acceptable). A SEP-10 typed helper is noted as a
  "follow-up" in doc.go and is genuinely absent — consistent with the
  doc, so a known gap not a defect.
- The cleanest single fix that resolves A14-02 + A14-03 together is to
  add `AssetsVerified(ctx, opts)` and `AssetByNetwork(ctx, slug,
  network)` methods returning the existing types, then refresh the
  `# Coverage` block in doc.go.
