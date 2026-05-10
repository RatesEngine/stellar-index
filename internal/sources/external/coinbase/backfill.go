package coinbase

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

// RESTEndpoint is Coinbase Exchange's public REST base.
const RESTEndpoint = "https://api.exchange.coinbase.com"

// candlesPathTemplate serves historical candles per product.
// Docs: https://docs.cloud.coinbase.com/exchange/reference/exchangerestapi_getproductcandles
const candlesPathTemplate = "/products/%s/candles"

// coinbaseMaxResponse is Coinbase's per-request cap — **300
// candles**, tightest of the four CEXes. We paginate aggressively
// to cover longer ranges.
const coinbaseMaxResponse = 300

// Backfill implements external.Backfiller for Coinbase Exchange.
//
// Coinbase's candle response is **a positional array with an unusual
// field order**: [time_sec, low, high, open, close, volume]. Every
// other CEX in our fleet uses OHLC (open/high/low/close); Coinbase
// swapped to LHOC. Positional parsers that assume OHLC get wrong
// prices silently. We read explicitly by index — comments below
// mark each slot.
//
// Response depth: no documented hard cap beyond the 300-candle
// per-request limit. Coinbase serves back to product listing
// (XLM-USD since 2019).
func (s *Streamer) Backfill(ctx context.Context, pair canonical.Pair, from, to time.Time, granularity time.Duration) ([]canonical.Trade, error) {
	if !from.Before(to) {
		return nil, fmt.Errorf("coinbase.Backfill: from %v must be before to %v", from, to)
	}
	granSec, err := granularityToSeconds(granularity)
	if err != nil {
		return nil, err
	}
	inverse := make(map[string]string, len(s.PairMap))
	for sym, p := range s.PairMap {
		inverse[p.String()] = sym
	}
	product, ok := inverse[pair.String()]
	if !ok {
		return nil, fmt.Errorf("coinbase.Backfill: pair %s not in configured PairMap", pair.String())
	}

	endpoint := s.restBase() + fmt.Sprintf(candlesPathTemplate, product)
	startSec := from.Unix()
	endSec := to.Unix()
	var out []canonical.Trade

	for startSec < endSec {
		// Coinbase paginates by `start` + `end` in ISO-8601 or
		// UNIX seconds. We request 300 candles' worth per call
		// to maximise per-request yield.
		windowEnd := startSec + int64(granSec)*coinbaseMaxResponse
		if windowEnd > endSec {
			windowEnd = endSec
		}

		q := url.Values{}
		q.Set("granularity", strconv.Itoa(granSec))
		q.Set("start", time.Unix(startSec, 0).UTC().Format(time.RFC3339))
		q.Set("end", time.Unix(windowEnd, 0).UTC().Format(time.RFC3339))

		candles, err := fetchCoinbaseCandles(ctx, endpoint, q)
		if err != nil {
			return nil, fmt.Errorf("coinbase.Backfill: %w", err)
		}
		if len(candles) == 0 {
			// Advance window even on empty response so an illiquid
			// range doesn't loop forever.
			startSec = windowEnd
			continue
		}

		// Coinbase returns candles in REVERSE chronological order
		// (newest first). Walk the slice backwards so we emit
		// chronologically.
		for i := len(candles) - 1; i >= 0; i-- {
			c := candles[i]
			trade, err := coinbaseCandleToTrade(c, product, pair, granSec)
			if err != nil {
				continue
			}
			out = append(out, trade)
		}

		// Advance startSec to one granularity past the most-recent
		// emitted candle (= candles[0].time_sec + granularity).
		newestOpen, ok := candles[0].openTimeSec()
		if !ok {
			break
		}
		next := newestOpen + int64(granSec)
		if next <= startSec {
			break
		}
		startSec = next
	}
	return out, nil
}

func (s *Streamer) restBase() string {
	if s.Endpoint == "" || strings.HasPrefix(s.Endpoint, "ws://") || strings.HasPrefix(s.Endpoint, "wss://") {
		return RESTEndpoint
	}
	return s.Endpoint
}

// coinbaseCandle is the positional-array form Coinbase returns.
// Layout: [time_sec, low, high, open, close, volume]. NOT the
// standard OHLC order — callers must address by index with care.
type coinbaseCandle []any

