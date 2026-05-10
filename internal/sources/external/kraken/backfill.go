package kraken

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// RESTEndpoint is Kraken's public REST base. Public endpoints need
// no auth; private (trading) endpoints do, but we never use them.
const RESTEndpoint = "https://api.kraken.com"

// ohlcPath is the historical candle endpoint.
// Docs: https://docs.kraken.com/api/docs/rest-api/get-ohlc-data
const ohlcPath = "/0/public/OHLC"

// krakenMaxResponse is the hard cap on candles returned per call —
// documented at 720 and unaffected by query params. Implies ~30
// days at 1h granularity or ~30 weeks at 1d. The caller can
// paginate via `since` but the venue's total historical depth is
// effectively capped at this window for recent data (older data is
// simply not served via this endpoint).
const krakenMaxResponse = 720

// Backfill implements external.Backfiller. Returns historical
// trades synthesised from Kraken OHLC candles — one canonical.Trade
// per bucket, with Kraken's own VWAP field providing the effective
// price (multiplied by base volume to compute quote volume).
//
// IMPORTANT depth caveat: Kraken caps at 720 intervals. At 1h that's
// ~30 days back; at 1d it's ~2 years. Callers asking for more get
// a truncated response — log + continue; it's the venue's limit,
// not a bug in our code. Documented in
// docs/discovery/oracles/band.md and external.Registry
// (BackfillAvailable=true with 30-day caveat).
func (s *Streamer) Backfill(ctx context.Context, pair canonical.Pair, from, to time.Time, granularity time.Duration) ([]canonical.Trade, error) { //nolint:gocognit // dispatch-heavy; splitting would reduce linearity
	if !from.Before(to) {
		return nil, fmt.Errorf("kraken.Backfill: from %v must be before to %v", from, to)
	}
	interval, err := granularityToMinutes(granularity)
	if err != nil {
		return nil, err
	}
	inverse := make(map[string]string, len(s.PairMap))
	for sym, p := range s.PairMap {
		inverse[p.String()] = sym
	}
	symbol, ok := inverse[pair.String()]
	if !ok {
		return nil, fmt.Errorf("kraken.Backfill: pair %s not in configured PairMap", pair.String())
	}

	endpoint := s.restBase() + ohlcPath
	sinceSec := from.Unix()
	endSec := to.Unix()
	var out []canonical.Trade

	for sinceSec < endSec {
		q := url.Values{}
		q.Set("pair", symbol)
		q.Set("interval", strconv.Itoa(interval))
		if sinceSec > 0 {
			q.Set("since", strconv.FormatInt(sinceSec, 10))
		}

		candles, lastTs, err := fetchKrakenOHLC(ctx, endpoint, q)
		if err != nil {
			return nil, fmt.Errorf("kraken.Backfill: %w", err)
		}
		if len(candles) == 0 {
			break
		}

		// Each candle's time is the OPEN time. We stamp the
		// synthesised Trade with the close time — open + interval.
		intervalSec := int64(granularity / time.Second)
		for _, c := range candles {
			openTs, ok := c.openTimeSec()
			if !ok {
				continue
			}
			if openTs >= endSec {
				break
			}
			closeTs := openTs + intervalSec - 1
			trade, err := krakenCandleToTrade(c, symbol, pair, closeTs)
			if err != nil {
				continue
			}
			out = append(out, trade)
		}

		// Advance via the `last` cursor Kraken returns, one interval
		// past the last delivered candle. Guard against no-progress
		// in the rare case `last` doesn't move.
		next := lastTs + 1
		if next <= sinceSec {
			break
		}
		sinceSec = next
		if len(candles) < krakenMaxResponse {
			break
		}
	}
	return out, nil
}

// restBase returns the REST URL. When Endpoint is a ws:// URL (the
// streaming default), we fall back to the production REST host.
// Tests override with an http:// value to redirect at httptest.
func (s *Streamer) restBase() string {
	if s.Endpoint == "" || strings.HasPrefix(s.Endpoint, "ws://") || strings.HasPrefix(s.Endpoint, "wss://") {
		return RESTEndpoint
	}
	return s.Endpoint
}

// krakenCandle is the positional-array form Kraken returns.
// Layout: [time, open, high, low, close, vwap, volume, count].
type krakenCandle []any

func (k krakenCandle) openTimeSec() (int64, bool) { return k.intAt(0) }
func (k krakenCandle) closeStr() (string, bool)   { return k.stringAt(4) }
func (k krakenCandle) vwapStr() (string, bool)    { return k.stringAt(5) }
func (k krakenCandle) volumeStr() (string, bool)  { return k.stringAt(6) }

func (k krakenCandle) intAt(i int) (int64, bool) {
	if i >= len(k) {
		return 0, false
	}
	switch v := k[i].(type) {
	case float64:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	}
	return 0, false
}

