package exchangeratesapi

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
	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// Poller implements external.Poller for exchangeratesapi.io. One
// Poller per process; thread-safe — PollOnce is the only method
// the framework calls and it's side-effect-free besides the HTTP
// GET.
type Poller struct {
	// APIKey is the access_key passed as a query parameter.
	// Required; Poller construction returns ErrAPIKeyRequired
	// when empty.
	APIKey string

	// Base is the base currency the venue quotes rates against
	// (e.g. "USD" → rates = {EUR: 0.92, GBP: 0.78, ...}). Free
	// tier forces this to EUR; paid tiers accept any code. Empty
	// string defaults to USD.
	Base string

	// Endpoint overrides the REST base URL. Defaults to
	// [DefaultEndpoint]; tests point at httptest servers.
	Endpoint string

	// Interval is the poll cadence. Defaults to
	// [DefaultPollInterval] (60s, matches Professional tier
	// rate-limit).
	Interval time.Duration

	// Symbols is the explicit target currency list; empty means
	// "every symbol derivable from the configured pair list."
	// Setting here overrides pair-list-derived symbols — useful
	// when operator wants to pre-warm unused pairs.
	Symbols []string
}

// NewPoller constructs a Poller with validated config. Surfaces
// ErrAPIKeyRequired when APIKey is empty so operator sees the
// failure at startup.
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

// latestResponse is the JSON shape the /latest endpoint returns.
// Floats kept as json.Number so we scale to integer form without
// the float64 precision round-trip.
type latestResponse struct {
	Success   bool                   `json:"success"`
	Timestamp int64                  `json:"timestamp"`
	Base      string                 `json:"base"`
	Date      string                 `json:"date"`
	Rates     map[string]json.Number `json:"rates"`
	Error     *apiError              `json:"error,omitempty"`
}

type apiError struct {
	Code int    `json:"code"`
	Type string `json:"type"`
	Info string `json:"info"`
}

