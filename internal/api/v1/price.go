package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// priceBatchMaxAssets is the upper bound on asset_ids per
// /v1/price/batch GET. Mirrors the OpenAPI spec.
const priceBatchMaxAssets = 100

// priceBatchMaxAssetsPOST is the upper bound on asset_ids per
// /v1/price/batch POST. The JSON-body variant exists precisely to
// raise the GET ceiling without bloating query strings.
const priceBatchMaxAssetsPOST = 1000

// DivergenceLooker is the read-side interface the v1 server uses
// to consult cached divergence results when serving /v1/price.
// Production implementation: an adapter around
// `internal/divergence.Service.LookupCached` wired in the binary.
//
// The server treats absence (`found=false`) and read errors as
// "warning unset". Read errors are logged at WARN; absence is the
// expected steady-state for assets the divergence worker hasn't
// reached yet (TTL'd out, never refreshed, etc.).
type DivergenceLooker interface {
	DivergenceFiringFor(ctx context.Context, asset canonical.Asset) (bool, error)
}

// FrozenLooker is the read-side interface the v1 server uses to
// determine whether the most-recent published bucket for an
// asset/quote pair was frozen by the anomaly checker (ADR-0019).
// Production implementation: a Redis-backed adapter the aggregator
// populates at bucket-close time, wired in the binary.
//
// When FrozenForPair returns true, the snapshot served by
// PriceReader.LatestPrice IS the previous bucket's last-known-good
// VWAP — not a fresh aggregation. The handler sets flags.frozen=true
// and flags.single_source=true on the response (per the
// anomaly.ActionFreeze contract in
// internal/aggregate/anomaly/decision.go).
//
// Read errors fall through with frozen=false (better to serve a
// price without the warning flag than to 5xx because of a Redis
// blip). Absence (no freeze marker) means "not frozen" — the
// steady-state for healthy buckets.
type FrozenLooker interface {
	FrozenForPair(ctx context.Context, asset, quote canonical.Asset) (bool, error)
}

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

	// RecentClosedSnapshots returns up to `n` most-recent CLOSED
	// 1-minute VWAP snapshots for asset/quote, newest first. Used
	// by the SEP-40 `prices(asset, records)` passthrough at
	// /v1/oracle/prices. Empty slice + nil error when the pair has
	// no closed buckets yet (rather than ErrPriceNotFound — the
	// caller distinguishes "asset is unknown" from "asset has no
	// historical buckets" by combining this with the asset-existence
	// check at the storage layer).
	//
	// n is clamped to the SEP-40 cap (200) by the caller; the
	// implementation can assume 1 ≤ n ≤ 200.
	RecentClosedSnapshots(ctx context.Context, asset, quote canonical.Asset, n int) ([]PriceSnapshot, error)
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

	// Confidence is the multi-factor confidence score per ADR-0019,
	// in [0, 1]. Populated only on `/v1/price` (the closed-bucket
	// surface) — tip/observations/oracle surfaces leave it unset
	// (omitempty hides). Nil pointer = "not available" (typically
	// pre-launch when the aggregator's confidence-compute path
	// isn't running yet); a populated value means the bucket has
	// a fresh score in the cache.
	Confidence *float64 `json:"confidence,omitempty"`

	// ConfidenceFactors is the per-factor decomposition that
	// accompanies Confidence. Optional with the same semantics —
	// nil means "not available".
	ConfidenceFactors *ConfidenceFactors `json:"confidence_factors,omitempty"`
}

// ConfidenceFactors mirrors `confidence.Factors` on the wire so
// the API package doesn't force an aggregate import on SDK
// consumers. Same field names and JSON tags.
type ConfidenceFactors struct {
	ZScore          float64 `json:"z_score"`
	SourceCount     float64 `json:"source_count"`
	Diversity       float64 `json:"diversity"`
	Liquidity       float64 `json:"liquidity"`
	CrossOracle     float64 `json:"cross_oracle"`
	BaselineQuality float64 `json:"baseline_quality"`
}

// ConfidenceLooker is the read-side interface the v1 server uses
// to fetch the cached confidence score on `/v1/price`. Production
// wiring: a Redis adapter that GETs `confidence:<base>:<quote>:<window>`
// and decodes the JSON-encoded `confidence.Score` written by the
// aggregator.
//
// The interface returns `(_, false, nil)` for absent cache entries
// (typical pre-launch state); read errors propagate via the third
// return so the handler can log without breaking the response —
// confidence is enrichment, not a publish-blocking signal.
type ConfidenceLooker interface {
	LookupConfidence(ctx context.Context, asset, quote canonical.Asset, window time.Duration) (PriceSnapshotConfidence, bool, error)
}

