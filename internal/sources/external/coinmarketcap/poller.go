// Package coinmarketcap polls CoinMarketCap's Pro /v2 quotes endpoint
// for cross-check reference prices. `ClassAggregator` — divergence
// signal only, excluded from VWAP.
//
// Tier notes:
//   - Hobbyist / Basic: 10k credits/month, 30 calls/min. Usable for
//     low-cadence divergence checks.
//   - Startup: 120k/month, 30/min. Fine for 1-min cadence.
//   - Standard ($79/mo): 500k/month, 60/min, **redistribution allowed**.
//     This is the minimum for production (earlier tiers prohibit
//     redistributing the data).
//
// Wire shape (verified 2026-04-24):
//
//	GET https://pro-api.coinmarketcap.com/v2/cryptocurrency/quotes/latest?symbol=XLM,BTC,ETH&convert=USD
//	Header: X-CMC_PRO_API_KEY: KEY
//
//	{
//	  "data": {
//	    "XLM": [{ "quote": { "USD": { "price": 0.17582, "last_updated": "..." }}}],
//	    "BTC": [{ "quote": { "USD": { "price": 50000.0,  "last_updated": "..." }}}]
//	  },
//	  "status": { "error_code": 0, "error_message": null, ... }
//	}
//
// Note: CMC wraps each symbol's payload in an array because multiple
// coins can share a ticker (e.g. two distinct projects both ticker
// "ETH2"). We take the first entry — for our mainstream-asset
// coverage this is always the canonical project.
package coinmarketcap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/external"
)

const (
	SourceName                = "coinmarketcap"
	DefaultEndpoint           = "https://pro-api.coinmarketcap.com"
	QuotesLatestPath          = "/v2/cryptocurrency/quotes/latest"
	DefaultPollInterval       = 60 * time.Second
	DefaultDecimals     uint8 = 8

	// APIKeyHeader is CMC's auth convention — a named header rather
	// than query param or Authorization bearer.
	APIKeyHeader = "X-CMC_PRO_API_KEY"
)

var _ = external.ClassAggregator

var (
	ErrAPIKeyRequired    = errors.New("coinmarketcap: API key required (see config.External.CoinMarketCap.APIKey or env COINMARKETCAP_API_KEY)")
	ErrAPIRejected       = errors.New("coinmarketcap: API rejected request")
	ErrMalformedResponse = errors.New("coinmarketcap: malformed response")
)

// Poller implements external.Poller.
type Poller struct {
	APIKey   string
	Endpoint string
	Interval time.Duration

	// CMCIDs maps upper-case ticker → CMC numeric id (as a
	// string, e.g. "512" for XLM). F-1237 (codex audit-2026-05-12):
	// querying CMC by `symbol=` is ambiguous — multiple coins can
	// share the same ticker (LUNA, LUNC, etc.) and CMC picks the
	// highest-ranked match, which can drift between tickers and
	// over time. When this map is populated, the poller queries by
	// `id=<numeric>` for every ticker present so the bound asset
	// is unambiguous. Tickers without an entry fall back to the
	// `symbol=` query — same behaviour as before.
	//
	// Wired from `currency.Catalogue.CoinMarketCapIDs()` in the
	// indexer/aggregator binaries. Leave nil to preserve the
	// legacy symbol-only path.
	CMCIDs map[string]string
}

// NewPoller constructs a Poller with validated API key.
func NewPoller(apiKey string) (*Poller, error) {
	if apiKey == "" {
		return nil, ErrAPIKeyRequired
	}
	return &Poller{
		APIKey:   apiKey,
		Endpoint: DefaultEndpoint,
		Interval: DefaultPollInterval,
	}, nil
}

func (p *Poller) Name() string { return SourceName }

func (p *Poller) Class() external.Class { return external.ClassAggregator }

func (p *Poller) PollInterval() time.Duration {
	if p.Interval <= 0 {
		return DefaultPollInterval
	}
	return p.Interval
}

// quotesResponse matches CMC's /v2 quotes/latest shape.
type quotesResponse struct {
	Data   map[string][]cmcCoin `json:"data"`
	Status cmcStatus            `json:"status"`
}

type cmcCoin struct {
	Symbol string              `json:"symbol"`
	Quote  map[string]cmcQuote `json:"quote"`
}

type cmcQuote struct {
	Price       float64 `json:"price"`
	LastUpdated string  `json:"last_updated"`
}

type cmcStatus struct {
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
	Timestamp    string `json:"timestamp"`
}

