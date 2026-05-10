// Package binance streams live aggregated trades from Binance's
// public combined-stream WebSocket endpoint and translates them into
// canonical.Trade values.
//
// Why aggTrade, not raw trades: Binance emits one `@trade` message
// per executed fill, which on a busy pair like XLMUSDT is a firehose
// of ≥ 50 msg/s at typical US hours. The `@aggTrade` stream merges
// consecutive fills at the same price in the same millisecond into a
// single message — lossless for VWAP because the aggregated volume is
// preserved, and ~5-10× lower throughput in practice. The same
// pattern powers ~/code/rates's Binance connector and it's been
// stable at production volume there for years.
//
// Wire format (verified 2026-04-24 against
// https://developers.binance.com/docs/binance-spot-api-docs/web-socket-streams):
//
//	wss://stream.binance.com:9443/stream?streams=<sym1>@aggTrade/<sym2>@aggTrade
//
// Each frame:
//
//	{
//	  "stream": "xlmusdt@aggTrade",
//	  "data": {
//	    "e": "aggTrade",       // event type
//	    "E": 1745000000000,    // event time (ms)
//	    "s": "XLMUSDT",        // symbol
//	    "a": 123456,           // aggregate trade ID
//	    "p": "0.1758",         // price (string, exact decimal)
//	    "q": "152.34",         // quantity in base (string)
//	    "f": 12345,            // first underlying trade id
//	    "l": 12399,            // last underlying trade id
//	    "T": 1745000000000,    // trade time (ms — the ledger-close-equivalent)
//	    "m": true              // buyer was maker (→ trade was seller-initiated)
//	  }
//	}
//
// Symbol normalization: Binance concatenates base+quote with no
// separator, always uppercase (XLMUSDT not xlm-usdt). Our normalizer
// consults a hardcoded pair map at init for the v1 pair set; future
// auto-enumeration pulls the full list from
// `GET /api/v3/exchangeInfo` at connector start.
//
// Depeg policy: a Binance XLMUSDT trade emits as
// canonical.Trade{Pair: XLM/USDT} — the aggregator's fiat-proxy
// table (USDT→USD, USDC→USD, etc.) decides at VWAP compute time
// whether to fold it into XLM/USD. Per Ash's guidance (memory:
// feedback_production_artifacts): stablecoins pegs to fiat at the
// aggregator layer, not at ingest. If the stablecoin depegs and
// VWAP goes sideways, that's the correct failure mode — the data
// stays honest.
package binance

import (
	"errors"

	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// SourceName is stamped on every canonical.Trade this package emits.
// Must match the registry key in external.Registry.
const SourceName = "binance"

// WSEndpoint is the public combined-stream entry point. No auth, no
// API key needed for spot market data streams.
const WSEndpoint = "wss://stream.binance.com:9443/stream"

// Compile-time assertion: the constant matches external.ClassExchange.
var _ = external.ClassExchange

// Errors surfaced by parse path. Transient connection errors live in
// streamer.go and never escape as package-level sentinels — they're
// logged + metered + retried inside Start.
var (
	// ErrDustTrade — base × price floor-divided to a 0 quote amount.
	// Real binance trade below our 10^8 integer-scale precision
	// floor. Drop silently rather than logging at ERROR.
	ErrDustTrade = errors.New("binance: dust trade (quote_amount underflow)")

	// ErrMalformedFrame — frame didn't decode to the aggTrade shape
	// we expect. Single-frame skip; logged and counted, doesn't
	// abort the stream.
	ErrMalformedFrame = errors.New("binance: malformed aggTrade frame")

	// ErrUnknownSymbol — frame's symbol isn't in our pair map.
	// Happens if Binance adds a new listing we haven't configured,
	// or if we subscribe to a stream the exchange later renames.
	// Per-frame skip.
	ErrUnknownSymbol = errors.New("binance: symbol not in configured pair map")
)
