// Package exchangeratesapi polls exchangeratesapi.io's REST endpoint
// for fiat reference rates. First Poller (not Streamer) in the
// external-connector framework; validates the Poller interface
// with a real venue.
//
// Role in the aggregator:
//
//   - Emits canonical.OracleUpdate (not Trade) — an FX reference
//     rate isn't an executed trade, it's a computed benchmark
//     sourced from interbank feeds + the ECB reference rate.
//   - Consumed by the triangulation layer: `XLM/USD × USD/EUR =
//     XLM/EUR` when no venue trades XLM/EUR directly (e.g. when
//     Kraken is the only direct-XLM-EUR venue and goes down).
//   - Class is ClassExchange in the registry — it's an authoritative
//     first-party computation, not a third-party aggregation of
//     other markets. Contributes to VWAP in the fiat-pair sense.
//
// Tier notes (important for procurement):
//
//   - Free tier: EUR base only, 1-hour cadence, no redistribution,
//     250 requests/month. Unusable for production.
//   - Basic ($9.99/mo): EUR base, 5-min cadence, 10,000 reqs/mo.
//   - Professional ($29.99/mo): **USD base + any base**, 1-min
//     cadence, 100,000 reqs/mo, **redistribution allowed**.
//   - Professional+ ($99.99/mo): 60-sec cadence, 300,000 reqs/mo.
//
// We target Professional tier at minimum. Free tier is rejected at
// startup because EUR-only-base would force every FX consumer to
// triangulate through EUR which is the wrong shape for a USD-quoted
// pricing API.
//
// Wire format verified 2026-04-24 against
// https://exchangeratesapi.io/documentation:
//
//	GET https://api.exchangeratesapi.io/v1/latest?access_key=KEY&base=USD&symbols=EUR,GBP,JPY,...
//
//	{
//	  "success": true,
//	  "timestamp": 1745000000,
//	  "base": "USD",
//	  "date": "2026-04-24",
//	  "rates": {
//	    "EUR": 0.92350,
//	    "GBP": 0.78450,
//	    "JPY": 149.5600
//	  }
//	}
package exchangeratesapi

import (
	"errors"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/external"
)

// SourceName is stamped on every canonical.OracleUpdate this
// package emits. Must match the registry key.
const SourceName = "exchangeratesapi"

// DefaultEndpoint is the exchangeratesapi.io REST base. Includes
// the `/v1` version prefix.
const DefaultEndpoint = "https://api.exchangeratesapi.io/v1"

// LatestPath is the endpoint for current rates. Combined with the
// `?base=...&symbols=...` query we build in poller.go.
const LatestPath = "/latest"

// DefaultPollInterval is the minimum cadence we run at when
// operator doesn't override — matches the Professional tier's
// 1-min refresh. Setting lower would waste quota without gaining
// resolution.
const DefaultPollInterval = 60 * time.Second

// DefaultDecimals is the precision at which we scale the incoming
// float64 rates. Five decimal places is enough for G10 cross-rates
// (typical precision ~4dp); 6 gives headroom for EM currencies.
const DefaultDecimals uint8 = 6

// DefaultBase is the base currency we query when operator doesn't
// override. USD is chosen because: (1) it's our primary quote asset,
// (2) triangulating other pairs through USD matches most consumer
// expectations, (3) free tier's EUR-only-base is unusable anyway so
// we may as well bake USD in as the opinionated default.
const DefaultBase = "USD"

// Compile-time assertion: exchange class in registry.
var _ = external.ClassExchange

// Errors surfaced by the poller.
var (
	// ErrAPIKeyRequired — operator enabled the source without
	// providing an API key. Surfaced at NewPoller time so the
	// indexer fails at startup, not at first poll.
	ErrAPIKeyRequired = errors.New("exchangeratesapi: API key required (see config.External.ExchangeRatesApi.APIKey)")

	// ErrAPIRejected — venue returned {"success": false, "error": {...}}.
	// Common causes: invalid key (401), rate limit exhausted (429
	// implicit via the success=false shape), base not available on
	// current tier (free tier rejects base!=EUR).
	ErrAPIRejected = errors.New("exchangeratesapi: API returned success=false")

	// ErrMalformedResponse — JSON didn't decode to the documented
	// shape. Single-poll skip; next tick retries.
	ErrMalformedResponse = errors.New("exchangeratesapi: malformed response")
)
