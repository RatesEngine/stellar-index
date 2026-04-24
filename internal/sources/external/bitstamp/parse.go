package bitstamp

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// externalAmountDecimals — same 10^8 scale as Binance and Kraken.
const externalAmountDecimals = 8

// eventEnvelope is the shared outer shape. Every message has an
// `event` field; trade frames add `channel` + `data` payload.
type eventEnvelope struct {
	Event   string          `json:"event"`
	Channel string          `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

// tradePayload matches Bitstamp's live_trades_* data shape.
// Field names + types verified against
// https://www.bitstamp.net/websocket/v2/ (2026-04-24).
//
// We read the *_str variants for price/amount — the float64
// siblings exist but the string form is authoritative and
// precision-safe. Microtimestamp arrives as a string-form
// microsecond count (not a JSON number).
type tradePayload struct {
	ID             int64  `json:"id"`
	Timestamp      string `json:"timestamp"`      // unix seconds, string
	Microtimestamp string `json:"microtimestamp"` // unix microseconds, string
	Amount         any    `json:"amount"`         // float form — ignored
	AmountStr      string `json:"amount_str"`     // base quantity
	Price          any    `json:"price"`          // float form — ignored
	PriceStr       string `json:"price_str"`      // quote-per-base
	Type           int    `json:"type"`           // 0 = buy, 1 = sell (unused)
	BuyOrderID     int64  `json:"buy_order_id"`
	SellOrderID    int64  `json:"sell_order_id"`
}

// parseFrame dispatches on the envelope's `event` field. Trade
// frames yield one canonical.Trade; subscription confirms / errors /
// reconnect requests return (nil, nil) or signalled via
// ErrRequestedReconnect so the streamer can react.
func parseFrame(raw []byte, pairMap map[string]canonical.Pair) (canonical.Trade, bool, error) {
	var env eventEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return canonical.Trade{}, false, fmt.Errorf("%w: envelope: %w", ErrMalformedFrame, err)
	}

	switch env.Event {
	case EventTrade:
		tr, err := parseTrade(env, pairMap)
		if err != nil {
			return canonical.Trade{}, false, err
		}
		return tr, true, nil
	case EventRequestReconnect:
		// Signal the streamer to close + reconnect. Not a parse
		// failure — the caller inspects the error chain.
		return canonical.Trade{}, false, ErrRequestedReconnect
	case EventSubscriptionSucceeded,
		EventUnsubscriptionSucceeded,
		EventError:
		// Not our concern at the parse layer. A `bts:error` on a
		// subscribe request is rare; if it ever matters we surface
		// it here via a dedicated sentinel. For now, swallow and
		// continue.
		return canonical.Trade{}, false, nil
	}

	// Unknown event types are also ignored — keeps the stream
	// robust across vendor additions.
	return canonical.Trade{}, false, nil
}

// parseTrade converts one live_trades_* frame into a canonical.Trade.
func parseTrade(env eventEnvelope, pairMap map[string]canonical.Pair) (canonical.Trade, error) {
	// Channel looks like "live_trades_xlmusd" — strip the prefix
	// to get the symbol we look up in pairMap.
	if !strings.HasPrefix(env.Channel, ChannelPrefix) {
		return canonical.Trade{}, fmt.Errorf("%w: trade on unexpected channel %q", ErrMalformedFrame, env.Channel)
	}
	symbol := strings.TrimPrefix(env.Channel, ChannelPrefix)
	pair, ok := pairMap[strings.ToLower(symbol)]
	if !ok {
		return canonical.Trade{}, fmt.Errorf("%w: %q", ErrUnknownChannel, symbol)
	}

	var t tradePayload
	if err := json.Unmarshal(env.Data, &t); err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: trade data: %w", ErrMalformedFrame, err)
	}
	if t.AmountStr == "" || t.PriceStr == "" {
		return canonical.Trade{}, fmt.Errorf("%w: missing amount_str / price_str", ErrMalformedFrame)
	}

	base, err := decimalStringToScaledInt(t.AmountStr, externalAmountDecimals)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: amount %q: %w", ErrMalformedFrame, t.AmountStr, err)
	}
	price, err := decimalStringToScaledInt(t.PriceStr, externalAmountDecimals)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: price %q: %w", ErrMalformedFrame, t.PriceStr, err)
	}
	quote := new(big.Int).Quo(new(big.Int).Mul(base, price), pow10(externalAmountDecimals))

	ts, err := parseMicrotimestamp(t.Microtimestamp, t.Timestamp)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: %w", ErrMalformedFrame, err)
	}

	return canonical.Trade{
		Source:      SourceName,
		Ledger:      0,
		TxHash:      formatTxHash(symbol, t.ID),
		OpIndex:     0,
		Timestamp:   ts,
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(base),
		QuoteAmount: canonical.NewAmount(quote),
	}, nil
}

// parseMicrotimestamp prefers the microsecond-precision field but
// falls back to the seconds timestamp if micro is missing — rare in
// practice but defensive against vendor frame variation.
func parseMicrotimestamp(micro, secs string) (time.Time, error) {
	if micro != "" {
		us, err := strconv.ParseInt(micro, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("microtimestamp %q: %w", micro, err)
		}
		return time.UnixMicro(us).UTC(), nil
	}
	if secs != "" {
		s, err := strconv.ParseInt(secs, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("timestamp %q: %w", secs, err)
		}
		return time.Unix(s, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("timestamp fields empty")
}

// decimalStringToScaledInt — same semantics as the Binance/Kraken
// helpers; duplicated per-package so each source's scaling
// convention stays local and auditable.
// decimalStringToScaledInt — targetDecimals kept as param for symmetry with the other external parsers.
//
//nolint:unparam // currently always externalAmountDecimals
func decimalStringToScaledInt(s string, targetDecimals int) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("empty decimal string")
	}
	if strings.ContainsAny(s, "eE") {
		return nil, fmt.Errorf("scientific notation %q not supported", s)
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	intPart, fracPart := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart = s[:dot]
		fracPart = s[dot+1:]
	}
	if intPart == "" {
		intPart = "0"
	}
	if len(fracPart) > targetDecimals {
		fracPart = fracPart[:targetDecimals]
	}
	for len(fracPart) < targetDecimals {
		fracPart += "0"
	}
	v, ok := new(big.Int).SetString(intPart+fracPart, 10)
	if !ok {
		return nil, fmt.Errorf("not a decimal: %q", s)
	}
	if neg {
		v.Neg(v)
	}
	return v, nil
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// formatTxHash — 64-char hex from (symbol, trade_id). Symbol-
// normalised (no case sensitivity) so aliases don't break dedup.
func formatTxHash(symbol string, tradeID int64) string {
	normalised := strings.ToUpper(symbol)
	s := fmt.Sprintf("%s-%020d", normalised, tradeID)
	var hex strings.Builder
	hex.Grow(64)
	for _, b := range []byte(s) {
		fmt.Fprintf(&hex, "%02x", b)
		if hex.Len() >= 64 {
			break
		}
	}
	for hex.Len() < 64 {
		hex.WriteByte('0')
	}
	return hex.String()[:64]
}
