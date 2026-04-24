// Package polygonforex polls Polygon.io's Forex Snapshot endpoint
// for institutional-grade FX reference rates. This is the top-tier
// authoritative FX source in our external-connector fleet — the
// proposal's "authority that will not make mistakes" requirement,
// pre-approved by Ash on 2026-04-24.
//
// Polygon.io aggregates from interbank and institutional FX feeds
// (OANDA is one of their sources, among others) and packages them
// as REST snapshots + WebSocket streams. We use the batch snapshot
// endpoint because it returns every forex ticker in a single call,
// which fits the Poller interface cleanly and respects per-minute
// rate-limit budgets on paid tiers.
//
// Tier notes (procurement context):
//
//   - Starter ($29/mo): 5 calls/min, delayed data, no snapshot.
//     Insufficient — we can't get fresh FX at our 60s cadence.
//   - Developer ($99/mo): 100 calls/min, 15-min-delayed data,
//     per-pair /v1/conversion/ only. Usable for cross-check
//     work but not primary.
//   - Advanced ($199/mo): unlimited calls, real-time, **snapshot
//     endpoint unlocked**. This is what this poller targets.
//   - Currencies Advanced ($249/mo): adds forex WebSocket streams.
//     Future work — swap from REST poll to WS when we need
//     sub-second cadence.
//
// Wire format verified 2026-04-24 against
// https://polygon.io/docs/forex/get_v2_snapshot_locale_global_markets_forex_tickers:
//
//	GET https://api.polygon.io/v2/snapshot/locale/global/markets/forex/tickers?apiKey=KEY
//
//	{
//	  "status": "OK",
//	  "tickers": [
//	    {
//	      "ticker": "C:USDEUR",
//	      "lastQuote": {
//	        "a": 0.9236, "b": 0.9234,
//	        "x": 48, "t": 1745000000000
//	      },
//	      "updated": 1745000000000
//	    },
//	    ...
//	  ]
//	}
//
// Ticker format: "C:" prefix (Polygon's currency convention) +
// ISO-4217 base + quote, no separator. "C:USDEUR" means "1 USD ->
// EUR rate." We parse the two 3-letter codes after the prefix.
package polygonforex

import (
	"errors"
	"time"

	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// SourceName is stamped on every canonical.OracleUpdate this
// package emits. Must match the registry key.
const SourceName = "polygon-forex"

// DefaultEndpoint is the Polygon.io REST base.
const DefaultEndpoint = "https://api.polygon.io"

// SnapshotPath is the forex-tickers snapshot endpoint. Returns
// every active forex ticker in one call — the reason we prefer
// it over per-pair /v1/conversion/ requests.
const SnapshotPath = "/v2/snapshot/locale/global/markets/forex/tickers"

// TickerPrefix is the Polygon currency-ticker prefix. "C:USDEUR"
// parses to base=USD, quote=EUR.
const TickerPrefix = "C:"

// DefaultPollInterval is 60s — matches ExchangeRatesApi, balanced
// against Polygon's real-time-at-Advanced-tier unlimited budget.
// Lower cadence wastes resources without gaining resolution for
// FX (institutional FX spreads don't move faster than tens of
// seconds in normal markets).
const DefaultPollInterval = 60 * time.Second

// DefaultDecimals — 6dp matches ExchangeRatesApi so aggregator
// math across FX sources uses a uniform scale.
const DefaultDecimals uint8 = 6

// DefaultBase — USD is our canonical quote for fiat rates. Operator
// override via config.
const DefaultBase = "USD"

// Compile-time assertion: exchange class in registry.
var _ = external.ClassExchange

var (
	// ErrAPIKeyRequired — operator enabled source without a key.
	ErrAPIKeyRequired = errors.New("polygon-forex: API key required (see config.External.PolygonForex.APIKey or env POLYGON_API_KEY)")

	// ErrAPIRejected — venue returned status != "OK" or HTTP
	// 4xx on a request that should have succeeded.
	ErrAPIRejected = errors.New("polygon-forex: API rejected request")

	// ErrMalformedResponse — JSON didn't decode to the documented
	// shape. Single-poll skip; next tick retries.
	ErrMalformedResponse = errors.New("polygon-forex: malformed response")

	// ErrMalformedTicker — a ticker didn't parse as "C:XXXYYY".
	// Per-entry skip; other tickers in the same snapshot still emit.
	ErrMalformedTicker = errors.New("polygon-forex: malformed ticker")
)