// PollOnce implements external.Poller.
func (p *Poller) PollOnce(ctx context.Context, pairs []canonical.Pair) ([]canonical.Trade, []canonical.OracleUpdate, error) { //nolint:gocognit,gocyclo,funlen // dispatch-heavy; splitting would reduce linearity
	symbolSet := map[string]struct{}{}
	cryptoAssets := map[string]canonical.Asset{}
	currencySet := map[string]struct{}{}
	fiatAssets := map[string]canonical.Asset{}
	wantedCombos := map[string]struct{}{}

	for _, pair := range pairs {
		if pair.Base.Type != canonical.AssetCrypto || pair.Quote.Type != canonical.AssetFiat {
			continue
		}
		sym := strings.ToUpper(pair.Base.Code)
		code := strings.ToUpper(pair.Quote.Code)
		symbolSet[sym] = struct{}{}
		cryptoAssets[sym] = pair.Base
		currencySet[code] = struct{}{}
		fiatAssets[code] = pair.Quote
		wantedCombos[sym+"/"+code] = struct{}{}
	}
	if len(symbolSet) == 0 || len(currencySet) == 0 {
		return nil, nil, nil
	}

	symbols := make([]string, 0, len(symbolSet))
	ids := make([]string, 0, len(symbolSet))
	for s := range symbolSet {
		if p.CMCIDs != nil {
			if id, ok := p.CMCIDs[s]; ok && id != "" {
				ids = append(ids, id)
				continue
			}
		}
		symbols = append(symbols, s)
	}
	currencies := make([]string, 0, len(currencySet))
	for c := range currencySet {
		currencies = append(currencies, c)
	}

	q := url.Values{}
	// F-1237 (codex audit-2026-05-12): prefer `id=<numeric>` over
	// `symbol=` for any ticker that has an authoritative CMC ID in
	// the catalogue. CMC's symbol-only path returns the highest-
	// ranked match for an ambiguous ticker, which silently binds
	// the wrong coin to our oracle output. The numeric ID is
	// stable across rankings.
	if len(ids) > 0 {
		q.Set("id", strings.Join(ids, ","))
	}
	if len(symbols) > 0 {
		q.Set("symbol", strings.Join(symbols, ","))
	}
	q.Set("convert", strings.Join(currencies, ","))

	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+QuotesLatestPath+"?"+q.Encode(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set(APIKeyHeader, p.APIKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, nil, fmt.Errorf("%w: 401 unauthorized — check CMC API key", ErrAPIRejected)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, nil, fmt.Errorf("%w: 429 rate limited — check tier", ErrAPIRejected)
	}

	var r quotesResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrMalformedResponse, err)
	}
	if r.Status.ErrorCode != 0 {
		return nil, nil, fmt.Errorf("%w: code=%d %s",
			ErrAPIRejected, r.Status.ErrorCode, r.Status.ErrorMessage)
	}

	updates := make([]canonical.OracleUpdate, 0, len(r.Data))
	for sym, coins := range r.Data {
		if len(coins) == 0 {
			continue
		}
		// Multiple entries can share a ticker — take the first.
		// For our mainstream assets this is always the canonical
		// project (verified 2026-04-24 for XLM/BTC/ETH/USDC/USDT
		// — CMC sorts by rank).
		coin := coins[0]
		// F-1237 (codex audit-2026-05-13): when the request used
		// `id=<numeric>`, CMC keys the response map by the
		// numeric ID string ("512", "1") rather than the ticker
		// ("XLM", "BTC"). Resolve back to the canonical asset
		// via `coin.Symbol` (always the upstream ticker) instead
		// of the response map key. Falls back to the map key
		// for the symbol-mode path so the existing tests stay
		// green.
		ticker := strings.ToUpper(sym)
		if _, ok := cryptoAssets[ticker]; !ok && coin.Symbol != "" {
			ticker = strings.ToUpper(coin.Symbol)
		}
		cryptoAsset, ok := cryptoAssets[ticker]
		if !ok {
			continue
		}
		for currency, quote := range coin.Quote {
			cUp := strings.ToUpper(currency)
			quoteAsset, ok := fiatAssets[cUp]
			if !ok {
				continue
			}
			if _, want := wantedCombos[ticker+"/"+cUp]; !want {
				continue
			}
			if quote.Price <= 0 {
				continue
			}
			scaled, err := floatToScaledInt(quote.Price, int(DefaultDecimals))
			if err != nil || scaled.Sign() <= 0 {
				continue
			}
			ts, err := time.Parse(time.RFC3339Nano, quote.LastUpdated)
			if err != nil {
				ts = time.Now().UTC()
			}
			u := canonical.OracleUpdate{
				Source:     SourceName,
				ContractID: "",
				Ledger:     0,
				TxHash:     syntheticTxHash(ticker, currency, ts.Unix()),
				OpIndex:    0,
				Timestamp:  ts.UTC(),
				Asset:      cryptoAsset,
				Quote:      quoteAsset,
				Price:      canonical.NewAmount(scaled),
				Decimals:   DefaultDecimals,
				Observer:   "",
			}
			updates = append(updates, u)
		}
	}
	return nil, updates, nil
}

// floatToScaledInt / decimalStringToScaledInt / syntheticTxHash —
// per-package scaling + hash helpers, parallel to other off-chain
// sources.
func floatToScaledInt(v float64, decimals int) (*big.Int, error) {
	if v < 0 || v != v {
		return nil, fmt.Errorf("bad value %v", v)
	}
	s := strconv.FormatFloat(v, 'f', decimals+2, 64)
	return decimalStringToScaledInt(s, decimals)
}

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

func syntheticTxHash(ticker, currency string, ts int64) string {
	s := fmt.Sprintf("CMC-%s-%s-%020d", strings.ToUpper(ticker), strings.ToUpper(currency), ts)
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
