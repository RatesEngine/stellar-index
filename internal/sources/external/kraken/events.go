// Package kraken streams live trades from Kraken's public WebSocket
// v2 trade channel. Strongest XLM fiat coverage of any venue we
// integrate — XLM/USD, XLM/EUR, XLM/GBP, XLM/AUD, XLM/CAD, XLM/CHF
// are all natively quoted (no stablecoin proxy).
//
// Architectural contrast with Binance:
//
//   - Symbol format is "XLM/USD" (slash-separated, uppercase), not
//     "XLMUSD". The slash is wire-format; we normalise both sides of
//     the mapping in pairs.go.
//   - Subscription is an explicit JSON method call after connect, not
//     a URL query string. Connect, then send a subscribe payload;
//     server acks with a success frame.
//   - Numbers arrive as JSON floats (not Binance's strings). We
//     decode via [encoding/json.Number] to preserve the original
//     decimal string representation — float64 for a $0.17582 price
//     at 10^8 scale is safe, but we bypass float entirely on principle
//     (i128 invariant, ADR-0003).
//   - BTC → XBT legacy aliasing does NOT apply on v2 — Kraken renamed
//     back to "BTC/USD" when they launched v2. v1 "XXBTZUSD" style is
//     not something we encounter.
//
// Wire format reference:
// https://docs.kraken.com/api/docs/websocket-v2/trade
//
// Typical session:
//
//	→ Dial wss://ws.kraken.com/v2
//	← {"channel":"status","data":[{"system":"online", ...}]}
//	→ {"method":"subscribe","params":{"channel":"trade","symbol":["XLM/USD","XLM/EUR"]}}
//	← {"method":"subscribe","success":true,...}
//	← {"channel":"trade","type":"snapshot","data":[...]}
//	← {"channel":"trade","type":"update","data":[{"symbol":"XLM/USD","side":"buy","qty":100.0,"price":0.17582,"ord_type":"market","trade_id":1234567,"timestamp":"2026-04-24T..."}]}
//	← {"channel":"heartbeat"}       # ignored
//
// The snapshot (last ~50 trades) carries real historical timestamps
// — we emit it like any other trade data. Deduplication against the
// backfill path uses the synthesised tx_hash (symbol + trade_id).
package kraken

import (
	"errors"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/external"
)

// SourceName is stamped on every canonical.Trade this package emits.
// Must match the registry key in external.Registry.
const SourceName = "kraken"

// WSEndpoint is Kraken's public v2 WebSocket URL. No auth for the
// trade channel.
const WSEndpoint = "wss://ws.kraken.com/v2"

// Channel names on the Kraken v2 stream. We only act on `trade`; the
// others we log-and-ignore so the stream stays open through
// subscription-ack, heartbeat, and status frames.
const (
	ChannelTrade     = "trade"
	ChannelHeartbeat = "heartbeat"
	ChannelStatus    = "status"
)

// Compile-time assertion: the constant matches external.ClassExchange.
var _ = external.ClassExchange

// Errors surfaced by the parser. Transient WS errors live in
// streamer.go and never escape as sentinels — they're logged and
// reconnected against.
var (
	// ErrMalformedFrame — JSON shape didn't match either the
	// channel envelope or one of the known payload types. Single-
	// frame skip; stream stays up.
	ErrMalformedFrame = errors.New("kraken: malformed frame")

	// ErrUnknownSymbol — trade's symbol isn't in the configured
	// PairMap. Happens if Kraken lists a new pair we haven't
	// added, or if we subscribed to a symbol that wasn't in the
	// map at construct time (defensive — Start rejects this path).
	ErrUnknownSymbol = errors.New("kraken: symbol not in configured PairMap")

	// ErrDustTrade — base × price floor-divided to a 0 quote
	// amount. Tiny lots (e.g. 1e-8 XLM at $0.16) underflow the
	// canonical.NewAmount integer scale (10^8). Real trades, but
	// below our precision floor — drop silently rather than
	// logging at ERROR. Same shape as the Coinbase + Binance +
	// Bitstamp dust filter (#814 / #1234).
	ErrDustTrade = errors.New("kraken: dust trade (quote_amount underflow)")
)
