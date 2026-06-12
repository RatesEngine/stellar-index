package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
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

	// PriceType is one of: "vwap", "twap", "last_trade", "peg" (see
	// Freighter RFP §Misc). Freighter prefers VWAP > TWAP >
	// last_trade; our reader picks the best available and reports it.
	// "peg" is emitted on the stablecoin self-peg path
	// (tryStablecoinFiatProxy) — a USD-pegged classic asset priced in
	// fiat:USD returns 1.0.
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

// resolveAssetOrBaseParam reads `asset` and `base` from the query
// string with `asset=` canonical (F-0061/F-0068/F-0091 closure). On
// the `asset=`-canonical endpoints (/v1/price, /v1/observations,
// /v1/chart) this accepts `base=` as an alias so clients copying
// URLs from /v1/twap (which uses `base=`) don't hit a 400 on their
// first try. Passing both is a 400 to avoid silent precedence
// picks. The dual is `parseBaseQuote` in history.go, which is
// `base=`-canonical and accepts `asset=` as the alias.
//
// Returns (raw, true) on success; writes a 400 Problem and returns
// ok=false on missing/conflicting params.
func resolveAssetOrBaseParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	rawAsset := r.URL.Query().Get("asset")
	rawBase := r.URL.Query().Get("base")
	if rawAsset != "" && rawBase != "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-parameter",
			"`asset` and `base` are mutually exclusive", http.StatusBadRequest,
			"both query parameters refer to the same value — pick one (this endpoint's canonical form is `asset=`; `base=` is accepted as an alias for /v1/twap compatibility)")
		return "", false
	}
	if rawAsset == "" {
		rawAsset = rawBase
	}
	if rawAsset == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-asset",
			"Missing asset parameter", http.StatusBadRequest,
			"asset query parameter is required (or `base=` as an alias for /v1/twap compatibility)")
		return "", false
	}
	return rawAsset, true
}