// PriceSnapshotConfidence is the read-side wire shape between
// [ConfidenceLooker] and the handler. Same fields as the
// orchestrator-side `confidence.Score` but local to this package
// so the storage adapter does the JSON decode + remap once.
type PriceSnapshotConfidence struct {
	Confidence float64
	Factors    ConfidenceFactors
}

// TriangulatedPriceLooker is the fallback path /v1/price consults
// after a Timescale miss. The aggregator's triangulation worker
// publishes implied VWAPs (e.g. XLM/EUR via XLM/USD × USD/EUR) into
// `vwap:<base>:<quote>:<window>` Redis keys with a `:provenance`
// sibling key set to "triangulated"; this Looker reads both and
// returns whether a triangulated value exists for the requested
// pair + the value itself.
//
// Production wiring: a Redis-backed adapter that reads
// [cachekeys.VWAP] and [cachekeys.VWAPProvenance]. Nil leaves
// /v1/price returning 404 for triangulated-only pairs (the
// existing behaviour) — wire when the aggregator's triangulation
// chains are configured + Redis is reachable.
type TriangulatedPriceLooker interface {
	// LookupTriangulatedVWAP returns the triangulated VWAP for the
	// pair + window if one is cached AND the provenance marker
	// confirms it came from triangulation (vs. a direct per-pair
	// refresh that happened to write to the same key).
	//
	// Return values:
	//   value           — decimal string when found.
	//   isTriangulated  — true when the provenance marker says so.
	//                     false means the cache had a value but it
	//                     was a direct VWAP, not triangulated; the
	//                     handler should NOT use this as a Timescale
	//                     fallback (Timescale already had the
	//                     direct value and returned ErrPriceNotFound
	//                     for some other reason).
	//   found           — true when any value was in the cache.
	//   err             — propagates Redis errors so the handler
	//                     can log them; cache misses are NOT errors
	//                     (found=false, err=nil).
	LookupTriangulatedVWAP(ctx context.Context, base, quote canonical.Asset, window time.Duration) (value string, isTriangulated, found bool, err error)
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
	triangulated := false
	if errors.Is(err, ErrPriceNotFound) {
		// Timescale miss — fall through to the triangulation cache
		// if a TriangulatedPriceLooker is wired (per F-0014). When
		// the aggregator's triangulation worker has cached an
		// implied value for this pair, serve that with
		// `flags.triangulated=true`. When no looker is wired or no
		// triangulated value is cached, the original 404 stands.
		var ok bool
		snapshot, sources, triangulated, ok = s.tryRedisVWAPFallback(r.Context(), asset, quote)
		stale = false
		if !ok {
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/price-not-found",
				"No price data for pair", http.StatusNotFound,
				"no trades or oracle observations for "+asset.String()+" / "+quote.String())
			return
		}
		err = nil
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

	// Confidence is enrichment per ADR-0019. Looked up only on
	// `/v1/price` (the closed-bucket surface) — tip + observations
	// surfaces don't carry it. Best-effort: cache misses + read
	// errors leave the snapshot's Confidence/ConfidenceFactors
	// fields nil, and the response ships cleanly without them.
	s.attachConfidence(r, &snapshot, asset, quote)

	flags := Flags{Stale: stale, Triangulated: triangulated}
	frozen := s.lookupFrozen(r, asset, quote)
	flags.Frozen = frozen
	// SingleSource is forced true when the snapshot is the LKG
	// fallback — by the ActionFreeze contract every frozen response
	// is single-sourced (a multi-source bucket couldn't have been
	// frozen). When NOT frozen, derive from the observation count.
	if frozen {
		flags.SingleSource = true
	} else {
		flags.SingleSource = len(sources) == 1
	}
	if s.divergence != nil {
		// Divergence lookup is best-effort. A failure here logs at
		// WARN and falls through with the flag unset — better to
		// serve a fresh price without a warning than to 5xx because
		// a Redis blip lost the cached divergence record.
		if firing, derr := s.divergence.DivergenceFiringFor(r.Context(), asset); derr == nil {
			flags.DivergenceWarning = firing
		} else if !clientAborted(r, derr) {
			s.logger.Warn("divergence lookup failed",
				"err", derr,
				"asset", asset.String())
		}
	}
	writeJSON(w, snapshot, flags, sources...)
}

