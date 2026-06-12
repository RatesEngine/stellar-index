package v1

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// Tip-surface tunables per ADR-0018.
//
// defaultTipWindowSeconds matches the ADR's default; minTipWindowSeconds
// and maxTipWindowSeconds enforce the documented clamp. The cap exists
// to keep the rolling-window scan cheap even when the underlying
// hypertable has millions of rows in a 60s span on a hot pair — and to
// keep the surface honest about being a "tip", not a small-window
// historical aggregator (that's /v1/vwap's job).
const (
	defaultTipWindowSeconds = 5
	minTipWindowSeconds     = 1
	maxTipWindowSeconds     = 60

	// tipWindowMaxTrades caps the scan within a tip window. The rolling
	// window is short (≤60s), so this is the worst-case row count we
	// load into memory for one VWAP. Mirrors /v1/vwap's own cap.
	tipWindowMaxTrades = 10000
)

// handlePriceTip serves GET /v1/price/tip per ADR-0018.
//
// Two in-contract response branches:
//
//   - Window VWAP: at least one trade in [now-window_seconds, now). The
//     handler returns the VWAP with price_type="vwap", window_seconds=N.
//   - Last-good fallback: window is empty. The handler returns
//     PriceReader.LatestPrice's most-recent observation as-is — no
//     synthetic age cap, the customer reads observed_at and decides.
//
// flags.stale is **always false** on this surface — both branches are
// in-contract per ADR-0018 §"flags.stale semantic". The freeze flag
// also stays unset here: freeze is a closed-bucket concept and the tip
// surface has no closed-bucket guarantee. Divergence flagging still
// applies (asset-level, not bucket-level).
//
// URL discipline: ?granularity= is rejected with 400 — granularity is
// a closed-bucket concept and accepting it on the tip URL would let a
// stray query string silently turn a tip request into something
// closed-bucket-shaped (ADR-0018 §"URL discipline").
func (s *Server) handlePriceTip(w http.ResponseWriter, r *http.Request) {
	// PriceReader is the fallback path; without it the tip surface
	// can't degrade and there's nothing meaningful to serve. The
	// rolling-window path needs HistoryReader but we'll degrade
	// gracefully when only one of them is wired.
	if s.prices == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-unavailable",
			"Price serving not configured", http.StatusServiceUnavailable,
			"this deployment has no PriceReader wired — check binary configuration")
		return
	}

	// Reject URL-discipline violations BEFORE asset/quote parsing —
	// a request that mixes tip + closed-bucket semantics is malformed
	// regardless of whether the asset/quote happen to parse.
	if r.URL.Query().Get("granularity") != "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-tip-param",
			"granularity is not valid on /v1/price/tip", http.StatusBadRequest,
			"granularity is a closed-bucket concept (ADR-0018); use /v1/price for closed-bucket VWAP")
		return
	}

	asset, quote, ok := s.parseTipAssetQuote(w, r)
	if !ok {
		return
	}

	window, ok := parseTipWindowSeconds(w, r)
	if !ok {
		return
	}

	snapshot, sources, err := s.computeTip(r.Context(), asset, quote, window)
	if errors.Is(err, ErrPriceNotFound) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-not-found",
			"No price data for pair", http.StatusNotFound,
			"no trades or oracle observations for "+asset.String()+" / "+quote.String())
		return
	}
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if IsCacheUnavailable(err) {
			s.logger.Warn("computeTip cache unavailable",
				"err", err, "asset", asset.String(), "quote", quote.String())
			writeCacheUnavailableProblem(w, r)
			return
		}
		s.logger.Error("computeTip failed",
			"err", err, "asset", asset.String(), "quote", quote.String())
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	// Per ADR-0018: stale stays FALSE on either branch — both are
	// in-contract on this surface. We deliberately IGNORE the
	// staleness bit PriceReader sets for /v1/price; tip has its own
	// envelope contract.
	flags := Flags{SingleSource: len(sources) == 1}
	flags.DivergenceWarning = s.lookupDivergenceFlag(r, asset)
	writeJSON(w, snapshot, flags, sources...)
}

