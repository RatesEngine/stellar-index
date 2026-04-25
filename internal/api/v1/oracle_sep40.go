package v1

import (
	"errors"
	"net/http"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// SEP40Price is the wire shape for the SEP-40 passthrough
// endpoints (/v1/oracle/lastprice, /v1/oracle/x_last_price).
//
// Field set is deliberately minimal — SEP-40 oracle contracts
// expose `(price, timestamp)` only. Adding source/confidence/
// price_type would let in a richer view via the SEP-40 surface,
// but those fields are already on /v1/oracle/latest and
// /v1/price; mixing them in here would break the "this surface
// matches what an on-chain SEP-40 oracle returns" contract that
// integrators rely on.
type SEP40Price struct {
	Asset     string    `json:"asset"`
	Price     string    `json:"price"`
	Timestamp time.Time `json:"timestamp"`
}

// handleOracleLastPrice serves GET /v1/oracle/lastprice?asset=<id>.
//
// SEP-40 `lastprice(asset) -> Option<PriceData>` passthrough.
// The on-chain oracle contract's native quote is fixed by the
// contract; our API mirrors that semantic by quoting in
// fiat:USD always — clients wanting a different quote should
// hit /v1/price?asset=&quote= or /v1/oracle/x_last_price.
//
// 404 when no price observation exists for the asset.
func (s *Server) handleOracleLastPrice(w http.ResponseWriter, r *http.Request) {
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
	if asset.Equal(defaultPriceQuote) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/identity-price",
			"Asset is the SEP-40 quote", http.StatusBadRequest,
			"price of fiat:USD in itself is always 1; SEP-40 lastprice quotes everything in fiat:USD")
		return
	}

	snapshot, sources, stale, err := reader.LatestPrice(r.Context(), asset, defaultPriceQuote)
	if errors.Is(err, ErrPriceNotFound) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-not-found",
			"No price data for asset", http.StatusNotFound,
			"no observation for "+asset.String())
		return
	}
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("LatestPrice (sep40 lastprice) failed",
			"err", err, "asset", asset.String())
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	out := SEP40Price{
		Asset:     asset.String(),
		Price:     snapshot.Price,
		Timestamp: snapshot.ObservedAt,
	}
	writeJSON(w, out, Flags{Stale: stale}, sources...)
}

// handleOracleXLastPrice serves
// GET /v1/oracle/x_last_price?base=<id>&quote=<id>.
//
// SEP-40 `x_last_price(base, quote)` passthrough — returns the
// last observed price of `base` in terms of `quote`. The
// `asset` field in the response carries the canonical base
// identifier so existing SEP-40 clients can reuse their
// lastprice parsing path; the implicit quote is whatever was
// passed in the request.
//
// 404 when no observation exists for the pair.
func (s *Server) handleOracleXLastPrice(w http.ResponseWriter, r *http.Request) {
	reader := s.prices
	if reader == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-unavailable",
			"Price serving not configured", http.StatusServiceUnavailable,
			"this deployment has no PriceReader wired — check binary configuration")
		return
	}

	rawBase := r.URL.Query().Get("base")
	if rawBase == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-base",
			"Missing base parameter", http.StatusBadRequest,
			"base query parameter is required")
		return
	}
	base, err := canonical.ParseAsset(rawBase)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-asset-id",
			"Invalid base identifier", http.StatusBadRequest,
			err.Error())
		return
	}

	rawQuote := r.URL.Query().Get("quote")
	if rawQuote == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-quote",
			"Missing quote parameter", http.StatusBadRequest,
			"quote query parameter is required")
		return
	}
	quote, err := canonical.ParseAsset(rawQuote)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-quote",
			"Invalid quote identifier", http.StatusBadRequest,
			err.Error())
		return
	}
	if base.Equal(quote) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/identity-pair",
			"Base and quote are the same", http.StatusBadRequest,
			"price of an asset in itself is always 1; base and quote must differ")
		return
	}

	snapshot, sources, stale, err := reader.LatestPrice(r.Context(), base, quote)
	if errors.Is(err, ErrPriceNotFound) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-not-found",
			"No price data for pair", http.StatusNotFound,
			"no observation for "+base.String()+" / "+quote.String())
		return
	}
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("LatestPrice (sep40 x_last_price) failed",
			"err", err, "base", base.String(), "quote", quote.String())
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	out := SEP40Price{
		Asset:     base.String(),
		Price:     snapshot.Price,
		Timestamp: snapshot.ObservedAt,
	}
	writeJSON(w, out, Flags{Stale: stale}, sources...)
}