// confidenceLookupWindow is the cache window the API consults for
// `/v1/price`'s confidence lookup. Matches the smallest window in
// `orchestrator.DefaultWindows` (5m) — the freshest cached score.
//
// When the L3.1 closed-bucket-CAGG read path lands, this constant
// can refine to the API's actual served granularity. For now 5m
// is the right tradeoff: covered by the aggregator's default
// window set + hot enough that a stale score TTL's out before
// being read.
const confidenceLookupWindow = 5 * time.Minute

// triangulationLookupWindow is the window the API queries the
// triangulation cache at. Mirrors the aggregator's default
// triangulation worker cadence (1 minute). When the worker emits
// implied VWAPs at multiple windows, the API picks 1m as the
// freshest available — older windows are inferior.
const triangulationLookupWindow = 1 * time.Minute

// tryRedisVWAPFallback consults the wired [TriangulatedPriceLooker]
// (if any) after a Timescale miss on /v1/price. Returns ok=true with
// a synthesised snapshot when the aggregator has cached a VWAP for
// this pair under [cachekeys.VWAP] — covering BOTH the triangulation
// worker's implied values AND direct stablecoin-fiat-proxy rewrites
// that don't appear in `prices_1m` (because the CAGG groups by
// literal trade pair, not the aggregator's rewritten target).
//
// The provenance marker, when present, controls the `triangulated`
// flag on the returned snapshot — direct rewrites have no marker and
// surface as `flags.triangulated=false`; triangulated implied values
// surface as `flags.triangulated=true`. Pre-2026-05-04 the handler
// rejected marker-absent cache hits to honour a "Timescale is the
// source of truth for direct VWAPs" invariant, but that invariant
// only applies to LITERAL trade pairs — for aggregator-rewritten
// pairs (XLM/fiat:USD synthesised from XLM/USDC-GA5Z…) Timescale's
// CAGG fundamentally can't be the source of truth, since the rewrite
// happens at app layer post-CAGG.
//
// The synthesised snapshot has:
//   - Price       — the cached value
//   - PriceType   — "vwap" (rewritten + triangulated values are both VWAPs)
//   - ObservedAt  — time.Now() rounded down to the window. The
//     aggregator overwrites the cache key on each
//     tick at the same cadence, so "now-aligned-to-
//     window-end" is a faithful approximation
//     without round-tripping a separate timestamp.
//   - WindowSec   — [triangulationLookupWindow] in seconds
//   - Sources     — empty []string{} — Redis VWAP keys carry the
//     value alone; per-source provenance is only available via the
//     prices_1m CAGG path, which is preferred when populated
//   - Stale       — false; cache TTL is bound to window, so a
//     non-expired key is by construction within-window
func (s *Server) tryRedisVWAPFallback(ctx context.Context, asset, quote canonical.Asset) (PriceSnapshot, []string, bool, bool) {
	// Returns: snapshot, sources, triangulated, ok.
	// Stale is intentionally NOT returned (always false here) —
	// the cache TTL is bound to the lookup window so a non-expired
	// key is by construction within-window.
	if s.triangulated == nil {
		return PriceSnapshot{}, nil, false, false
	}
	value, isTriangulated, found, err := s.triangulated.LookupTriangulatedVWAP(ctx, asset, quote, triangulationLookupWindow)
	if err != nil {
		s.logger.Warn("vwap cache lookup failed",
			"err", err, "asset", asset.String(), "quote", quote.String())
		return PriceSnapshot{}, nil, false, false
	}
	if !found {
		return PriceSnapshot{}, nil, false, false
	}
	now := time.Now().UTC()
	// Round-down to the window boundary so observed_at lines up
	// with the aggregator's tick rather than wall-clock noise.
	observedAt := now.Truncate(triangulationLookupWindow)
	snap := PriceSnapshot{
		AssetID:       asset.String(),
		Quote:         quote.String(),
		Price:         value,
		PriceType:     "vwap",
		ObservedAt:    observedAt,
		WindowSeconds: int(triangulationLookupWindow.Seconds()),
	}
	return snap, []string{}, isTriangulated, true
}

