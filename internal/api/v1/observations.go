package v1

import (
	"context"
	"net/http"
	"sort"
	"time"

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

	// Fast-path for the synthesized USD reference quote (#29).
	// `fiat:USD` (ADR-0010) is a reference-currency abstraction that is
	// ALWAYS triangulated — no venue trades directly against it, so
	// trades.quote_asset never holds the literal value `fiat:USD`. A
	// `LatestTradePerSource` lookup for it does an unbounded per-chunk
	// fan-out proving emptiness (measured >60s on r1, blowing the 8s
	// ceiling below → 503, the status-page incident). The result is
	// always an empty observations array; short-circuit to the
	// triangulation-hint branch, no storage call.
	//
	// F-1325: this short-circuit was previously applied to EVERY fiat:*
	// AND crypto:* quote — but that premise is false. CEX connectors
	// write REAL trades quoted in `crypto:USDT`, `crypto:BTC`,
	// `fiat:EUR` etc. (binance XLMUSDT/XLMBTC/BTCEUR → quote_asset =
	// "crypto:USDT"/"crypto:BTC"/"fiat:EUR"), so suppressing those
	// silently hid live CEX observations on the rawest API surface.
	// Only `fiat:USD` is genuinely never a stored trade quote; everything
	// else flows through the real (8s-bounded, index-covered) scan below.
	if pair.Quote.Type == canonical.AssetFiat && pair.Quote.Code == "USD" {
		s.writeEmptyObservationsFor(w, r, pair, source)
		return
	}

	// 8s ceiling on the trades hypertable scan. Same pattern as
	// #1082, #1099-#1106. The deliberate 2026-05-08 prod test
	// (asset=native&quote=USDC-G…) hit a 10s curl timeout against
	// the unguarded handler — the cold-cache "latest trade per
	// source" scan over a high-traffic pair can run several seconds
	// on first hit. The bound surfaces a structured 503 instead of
	// holding the connection open until the upstream LB cuts it.
	obsCtx, obsCancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer obsCancel()
	trades, ok := s.fetchObservationsOrWriteError(w, r, obsCtx, pair, source, aggregate, asset, quote)
	if !ok {
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
	// Map iteration is unordered; sort so the `sources` array is
	// byte-identical across requests (the response-equality contract).
	sort.Strings(srcs)

	// Single-source flag: true when exactly one source contributed
	// (informational). Stale and Frozen stay false on this surface
	// per ADR-0018.
	flags := Flags{SingleSource: len(srcs) == 1}

	// Triangulation hint: an empty observations array is genuinely
	// confusing when the same (asset, quote) pair returns a price
	// from /v1/price via triangulation or stablecoin-fiat proxy. The
	// consumer sees `data: []` on /v1/observations and assumes the
	// pair is unpriced, when really it's just that no source carries
	// a DIRECT trade for it (ADR-0018 Surface 3 is raw-only by
	// design). Set flags.triangulated=true when an empty result
	// coexists with a triangulated/proxied price elsewhere — same
	// signal /v1/price returns. R-011 in
	// `docs/review-2026-05-10.md`.
	if len(rows) == 0 && source == "" {
		flags.Triangulated = s.observationsHaveTriangulatedPrice(obsCtx, pair)
	}

	writeJSON(w, rows, flags, srcs...)
}

// writeEmptyObservationsFor writes the canonical empty observations
// result + triangulation hint for the #29 aggregator-only-quote
// fast-path (and any future path the handler decides has no rows to
// consult). Extracted from handleObservations to keep gocognit
// complexity below threshold; mirrors the post-storage empty-result
// branch's flag-setting logic but never has SingleSource (zero rows).
// Uses a tight (2s) ctx for the triangulation lookup so a slow
// /v1/price fallback can't stall the fast-path.
func (s *Server) writeEmptyObservationsFor(w http.ResponseWriter, r *http.Request, pair canonical.Pair, source string) {
	flags := Flags{}
	if source == "" {
		triCtx, triCancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer triCancel()
		flags.Triangulated = s.observationsHaveTriangulatedPrice(triCtx, pair)
	}
	writeJSON(w, []TradeRow{}, flags)
}

// fetchObservationsOrWriteError runs computeObservations and translates
// any error into the right problem+json (client-abort silent return,
// timeout 503, cache-unavailable 503, generic internal 500). Returns
// (trades, true) on success; (_, false) means a response was already
// written and the caller must return. Extracted from handleObservations
// to keep its gocognit complexity below the 20 ceiling after the
// cache-unavailable branch was added for F-0090.
func (s *Server) fetchObservationsOrWriteError(
	w http.ResponseWriter, r *http.Request,
	obsCtx context.Context,
	pair canonical.Pair, source, aggregate string,
	asset, quote canonical.Asset,
) ([]canonical.Trade, bool) {
	trades, err := s.computeObservations(obsCtx, pair, source, aggregate)
	if err == nil {
		return trades, true
	}
	if clientAborted(r, err) {
		return nil, false
	}
	if handlerTimedOut(obsCtx, err) {
		s.logger.Warn("computeObservations deadline exceeded",
			"asset", asset.String(), "quote", quote.String(), "source", source)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/observations-timeout",
			"Observations query timed out", http.StatusServiceUnavailable,
			"the trades hypertable scan didn't return in 8s; retry shortly.")
		return nil, false
	}
	if IsCacheUnavailable(err) {
		s.logger.Warn("computeObservations cache unavailable",
			"err", err, "asset", asset.String(), "quote", quote.String(),
			"source", source)
		writeCacheUnavailableProblem(w, r)
		return nil, false
	}
	s.logger.Error("LatestTradePerSource failed",
		"err", err, "asset", asset.String(), "quote", quote.String(),
		"source", source)
	writeProblem(w, r,
		"https://api.ratesengine.net/errors/internal",
		"Internal error", http.StatusInternalServerError, "")
	return nil, false
}

// observationsHaveTriangulatedPrice is the best-effort lookup that
// powers the empty-observations triangulation hint. Returns true
// when /v1/price would have served a value for the same pair via
// either the Redis VWAP cache (triangulation worker output) or the
// stablecoin-fiat proxy (X / fiat:USD → X / <peg>). All errors
// downgrade to false silently — this is a UX hint, not a
// load-bearing signal.
func (s *Server) observationsHaveTriangulatedPrice(ctx context.Context, pair canonical.Pair) bool {
	if _, _, _, ok := s.tryRedisVWAPFallback(ctx, pair.Base, pair.Quote); ok {
		return true
	}
	if _, _, ok := s.tryStablecoinFiatProxy(ctx, pair.Base, pair.Quote); ok {
		return true
	}
	return false
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
	rawAsset, ok := resolveAssetOrBaseParam(w, r)
	if !ok {
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