// parsePriceAssetParam preserves the historic name for /v1/price's
// handler while delegating to the shared `asset=`-canonical resolver.
func parsePriceAssetParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	return resolveAssetOrBaseParam(w, r)
}

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

	rawAsset, ok := parsePriceAssetParam(w, r)
	if !ok {
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

	// Try the user's literal (asset, quote) first; if not found, try
	// known aliases. XLM in particular surfaces in two canonical
	// forms across the codebase — `native` (per-network) and
	// `crypto:XLM` (global ticker) — and the aggregator writes VWAP
	// under whichever form matches its configured pair set. Without
	// this loop, /v1/price?asset=native falls through to the
	// triangulation fallback even though a fresh `crypto:XLM/fiat:USD`
	// VWAP is sitting in cache. The 39-hour-stale signal we shipped
	// on 2026-05-29 was exactly this — F-1308 fixed the staleness
	// gauge but not the price-read path. ADR-0010 + F-1308.
	snapshot, sources, stale, err := s.readPriceWithAliases(r.Context(), reader, asset, quote)
	triangulated := false
	if errors.Is(err, ErrPriceNotFound) {
		var ok bool
		snapshot, sources, triangulated, ok = s.priceFallback(r.Context(), asset, quote)
		// F-1254 (audit-2026-05-12): when the closed-bucket VWAP read
		// returned ErrPriceNotFound and we degraded to one of the
		// priceFallback chain (last-trade / stablecoin proxy /
		// triangulation), the response is BY DEFINITION below the
		// surface's documented baseline contract — that's the exact
		// `flags.stale` semantic per ADR-0018. The May-10 SEV-2
		// (Redis BGSAVE blocked → cache empty → every closed-bucket
		// read hit ErrPriceNotFound → priceFallback served last-trade
		// for ~9 h) didn't surface stale=true to customers because
		// this assignment was clearing the flag. Customers got stale
		// data with stale=false. Set stale=true on every fallback —
		// the chain itself is the staleness signal.
		stale = ok
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
	// aggregator owns this metric (F-1306, audit-2026-05-13):
	// `internal/aggregate/orchestrator/orchestrator.go::emitStalenessGauges`
	// runs at end-of-Tick and emits per-asset staleness for every
	// configured pair in the bounded VWAP set.

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
// Redis VWAP cache at. Set to 5 minutes because that is the
// aggregator orchestrator's smallest default window
// ([orchestrator.DefaultWindows] = [5m, 1h, 24h]); both the
// per-pair direct refresh AND the triangulator emit a
// `vwap:<base>:<quote>:300` key on every tick, so the API has a
// fresh value to serve. A 1-minute lookup (the previous value)
// missed every read because no upstream writer emits at 1m.
//
// The aggregator's tick cadence (default 30s, [Config.Interval])
// overwrites the 5m key well inside its TTL, so the served
// `observed_at` is at most ~30s stale relative to bucket-end.
const triangulationLookupWindow = 5 * time.Minute

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
	// F-1305 (codex audit-2026-05-13): observed_at = now, not
	// now.Truncate(window). The cache TTL is bound to the lookup
	// window AND the aggregator overwrites the key on every tick
	// (30s cadence per orchestrator.DefaultInterval), so a
	// non-expired key was written ≤ tick-cadence ago. Window-
	// truncated observed_at stamped responses with 0-5min
	// staleness despite the value being fresh — that's what
	// drove F-1305's 83-186s probe freshness even though the
	// aggregator was writing every 30s. Honest stamp: the value
	// is current as of read time.
	observedAt := now
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

// assetAliases returns the list of canonical-asset forms equivalent
// to `asset` that the price-read path should try, in priority order
// (the literal input first, then known aliases). XLM is the only
// asset with two canonical forms today: `native` (per-network
// strkey-less form) and `crypto:XLM` (cross-network global ticker
// form). Both appear in production trade rows depending on the
// emitting source (SDEX writes `native`, every CEX writes
// `crypto:XLM`); the aggregator VWAPs under whichever the configured
// pair set names. Without the alias loop, /v1/price reading by one
// form misses VWAPs published under the other.
//
// Keeping the function tiny and explicit rather than wiring it
// through a broader asset-equivalence registry: this is the only
// known pair today and a registry would over-design the problem.
// Add a second case here (and a test) if a future asset acquires
// a second canonical form.
func assetAliases(asset canonical.Asset) []canonical.Asset {
	out := []canonical.Asset{asset}
	switch asset.String() {
	case "native":
		if alt, err := canonical.ParseAsset("crypto:XLM"); err == nil {
			out = append(out, alt)
		}
	case "crypto:XLM":
		if alt, err := canonical.ParseAsset("native"); err == nil {
			out = append(out, alt)
		}
	}
	return out
}

// readPriceWithAliases is the alias-aware wrapper around
// reader.LatestPrice. It tries each (assetAlias, quote) pair in
// order and returns the FIRST result that:
//   - succeeds with err == nil, AND
//   - is not stale (or if every alias is stale, the freshest one).
//
// Pre-this-helper the bare LatestPrice(native, fiat:USD) hit only
// the `native` key and missed the `crypto:XLM` VWAP that CEX
// trades populate. On 2026-05-29 this caused /v1/price?asset=native
// to fall through to a 39h-stale triangulated SDEX bucket even
// though fresh CEX data sat in cache under the alias key.
func (s *Server) readPriceWithAliases(ctx context.Context, reader PriceReader, asset, quote canonical.Asset) (PriceSnapshot, []string, bool, error) {
	aliases := assetAliases(asset)
	var firstSnap PriceSnapshot
	var firstSrcs []string
	var firstErr error
	freshFound := false
	for _, a := range aliases {
		snap, srcs, stale, err := reader.LatestPrice(ctx, a, quote)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !stale {
			// Fresh hit — return immediately.
			return snap, srcs, false, nil
		}
		// Stale — remember the first stale result as a fallback
		// in case every alias is stale.
		if !freshFound {
			firstSnap, firstSrcs = snap, srcs
			freshFound = true
		}
	}
	if freshFound {
		// Every alias was stale; return the first stale result so
		// the caller still surfaces flags.stale=true rather than
		// falling all the way through to priceFallback.
		return firstSnap, firstSrcs, true, nil
	}
	// Every alias errored; return the first error so the caller's
	// errors.Is(err, ErrPriceNotFound) branch still triggers
	// priceFallback as before.
	return PriceSnapshot{}, nil, false, firstErr
}

// priceFallback runs the post-Timescale-miss fallback chain for
// /v1/price. Three layers, tried in order:
//
//  1. Redis VWAP cache (covers triangulated chains + stablecoin
//     proxy rewrites that the aggregator has cached; provenance
//     marker controls flags.triangulated).
//  2. Read-time stablecoin → fiat:USD rewrite against the operator-
//     declared classic USD-pegs (catches the case where the
//     aggregator's [aggregate].enable_stablecoin_fiat_proxy isn't
//     enabled but trades.usd_pegged_classic_assets is — the same
//     fix the chart handler ships per #98 / PR #1015).
//  3. Fiat-vs-fiat cross-rate from the forex snapshot
//     (always returns triangulated=true since the value is derived).
//
// Returns ok=false when every layer misses; the caller turns that
// into a 404. Extracted from handlePrice to keep that handler
// under the gocognit cap.
func (s *Server) priceFallback(ctx context.Context, asset, quote canonical.Asset) (PriceSnapshot, []string, bool, bool) {
	if snap, srcs, triangulated, ok := s.tryRedisVWAPFallback(ctx, asset, quote); ok {
		return snap, srcs, triangulated, true
	}
	if snap, srcs, ok := s.tryStablecoinFiatProxy(ctx, asset, quote); ok {
		return snap, srcs, true, true
	}
	if snap, srcs, ok := s.tryFiatCrossRate(asset, quote); ok {
		return snap, srcs, true, true
	}
	return PriceSnapshot{}, nil, false, false
}

// tryStablecoinFiatProxy handles the X / fiat:USD → X / <classic-USD-peg>
// rewrite at handler read time, as a safety net for deployments
// where the aggregator's [aggregate].enable_stablecoin_fiat_proxy
// is not enabled. The literal X/fiat:USD pair never has rows in
// prices_1m on Stellar mainnet because no on-chain trades quote in
// fiat:USD — every USD-flavoured trade quotes in classic USDC
// (USDC-GA5Z…) or one of the other operator-declared pegs.
//
// Walks the operator's [trades].usd_pegged_classic_assets allow-list
// in priority order; first peg whose pair has a non-stale Timescale
// row wins. Same shape as chart.go's chartStablecoinFallback (#98 /
// PR #1015) — without it, /v1/price?asset=native&quote=fiat:USD 404s
// out-of-the-box on every fresh deployment, which is the most-basic
// possible query against the canonical price endpoint.
//
// Returns ok=false when:
//   - quote is not fiat:USD,
//   - usdPeggedClassics is empty (operator hasn't opted in),
//   - every peg's pair returns ErrPriceNotFound or an error.
//
// Sets flags.triangulated=true on the returned snapshot — the served
// price is the X/<peg> VWAP rounded by the implicit assumption peg ≈ $1.
// SingleSource is whatever the underlying X/<peg> lookup carried.
func (s *Server) tryStablecoinFiatProxy(ctx context.Context, asset, quote canonical.Asset) (PriceSnapshot, []string, bool) {
	if quote.Type != canonical.AssetFiat || quote.Code != "USD" {
		return PriceSnapshot{}, nil, false
	}
	if len(s.usdPeggedClassics) == 0 || s.prices == nil {
		return PriceSnapshot{}, nil, false
	}
	for _, peg := range s.usdPeggedClassics {
		if peg.Equal(asset) {
			// F-1232 (codex audit-2026-05-12): asset IS one of
			// the operator-declared USD pegs. /v1/price?asset=<peg>
			// &quote=fiat:USD previously skipped this peg and
			// fell through to the cross-rate path, returning 404
			// even though the asset-detail page surfaces an
			// approximately-$1 enrichment price for the same asset.
			// Return $1.0 with triangulated=true so the wire shape
			// is consistent. We return no sources (nil): the value is
			// derived from the peg assumption, not from VWAP-
			// contributing trades, so the handler's len(sources)==1
			// rule leaves SingleSource=false — an empty source set is
			// not "single-sourced". Flipping the flag would mean
			// emitting a synthetic source into the wire `sources[]`
			// array, which we deliberately don't do.
			snap := PriceSnapshot{
				AssetID:    asset.String(),
				Quote:      quote.String(),
				Price:      "1.000000000000",
				PriceType:  "peg",
				ObservedAt: time.Now().UTC(),
			}
			return snap, nil, true
		}
		snap, srcs, _, err := s.prices.LatestPrice(ctx, asset, peg)
		if err != nil {
			// ErrPriceNotFound is the common case (peg not active for
			// this asset); any other error gets the same treatment as
			// the chart fallback — silent skip, try the next peg.
			continue
		}
		// Rewrite the snapshot's Quote field so the wire response
		// reflects what the user asked for, not the proxy peg.
		snap.Quote = quote.String()
		return snap, srcs, true
	}
	return PriceSnapshot{}, nil, false
}

// tryFiatCrossRate synthesises a fiat-vs-fiat price by cross-rating
// through the wired CurrenciesReader's USD-base snapshot. Used as a
// last-resort fallback on /v1/price after both the Timescale read
// and the Redis VWAP cache miss — neither has fiat-vs-fiat trade
// data because fiat conversions don't have on-chain trade pairs.
//
// Algebra:
//
//	rate_usd[X] = "1 USD = N units of X" (forex worker convention)
//	1 X in Y    = (1/rate_usd[X]) × rate_usd[Y]
//	            = rate_usd[Y] / rate_usd[X]
//
// Special-cases the common (asset=fiat:X, quote=fiat:USD) form so
// /v1/price?asset=fiat:EUR&quote=fiat:USD returns
// 1/rate_usd[EUR] without spuriously looking up rate_usd[USD] (=1
// by definition).
//
// Returns ok=false when:
//   - Either side isn't a fiat asset.
//   - The currencies reader isn't wired or the cache hasn't warmed.
//   - One ticker isn't in the snapshot (rate_usd unknown).
//   - rate_usd[X] is zero (would divide by zero).
//
// PriceType is "vwap" because the upstream forex feed is itself a
// volume-weighted average across the upstream's source set; sources
// is `["massive"]` to credit the upstream feed.
func (s *Server) tryFiatCrossRate(asset, quote canonical.Asset) (PriceSnapshot, []string, bool) {
	if asset.Type != canonical.AssetFiat || quote.Type != canonical.AssetFiat {
		return PriceSnapshot{}, nil, false
	}
	if s.currencies == nil {
		return PriceSnapshot{}, nil, false
	}
	snap := s.currencies.Latest()
	if snap == nil {
		return PriceSnapshot{}, nil, false
	}

	// Build a lookup from ticker → rate_usd. The slice is small
	// (~110 entries) and called rarely (last-resort fallback), so
	// linear scan is fine; the alternative — a per-request map
	// allocation — would not pay off.
	var rateAsset, rateQuote float64
	var foundAsset, foundQuote bool
	for _, c := range snap.Currencies {
		if c.Ticker == asset.Code {
			rateAsset = c.RateUSD
			foundAsset = true
		}
		if c.Ticker == quote.Code {
			rateQuote = c.RateUSD
			foundQuote = true
		}
		if foundAsset && foundQuote {
			break
		}
	}
	if !foundAsset || rateAsset <= 0 {
		return PriceSnapshot{}, nil, false
	}
	// rate_usd[USD] is implicitly 1 — handle that without requiring
	// the snapshot to carry an explicit USD entry. foundQuote is
	// not read after this branch; the implicit-USD path just
	// supplies rateQuote.
	if !foundQuote {
		if quote.Code != "USD" {
			return PriceSnapshot{}, nil, false
		}
		rateQuote = 1
	}
	cross := rateQuote / rateAsset
	priceStr := strconv.FormatFloat(cross, 'f', -1, 64)
	return PriceSnapshot{
		AssetID:    asset.String(),
		Quote:      quote.String(),
		Price:      priceStr,
		PriceType:  "vwap",
		ObservedAt: snap.PublishedAt,
	}, []string{"massive"}, true
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
	// F-0073 closure: `pairs=` is accepted as an alias for
	// `asset_ids=` so clients arriving via cross-endpoint
	// extrapolation (CG-style sites that call a quotes endpoint
	// "pairs") don't hit a confusing 400 on their first try.
	// Passing both is a 400 — silent precedence is the same
	// anti-pattern F-0061 closes elsewhere.
	rawIDs := r.URL.Query().Get("asset_ids")
	rawPairs := r.URL.Query().Get("pairs")
	if rawIDs != "" && rawPairs != "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-parameter",
			"`asset_ids` and `pairs` are mutually exclusive", http.StatusBadRequest,
			"both query parameters refer to the same value — pick one (this endpoint's canonical form is `asset_ids=`; `pairs=` is accepted as an alias for cross-endpoint compatibility)")
		return
	}
	if rawIDs == "" {
		rawIDs = rawPairs
	}
	if rawIDs == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/missing-asset-ids",
			"Missing asset_ids parameter", http.StatusBadRequest,
			"asset_ids query parameter is required (comma-separated; `pairs=` is accepted as an alias)")
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

