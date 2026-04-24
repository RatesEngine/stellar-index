package polygonforex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// Poller implements external.Poller for Polygon.io Forex.
type Poller struct {
	APIKey   string
	Base     string        // target base currency (filter tickers to this)
	Endpoint string        // override for tests
	Interval time.Duration // poll cadence
}

// NewPoller constructs a Poller with validated config. Fails fast
// when APIKey is empty so operator catches the misconfiguration
// at indexer startup, not on first tick.
func NewPoller(apiKey string) (*Poller, error) {
	if apiKey == "" {
		return nil, ErrAPIKeyRequired
	}
	return &Poller{
		APIKey:   apiKey,
		Base:     DefaultBase,
		Endpoint: DefaultEndpoint,
		Interval: DefaultPollInterval,
	}, nil
}

// Name implements external.Connector.
func (p *Poller) Name() string { return SourceName }

// Class implements external.Connector.
func (p *Poller) Class() external.Class { return external.ClassExchange }

// PollInterval implements external.Poller.
func (p *Poller) PollInterval() time.Duration {
	if p.Interval <= 0 {
		return DefaultPollInterval
	}
	return p.Interval
}

// snapshotResponse matches Polygon's forex-snapshot shape.
type snapshotResponse struct {
	Status  string           `json:"status"`
	Tickers []snapshotTicker `json:"tickers"`
	Error   string           `json:"error,omitempty"`
	Message string           `json:"message,omitempty"`
}

type snapshotTicker struct {
	Ticker    string      `json:"ticker"`
	LastQuote lastQuote   `json:"lastQuote"`
	Updated   json.Number `json:"updated"` // ns-precision unix (int or string)
}

type lastQuote struct {
	Ask       json.Number `json:"a"`
	Bid       json.Number `json:"b"`
	Exchange  int         `json:"x"`
	Timestamp int64       `json:"t"` // ms unix
}

