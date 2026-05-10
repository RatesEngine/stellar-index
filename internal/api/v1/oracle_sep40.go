package v1

import (
	"context"
	"errors"
	"net/http"
	"strconv"
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
		// Same Redis VWAP fallback as /v1/price (price.go) — covers
		// stablecoin-proxy rewrites (XLM/fiat:USD synthesised from
		// XLM/USDC-G…) + triangulated chains. Without this, SEP-40
		// `lastprice(native)` 404s in steady state because
		// prices_1m has no literal native/fiat:USD bucket, while
		// /v1/price?asset=native&quote=fiat:USD succeeds via the
		// same fallback. Caught by the 2026-05-08 prod audit.
		var ok bool
		snapshot, sources, _, ok = s.tryRedisVWAPFallback(r.Context(), asset, defaultPriceQuote)
		stale = false
		if !ok {
			// Read-time stablecoin-fiat proxy: walks the operator's
			// classic USD pegs and rewrites X/fiat:USD to X/<peg>.
			// Same mechanism as /v1/price (#1217) — the SEP-40
			// surface needs identical fallback coverage so an
			// on-chain integrator drop-in-replacing the SEP-40
			// `lastprice()` call sees the same "available" set as
			// /v1/price.
			snapshot, sources, ok = s.tryStablecoinFiatProxy(r.Context(), asset, defaultPriceQuote)
		}
		if !ok {
			// Fiat-vs-fiat cross-rate from the forex snapshot —
			// covers `lastprice(fiat:EUR)` etc., which would 404
			// without this branch. Mirrors #1086's /v1/price
			// fiat fallback.
			snapshot, sources, ok = s.tryFiatCrossRate(asset, defaultPriceQuote)
		}
		if !ok {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/price-not-found",
				"No price data for asset", http.StatusNotFound,
				"no observation for "+asset.String())
			return
		}
		err = nil
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

// handleOraclePrices serves GET /v1/oracle/prices?asset=<id>&records=N.
//
// SEP-40 `prices(asset, records) -> Option<Vec<PriceData>>`
// passthrough. Returns up to `records` most-recent CLOSED 1-minute
// VWAP snapshots for the asset/USD pair (newest first), per the
// SEP-40 spec semantic that prices() is "the last N price records."
//
// Per ADR-0015 only closed buckets are returned; the in-progress
// bucket is excluded.
//
// Defaults + caps from the OpenAPI: records default 60, max 200.
//
// 200 with empty array when the asset has no closed buckets yet.
// 400 when records is out of range or asset is malformed.
// 503 when no PriceReader is wired.
func (s *Server) handleOraclePrices(w http.ResponseWriter, r *http.Request) {
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
			"price of fiat:USD in itself is always 1; SEP-40 prices() quotes everything in fiat:USD")
		return
	}

	records := oraclePricesDefault
	if raw := r.URL.Query().Get("records"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > oraclePricesMax {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-records",
				"Invalid records parameter", http.StatusBadRequest,
				"records must be an integer in [1, 200]")
			return
		}
		records = n
	}

	snapshots, triangulated, err := s.recentClosedWithStablecoinFallback(r.Context(), asset, defaultPriceQuote, records)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("RecentClosedSnapshots (sep40 prices) failed",
			"err", err, "asset", asset.String(), "records", records)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	out := make([]SEP40Price, len(snapshots))
	for i, snap := range snapshots {
		out[i] = SEP40Price{
			Asset:     asset.String(),
			Price:     snap.Price,
			Timestamp: snap.ObservedAt,
		}
	}
	writeJSON(w, out, Flags{Triangulated: triangulated})
}

// recentClosedWithStablecoinFallback wraps PriceReader.RecentClosedSnapshots
// with the same X/fiat:USD → X/<peg> retry shape used in the
// other handler-side stablecoin-proxy fallbacks (#1217 / #1218 /
// #1219 / #1220). When the literal asset/fiat:USD lookup returns an
// empty slice AND quote is fiat:USD AND the operator declared
// classic USD pegs, walks the pegs and returns the first non-empty
// asset/<peg> result. triangulated=true on the return so the
// envelope can stamp Flags{Triangulated: true}.
//
// Without this, /v1/oracle/prices?asset=native silently returns an
// empty data array on Stellar mainnet — same out-of-the-box failure
// mode as /v1/oracle/lastprice had pre-#1220, just expressed as
// 200-empty rather than 404.
func (s *Server) recentClosedWithStablecoinFallback(
	ctx context.Context, asset, quote canonical.Asset, n int,
) ([]PriceSnapshot, bool, error) {
	snapshots, err := s.prices.RecentClosedSnapshots(ctx, asset, quote, n)
	if err != nil {
		return nil, false, err
	}
	if len(snapshots) > 0 {
		return snapshots, false, nil
	}
	if quote.Type != canonical.AssetFiat || quote.Code != "USD" {
		return snapshots, false, nil
	}
	for _, peg := range s.usdPeggedClassics {
		if peg.Equal(asset) {
			continue
		}
		pegSnapshots, pegErr := s.prices.RecentClosedSnapshots(ctx, asset, peg, n)
		if pegErr != nil || len(pegSnapshots) == 0 {
			continue
		}
		return pegSnapshots, true, nil
	}
	return snapshots, false, nil
}

// oraclePricesDefault + Max mirror the OpenAPI bounds for the
// `records` parameter on /v1/oracle/prices. Documented inline in
// the spec; pinned here so the handler validates against the same
// numbers the spec promises.
const (
	oraclePricesDefault = 60
	oraclePricesMax     = 200
)

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
		// Same Redis VWAP fallback as /v1/price for stablecoin-
		// proxy + triangulated pairs. Companion to the equivalent
		// fix on /v1/oracle/lastprice — see that handler's comment
		// for the full rationale.
		var ok bool
		snapshot, sources, _, ok = s.tryRedisVWAPFallback(r.Context(), base, quote)
		stale = false
		if !ok {
			// Read-time stablecoin-fiat proxy: walks the operator's
			// classic USD pegs and rewrites X/fiat:USD to X/<peg>.
			// Mirrors the same fallback in /v1/price (#1217) and
			// /v1/oracle/lastprice (this PR) — kept here too so a
			// SEP-40 integrator calling `x_last_price(native, fiat:USD)`
			// sees the same coverage as a /v1/price caller.
			snapshot, sources, ok = s.tryStablecoinFiatProxy(r.Context(), base, quote)
		}
		if !ok {
			// Fiat-vs-fiat cross-rate via the forex snapshot —
			// covers `x_last_price(fiat:EUR, fiat:GBP)` etc.
			// Mirrors the `/v1/oracle/lastprice` and `/v1/price`
			// fiat fallbacks (#1086 / this PR).
			snapshot, sources, ok = s.tryFiatCrossRate(base, quote)
		}
		if !ok {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/price-not-found",
				"No price data for pair", http.StatusNotFound,
				"no observation for "+base.String()+" / "+quote.String())
			return
		}
		err = nil
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
