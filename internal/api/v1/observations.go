package v1

import (
	"net/http"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// handleObservations serves GET /v1/observations per ADR-0018
// Surface 3 — the lowest-level, no-aggregation surface.
//
// Wire shape: an array of TradeRow entries, one per source that has
// ever recorded a trade on the (asset, quote) pair. Empty array (NOT
// 404) when the pair has no observations — the array shape lets a
// caller polling for "any source data on this pair" cleanly observe
// the transition from zero → some without contract changes.
//
// Query parameters:
//
//   - asset (required) — canonical asset id; mirrors /v1/price.
//   - quote (optional, default fiat:USD)
//   - source (optional) — narrow to a single source; result is then
//     a 0- or 1-element array.
//   - aggregate=latest (optional) — collapse to the single most-recent
//     trade across all sources. Returns a 0- or 1-element array
//     (preserves the array wire shape; aggregate=latest does NOT
//     change the response wrapper).
//
// flags.stale is **always false** on this surface — there is no
// aggregation contract to fall short of (ADR-0018 §"flags.stale
// semantic"). Freeze + divergence flags are also intentionally NOT
// consulted here: observations is the rawest surface, and adding
// flags would imply an aggregation layer we explicitly didn't build.
//
// URL discipline (ADR-0018 §"URL discipline"): ?granularity= and
// ?window_seconds= return 400 — those are closed-bucket and tip
// concepts respectively; accepting them on /v1/observations would
// silently let a stray query param select between consistency tiers.
func (s *Server) handleObservations(w http.ResponseWriter, r *http.Request) {
	if s.history == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/observations-unavailable",
			"Observations serving not configured", http.StatusServiceUnavailable,
			"this deployment has no HistoryReader wired — check binary configuration")
		return
	}

	if !rejectObservationsTierParams(w, r) {
		return
	}

	asset, quote, ok := parseObservationsAssetQuote(w, r)
	if !ok {
		return
	}
	pair, err := canonical.NewPair(asset, quote)
	if err != nil {
		// Identity-pair was already rejected upstream; any other
		// validation error here is unexpected. Surface as 400.
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-pair",
			"Invalid pair", http.StatusBadRequest, err.Error())
		return
	}

	source := r.URL.Query().Get("source")
	if source != "" {
		// Validate against the in-memory registry so an unknown
		// source name returns 400 instead of an empty page (the
		// silent-empty-page anti-pattern: a typo in `?source=`
		// looks identical on the wire to "this source has no
		// trades for the pair", which sends callers chasing
		// nonexistent data). Same fail-fast guard as /v1/markets.
		if _, ok := external.Registry[source]; !ok {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/unknown-source",
				"Unknown source", http.StatusBadRequest,
				"source must be a registered source name (see /v1/sources for the canonical list); got "+source)
			return
		}
	}

	// aggregate is currently single-valued ("latest"); reject anything
	// else as a 400 rather than ignoring — keeps the surface honest.
	aggregate := r.URL.Query().Get("aggregate")
	if aggregate != "" && aggregate != "latest" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-aggregate",
			"Invalid aggregate parameter", http.StatusBadRequest,
			`aggregate must be "latest" or omitted`)
		return
	}

	trades, err := s.computeObservations(r.Context(), pair, source, aggregate)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("LatestTradePerSource failed",
			"err", err, "asset", asset.String(), "quote", quote.String(),
			"source", source)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	rows := make([]TradeRow, len(trades))
	srcSet := make(map[string]struct{}, len(trades))
	for i, t := range trades {
		rows[i] = tradeRowFrom(t, 0) // 0 → default 10 fractional digits
		srcSet[t.Source] = struct{}{}
	}
	srcs := make([]string, 0, len(srcSet))
	for src := range srcSet {
		srcs = append(srcs, src)
	}

	// Single-source flag: true when exactly one source contributed
	// (informational). Stale and Frozen stay false on this surface
	// per ADR-0018.
	flags := Flags{SingleSource: len(srcs) == 1}
	writeJSON(w, rows, flags, srcs...)
}

// rejectObservationsTierParams enforces the URL-discipline rule from
// ADR-0018: ?granularity= and ?window_seconds= are tier-selectors for
// other surfaces; accepting them on /v1/observations would let a
// query param silently change the consistency contract. Returns true
// when neither is present; writes a 400 + returns false otherwise.
func rejectObservationsTierParams(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	if q.Get("granularity") != "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-observations-param",
			"granularity is not valid on /v1/observations", http.StatusBadRequest,
			"granularity is a closed-bucket concept (ADR-0018); /v1/observations is raw per-source")
		return false
	}
	if q.Get("window_seconds") != "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-observations-param",
			"window_seconds is not valid on /v1/observations", http.StatusBadRequest,
			"window_seconds is a tip-surface concept (ADR-0018); /v1/observations does not aggregate")
		return false
	}
	return true
}

// parseObservationsAssetQuote — same shape as parseTipAssetQuote;
// kept separate so each surface's error type URLs stay legible in
// problem+json responses (a single helper would flatten the
// surface-specific error vocabulary).
func parseObservationsAssetQuote(w http.ResponseWriter, r *http.Request) (canonical.Asset, canonical.Asset, bool) {
	rawAsset := r.URL.Query().Get("asset")
	if rawAsset == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-asset",
			"Missing asset parameter", http.StatusBadRequest,
			"asset query parameter is required")
		return canonical.Asset{}, canonical.Asset{}, false
	}
	asset, err := canonical.ParseAsset(rawAsset)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-asset-id",
			"Invalid asset identifier", http.StatusBadRequest, err.Error())
		return canonical.Asset{}, canonical.Asset{}, false
	}
	quote := defaultPriceQuote
	if raw := r.URL.Query().Get("quote"); raw != "" {
		q, err := canonical.ParseAsset(raw)
		if err != nil {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/invalid-quote",
				"Invalid quote identifier", http.StatusBadRequest, err.Error())
			return canonical.Asset{}, canonical.Asset{}, false
		}
		quote = q
	}
	if asset.Equal(quote) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/identity-price",
			"Asset and quote are the same", http.StatusBadRequest,
			"price of an asset in itself is always 1; parameters must differ")
		return canonical.Asset{}, canonical.Asset{}, false
	}
	return asset, quote, true
}

// collapseToLatest returns a 0- or 1-element slice containing the
// single most-recent trade by Timestamp, ledger as the tie-breaker.
// Used by the aggregate=latest path to flatten a multi-source slice
// without changing the array-shaped response wire contract.
func collapseToLatest(trades []canonical.Trade) []canonical.Trade {
	if len(trades) == 0 {
		return trades
	}
	bestIdx := 0
	for i := 1; i < len(trades); i++ {
		if isLater(trades[i], trades[bestIdx]) {
			bestIdx = i
		}
	}
	return []canonical.Trade{trades[bestIdx]}
}

// isLater reports whether a is more recent than b. Timestamp is the
// primary order; ledger breaks ties (a higher ledger close at the
// same wall-clock second is more recent in practice — Stellar packs
// many trades into one ledger).
func isLater(a, b canonical.Trade) bool {
	if a.Timestamp.After(b.Timestamp) {
		return true
	}
	if a.Timestamp.Before(b.Timestamp) {
		return false
	}
	return a.Ledger > b.Ledger
}