// priceBatchConcurrency bounds the per-request fan-out in
// lookupPriceBatch. The batch endpoint resolves up to 100 (GET) /
// 1000 (POST) asset_ids, each a LatestPrice + fallback-chain DB
// round-trip; resolving them serially put p99 at the 10s handler
// ceiling. 16-wide parallelism collapses a 100-id batch to ~7
// rounds while staying well inside the DB connection pool's
// headroom even with several batches in flight.
const priceBatchConcurrency = 16

// batchRowResult is the per-id outcome computed by resolveBatchRow.
// Exactly one of {ok, skip, fail} characterises a result.
type batchRowResult struct {
	snap         PriceSnapshot
	sources      []string
	stale        bool
	frozen       bool
	triangulated bool
	asset        canonical.Asset

	ok   bool // a real price row — include in the envelope
	skip bool // per-asset miss — omit, do NOT 404 the batch
	fail *batchRowFailure
}

// batchRowFailure means the whole batch must abort with a
// problem+json. It is carried as a value rather than written
// inline because resolveBatchRow runs in a worker goroutine, where
// touching the shared http.ResponseWriter would be a data race —
// the orchestrator writes the first failure single-threaded.
type batchRowFailure struct {
	status int
	typ    string
	title  string
	detail string
}

// resolveBatchRow resolves one (raw_id, quote) pair for the batch
// endpoint. Pure + goroutine-safe: it performs only reads (DB,
// Redis) and returns a result value — it never touches w. Three
// outcomes: ok (a row), skip (per-asset miss — omit), or fail (a
// validation/internal error that aborts the whole batch). Also
// where the Redis-VWAP fallback for aggregator-rewritten pairs
// lives.
func (s *Server) resolveBatchRow(ctx context.Context, r *http.Request, raw string, quote canonical.Asset) batchRowResult {
	asset, err := canonical.ParseAsset(raw)
	if err != nil {
		return batchRowResult{fail: &batchRowFailure{
			status: http.StatusBadRequest,
			typ:    "https://api.ratesengine.net/errors/invalid-asset-id",
			title:  "Invalid asset identifier",
			detail: raw + ": " + err.Error(),
		}}
	}
	if asset.Equal(quote) {
		return batchRowResult{fail: &batchRowFailure{
			status: http.StatusBadRequest,
			typ:    "https://api.ratesengine.net/errors/identity-price",
			title:  "Asset and quote are the same",
			detail: "price of an asset in itself is always 1; " + raw + " matches the quote",
		}}
	}
	// F-1340: route the primary read through the rc.89 XLM dual-form
	// alias loop, exactly as handlePrice does. Pre-fix the batch path
	// queried the literal form only, so asset_ids=native returned
	// stale/empty while /v1/price?asset=native served fresh CEX VWAP
	// published under the crypto:XLM alias key.
	snap, sources, stale, err := s.readPriceWithAliases(ctx, s.prices, asset, quote)
	if errors.Is(err, ErrPriceNotFound) {
		// Share the full three-layer fallback chain with /v1/price
		// (priceFallback). Pre-2026-05-10 the batch path inlined
		// only Redis-VWAP + fiat-cross-rate and skipped
		// tryStablecoinFiatProxy — the layer that resolves
		// X/fiat:USD via the operator's classic USD-peg list. That
		// asymmetry caused asset_ids that returned 200 on
		// /v1/price (e.g. USDT-G…) to be silently dropped from the
		// batch envelope. R-005 in docs/review-2026-05-10.md.
		if fs, fsrc, ftri, ok := s.priceFallback(ctx, asset, quote); ok {
			// F-1254: priceFallback responses are by definition below
			// the closed-bucket VWAP contract (last-trade / proxy /
			// triangulation). Mark stale so callers can tell the
			// batch row was a fallback. Carry the triangulated bool
			// through too (G2-16): the single-asset /v1/price path
			// surfaces flags.triangulated for these same fallbacks, so
			// the batch envelope must OR it in for parity rather than
			// silently dropping it.
			return batchRowResult{
				snap: fs, sources: fsrc, stale: true, triangulated: ftri,
				asset: asset, ok: true,
				frozen: s.lookupFrozen(r, asset, quote),
			}
		}
		return batchRowResult{skip: true} // omit, do not 404 the batch
	}
	if err != nil {
		if clientAborted(r, err) {
			// Client disconnected — the orchestrator detects the
			// cancelled context after join and returns without
			// writing. Surface as a skip so no failure is reported.
			return batchRowResult{skip: true}
		}
		s.logger.Error("LatestPrice (batch) failed",
			"err", err, "asset", asset.String(), "quote", quote.String())
		return batchRowResult{fail: &batchRowFailure{
			status: http.StatusInternalServerError,
			typ:    "https://api.ratesengine.net/errors/internal",
			title:  "Internal error",
		}}
	}
	return batchRowResult{
		snap: snap, sources: sources, stale: stale, asset: asset, ok: true,
		frozen: s.lookupFrozen(r, asset, quote),
	}
}

