package v1

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

// PriceReader is the storage-side interface for /v1/price lookups.
//
// Production implementation: Redis hot path (the `price:<asset>`
// cache per ADR-0007), Timescale fallback to the latest trade for
// the pair. The MVP impl in cmd/ratesengine-api skips Redis and
// goes straight to the trades hypertable — the handler's Envelope
// flags mark those responses stale=true per the degradation
// envelope in docs/architecture/ha-plan.md §9.
type PriceReader interface {
	// LatestPrice returns the most recent known price of asset in
	// terms of quote. Returns [ErrPriceNotFound] when we have no
	// observation for the pair.
	//
	// Returns:
	//   - snapshot: the price observation.
	//   - sources: which connectors contributed (single-string slice
	//     for last-trade fallback; multi-element for VWAP).
	//   - stale: true when the reader couldn't find a fresh
	//     aggregated price and is serving a fallback (last trade
	//     older than the freshness target).
	LatestPrice(ctx context.Context, asset, quote canonical.Asset) (snapshot PriceSnapshot, sources []string, stale bool, err error)
}

// ErrPriceNotFound is what PriceReader.LatestPrice returns when no
// data exists for the pair. Handler translates to HTTP 404
// problem+json.
var ErrPriceNotFound = errors.New("api: price not found for pair")

// PriceSnapshot is the neutral shape returned by [PriceReader]. The
// handler wraps it in [Envelope].
type PriceSnapshot struct {
	// AssetID + Quote canonical strings match the request parameters.
	AssetID string `json:"asset_id"`
	Quote   string `json:"quote"`

	// Price as a decimal string — ADR-0003 forbids float here.
	// Computed by the reader from the underlying trade or CAGG row.
	Price string `json:"price"`

	// PriceType is one of: "vwap", "twap", "last_trade" (see
	// Freighter RFP §Misc). Freighter prefers VWAP > TWAP >
	// last_trade; our reader picks the best available and reports it.
	PriceType string `json:"price_type"`

	// ObservedAt is when the underlying trade closed (for
	// last_trade) or the aggregation-window end (for VWAP/TWAP).
	// RFC 3339 on the wire.
	ObservedAt time.Time `json:"observed_at"`

	// WindowSeconds is non-zero for VWAP/TWAP — the window size.
	// Zero for last_trade.
	WindowSeconds int `json:"window_seconds,omitempty"`
}

// ─── Handler ──────────────────────────────────────────────────────

// handlePrice serves GET /v1/price?asset=<id>&quote=<id>.
// `quote` defaults to "fiat:USD" if omitted (ADR-0010).
func (s *Server) handlePrice(w http.ResponseWriter, r *http.Request) {
	reader := s.prices
	if reader == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-unavailable",
			"Price serving not configured", http.StatusServiceUnavailable,
			"this deployment has no PriceReader wired — check binary configuration")
		return
	}

	rawAsset := r.URL.Query().Get("asset")
	if rawAsset == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-asset",
			"Missing asset parameter", http.StatusBadRequest,
			"asset query parameter is required")
		return
	}
	asset, err := canonical.ParseAsset(rawAsset)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-asset-id",
			"Invalid asset identifier", http.StatusBadRequest,
			err.Error())
		return
	}

	rawQuote := r.URL.Query().Get("quote")
	if rawQuote == "" {
		rawQuote = "fiat:USD"
	}
	quote, err := canonical.ParseAsset(rawQuote)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-quote",
			"Invalid quote identifier", http.StatusBadRequest,
			err.Error())
		return
	}

	if asset.Equal(quote) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/identity-price",
			"Asset and quote are the same", http.StatusBadRequest,
			"price of an asset in itself is always 1; parameters must differ")
		return
	}

	snapshot, sources, stale, err := reader.LatestPrice(r.Context(), asset, quote)
	if errors.Is(err, ErrPriceNotFound) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-not-found",
			"No price data for pair", http.StatusNotFound,
			"no trades or oracle observations for "+asset.String()+" / "+quote.String())
		return
	}
	if err != nil {
		s.logger.Error("LatestPrice failed",
			"err", err,
			"asset", asset.String(),
			"quote", quote.String())
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	// Mirror staleness into Prometheus. The gauge is a per-asset
	// snapshot of "how old was the most recent price we just
	// served" — cheap on cardinality (bounded by distinct queried
	// assets) and lets the price-stale alert fire without an
	// aggregator pipeline being live.
	if !snapshot.ObservedAt.IsZero() {
		age := time.Since(snapshot.ObservedAt).Seconds()
		obs.PriceStalenessSeconds.WithLabelValues(asset.String()).Set(age)
	}

	writeJSON(w, snapshot, Flags{Stale: stale}, sources...)
}

// ─── Helpers for PriceReader implementations ──────────────────────

// LastTradeToSnapshot converts a canonical.Trade into a
// PriceSnapshot with price_type="last_trade". Used by adapters
// that fall back from Redis to the trades hypertable.
//
// Price = QuoteAmount / BaseAmount as a decimal string at
// roundToDecimals precision. Callers responsible for supplying a
// reasonable `decimals` argument per the quote asset's scale.
func LastTradeToSnapshot(t canonical.Trade, decimals int) PriceSnapshot {
	return PriceSnapshot{
		AssetID:    t.Pair.Base.String(),
		Quote:      t.Pair.Quote.String(),
		Price:      priceRatioDecimal(t, decimals),
		PriceType:  "last_trade",
		ObservedAt: t.Timestamp,
	}
}

// priceRatioDecimal returns QuoteAmount / BaseAmount as a decimal
// string with `decimals` digits after the point. Pure-integer
// computation via big.Rat — no float in the hot path (ADR-0003).
//
// Guarantees:
//   - Never panics (guards against zero BaseAmount by returning "0").
//   - Always exactly `decimals` fractional digits; truncates (floors),
//     doesn't round.
//
// Example: QuoteAmount=12,420,000 and BaseAmount=1,000,000,000
// (100 XLM → 12.42 USDC at 7 decimals) with decimals=7 returns
// "0.0001242" — that's 1 USDC-stroop per XLM-stroop, which is
// what the ratio actually is. Callers choose decimals to produce
// the human-meaningful result; typical: decimals=quote_decimals +
// 7 (XLM stroops) for a display-ready figure. VWAP/OHLC paths
// avoid this by storing pre-scaled prices.
func priceRatioDecimal(t canonical.Trade, decimals int) string {
	base := t.BaseAmount.BigInt()
	quote := t.QuoteAmount.BigInt()
	if base.Sign() == 0 {
		return "0"
	}
	if decimals < 0 {
		decimals = 0
	}

	// Multiply quote by 10^decimals before integer-dividing by base.
	// This shifts the decimal point into the integer domain.
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	scaledQuote := new(big.Int).Mul(quote, scale)
	integerPart, _ := new(big.Int).DivMod(scaledQuote, base, new(big.Int))

	s := integerPart.String()
	// Pad with leading zeros if shorter than `decimals`.
	if len(s) <= decimals {
		pad := decimals - len(s) + 1
		s = leftPad(s, pad, '0')
	}
	// Insert the decimal point.
	if decimals == 0 {
		return s
	}
	split := len(s) - decimals
	return s[:split] + "." + s[split:]
}

func leftPad(s string, n int, c byte) string {
	buf := make([]byte, n+len(s))
	for i := 0; i < n; i++ {
		buf[i] = c
	}
	copy(buf[n:], s)
	return string(buf)
}
