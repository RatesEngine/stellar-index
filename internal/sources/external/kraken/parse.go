package kraken

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// externalAmountDecimals mirrors the Binance constant: every
// off-chain source normalises to 10^8 integer scale.
const externalAmountDecimals = 8

// channelEnvelope is the shared outer shape for every v2 message
// (trade, heartbeat, status, subscribe-ack, error). We dispatch on
// Channel first, then on Type (for trade frames — snapshot vs
// update) to pick the decoder.
type channelEnvelope struct {
	Channel string          `json:"channel"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
	// Method / Success present on subscribe acks. Error present on
	// subscribe rejections. We look at them only to classify the
	// frame; real error handling is in the streamer loop.
	Method  string          `json:"method,omitempty"`
	Success *bool           `json:"success,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

// tradePayload is one entry in a v2 trade frame's `data` array.
// Field names verified against
// docs.kraken.com/api/docs/websocket-v2/trade (2026-04-24).
//
// qty / price are json.Number so the decimal-string form reaches
// our scaling helper losslessly — float64 is fine at Kraken's
// precision but the i128 invariant says no floats for price paths.
type tradePayload struct {
	Symbol    string      `json:"symbol"`
	Side      string      `json:"side"`      // "buy" | "sell" (unused for price/volume, retained for future attribution)
	Qty       json.Number `json:"qty"`       // base-asset quantity, decimal
	Price     json.Number `json:"price"`     // quote-per-base, decimal
	OrdType   string      `json:"ord_type"`  // "market" | "limit" (unused)
	TradeID   int64       `json:"trade_id"`  // per-symbol monotonic
	Timestamp string      `json:"timestamp"` // RFC 3339 with subsecond precision
}

// parseFrame dispatches on the channel field; trade frames yield
// zero or more canonical.Trade values (snapshot carries many,
// update carries one or a few). Heartbeat / status / subscribe-ack
// frames return (nil, nil) — stream stays open, no logging
// required at the parse layer.
//
// Malformed frames return ErrMalformedFrame wrapped; the streamer
// counts and continues.
func parseFrame(raw []byte, pairMap map[string]canonical.Pair) ([]canonical.Trade, error) {
	var env channelEnvelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // preserve the decimal representation of qty/price
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("%w: envelope: %w", ErrMalformedFrame, err)
	}

	switch env.Channel {
	case ChannelTrade:
		return parseTradeFrame(env, pairMap)
	case ChannelHeartbeat, ChannelStatus:
		return nil, nil
	}

	// subscribe-ack / error frames carry Method but no Channel.
	// Non-trade envelopes with no Channel are not our concern —
	// silently ignore so the streamer loop doesn't flag ack
	// frames as decode failures.
	return nil, nil
}

// parseTradeFrame decodes the `data` array of a trade channel
// frame. Each entry is one trade; all share the same channel+type
// metadata.
func parseTradeFrame(env channelEnvelope, pairMap map[string]canonical.Pair) ([]canonical.Trade, error) {
	if len(env.Data) == 0 {
		return nil, nil
	}
	var items []tradePayload
	dec := json.NewDecoder(bytes.NewReader(env.Data))
	dec.UseNumber()
	if err := dec.Decode(&items); err != nil {
		return nil, fmt.Errorf("%w: trade data: %w", ErrMalformedFrame, err)
	}
	out := make([]canonical.Trade, 0, len(items))
	for i, t := range items {
		trade, err := buildTrade(t, pairMap)
		if err != nil {
			// Per-entry skip rather than aborting the whole frame;
			// the streamer counts the skip through its metrics
			// hook (future wiring).
			_ = i
			continue
		}
		out = append(out, trade)
	}
	return out, nil
}

// buildTrade turns one decoded tradePayload into a canonical.Trade.
func buildTrade(t tradePayload, pairMap map[string]canonical.Pair) (canonical.Trade, error) {
	pair, ok := pairMap[strings.ToUpper(t.Symbol)]
	if !ok {
		return canonical.Trade{}, fmt.Errorf("%w: %q", ErrUnknownSymbol, t.Symbol)
	}

	base, err := decimalStringToScaledInt(t.Qty.String(), externalAmountDecimals)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: qty %q: %w", ErrMalformedFrame, t.Qty.String(), err)
	}
	price, err := decimalStringToScaledInt(t.Price.String(), externalAmountDecimals)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: price %q: %w", ErrMalformedFrame, t.Price.String(), err)
	}
	// quote = base × price / 10^8
	quoteRaw := new(big.Int).Mul(base, price)
	quote := new(big.Int).Quo(quoteRaw, pow10(externalAmountDecimals))

	// Dust filter — when base × price floor-divides to 0 (e.g.
	// a 1e-8 XLM lot at $0.16), the canonical validator rejects
	// the row with "quote_amount must be positive, got 0". These
	// are real Kraken trades, just below our integer-scale
	// precision floor; drop silently. Same shape as the
	// Coinbase + Binance + Bitstamp dust filter.
	if quote.Sign() == 0 {
		return canonical.Trade{}, ErrDustTrade
	}

	ts, err := time.Parse(time.RFC3339Nano, t.Timestamp)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: timestamp %q: %w", ErrMalformedFrame, t.Timestamp, err)
	}

	return canonical.Trade{
		Source:      SourceName,
		Ledger:      0, // no ledger off-chain
		TxHash:      formatTxHash(t.Symbol, t.TradeID),
		OpIndex:     0,
		Timestamp:   ts.UTC(),
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(base),
		QuoteAmount: canonical.NewAmount(quote),
	}, nil
}

// decimalStringToScaledInt converts a decimal string to a *big.Int
// scaled by 10^targetDecimals. Semantics identical to the Binance
// helper — duplicated rather than shared because the amount-scaling
// convention is source-package-local and a future refactor to
// per-source scales would prefer the duplication.
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
	combined := intPart + fracPart
	v, ok := new(big.Int).SetString(combined, 10)
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

// formatTxHash — see binance.formatTxHash for rationale. 64-char
// hex synthesised from (symbol, trade_id) for canonical.Trade
// validation.
func formatTxHash(symbol string, tradeID int64) string {
	// Normalise symbol — strip slash so the hash matches regardless
	// of how Kraken formats it. "XLM/USD" and "XLMUSD" yield the
	// same underlying bytes prefix; safe for dedup across potential
	// future alias changes.
	normalised := strings.ReplaceAll(strings.ToUpper(symbol), "/", "")
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