// PollOnce implements external.Poller. One HTTP GET returns every
// forex ticker; we filter to those where the base matches p.Base
// and the quote is on our fiat allow-list, then emit one
// OracleUpdate per qualifying ticker.
//
// Emission shape mirrors ExchangeRatesApi: asset = quote currency,
// quote = base currency, price = how many base units cost 1 asset
// unit (inverse of Polygon's "1 base → X quote" quote).
func (p *Poller) PollOnce(ctx context.Context, pairs []canonical.Pair) ([]canonical.Trade, []canonical.OracleUpdate, error) { //nolint:gocognit,gocyclo,funlen // dispatch-heavy; splitting would reduce linearity
	base := p.Base
	if base == "" {
		base = DefaultBase
	}

	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	q := url.Values{}
	q.Set("apiKey", p.APIKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+SnapshotPath+"?"+q.Encode(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}

	// Polygon returns 200 on most errors with the error detail in
	// the body. 401 / 429 come through as HTTP status codes.
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, nil, fmt.Errorf("%w: 401 unauthorized — check API key", ErrAPIRejected)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, nil, fmt.Errorf("%w: 429 rate limited — check tier", ErrAPIRejected)
	}
	if resp.StatusCode >= 400 {
		return nil, nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	var snap snapshotResponse
	if err := json.Unmarshal(body, &snap); err != nil {
		return nil, nil, fmt.Errorf("%w: decode: %w", ErrMalformedResponse, err)
	}
	if !strings.EqualFold(snap.Status, "OK") {
		detail := snap.Error
		if detail == "" {
			detail = snap.Message
		}
		return nil, nil, fmt.Errorf("%w: status=%q %s", ErrAPIRejected, snap.Status, detail)
	}

	baseAsset, err := canonical.NewFiatAsset(base)
	if err != nil {
		return nil, nil, fmt.Errorf("base asset %q: %w", base, err)
	}

	// Pre-index pair list so we only emit for fiats the operator
	// cares about (avoids flooding oracle_updates with exotic
	// Polygon coverage that nobody queries for).
	wantedQuotes := map[string]struct{}{}
	for _, pp := range pairs {
		for _, a := range []canonical.Asset{pp.Base, pp.Quote} {
			if a.Type == canonical.AssetFiat && !strings.EqualFold(a.Code, base) {
				wantedQuotes[strings.ToUpper(a.Code)] = struct{}{}
			}
		}
	}

	baseUpper := strings.ToUpper(base)
	out := make([]canonical.OracleUpdate, 0, len(snap.Tickers))

	for _, t := range snap.Tickers {
		tbase, tquote, err := parseCurrencyTicker(t.Ticker)
		if err != nil {
			continue
		}
		if tbase != baseUpper {
			continue // not a <base>/<X> pair we're interested in
		}
		// Operator-filter: only emit for fiats in the configured
		// pair list. When pairs is empty/crypto-only we emit for
		// every known quote — defensive, keeps the poller useful
		// when called with an empty pair set for testing.
		if len(wantedQuotes) > 0 {
			if _, ok := wantedQuotes[tquote]; !ok {
				continue
			}
		}

		quoteAsset, err := canonical.NewFiatAsset(tquote)
		if err != nil {
			// Not on the ADR-0010 allow-list — skip per-entry.
			continue
		}

		// Mid-price from ask/bid. If either is missing, fall back
		// to the other; skip if both are zero.
		ask := t.LastQuote.Ask.String()
		bid := t.LastQuote.Bid.String()
		mid, err := midPriceString(ask, bid)
		if err != nil {
			continue
		}
		scaled, err := decimalStringToScaledInt(mid, int(DefaultDecimals))
		if err != nil || scaled.Sign() <= 0 {
			continue
		}

		// Polygon quotes "1 base = X quote"; we emit "price of
		// <quote> in <base>" — invert.
		scalePow := pow10(int(DefaultDecimals))
		inverted := new(big.Int).Div(
			new(big.Int).Mul(scalePow, scalePow),
			scaled,
		)
		if inverted.Sign() <= 0 {
			continue
		}

		ts := time.UnixMilli(t.LastQuote.Timestamp).UTC()
		if t.LastQuote.Timestamp == 0 {
			ts = time.Now().UTC()
		}

		u := canonical.OracleUpdate{
			Source:     SourceName,
			ContractID: "",
			Ledger:     0,
			TxHash:     syntheticTxHash(tbase, tquote, t.LastQuote.Timestamp),
			OpIndex:    0,
			Timestamp:  ts,
			Asset:      quoteAsset,
			Quote:      baseAsset,
			Price:      canonical.NewAmount(inverted),
			Decimals:   DefaultDecimals,
			Observer:   "",
		}
		out = append(out, u)
	}
	return nil, out, nil
}

// parseCurrencyTicker splits "C:USDEUR" into ("USD", "EUR"). Returns
// ErrMalformedTicker for unexpected shapes.
func parseCurrencyTicker(t string) (string, string, error) {
	if !strings.HasPrefix(t, TickerPrefix) {
		return "", "", fmt.Errorf("%w: missing C: prefix in %q", ErrMalformedTicker, t)
	}
	s := strings.ToUpper(strings.TrimPrefix(t, TickerPrefix))
	if len(s) != 6 {
		return "", "", fmt.Errorf("%w: expected 6-char base+quote in %q", ErrMalformedTicker, t)
	}
	return s[:3], s[3:], nil
}

// midPriceString returns (ask+bid)/2 as a decimal string. If only
// one side is present we use that; if both zero or empty, error.
func midPriceString(ask, bid string) (string, error) {
	askInt, err := decimalStringToScaledInt(ask, int(DefaultDecimals))
	if err != nil || askInt == nil {
		askInt = big.NewInt(0)
	}
	bidInt, err := decimalStringToScaledInt(bid, int(DefaultDecimals))
	if err != nil || bidInt == nil {
		bidInt = big.NewInt(0)
	}
	switch {
	case askInt.Sign() > 0 && bidInt.Sign() > 0:
		sum := new(big.Int).Add(askInt, bidInt)
		mid := new(big.Int).Quo(sum, big.NewInt(2))
		return intToDecimalString(mid, int(DefaultDecimals)), nil
	case askInt.Sign() > 0:
		return intToDecimalString(askInt, int(DefaultDecimals)), nil
	case bidInt.Sign() > 0:
		return intToDecimalString(bidInt, int(DefaultDecimals)), nil
	}
	return "", fmt.Errorf("no usable ask/bid")
}

// decimalStringToScaledInt — shared helper replicating the
// per-package scaling convention.
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

// intToDecimalString re-formats a scaled integer as "<int>.<frac>"
// with exactly decimals digits after the point. Inverse of
// decimalStringToScaledInt.
func intToDecimalString(n *big.Int, decimals int) string {
	if n.Sign() == 0 {
		if decimals == 0 {
			return "0"
		}
		return "0." + strings.Repeat("0", decimals)
	}
	s := n.String()
	if len(s) <= decimals {
		// Left-pad with zeros.
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	cut := len(s) - decimals
	return s[:cut] + "." + s[cut:]
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// syntheticTxHash synthesises a 64-hex-char hash from
// (base, quote, timestamp). Stable across polls at the same ts.
func syntheticTxHash(base, quote string, ts int64) string {
	s := fmt.Sprintf("PGFX-%s-%s-%020d", base, quote, ts)
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
