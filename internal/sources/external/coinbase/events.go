// Package coinbase streams live trade matches from Coinbase
// Exchange's public WebSocket feed. Adds US price discovery for
// XLM/USD — a venue missing from our reference implementation
// (`~/code/rates`), so this is the net-new connector in the Phase-2
// CEX fleet.
//
// Venue-naming note: Coinbase has two distinct API families —
// Coinbase Exchange (the ex-Pro API; public, public-key auth for
// private data, no auth for matches/ticker) and Coinbase Advanced
// Trade (the retail-facing new surface; OAuth, rate-limited for
// non-authenticated use). We target **Exchange** — it's the stable
// institutional-grade feed used by most market-data consumers and
// matches the capability profile of Binance/Kraken/Bitstamp.
//
// Architectural contrast with prior CEXes:
//
//   - Symbol format: "XLM-USD" (dash), between Kraken's "XLM/USD"
//     and Binance's "XLMUSD".
//   - Subscribe shape: `{"type":"subscribe","channels":[{"name":
//     "matches","product_ids":[...]}]}` — one message covers all
//     product IDs.
//   - The `matches` channel publishes executed trades (not quote
//     updates). On subscribe Coinbase also sends one `last_match`
//     per product to prime consumer state — treated as a real
//     historical trade, identical shape to live matches.
//   - Numbers arrive as strings natively — no json.Number dance.
//   - Time is RFC 3339 with nanosecond precision.
//
// Wire format reference:
// https://docs.cloud.coinbase.com/exchange/docs/websocket-channels
package coinbase

import (
	"errors"

	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// SourceName is stamped on every canonical.Trade this package emits.
const SourceName = "coinbase"

// WSEndpoint is Coinbase Exchange's public WebSocket URL. No
// authentication for the matches channel.
const WSEndpoint = "wss://ws-feed.exchange.coinbase.com"

// Message `type` values on the Coinbase wire. We act on `match`
// and `last_match`; `subscriptions` is the subscribe ack; `error`
// is a subscription failure; others (heartbeat, ticker, l2update)
// we never subscribe to in v1.
const (
	TypeMatch         = "match"
	TypeLastMatch     = "last_match"
	TypeSubscriptions = "subscriptions"
	TypeError         = "error"
)

// ChannelName — only `matches` is subscribed in v1. `ticker` and
// `level2` channels exist and would add book-depth detail; defer
// until the aggregator needs it.
const ChannelName = "matches"

// Compile-time assertion: venue's class matches external.ClassExchange.
var _ = external.ClassExchange

// Errors surfaced by the parser.
var (
	// ErrMalformedFrame — envelope or payload JSON didn't match
	// the expected shape. Single-frame skip; stream stays up.
	ErrMalformedFrame = errors.New("coinbase: malformed frame")

	// ErrUnknownProduct — match arrived on a product_id not in
	// our PairMap. Defensive: Start rejects subscription-level
	// misconfiguration upfront, so hitting this at runtime means
	// Coinbase listed a new pair we're implicitly subscribed to.
	ErrUnknownProduct = errors.New("coinbase: product_id not in configured PairMap")

	// ErrSubscriptionRejected — venue returned a `type:"error"`
	// frame in response to our subscribe. Usually a typo in a
	// product_id; treat as unrecoverable for this connection.
	ErrSubscriptionRejected = errors.New("coinbase: subscription rejected")
)
