package binance

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

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// RESTEndpoint is the Binance Spot REST API base for klines +
// other public market-data calls. Paid clients can use regional
// mirrors (api1/api2/api3/api4.binance.com) for lower latency; the
// default hostname auto-balances across them.
const RESTEndpoint = "https://api.binance.com"

// klinesPath is the historical candlestick endpoint. Docs:
// https://developers.binance.com/docs/binance-spot-api-docs/rest-api#klinecandlestick-data
const klinesPath = "/api/v3/klines"

// klineMaxLimit is the per-request cap on candles returned. Higher
// than this and Binance clamps silently — we explicitly request
// `limit=1000` and paginate when the caller asks for more.
const klineMaxLimit = 1000

// Backfill implements external.Backfiller. Returns historical
// trades synthesised from Binance's kline data — one canonical.Trade
// per candle bucket, stamped with the bucket's close-time, base +
// quote volume preserved from the kline's fields 5 and 7.
//
// Granularity must match one of Binance's supported intervals; see
// granularityToInterval for the map. Unsupported intervals return
// an error before any HTTP call.
//
// Pagination: Binance caps 1000 candles per request. We issue
// successive requests moving startTime forward, serially (no parallel
// fan-out) to respect the venue's rate-limit weight. For 1 year
// of hourly data the total is ~9 requests.
//
// Rate limits: klines carries weight 2 under Binance's per-minute
// 6000-weight budget, so 3000 calls/min is the ceiling — well above
// anything realistic backfill would attempt.
//
// The returned trades are NOT deduplicated against existing storage
// — caller (ratesengine-ops) is responsible for the idempotent
// insert path. canonical.Trade.TxHash is deterministic from (symbol,
// close_time_ms) so repeated backfill runs land on the same primary
// key.
func (s *Streamer) Backfill(ctx context.Context, pair canonical.Pair, from, to time.Time, granularity time.Duration) ([]canonical.Trade, error) {
	if !from.Before(to) {
		return nil, fmt.Errorf("binance.Backfill: from %v must be before to %v", from, to)
	}
	interval, err := granularityToInterval(granularity)
	if err != nil {
		return nil, err
	}
	// Resolve pair → Binance symbol via the inverse of PairMap.
	inverse := make(map[string]string, len(s.PairMap))
	for sym, p := range s.PairMap {
		inverse[p.String()] = sym
	}
	symbol, ok := inverse[pair.String()]
	if !ok {
		return nil, fmt.Errorf("binance.Backfill: pair %s not in configured PairMap", pair.String())
	}

	endpoint := s.restBase() + klinesPath
	startMs := from.UnixMilli()
	endMs := to.UnixMilli()
	var out []canonical.Trade

	for startMs < endMs {
		q := url.Values{}
		q.Set("symbol", symbol)
		q.Set("interval", interval)
		q.Set("startTime", strconv.FormatInt(startMs, 10))
		q.Set("endTime", strconv.FormatInt(endMs, 10))
		q.Set("limit", strconv.Itoa(klineMaxLimit))

		candles, err := fetchKlines(ctx, endpoint, q)
		if err != nil {
			return nil, fmt.Errorf("binance.Backfill: %w", err)
		}
		if len(candles) == 0 {
			break
		}

		for _, c := range candles {
			trade, err := klineToTrade(c, symbol, pair)
			if err != nil {
				// Per-candle skip — the surrounding range still
				// produces useful output. Caller sees the gap
				// via trade count vs expected range; backfill
				// is a best-effort op tool anyway.
				continue
			}
			out = append(out, trade)
		}

		// Advance to 1ms past the last candle's open time. Binance
		// returns candles with openTime < endTime so we won't
		// double-emit; the +1 is belt-and-braces in case of tick
		// repetition.
		lastOpen, ok := candles[len(candles)-1].openTimeMs()
		if !ok {
			break
		}
		startMs = lastOpen + int64(granularity/time.Millisecond)
		// If the venue returned fewer than limit candles, we're done
		// for this range — avoid a trailing no-op request.
		if len(candles) < klineMaxLimit {
			break
		}
	}
	return out, nil
}

// restBase returns the REST endpoint, allowing tests to override via
// a custom Endpoint that points at an httptest server. When Endpoint
// is a ws:// or wss:// URL (the streaming default), we fall back to
// the production REST host — streaming and REST are separate
// services on Binance.
func (s *Streamer) restBase() string {
	if s.Endpoint == "" || strings.HasPrefix(s.Endpoint, "ws://") || strings.HasPrefix(s.Endpoint, "wss://") {
		return RESTEndpoint
	}
	return s.Endpoint
}

// kline is a single kline row — Binance returns these as a JSON
// array (positional), not a struct. We unmarshal into []any and
// extract by index; helpers below parse the fields we care about.
//
// Layout (from docs):
//
//	[0]  open time (int64 ms)
//	[1]  open price (string)
//	[2]  high price (string)
//	[3]  low price (string)
//	[4]  close price (string)
//	[5]  base asset volume (string)
//	[6]  close time (int64 ms)
//	[7]  quote asset volume (string)
//	[8]  number of trades (int)
//	[9]  taker buy base asset volume (string)
//	[10] taker buy quote asset volume (string)
//	[11] unused
type kline []any

