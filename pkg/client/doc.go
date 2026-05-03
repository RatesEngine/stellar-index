// Package client is the official Go SDK for the Rates Engine
// public API.
//
// # Stability
//
// Public surface under SemVer per [ADR-0005]. Within the v0.x
// pre-release line breaking changes may occur but are documented in
// CHANGELOG.md. Once we tag v1.0.0 the contract is binding:
// removing or repurposing any exported identifier requires a major
// version bump.
//
// # Quick start
//
//	package main
//
//	import (
//	    "context"
//	    "fmt"
//	    "github.com/RatesEngine/rates-engine/pkg/client"
//	)
//
//	func main() {
//	    c := client.New(client.Options{
//	        BaseURL: "https://api.ratesengine.net",
//	        APIKey:  "rek_…",  // optional; anonymous works at low rate-limit
//	    })
//	    p, err := c.Price(context.Background(), client.PriceQuery{
//	        Asset: "native",
//	        Quote: "fiat:USD",
//	    })
//	    if err != nil {
//	        // err is a *APIError when the server returned a problem+json;
//	        // unwrap with errors.As to inspect HTTP status / type / detail.
//	        panic(err)
//	    }
//	    fmt.Printf("XLM/USD = %s (%s, observed %s)\n",
//	        p.Data.Price, p.Data.PriceType, p.Data.ObservedAt)
//	}
//
// # Authentication
//
// Three modes mirror the server's [config.APIConfig].AuthMode:
//
//   - **Anonymous** — no APIKey on the client; rate-limited per IP.
//   - **API key** — set Options.APIKey; sent as
//     `Authorization: Bearer <key>` on every request.
//   - **SEP-10** — the server-side verifier ships at
//     `/v1/auth/sep10/{challenge,token}`. Obtain a JWT via the
//     SEP-10 challenge → sign → verify flow and pass it as
//     Options.APIKey (the SDK forwards any token verbatim in the
//     `Authorization: Bearer` header — the server's auth
//     middleware accepts both `rek_*` API keys and SEP-10 JWTs at
//     the same surface). A typed SEP-10 helper wrapping the two-
//     step flow lands as a follow-up.
//
// # Error handling
//
// Methods return either the success envelope OR an error. Network
// / parse errors come through wrapped in fmt.Errorf; HTTP errors
// from the server come through as *[APIError]. Detect via
// [errors.As]:
//
//	var apiErr *client.APIError
//	if errors.As(err, &apiErr) && apiErr.Status == 404 {
//	    // pair not found — fall back to a different quote
//	}
//
// # Concurrency
//
// [Client] is safe for concurrent use after construction. The
// underlying *http.Client is shared across calls; each request
// opens a fresh connection from the transport pool.
//
// # Coverage
//
// Typed methods today: Price, Assets, Asset, AssetMetadata,
// HistorySinceInception, Me, Usage, CreateKey. SDK additions in
// the queue (PRs #446–#450 from the 2026-05-02 audit pass):
// PriceBatch, PriceTip, OHLC, History (bounded-range raw
// trades), Sources, Markets, Pair.
//
// Surfaces deliberately not in the SDK:
//
//   - SSE streams (`/v1/price/{,tip/}stream`, `/v1/observations/stream`)
//     — architecturally outside the request/response shape; consumers
//     use `net/http` with an eventsource-style reader directly.
//   - VWAP / TWAP — the server pre-computes these but consumers can
//     also derive them from the raw trades the History method
//     returns; we don't ship a redundant SDK helper today.
//   - SEP-40 oracle passthrough (`/v1/oracle/*`) — intended for
//     SEP-40 oracle-shape consumers specifically; those callers
//     typically use `internal/sources/reflector` or speak SEP-40
//     directly rather than going through this generic SDK.
//   - Operator endpoints (`/v1/healthz`, `/v1/readyz`, `/v1/version`,
//     `/metrics`) — infra-facing, not customer-facing.
//
// [ADR-0005]: https://github.com/RatesEngine/rates-engine/blob/main/docs/adr/0005-monorepo.md
package client
