package bitstamp

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

// RESTEndpoint is Bitstamp's public v2 REST base. Trade and OHLC
// endpoints need no auth; private endpoints use HMAC.
const RESTEndpoint = "https://www.bitstamp.net"

// ohlcPathTemplate is the per-pair OHLC path — pair slot is
// substituted with the lowercase concat symbol ("xlmusd").
const ohlcPathTemplate = "/api/v2/ohlc/%s/"

// bitstampMaxLimit is the per-request candle cap. Higher values
// silently clamp to this; explicit limit=1000 + pagination via
// `start` is the reliable path.
const bitstampMaxLimit = 1000

// Backfill implements external.Backfiller. Returns historical
// trades synthesised from Bitstamp OHLC candles.
//
// Response depth: Bitstamp retains historical OHLC back to pair
// listing — XLMUSD to 2017, BTCUSD further. Deeper than Kraken's
// 720-interval cap, so operators wanting multi-year backfill for
// XLM in USD/EUR/GBP should prefer Bitstamp over Kraken.
func (s *Streamer) Backfill(ctx context.Context, pair canonical.Pair, from, to time.Time, granularity time.Duration) ([]canonical.Trade, error) {
	if !from.Before(to) {
		return nil, fmt.Errorf("bitstamp.Backfill: from %v must be before to %v", from, to)
	}
	stepSec, err := granularityToSeconds(granularity)
	if err != nil {
		return nil, err
	}
	inverse := make(map[string]string, len(s.PairMap))
	for sym, p := range s.PairMap {
		inverse[p.String()] = sym
	}
	symbol, ok := inverse[pair.String()]
	if !ok {
		return nil, fmt.Errorf("bitstamp.Backfill: pair %s not in configured PairMap", pair.String())
	}

	endpoint := s.restBase() + fmt.Sprintf(ohlcPathTemplate, symbol)
	startSec := from.Unix()
	endSec := to.Unix()
	var out []canonical.Trade

	for startSec < endSec {
		q := url.Values{}
		q.Set("step", strconv.Itoa(stepSec))
		q.Set("limit", strconv.Itoa(bitstampMaxLimit))
		q.Set("start", strconv.FormatInt(startSec, 10))
		q.Set("end", strconv.FormatInt(endSec, 10))

		candles, err := fetchBitstampOHLC(ctx, endpoint, q)
		if err != nil {
			return nil, fmt.Errorf("bitstamp.Backfill: %w", err)
		}
		if len(candles) == 0 {
			break
		}

		for _, c := range candles {
			trade, err := bitstampCandleToTrade(c, symbol, pair, stepSec)
			if err != nil {
				continue
			}
			out = append(out, trade)
		}

		// Advance: one step past the last candle's open time.
		lastTs, err := strconv.ParseInt(candles[len(candles)-1].Timestamp, 10, 64)
		if err != nil {
			break
		}
		next := lastTs + int64(stepSec)
		if next <= startSec {
			break
		}
		startSec = next
		if len(candles) < bitstampMaxLimit {
			break
		}
	}
	return out, nil
}

// restBase returns the REST URL, falling back to production when
// Endpoint is the streaming ws:// default.
func (s *Streamer) restBase() string {
	if s.Endpoint == "" || strings.HasPrefix(s.Endpoint, "ws://") || strings.HasPrefix(s.Endpoint, "wss://") {
		return RESTEndpoint
	}
	return s.Endpoint
}

// bitstampCandle matches Bitstamp's OHLC response entry. All
// numeric fields arrive as strings (matches the WS precision
// policy). Timestamp is UNIX seconds as a string.
type bitstampCandle struct {
	Timestamp string `json:"timestamp"`
	Open      string `json:"open"`
	High      string `json:"high"`
	Low       string `json:"low"`
	Close     string `json:"close"`
	Volume    string `json:"volume"` // base asset
}

// ohlcResponse matches the wrapping envelope.
type ohlcResponse struct {
	Data struct {
		Pair string           `json:"pair"`
		OHLC []bitstampCandle `json:"ohlc"`
	} `json:"data"`
}

func fetchBitstampOHLC(ctx context.Context, endpoint string, q url.Values) ([]bitstampCandle, error) {
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
	var r ohlcResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return r.Data.OHLC, nil
}

// bitstampCandleToTrade synthesises a canonical.Trade from one
// candle. Bitstamp doesn't publish a VWAP or quote-volume field —
// we use close price + base volume and derive quote as
// close × volume. Close-weighted is not strictly VWAP but for
// 1h/1d buckets it's a reasonable approximation.
func bitstampCandleToTrade(c bitstampCandle, symbol string, pair canonical.Pair, stepSec int) (canonical.Trade, error) {
	openSec, err := strconv.ParseInt(c.Timestamp, 10, 64)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("timestamp %q: %w", c.Timestamp, err)
	}
	closeSec := openSec + int64(stepSec) - 1

	base, err := decimalStringToScaledInt(c.Volume, externalAmountDecimals)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("volume %q: %w", c.Volume, err)
	}
	if base.Sign() == 0 {
		return canonical.Trade{}, fmt.Errorf("zero volume")
	}
	price, err := decimalStringToScaledInt(c.Close, externalAmountDecimals)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("close %q: %w", c.Close, err)
	}
	quoteRaw := new(big.Int).Mul(base, price)
	quote := new(big.Int).Quo(quoteRaw, pow10(externalAmountDecimals))

	return canonical.Trade{
		Source:      SourceName,
		Ledger:      0,
		TxHash:      backfillTxHash(symbol, closeSec),
		OpIndex:     0,
		Timestamp:   time.Unix(closeSec, 0).UTC(),
		Pair:        pair,
		BaseAmount:  canonical.NewAmount(base),
		QuoteAmount: canonical.NewAmount(quote),
	}, nil
}

// backfillTxHash is the Bitstamp analogue of the per-venue
// synthetic hash. Symbol is the lowercase concat form.
func backfillTxHash(symbol string, closeSec int64) string {
	normalised := strings.ToUpper(symbol)
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

// granularityToSeconds maps Durations to Bitstamp's step values
// (seconds). Supported: 60, 180, 300, 900, 1800, 3600, 7200,
// 14400, 21600, 43200, 86400, 259200.
func granularityToSeconds(d time.Duration) (int, error) {
	switch d {
	case 1 * time.Minute:
		return 60, nil
	case 3 * time.Minute:
		return 180, nil
	case 5 * time.Minute:
		return 300, nil
	case 15 * time.Minute:
		return 900, nil
	case 30 * time.Minute:
		return 1800, nil
	case 1 * time.Hour:
		return 3600, nil
	case 2 * time.Hour:
		return 7200, nil
	case 4 * time.Hour:
		return 14400, nil
	case 6 * time.Hour:
		return 21600, nil
	case 12 * time.Hour:
		return 43200, nil
	case 24 * time.Hour:
		return 86400, nil
	case 3 * 24 * time.Hour:
		return 259200, nil
	}
	return 0, fmt.Errorf("bitstamp.Backfill: unsupported granularity %v (supported: 1m/3m/5m/15m/30m/1h/2h/4h/6h/12h/1d/3d)", d)
}