func (k krakenCandle) stringAt(i int) (string, bool) {
	if i >= len(k) {
		return "", false
	}
	s, ok := k[i].(string)
	return s, ok
}

// ohlcResponse is the shape Kraken returns for /0/public/OHLC.
// The `result` field has a dynamic key (the pair name) plus a
// `last` sentinel integer, so we decode it as RawMessage and
// fish out the candle array by iterating keys.
type ohlcResponse struct {
	Error  []string                   `json:"error"`
	Result map[string]json.RawMessage `json:"result"`
}

// fetchKrakenOHLC performs one HTTP GET and returns candles +
// the `last` timestamp cursor (for pagination).
func fetchKrakenOHLC(ctx context.Context, endpoint string, q url.Values) ([]krakenCandle, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, 0, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}
	var r ohlcResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, 0, fmt.Errorf("decode: %w", err)
	}
	if len(r.Error) > 0 {
		return nil, 0, fmt.Errorf("kraken api error: %v", r.Error)
	}

	var candles []krakenCandle
	var last int64
	for key, raw := range r.Result {
		if key == "last" {
			// `last` is a single integer (unquoted).
			_ = json.Unmarshal(raw, &last)
			continue
		}
		// Any other key is the pair's candle array.
		if err := json.Unmarshal(raw, &candles); err != nil {
			return nil, 0, fmt.Errorf("decode candles for %s: %w", key, err)
		}
	}
	return candles, last, nil
}

// krakenCandleToTrade synthesises a canonical.Trade from a Kraken
// candle. Price is the candle's VWAP (authoritative for the
// bucket); quote amount is computed as price × base volume.
func krakenCandleToTrade(c krakenCandle, symbol string, pair canonical.Pair, closeTs int64) (canonical.Trade, error) {
	volStr, ok := c.volumeStr()
	if !ok {
		return canonical.Trade{}, fmt.Errorf("missing volume")
	}
	vwapStr, ok := c.vwapStr()
	if !ok || vwapStr == "0" || vwapStr == "0.0" {
		// Fall back to close price when VWAP is unavailable (rare,
		// happens for zero-volume buckets which we'd skip anyway).
		vwapStr, ok = c.closeStr()
		if !ok {
			return canonical.Trade{}, fmt.Errorf("missing vwap + close")
		}
	}
	base, err := decimalStringToScaledInt(volStr, externalAmountDecimals)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("volume %q: %w", volStr, err)
	}
	if base.Sign() == 0 {
		return canonical.Trade{}, fmt.Errorf("zero volume")
	}
	price, err := decimalStringToScaledInt(vwapStr, externalAmountDecimals)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("vwap %q: %w", vwapStr, err)
	}
	// quote = base × price / 10^8
	quoteRaw := new(big.Int).Mul(base, price)
	quote := new(big.Int).Quo(quoteRaw, pow10(externalAmountDecimals))
	// Dust filter — see parse.go::buildTrade for the rationale.
	// Same underflow can happen on a candle whose `volume` * `vwap`
	// rounds to 0 at our 10^8 precision floor.
	if quote.Sign() == 0 {
		return canonical.Trade{}, ErrDustTrade
	}

	return canonical.Trade{
		Source:      SourceName,
		Ledger:      0,
		TxHash:      backfillTxHash(symbol, closeTs),
		OpIndex:     0,
		Timestamp:   time.Unix(closeTs, 0).UTC(),
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(base),
		QuoteAmount: canonical.NewAmount(quote),
	}, nil
}

// backfillTxHash mirrors the Binance pattern but sources from
// Kraken's symbol format (no slashes at this layer — we work from
// the venue's v1 altname).
func backfillTxHash(symbol string, closeTs int64) string {
	normalised := strings.ReplaceAll(strings.ToUpper(symbol), "/", "")
	s := fmt.Sprintf("%s-BF-%020d", normalised, closeTs)
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

// granularityToMinutes maps a time.Duration to Kraken's interval
// parameter (in minutes). Kraken accepts: 1, 5, 15, 30, 60, 240,
// 1440, 10080, 21600 (minutes).
func granularityToMinutes(d time.Duration) (int, error) {
	switch d {
	case 1 * time.Minute:
		return 1, nil
	case 5 * time.Minute:
		return 5, nil
	case 15 * time.Minute:
		return 15, nil
	case 30 * time.Minute:
		return 30, nil
	case 1 * time.Hour:
		return 60, nil
	case 4 * time.Hour:
		return 240, nil
	case 24 * time.Hour:
		return 1440, nil
	case 7 * 24 * time.Hour:
		return 10080, nil
	case 15 * 24 * time.Hour:
		return 21600, nil
	}
	return 0, fmt.Errorf("kraken.Backfill: unsupported granularity %v (supported: 1m/5m/15m/30m/1h/4h/1d/1w/15d)", d)
}
