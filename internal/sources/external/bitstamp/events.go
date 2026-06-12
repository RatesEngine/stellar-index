// Package bitstamp streams live trades from Bitstamp's public
// WebSocket API. Adds EUR/GBP depth to the XLM market coverage
// Kraken already provides, with a different liquidity profile
// (Bitstamp skews European retail).
//
// Architectural contrast with Kraken:
//
//   - Subscription model: Bitstamp requires ONE subscribe message
//     per channel; Kraken accepts an array in a single method call.
//     We send N subscribe frames sequentially after connect.
//   - Channel naming: "live_trades_xlmusd" (lowercase, concat, no
//     separator). The venue is a holdover from its Pusher-protocol
//     origins even after the raw-WS migration in 2020.
//   - Precision: Bitstamp emits BOTH float forms (price, amount) AND
//     string forms (price_str, amount_str). We use the string forms
//     uniformly — the i128 invariant (ADR-0003) says no floats on
//     the price path, and Bitstamp's string fields preserve vendor-
//     side precision.
//   - Microtimestamp: stringified microseconds-since-epoch, not ms
//     (Binance) or RFC3339 (Kraken).
//   - Periodic server-initiated reconnect: Bitstamp sends a
//     `bts:request_reconnect` event every ~hour asking clients to
//     reconnect and rebalance to a different node. We honour it by
//     closing the connection; backoff reconnect picks up.
//
// Wire format reference:
// https://www.bitstamp.net/websocket/v2/
//
// Typical session:
//
//	→ Dial wss://ws.bitstamp.net
//	→ {"event":"bts:subscribe","data":{"channel":"live_trades_xlmusd"}}
//	← {"event":"bts:subscription_succeeded","channel":"live_trades_xlmusd","data":{}}
//	→ {"event":"bts:subscribe","data":{"channel":"live_trades_xlmeur"}}
//	← {"event":"bts:subscription_succeeded","channel":"live_trades_xlmeur","data":{}}
//	← {"event":"trade","channel":"live_trades_xlmusd","data":{"id":...,"price_str":"0.17582","amount_str":"100.5","microtimestamp":"1745000000123456","type":0,...}}
//	← {"event":"bts:request_reconnect","channel":"","data":{}}  # ~hourly
package bitstamp

import (
	"errors"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/external"
)

// SourceName is stamped on every canonical.Trade this package emits.
// Must match the registry key in external.Registry.
const SourceName = "bitstamp"

// WSEndpoint is Bitstamp's public v2 WebSocket URL.
const WSEndpoint = "wss://ws.bitstamp.net"

// Event types on the Bitstamp wire. We only act on `trade`; the
// others either confirm state (subscription_succeeded), indicate
// errors (bts:error), or request we rebalance (bts:request_reconnect).
const (
	EventTrade                   = "trade"
	EventSubscriptionSucceeded   = "bts:subscription_succeeded"
	EventUnsubscriptionSucceeded = "bts:unsubscription_succeeded"
	EventRequestReconnect        = "bts:request_reconnect"
	EventError                   = "bts:error"
)

// ChannelPrefix is the naming convention for live-trade channels —
// "live_trades_<sym>" where sym is lowercase concatenated.
const ChannelPrefix = "live_trades_"

// Compile-time assertion: venue's class matches external.ClassExchange.
var _ = external.ClassExchange

// Errors surfaced by the parser. Transient WS errors live in
// streamer.go.
var (
	// ErrMalformedFrame — JSON shape didn't match the channel
	// envelope or the trade event payload. Single-frame skip.
	ErrMalformedFrame = errors.New("bitstamp: malformed frame")

	// ErrUnknownChannel — trade event arrived on a channel not in
	// the configured pair map. Defensive; Start rejects
	// misconfiguration upfront.
	ErrUnknownChannel = errors.New("bitstamp: channel not in configured PairMap")

	// ErrRequestedReconnect — venue asked us to reconnect. The
	// streamer closes the connection and re-enters the backoff
	// loop; not an error the caller sees but an internal signal.
	ErrRequestedReconnect = errors.New("bitstamp: server requested reconnect")

	// ErrDustTrade — base × price floor-divided to a 0 quote
	// amount. Tiny bitstamp lots (e.g. 1e-8 XLM at $0.16) underflow
	// the canonical.NewAmount integer scale (10^8). Real trades, but
	// below our precision floor — drop silently rather than logging
	// at ERROR. Same shape as the Coinbase + Binance dust filter
	// (see #814).
	ErrDustTrade = errors.New("bitstamp: dust trade (quote_amount underflow)")
)