// attachConfidence consults the wired ConfidenceLooker (when set)
// and populates snap.Confidence + snap.ConfidenceFactors. Best-
// effort: cache misses + read errors leave the fields nil so the
// response still ships cleanly without confidence enrichment.
func (s *Server) attachConfidence(r *http.Request, snap *PriceSnapshot, asset, quote canonical.Asset) {
	if s.confidence == nil {
		return
	}
	got, ok, err := s.confidence.LookupConfidence(r.Context(), asset, quote, confidenceLookupWindow)
	if err != nil {
		if !clientAborted(r, err) {
			s.logger.Warn("confidence lookup failed",
				"err", err, "asset", asset.String(), "quote", quote.String())
		}
		return
	}
	if !ok {
		return // cache miss — leave fields nil
	}
	c := got.Confidence
	snap.Confidence = &c
	f := got.Factors
	snap.ConfidenceFactors = &f
}

// lookupFrozen consults the FrozenLooker (when wired) for the
// supplied pair and returns whether the most-recent published bucket
// was frozen. Read errors and absence both fall through with
// frozen=false — same best-effort posture as divergence lookup.
func (s *Server) lookupFrozen(r *http.Request, asset, quote canonical.Asset) bool {
	if s.freeze == nil {
		return false
	}
	frozen, err := s.freeze.FrozenForPair(r.Context(), asset, quote)
	if err != nil {
		if !clientAborted(r, err) {
			s.logger.Warn("freeze lookup failed",
				"err", err,
				"asset", asset.String(),
				"quote", quote.String())
		}
		return false
	}
	return frozen
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
//     POST /v1/price/batch which accepts up to 1000 in the JSON body.
//   - duplicates are de-duplicated server-side.
//
// Top-level Stale flag is the OR over per-row stale flags — if any
// returned price is stale, the envelope flag is set. This matches
// the single-asset /v1/price contract.
func (s *Server) handlePriceBatch(w http.ResponseWriter, r *http.Request) {
	rawIDs := r.URL.Query().Get("asset_ids")
	if rawIDs == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-asset-ids",
			"Missing asset_ids parameter", http.StatusBadRequest,
			"asset_ids query parameter is required (comma-separated)")
		return
	}
	s.runPriceBatch(w, r, strings.Split(rawIDs, ","), r.URL.Query().Get("quote"), priceBatchMaxAssets)
}