// computeTip is the shared core of [Server.handlePriceTip] and
// [Server.handlePriceTipStream]. Tries the rolling-window VWAP first
// (when HistoryReader is wired and the window has trades), falling
// back to PriceReader.LatestPrice when the window is empty, then to
// the aggregator's Redis VWAP cache for stablecoin-fiat-proxy
// rewritten pairs whose literal form is absent from prices_1m.
//
// Returns ErrPriceNotFound when no branch can produce a snapshot —
// caller turns that into 404 on the request endpoint and into
// "stream cannot start" on the stream endpoint. Any other error is
// surfaced as-is for caller-side logging + 500 mapping.
func (s *Server) computeTip(ctx context.Context, asset, quote canonical.Asset, windowSeconds int) (PriceSnapshot, []string, error) {
	if snap, sources, ok := s.tipWindowVWAP(ctx, asset, quote, windowSeconds); ok {
		return snap, sources, nil
	}
	// Fallback: most-recent known observation for the pair. PriceReader
	// returns price_type="last_trade" today (MVP) and "vwap" once the
	// aggregator wires the closed-bucket cache; both are in-contract
	// for the tip fallback per ADR-0018 (the customer reads price_type
	// + observed_at to know what they got).
	//
	// F-1340: route through the rc.89 XLM dual-form alias loop, exactly
	// as handlePrice does, so /v1/price/tip?asset=native resolves a
	// fresh crypto:XLM observation rather than missing it on the
	// literal form.
	snap, sources, _, err := s.readPriceWithAliases(ctx, s.prices, asset, quote)
	if err == nil {
		return snap, sources, nil
	}
	if !errors.Is(err, ErrPriceNotFound) {
		return PriceSnapshot{}, nil, err
	}
	// Final fallback: Redis VWAP cache. For aggregator-rewritten pairs
	// (XLM/fiat:USD synthesised from XLM/USDC-GA5Z…) the literal pair
	// is absent from prices_1m so the storePriceReader miss above is
	// expected. The Redis vwap: key IS the source of truth for these
	// values. Mirrors the /v1/price handler's tryRedisVWAPFallback so
	// both surfaces serve the same data; provenance marker (when
	// present) is dropped since the tip envelope has no triangulated
	// flag — operators reading the marker for forensics use /v1/price
	// instead.
	if cacheSnap, cacheSources, _, ok := s.tryRedisVWAPFallback(ctx, asset, quote); ok {
		return cacheSnap, cacheSources, nil
	}
	// Read-time stablecoin-fiat proxy: rewrites X/fiat:USD to X/<peg>
	// at request time using the operator's
	// [trades].usd_pegged_classic_assets allow-list. Mirrors the
	// equivalent fallback in priceFallback (#1217). Without this
	// /v1/price/tip?asset=native&quote=fiat:USD 404s out of the box on
	// every fresh deployment because nothing on-chain ever quotes in
	// fiat:USD — same exact failure mode as /v1/price had.
	if proxySnap, proxySources, ok := s.tryStablecoinFiatProxy(ctx, asset, quote); ok {
		return proxySnap, proxySources, nil
	}
	// Last-resort fiat-vs-fiat cross-rate via the forex snapshot.
	// Same machinery /v1/price uses (see tryFiatCrossRate). Without
	// this branch /v1/price/tip?asset=fiat:EUR&quote=fiat:USD 404s
	// because no on-chain pair carries fiat-vs-fiat trades.
	if fxSnap, fxSources, ok := s.tryFiatCrossRate(asset, quote); ok {
		return fxSnap, fxSources, nil
	}
	return PriceSnapshot{}, nil, ErrPriceNotFound
}