func (c coinbaseCandle) openTimeSec() (int64, bool) { return c.intAt(0) }
func (c coinbaseCandle) closeFloat() (float64, bool) {
	if len(c) < 5 {
		return 0, false
	}
	if v, ok := c[4].(float64); ok {
		return v, true
	}
	return 0, false
}

func (c coinbaseCandle) volumeFloat() (float64, bool) {
	if len(c) < 6 {
		return 0, false
	}
	if v, ok := c[5].(float64); ok {
		return v, true
	}
	return 0, false
}

func (c coinbaseCandle) intAt(i int) (int64, bool) {
	if i >= len(c) {
		return 0, false
	}
	switch v := c[i].(type) {
	case float64:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	}
	return 0, false
}

func fetchCoinbaseCandles(ctx context.Context, endpoint string, q url.Values) ([]coinbaseCandle, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	// Coinbase requires a User-Agent — unlike most public APIs,
	// empty User-Agent returns 400.
	req.Header.Set("User-Agent", "ratesengine/1.0")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}
	var out []coinbaseCandle
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return out, nil
}

// coinbaseCandleToTrade synthesises a canonical.Trade. Coinbase
// doesn't publish quote volume — we compute it as close × volume,
// same approach as Bitstamp.
func coinbaseCandleToTrade(c coinbaseCandle, product string, pair canonical.Pair, granSec int) (canonical.Trade, error) {
	openSec, ok := c.openTimeSec()
	if !ok {
		return canonical.Trade{}, fmt.Errorf("missing time")
	}
	closeSec := openSec + int64(granSec) - 1

	vol, ok := c.volumeFloat()
	if !ok || vol == 0 {
		return canonical.Trade{}, fmt.Errorf("missing or zero volume")
	}
	closePrice, ok := c.closeFloat()
	if !ok || closePrice == 0 {
		return canonical.Trade{}, fmt.Errorf("missing or zero close")
	}

	// Coinbase returns amounts as JSON numbers (not strings),
	// so we round-trip through the string form via FormatFloat
	// to avoid float-state leakage into our integer math. At
	// 8dp this is lossless for any realistic candle volume
	// (<2^53).
	base, err := decimalStringToScaledInt(
		strconv.FormatFloat(vol, 'f', externalAmountDecimals, 64),
		externalAmountDecimals,
	)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("volume: %w", err)
	}
	price, err := decimalStringToScaledInt(
		strconv.FormatFloat(closePrice, 'f', externalAmountDecimals, 64),
		externalAmountDecimals,
	)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("close: %w", err)
	}
	quoteRaw := new(big.Int).Mul(base, price)
	quote := new(big.Int).Quo(quoteRaw, pow10(externalAmountDecimals))
	if quote.Sign() == 0 {
		return canonical.Trade{}, ErrDustTrade
	}

	return canonical.Trade{
		Source:      SourceName,
		Ledger:      0,
		TxHash:      backfillTxHash(product, closeSec),
		OpIndex:     0,
		Timestamp:   time.Unix(closeSec, 0).UTC(),
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(base),
		QuoteAmount: canonical.NewAmount(quote),
	}, nil
}

// backfillTxHash from (product, close_time_sec). Dash-stripped to
// match the live-stream hash convention.
func backfillTxHash(product string, closeSec int64) string {
	normalised := strings.ReplaceAll(strings.ToUpper(product), "-", "")
	s := fmt.Sprintf("%s-BF-%020d", normalised, closeSec)
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

// granularityToSeconds maps time.Duration → Coinbase's supported
// granularity values (seconds): 60, 300, 900, 3600, 21600, 86400.
// Narrower set than Binance/Kraken/Bitstamp — Coinbase only
// supports these six.
func granularityToSeconds(d time.Duration) (int, error) {
	switch d {
	case 1 * time.Minute:
		return 60, nil
	case 5 * time.Minute:
		return 300, nil
	case 15 * time.Minute:
		return 900, nil
	case 1 * time.Hour:
		return 3600, nil
	case 6 * time.Hour:
		return 21600, nil
	case 24 * time.Hour:
		return 86400, nil
	}
	return 0, fmt.Errorf("coinbase.Backfill: unsupported granularity %v (supported: 1m/5m/15m/1h/6h/1d)", d)
}