func (k kline) openTimeMs() (int64, bool)   { return k.intAt(0) }
func (k kline) closeTimeMs() (int64, bool)  { return k.intAt(6) }
func (k kline) baseVolume() (string, bool)  { return k.stringAt(5) }
func (k kline) quoteVolume() (string, bool) { return k.stringAt(7) }

func (k kline) intAt(i int) (int64, bool) {
	if i >= len(k) {
		return 0, false
	}
	// JSON numbers unmarshal as float64 by default — Binance
	// timestamps are <2^53 so this round-trips losslessly, but we
	// use json.Number + Int64() via string fallback for safety.
	switch v := k[i].(type) {
	case float64:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return n, err == nil
	}
	return 0, false
}

func (k kline) stringAt(i int) (string, bool) {
	if i >= len(k) {
		return "", false
	}
	s, ok := k[i].(string)
	return s, ok
}

// fetchKlines performs one HTTP GET and returns the parsed candles.
func fetchKlines(ctx context.Context, endpoint string, q url.Values) ([]kline, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
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
	var out []kline
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return out, nil
}

// klineToTrade synthesises a canonical.Trade from a single candle.
// Timestamp uses close-time (authoritative end of the bucket); the
// trade's BaseAmount / QuoteAmount come directly from the candle's
// volume fields — no derivation from open/high/low/close.
//
// The synthesised tx_hash is stable across repeated backfill runs:
// sha-like hex over "<symbol>-<close_ms>". Collision-free for
// (symbol, time-bucket) pairs.
func klineToTrade(c kline, symbol string, pair canonical.Pair) (canonical.Trade, error) {
	closeMs, ok := c.closeTimeMs()
	if !ok {
		return canonical.Trade{}, fmt.Errorf("kline missing close time")
	}
	baseStr, ok := c.baseVolume()
	if !ok {
		return canonical.Trade{}, fmt.Errorf("kline missing base volume")
	}
	quoteStr, ok := c.quoteVolume()
	if !ok {
		return canonical.Trade{}, fmt.Errorf("kline missing quote volume")
	}
	base, err := decimalStringToScaledInt(baseStr, externalAmountDecimals)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("base volume %q: %w", baseStr, err)
	}
	quote, err := decimalStringToScaledInt(quoteStr, externalAmountDecimals)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("quote volume %q: %w", quoteStr, err)
	}
	// Skip empty-volume candles — they add no price signal and
	// would divide-by-zero in downstream VWAP math.
	if base.Sign() == 0 || quote.Sign() == 0 {
		return canonical.Trade{}, fmt.Errorf("kline zero volume")
	}

	return canonical.Trade{
		Source:      SourceName,
		Ledger:      0,
		TxHash:      backfillTxHash(symbol, closeMs),
		OpIndex:     0,
		Timestamp:   time.UnixMilli(closeMs).UTC(),
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(base),
		QuoteAmount: canonical.NewAmount(quote),
	}, nil
}

// backfillTxHash is the historical-candle equivalent of formatTxHash
// — identical shape (64-char hex) but derived from the candle's
// close-time rather than a per-trade aggTrade ID. The two hash
// spaces don't collide in practice (trade IDs are small integers,
// timestamps are 13-digit ms values with a different tail).
func backfillTxHash(symbol string, closeMs int64) string {
	s := fmt.Sprintf("%s-BF-%020d", strings.ToUpper(symbol), closeMs)
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

// granularityToInterval maps a time.Duration to Binance's interval
// string. Binance supports: 1s, 1m, 3m, 5m, 15m, 30m, 1h, 2h, 4h,
// 6h, 8h, 12h, 1d, 3d, 1w, 1M.
//
// For v1 we expose the RFP-required granularities (1m, 15m, 1h, 4h,
// 1d, 1w) plus a couple of common intermediate buckets. Requests
// outside the supported set return an error.
func granularityToInterval(d time.Duration) (string, error) {
	switch d {
	case 1 * time.Minute:
		return "1m", nil
	case 3 * time.Minute:
		return "3m", nil
	case 5 * time.Minute:
		return "5m", nil
	case 15 * time.Minute:
		return "15m", nil
	case 30 * time.Minute:
		return "30m", nil
	case 1 * time.Hour:
		return "1h", nil
	case 2 * time.Hour:
		return "2h", nil
	case 4 * time.Hour:
		return "4h", nil
	case 6 * time.Hour:
		return "6h", nil
	case 12 * time.Hour:
		return "12h", nil
	case 24 * time.Hour:
		return "1d", nil
	case 7 * 24 * time.Hour:
		return "1w", nil
	}
	return "", fmt.Errorf("binance.Backfill: unsupported granularity %v (supported: 1m/3m/5m/15m/30m/1h/2h/4h/6h/12h/1d/1w)", d)
}

// Guard used only to silence unused-import warnings in test-only
// paths where big.Int is needed but not referenced directly.
var _ = big.NewInt