// lookupPriceBatch resolves every id concurrently — bounded by
// priceBatchConcurrency — and writes the envelope. Per-id results
// land in an index-keyed slice so the response preserves
// first-occurrence order regardless of which worker finished
// first. Missing observations (ErrPriceNotFound) are omitted, not
// 404'd; the first validation/internal failure in input order
// aborts the whole batch with a problem+json.
func (s *Server) lookupPriceBatch(w http.ResponseWriter, r *http.Request, ids []string, quote canonical.Asset) {
	ctx := r.Context()
	results := make([]batchRowResult, len(ids))

	// Bounded fan-out: each worker writes its own results[i] slot
	// (disjoint memory — no lock needed) and the semaphore caps how
	// many DB round-trips run at once.
	sem := make(chan struct{}, priceBatchConcurrency)
	var wg sync.WaitGroup
	for i, raw := range ids {
		wg.Add(1)
		go func(i int, raw string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = s.resolveBatchRow(ctx, r, raw, quote)
		}(i, raw)
	}
	wg.Wait()

	// Client disconnected mid-batch — nothing meaningful to write.
	if ctx.Err() != nil {
		return
	}
	// First failure in INPUT order aborts the batch — deterministic
	// regardless of worker completion order.
	for i := range results {
		if f := results[i].fail; f != nil {
			writeProblem(w, r, f.typ, f.title, f.status, f.detail)
			return
		}
	}

	out := make([]PriceSnapshot, 0, len(ids))
	allSources := map[string]struct{}{}
	anyStale := false
	anyFrozen := false
	anySingleSource := false
	anyTriangulated := false
	for i := range results {
		row := results[i]
		if !row.ok {
			continue // skip — per-asset miss
		}
		if row.stale {
			anyStale = true
		}
		if row.triangulated {
			anyTriangulated = true
		}
		for _, src := range row.sources {
			allSources[src] = struct{}{}
		}
		// Per-row freeze (resolved by resolveBatchRow) → OR into the
		// envelope flag. Best-effort: an absent freeze marker means
		// "not frozen".
		if row.frozen {
			anyFrozen = true
			anySingleSource = true // freeze implies single-source
		} else if len(row.sources) == 1 {
			anySingleSource = true
		}
		out = append(out, row.snap)
	}
	srcs := make([]string, 0, len(allSources))
	for src := range allSources {
		srcs = append(srcs, src)
	}
	// Sort for ADR-0015 byte-identical cross-region property —
	// single-asset reads sort sources at the storage boundary
	// (timescale.normalizeVwapSources, F-0016 closure); the batch
	// endpoint unions per-row sources through a map and previously
	// emitted them in map-iteration order, breaking the
	// byte-identical contract for batch responses (F-1259 in
	// audit-2026-05-12).
	sort.Strings(srcs)
	writeJSON(w, out, Flags{
		Stale:        anyStale,
		Frozen:       anyFrozen,
		SingleSource: anySingleSource,
		Triangulated: anyTriangulated,
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
