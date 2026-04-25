package v1

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// priceBatchMaxAssets is the upper bound on asset_ids per
// /v1/price/batch GET. Mirrors the OpenAPI spec; the POST variant
// (planned) raises this to 1000.
const priceBatchMaxAssets = 100

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

// defaultPriceQuote is the implicit `quote` used by /v1/price when
// the client omits the query param. Parsed once at package init
// so a regression that removes USD from the fiat allow-list
// produces a loud init panic — instead of silently 400ing every
// no-quote /v1/price request in production.
var defaultPriceQuote = mustParseAsset("fiat:USD")

func mustParseAsset(s string) canonical.Asset {
	a, err := canonical.ParseAsset(s)
	if err != nil {
		panic("api/v1: defaultPriceQuote " + s + " must parse: " + err.Error())
	}
	return a
}

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
	var quote canonical.Asset
	if rawQuote == "" {
		quote = defaultPriceQuote
	} else {
		var err error
		quote, err = canonical.ParseAsset(rawQuote)
		if err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-quote",
				"Invalid quote identifier", http.StatusBadRequest,
				err.Error())
			return
		}
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
		if clientAborted(r, err) {
			return // middleware labels request as 499
		}
		s.logger.Error("LatestPrice failed",
			"err", err,
			"asset", asset.String(),
			"quote", quote.String())
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	// Intentionally do NOT emit obs.PriceStalenessSeconds here —
	// the handler would create one series per distinct queried
	// asset, and Stellar has tens of thousands of them (see the
	// cardinality warning on the metric declaration). The
	// aggregator owns this metric when it ships and will restrict
	// emission to a top-N allow-list.
	writeJSON(w, snapshot, Flags{Stale: stale}, sources...)
}

// handlePriceBatch serves GET /v1/price/batch?asset_ids=A,B,C&quote=<id>.
//
// Looks up the latest price for each asset_id in turn. Missing
// observations are omitted from the response — not 404'd —
// because the envelope's data field is `array, items: Price`,
// and "we have prices for some of the requested assets but not
// others" is meaningfully different from "the request was
// malformed." A caller asking for 5 assets and getting back 3
// rows knows immediately which 2 we don't have data for.
//
// Limits:
//   - asset_ids count: 1..100 (priceBatchMaxAssets). Above 100, use
//     the planned POST /v1/price/batch variant which accepts up to
//     1000 in the JSON body.
//   - duplicates are de-duplicated server-side.
//
// Top-level Stale flag is the OR over per-row stale flags — if any
// returned price is stale, the envelope flag is set. This matches
// the single-asset /v1/price contract.
func (s *Server) handlePriceBatch(w http.ResponseWriter, r *http.Request) {
	reader := s.prices
	if reader == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-unavailable",
			"Price serving not configured", http.StatusServiceUnavailable,
			"this deployment has no PriceReader wired — check binary configuration")
		return
	}

	rawIDs := r.URL.Query().Get("asset_ids")
	if rawIDs == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-asset-ids",
			"Missing asset_ids parameter", http.StatusBadRequest,
			"asset_ids query parameter is required (comma-separated)")
		return
	}
	parts := strings.Split(rawIDs, ",")
	// Trim whitespace + drop empty entries — `?asset_ids=A,,B,` should
	// resolve to ["A","B"], not 4 entries with two empty strings, so
	// the limit check counts only real assets.
	ids := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		ids = append(ids, t)
	}
	if len(ids) == 0 {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-asset-ids",
			"Missing asset_ids parameter", http.StatusBadRequest,
			"asset_ids query parameter must contain at least one id")
		return
	}
	if len(ids) > priceBatchMaxAssets {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/too-many-assets",
			"Too many assets", http.StatusBadRequest,
			"asset_ids may contain at most 100 entries — use POST /v1/price/batch for larger batches")
		return
	}

	rawQuote := r.URL.Query().Get("quote")
	var quote canonical.Asset
	if rawQuote == "" {
		quote = defaultPriceQuote
	} else {
		var err error
		quote, err = canonical.ParseAsset(rawQuote)
		if err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-quote",
				"Invalid quote identifier", http.StatusBadRequest,
				err.Error())
			return
		}
	}

	out := make([]PriceSnapshot, 0, len(ids))
	allSources := map[string]struct{}{}
	anyStale := false

	for _, raw := range ids {
		asset, err := canonical.ParseAsset(raw)
		if err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-asset-id",
				"Invalid asset identifier", http.StatusBadRequest,
				raw+": "+err.Error())
			return
		}
		if asset.Equal(quote) {
			// Identity pair is meaningless; reject the whole request
			// rather than silently dropping the entry. Same logic as
			// /v1/price.
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/identity-price",
				"Asset and quote are the same", http.StatusBadRequest,
				"price of an asset in itself is always 1; "+raw+" matches the quote")
			return
		}

		snapshot, sources, stale, err := reader.LatestPrice(r.Context(), asset, quote)
		if errors.Is(err, ErrPriceNotFound) {
			// Per the docstring: omit, do not 404 the whole batch.
			continue
		}
		if err != nil {
			if clientAborted(r, err) {
				return
			}
			s.logger.Error("LatestPrice (batch) failed",
				"err", err, "asset", asset.String(), "quote", quote.String())
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/internal",
				"Internal error", http.StatusInternalServerError, "")
			return
		}
		if stale {
			anyStale = true
		}
		for _, src := range sources {
			allSources[src] = struct{}{}
		}
		out = append(out, snapshot)
	}

	srcs := make([]string, 0, len(allSources))
	for s := range allSources {
		srcs = append(srcs, s)
	}
	writeJSON(w, out, Flags{Stale: anyStale}, srcs...)
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