// PollOnce implements external.Poller. Fetches the current rate
// board for Base → (symbols derived from pairs), returns one
// canonical.OracleUpdate per rate.
//
// Pairs tell us which symbols to request: for each pair, if the
// quote currency is a fiat code we know, we include the base-
// currency code in the symbols list. The venue returns rates as
// `Base -> <symbol>` (e.g. USD -> EUR = 0.92, meaning 1 USD buys
// 0.92 EUR). We invert where the pair's base asset matches the
// venue base to get the right direction.
//
// Pairs with crypto bases (XLM/USD, BTC/USD) are skipped at this
// layer — ExchangeRatesApi only quotes fiat-fiat; crypto prices
// come from exchange streamers. The Poller silently ignores
// non-fiat pairs rather than erroring, because mixed-pair configs
// are normal (operator enables the FX poller for fiat triangulation
// without needing to audit the crypto pairs already covered by
// Binance/Kraken/Bitstamp).
func (p *Poller) PollOnce(ctx context.Context, pairs []canonical.Pair) ([]canonical.Trade, []canonical.OracleUpdate, error) {
	base := p.Base
	if base == "" {
		base = DefaultBase
	}
	symbols := p.resolveSymbols(base, pairs)
	if len(symbols) == 0 {
		// No fiat cross-rates needed — silent no-op.
		return nil, nil, nil
	}

	q := url.Values{}
	q.Set("access_key", p.APIKey)
	q.Set("base", base)
	q.Set("symbols", strings.Join(symbols, ","))

	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+LatestPath+"?"+q.Encode(), nil)
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}

	// exchangeratesapi serves 200 OK even on auth errors — the
	// `success` field is the authoritative status. We surface 5xx /
	// non-200 as errors but let the JSON shape drive the rest.
	if resp.StatusCode >= 500 {
		return nil, nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	var lr latestResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, nil, fmt.Errorf("%w: decode: %w", ErrMalformedResponse, err)
	}
	if !lr.Success {
		errInfo := "unknown"
		if lr.Error != nil {
			errInfo = fmt.Sprintf("code=%d type=%s info=%s", lr.Error.Code, lr.Error.Type, lr.Error.Info)
		}
		return nil, nil, fmt.Errorf("%w: %s", ErrAPIRejected, errInfo)
	}
	if !strings.EqualFold(lr.Base, base) {
		// Paranoid sanity — if the API's `base` field doesn't match
		// what we asked for, stop rather than stamp mislabelled
		// rows. Happens on free tier when we ask for USD base:
		// venue silently returns EUR.
		return nil, nil, fmt.Errorf("%w: base mismatch — requested %q, got %q", ErrAPIRejected, base, lr.Base)
	}

	ts := time.Unix(lr.Timestamp, 0).UTC()
	if lr.Timestamp == 0 {
		ts = time.Now().UTC()
	}

	updates := make([]canonical.OracleUpdate, 0, len(lr.Rates))
	baseAsset, err := canonical.NewFiatAsset(base)
	if err != nil {
		return nil, nil, fmt.Errorf("base asset %q: %w", base, err)
	}

	// Emit one OracleUpdate per target currency — the rate
	// `<symbol>/<base>` (inverted from venue's `<base>/<symbol>`
	// quote). Example: venue says "USD base, rate EUR = 0.9235"
	// meaning 1 USD = 0.9235 EUR. We emit the EUR asset priced
	// *in* USD (the quote) by inverting: 1 EUR = 1/0.9235 USD ≈
	// 1.0828 USD.
	//
	// This inversion keeps our canonical convention: OracleUpdate
	// carries "price of <asset> in <quote>" — readable as "how much
	// Quote does one unit of Asset cost."
	for sym, rateNum := range lr.Rates {
		sym = strings.ToUpper(strings.TrimSpace(sym))
		if sym == "" {
			continue
		}
		symAsset, err := canonical.NewFiatAsset(sym)
		if err != nil {
			// Unknown currency code (not on ADR-0010 allow-list);
			// skip per-entry, keep emitting the rest. Matches the
			// Reflector decoder's ErrUnknownSymbol pattern.
			continue
		}
		scaled, err := decimalStringToScaledInt(rateNum.String(), int(DefaultDecimals))
		if err != nil || scaled.Sign() <= 0 {
			continue
		}
		// Invert: we want "price of symAsset in baseAsset units."
		// venueRate = base per 1 symbol  →  our price = 1 / venueRate.
		// Do this in big.Int at 10^(2*decimals) then divide.
		scalePow := pow10(int(DefaultDecimals))
		inverted := new(big.Int).Div(
			new(big.Int).Mul(scalePow, scalePow),
			scaled,
		)
		if inverted.Sign() <= 0 {
			continue
		}

		u := canonical.OracleUpdate{
			Source:     SourceName,
			ContractID: "",
			Ledger:     0,
			TxHash:     backfillTxHash(sym, base, lr.Timestamp),
			OpIndex:    0,
			Timestamp:  ts,
			Asset:      symAsset,
			Quote:      baseAsset,
			Price:      canonical.NewAmount(inverted),
			Decimals:   DefaultDecimals,
			Observer:   "",
		}
		updates = append(updates, u)
	}
	return nil, updates, nil
}

// resolveSymbols walks the pairs list and returns the unique set of
// fiat codes to query. If p.Symbols is set explicitly, we honour
// that; otherwise we derive from pairs whose assets are on the
// fiat allow-list.
func (p *Poller) resolveSymbols(base string, pairs []canonical.Pair) []string {
	if len(p.Symbols) > 0 {
		return p.Symbols
	}
	seen := map[string]struct{}{}
	baseUpper := strings.ToUpper(base)
	for _, pair := range pairs {
		for _, a := range []canonical.Asset{pair.Base, pair.Quote} {
			if a.Type != canonical.AssetFiat {
				continue
			}
			code := strings.ToUpper(a.Code)
			if code == baseUpper {
				continue // we don't need the base-to-base "rate 1"
			}
			seen[code] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

// decimalStringToScaledInt — same helper as the CEX packages. Kept
// here so each package's scaling convention is self-contained.
func decimalStringToScaledInt(s string, targetDecimals int) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("empty decimal string")
	}
	if strings.ContainsAny(s, "eE") {
		// ExchangeRatesApi has been known to emit very small
		// inverted rates in scientific notation — not in practice
		// for major pairs but defensive against exotics. We
		// re-serialise via ParseFloat then format as %f to
		// normalise back to plain decimal.
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("not a decimal: %q", s)
		}
		s = strconv.FormatFloat(f, 'f', targetDecimals+2, 64)
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

// backfillTxHash synthesises a canonical.OracleUpdate tx_hash from
// (symbol, base, timestamp). canonical.OracleUpdate.Validate()
// requires a 64-char hex string; we build one that's stable across
// repeat polls when the venue timestamp is the same.
func backfillTxHash(symbol, base string, ts int64) string {
	s := fmt.Sprintf("XRATES-%s-%s-%020d", strings.ToUpper(base), strings.ToUpper(symbol), ts)
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