// parseTipAssetQuote pulls asset (required) + quote (defaulted to
// fiat:USD) from the request, writes a 400 + returns ok=false on any
// validation failure. Mirrors the equivalent parsing in handlePrice
// rather than sharing a helper — handlePrice writes its own
// price-specific error type URLs and we want the tip handler's
// errors to be self-explanatory in problem+json.
func (s *Server) parseTipAssetQuote(w http.ResponseWriter, r *http.Request) (canonical.Asset, canonical.Asset, bool) {
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

// parseTipWindowSeconds reads the optional window_seconds query param,
// defaulting to defaultTipWindowSeconds and rejecting values outside
// [minTipWindowSeconds, maxTipWindowSeconds]. Returns (seconds, true)
// on success or (0, false) after writing a 400.
func parseTipWindowSeconds(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("window_seconds")
	if raw == "" {
		return defaultTipWindowSeconds, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < minTipWindowSeconds || n > maxTipWindowSeconds {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-window",
			"Invalid window_seconds", http.StatusBadRequest,
			"window_seconds must be an integer in [1, 60]")
		return 0, false
	}
	return n, true
}

// tipWindowVWAP runs the rolling-window VWAP path. Returns
// (snapshot, sources, true) when at least one trade landed in the
// window and VWAP succeeded; (_, _, false) otherwise so the caller
// drops to the LatestPrice fallback.
//
// Errors are intentionally swallowed (logged when the request
// context is still alive, not surfaced) — the tip contract
// guarantees the caller a response if the pair has any observation
// at all, and the LatestPrice fallback is the authoritative answer
// when the rolling window can't produce one. Surfacing a 5xx here
// would make a transient hypertable hiccup turn the entire tip
// surface red even when the fallback is healthy.
func (s *Server) tipWindowVWAP(ctx context.Context, asset, quote canonical.Asset, windowSeconds int) (PriceSnapshot, []string, bool) {
	if s.history == nil {
		return PriceSnapshot{}, nil, false
	}
	pair, err := canonical.NewPair(asset, quote)
	if err != nil {
		// Identity-pair was already rejected upstream; any other
		// validation error here is unexpected. Drop to fallback.
		return PriceSnapshot{}, nil, false
	}

	now := time.Now().UTC()
	from := now.Add(-time.Duration(windowSeconds) * time.Second)
	trades, err := s.history.TradesInRange(ctx, pair, from, now, tipWindowMaxTrades)
	if err != nil {
		// Don't log under a cancelled ctx — that's just the client
		// disconnecting (or, on the stream path, the per-tick scope
		// completing).
		if ctx.Err() == nil {
			s.logger.Warn("TradesInRange failed (tip window) — falling back to LatestPrice",
				"err", err, "asset", asset.String(), "quote", quote.String(),
				"window_seconds", windowSeconds)
		}
		return PriceSnapshot{}, nil, false
	}
	if len(trades) == 0 {
		return PriceSnapshot{}, nil, false
	}

	price, err := aggregate.VWAP(trades)
	if err != nil {
		// All-zero-volume input. The fallback path will produce a
		// usable response.
		return PriceSnapshot{}, nil, false
	}

	sources := distinctTradeSources(trades)
	return PriceSnapshot{
		AssetID:       asset.String(),
		Quote:         quote.String(),
		Price:         ratToDecimal(price, ohlcPriceDigits),
		PriceType:     "vwap",
		ObservedAt:    now,
		WindowSeconds: windowSeconds,
	}, sources, true
}

// lookupDivergenceFlag mirrors handlePrice's best-effort divergence
// lookup. Pulled into a helper so the tip handler doesn't duplicate
// the error-handling shape. Returns false when no DivergenceLooker is
// wired or when the lookup errors.
func (s *Server) lookupDivergenceFlag(r *http.Request, asset canonical.Asset) bool {
	if s.divergence == nil {
		return false
	}
	firing, err := s.divergence.DivergenceFiringFor(r.Context(), asset)
	if err != nil {
		if !clientAborted(r, err) {
			s.logger.Warn("divergence lookup failed (tip)",
				"err", err, "asset", asset.String())
		}
		return false
	}
	return firing
}

// distinctTradeSources returns the unique source names from a slice
// of trades, preserving first-occurrence order. Used to populate the
// envelope's sources array on the tip-window path.
func distinctTradeSources(trades []canonical.Trade) []string {
	if len(trades) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(trades))
	out := make([]string, 0, len(trades))
	for i := range trades {
		src := trades[i].Source
		if _, dup := seen[src]; dup {
			continue
		}
		seen[src] = struct{}{}
		out = append(out, src)
	}
	return out
}