// handlePriceBatchPost serves POST /v1/price/batch with JSON body
// {"asset_ids": [...], "quote": "..."}. Same semantics as the GET
// variant, with the asset_ids ceiling raised to 1000 — that's the
// reason the JSON body shape exists at all (a 1000-entry query
// string blows past most reverse-proxies' default 8 KiB header
// limit).
func (s *Server) handlePriceBatchPost(w http.ResponseWriter, r *http.Request) {
	// Cap the request body so a malicious client can't make us spend
	// memory parsing a 100 MiB JSON object. 1 MiB is plenty for 1000
	// canonical asset ids — the largest realistic ones (contract
	// strkeys at 56 bytes + quotes/commas) are well under 100 KiB.
	const maxBody = 1 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	var body struct {
		AssetIDs []string `json:"asset_ids"`
		Quote    string   `json:"quote"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-body",
			"Invalid JSON body", http.StatusBadRequest, err.Error())
		return
	}
	if len(body.AssetIDs) == 0 {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-asset-ids",
			"Missing asset_ids", http.StatusBadRequest,
			"request body must include a non-empty asset_ids array")
		return
	}
	s.runPriceBatch(w, r, body.AssetIDs, body.Quote, priceBatchMaxAssetsPOST)
}

// runPriceBatch is the shared core of GET + POST /v1/price/batch.
// Trims, de-duplicates, enforces `limit`, parses the quote (default
// fiat:USD), and writes the response. Either dispatches directly on
// successful completion or has already written a problem+json.
//
// Caller passes `rawIDs` in the order the user supplied; output
// preserves first-occurrence order.
//
// Implementation note: split into helpers (parsePriceBatchIDs,
// parsePriceBatchQuote, lookupPriceBatch) to keep each step's
// cognitive complexity within the gocognit lint budget. Each
// helper writes its own problem+json on failure and signals back
// via a sentinel return; the orchestrator only sequences them.
func (s *Server) runPriceBatch(w http.ResponseWriter, r *http.Request, rawIDs []string, rawQuote string, limit int) {
	if s.prices == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-unavailable",
			"Price serving not configured", http.StatusServiceUnavailable,
			"this deployment has no PriceReader wired — check binary configuration")
		return
	}
	ids, ok := s.parsePriceBatchIDs(w, r, rawIDs, limit)
	if !ok {
		return
	}
	quote, ok := s.parsePriceBatchQuote(w, r, rawQuote)
	if !ok {
		return
	}
	s.lookupPriceBatch(w, r, ids, quote)
}

// parsePriceBatchIDs trims, de-duplicates, and length-checks the
// requested asset_ids. ok=false means a problem+json has already
// been written.
func (s *Server) parsePriceBatchIDs(w http.ResponseWriter, r *http.Request, rawIDs []string, limit int) ([]string, bool) {
	ids := make([]string, 0, len(rawIDs))
	seen := make(map[string]struct{}, len(rawIDs))
	for _, p := range rawIDs {
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
			"Missing asset_ids", http.StatusBadRequest,
			"asset_ids must contain at least one non-empty id")
		return nil, false
	}
	if len(ids) > limit {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/too-many-assets",
			"Too many assets", http.StatusBadRequest,
			fmt.Sprintf("asset_ids may contain at most %d entries", limit))
		return nil, false
	}
	return ids, true
}

// parsePriceBatchQuote parses the optional quote, defaulting to
// fiat:USD. ok=false means a 400 has already been written.
func (s *Server) parsePriceBatchQuote(w http.ResponseWriter, r *http.Request, raw string) (canonical.Asset, bool) {
	if raw == "" {
		return defaultPriceQuote, true
	}
	q, err := canonical.ParseAsset(raw)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-quote",
			"Invalid quote identifier", http.StatusBadRequest,
			err.Error())
		return canonical.Asset{}, false
	}
	return q, true
}

// lookupPriceBatch fetches the latest price for each id and writes
// the envelope. Missing observations (ErrPriceNotFound) are omitted
// from the response, not 404'd. Any other reader error aborts the
// whole batch with a 500.
func (s *Server) lookupPriceBatch(w http.ResponseWriter, r *http.Request, ids []string, quote canonical.Asset) {
	reader := s.prices
	out := make([]PriceSnapshot, 0, len(ids))
	allSources := map[string]struct{}{}
	anyStale := false
	anyFrozen := false
	anySingleSource := false
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
		// Per-row freeze lookup → OR into envelope flag. Same
		// best-effort posture as the single-asset path; an absent
		// freeze marker means "not frozen" and a Redis blip just
		// leaves the flag at its previous value.
		if s.lookupFrozen(r, asset, quote) {
			anyFrozen = true
			anySingleSource = true // freeze implies single-source
		} else if len(sources) == 1 {
			anySingleSource = true
		}
		out = append(out, snapshot)
	}
	srcs := make([]string, 0, len(allSources))
	for src := range allSources {
		srcs = append(srcs, src)
	}
	writeJSON(w, out, Flags{
		Stale:        anyStale,
		Frozen:       anyFrozen,
		SingleSource: anySingleSource,
	}, srcs...)
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

// VWAP1mToSnapshot is the CAGG-served counterpart to
// [LastTradeToSnapshot]. Maps a 1-minute prices_1m row to the
// neutral [PriceSnapshot] shape the handler returns.
//
// `assetID` and `quote` are the request's canonical asset strings —
// passed in rather than re-derived from the row so the handler's
// echo of the request parameters stays exactly as the client sent
// them (matches the last_trade path's behaviour).
//
// `vwap` is the row's NUMERIC vwap column, already a decimal string
// from Postgres' text serialisation — passed through without
// re-parsing. `bucketStart` is the start of the closed 1-minute
// window; the snapshot's `observed_at` is the END of that window
// (`bucketStart + 1m`) since the bucket's price represents trades
// that closed during it.
func VWAP1mToSnapshot(assetID, quote, vwap string, bucketStart time.Time) PriceSnapshot {
	return PriceSnapshot{
		AssetID:       assetID,
		Quote:         quote,
		Price:         vwap,
		PriceType:     "vwap",
		ObservedAt:    bucketStart.Add(60 * time.Second),
		WindowSeconds: 60,
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
