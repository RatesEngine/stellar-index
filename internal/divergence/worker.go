package divergence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/cachekeys"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// Cache is the Redis subset the [Service] needs. Declared as an
// interface so tests can substitute miniredis or a fake without
// pulling the full redis.UniversalClient surface.
//
// SAdd / SMembers / Expire were added for F-1344: the worker keys
// divergence results per-pair (`div:<base>/<quote>`) and maintains a
// per-base index SET (`div:idx:<base>`) so the by-asset reader can
// discover and OR every quote's WarningFired flag without a
// blocking KEYS/SCAN on the hot path. redis.UniversalClient
// satisfies all five methods.
type Cache interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	SAdd(ctx context.Context, key string, members ...any) *redis.IntCmd
	SMembers(ctx context.Context, key string) *redis.StringSliceCmd
	Expire(ctx context.Context, key string, expiration time.Duration) *redis.BoolCmd
}

// CachedResult is the wire shape stored at the `div:<base>/<quote>`
// Redis key per ADR-0007. Mirrors most of [Result] but with a couple
// of derived fields for API-side consumers that don't want to redo
// the threshold logic.
type CachedResult struct {
	// PairID is the canonical pair string the result is for.
	PairID string `json:"pair_id"`

	// OurPrice / Median / DivergencePct mirror the comparator output.
	OurPrice      float64 `json:"our_price"`
	Median        float64 `json:"median"`
	DivergencePct float64 `json:"divergence_pct"`

	// WarningFired is `SuccessCount >= MinSourcesForWarning AND
	// DivergencePct > Threshold` evaluated by the worker. Cached so
	// API readers don't need to know the threshold values.
	WarningFired bool `json:"warning_fired"`

	// Sources / Failures mirror Result, kept for operator
	// dashboards.
	Sources  map[string]float64 `json:"sources,omitempty"`
	Failures map[string]string  `json:"failures,omitempty"`

	// SuccessCount + FailureCount counters for the run.
	SuccessCount int `json:"success_count"`
	FailureCount int `json:"failure_count"`

	// ComputedAt is when the worker wrote this result. RFC 3339 UTC.
	ComputedAt time.Time `json:"computed_at"`
}

// ObservationSink is the optional durable-mirror seam for
// divergence observations. The Service calls RecordObservation
// once per (pair, reference) tuple every refresh tick, capturing
// the our_price / ref_price / delta_pct triple plus a firing/clear
// status.
//
// Today the worker writes only the aggregate result + a boolean
// firing flag to Redis with a TTL. The historical per-reference
// deltas are lost. The durable mirror persists them so the
// explorer /divergences page (explorer-data-inventory.md
// §7.19) can plot the actual divergence over time and so incident
// post-mortems can verify "Reflector drifted N% from us at ledger
// X" against ground truth.
//
// Implementations must NOT block the worker's hot path on network
// failures. Production wires
// `internal/storage/timescale.DivergenceSink`.
type ObservationSink interface {
	// RecordObservation persists one (pair, reference) comparison.
	// firing = true when |delta_pct| exceeded the per-reference
	// threshold at observation time.
	RecordObservation(ctx context.Context, obs ObservationRecord) error
}

// ObservationRecord is the per-(pair, reference) observation passed
// to ObservationSink. Decoupled from the internal CachedResult shape
// so the sink can evolve without the Service changing.
type ObservationRecord struct {
	Pair       canonical.Pair
	Reference  string
	OurPrice   float64
	RefPrice   float64
	DeltaPct   float64
	Firing     bool
	ObservedAt time.Time
}

// ServiceOptions configures a [Service].
type ServiceOptions struct {
	// References is the list of external sources to compare against.
	// Empty list disables divergence checking (Service.RefreshPair
	// returns nil without writing).
	References []Reference

	// Cache is the Redis client used to store CachedResult JSON
	// at div:<base>/<quote> keys. Required.
	Cache Cache

	// Threshold is the divergence percentage above which
	// WarningFired is true on the cached result. Default 5.0
	// (5%). Operators tune higher for noisier asset classes.
	Threshold float64

	// MinSourcesForWarning is the minimum number of successful
	// references required before WarningFired can be true. Default
	// 2 — a single dissenting source isn't enough to call divergence.
	MinSourcesForWarning int

	// PerReferenceTimeout is forwarded to [Compare] via
	// [CompareOptions]. Default 5s.
	PerReferenceTimeout time.Duration

	// ObservationSink, when non-nil, receives one record per (pair,
	// reference) tuple every refresh. Persists the per-reference
	// delta history that the Redis cache discards. Optional — nil
	// keeps legacy Redis-only behaviour.
	ObservationSink ObservationSink

	// Logger, when non-nil, receives WARN-level log lines for sink
	// failures. Optional — nil silences the path (legacy behaviour).
	// The aggregator passes its component logger so failures land
	// in the same journal stream as the rest of the orchestrator.
	Logger *slog.Logger

	// OnWarningFired, when non-nil, is invoked from RefreshPair on
	// the EDGE — a refresh that flips a pair from "below
	// threshold" → "above threshold" (or fires for the first
	// time). Best-effort: errors / panics inside the hook do not
	// propagate. F-1249 (codex audit-2026-05-12): the aggregator
	// wires this to customerwebhook.Fanout.Publish so dashboard
	// hooks subscribed to `divergence.firing` get a callback.
	//
	// Edge-only firing (vs every-refresh-while-firing) prevents
	// the API binary's delivery queue from re-spamming subscribers
	// on every aggregator tick a divergence stays above the
	// threshold. The fanout service is itself idempotent on the
	// subscriber list but the callbacks would still pile up.
	OnWarningFired WarningHook
}

// WarningHook is the callback shape for edge-triggered divergence
// warnings. `cached` is the same CachedResult Redis has just
// stored; reuse it to build the webhook payload.
type WarningHook func(ctx context.Context, pair canonical.Pair, cached CachedResult)

// Service wraps a set of References + a cache writer, exposing a
// single [Service.RefreshPair] method the aggregator hooks into
// after writing each fresh VWAP. Writes the cached result to
// Redis at the `div:<base>/<quote>` key per ADR-0007.
//
// Service is safe for concurrent RefreshPair calls — the
// underlying Cache and References must also be concurrent-safe
// (they all are by contract).
type Service struct {
	refs       []Reference
	cache      Cache
	threshold  float64
	minSources int
	timeout    time.Duration
	sink       ObservationSink
	// logger is optional — nil-safe. When set, sink failures are
	// logged at WARN per (pair, reference) instead of being
	// silently dropped. Pre-2026-05-10 the missing-log meant
	// Postgres write failures (e.g. during the disk-full SEV-2
	// cascade) silently dropped every divergence_observations row
	// — operators only saw it when the explorer's /divergences
	// page surfaced a gap, days later.
	logger *slog.Logger

	// onWarning + warningState power the edge-triggered fan-out
	// hook (F-1249 codex audit-2026-05-12). `warningState` maps
	// pair.String() → most-recent WarningFired bool; the hook fires
	// only on `false → true` transitions.
	onWarning    WarningHook
	warningMu    sync.Mutex
	warningState map[string]bool
}

// NewService constructs a divergence service. Returns an error when
// required options are missing.
func NewService(opts ServiceOptions) (*Service, error) {
	if opts.Cache == nil {
		return nil, errors.New("divergence: Cache is required")
	}
	threshold := opts.Threshold
	if threshold <= 0 {
		threshold = 5.0
	}
	minSources := opts.MinSourcesForWarning
	if minSources <= 0 {
		minSources = 2
	}
	timeout := opts.PerReferenceTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Service{
		refs:         opts.References,
		cache:        opts.Cache,
		threshold:    threshold,
		minSources:   minSources,
		timeout:      timeout,
		sink:         opts.ObservationSink,
		logger:       opts.Logger,
		onWarning:    opts.OnWarningFired,
		warningState: map[string]bool{},
	}, nil
}

// RefreshPair runs one divergence check for the supplied pair +
// our-price, then writes the cached result to Redis at
// div:<base>/<quote> and records the quote in the per-base index
// set. The aggregator calls this from the bucket-close path AFTER
// the VWAP has been written to its own Redis key.
//
// Returns nil when the worker has no References configured (silent
// no-op so an operator who hasn't enabled divergence yet doesn't
// see a torrent of "skipped" log lines). Returns the underlying
// error when Compare's network calls all fail, but cache-write
// errors are returned separately so the caller can decide whether
// to retry.
func (s *Service) RefreshPair(ctx context.Context, pair canonical.Pair, ourPrice float64, observedAt time.Time) error {
	if len(s.refs) == 0 {
		return nil
	}
	res := Compare(ctx, s.refs, pair, ourPrice, observedAt, CompareOptions{
		PerReferenceTimeout: s.timeout,
		MinSuccessForMedian: 1, // surface even single-source signals; threshold gate handles trustworthiness
	})

	cached := CachedResult{
		PairID:        pair.String(),
		OurPrice:      ourPrice,
		Median:        res.Median,
		DivergencePct: res.DivergencePct,
		WarningFired:  res.SuccessCount >= s.minSources && res.DivergencePct > s.threshold,
		Sources:       res.Sources,
		Failures:      res.Failures,
		SuccessCount:  res.SuccessCount,
		FailureCount:  res.FailureCount,
		ComputedAt:    time.Now().UTC(),
	}

	body, err := json.Marshal(cached)
	if err != nil {
		// Should be unreachable — CachedResult has no func/chan
		// fields. Wrap for diagnostic completeness.
		return fmt.Errorf("divergence: marshal cached result: %w", err)
	}

	// F-1344 (G16-03): write a PER-PAIR key, not a per-base key. The
	// orchestrator calls RefreshPair once per configured pair; a
	// per-base key let the last pair in iteration order clobber the
	// asset's divergence verdict. The per-pair key keeps each pair's
	// result independent; the by-asset reader (LookupCached) ORs them.
	key := cachekeys.Divergence(pair)
	if err := s.cache.Set(ctx, key, body, cachekeys.DivergenceTTL).Err(); err != nil {
		return fmt.Errorf("divergence: cache set %s: %w", key, err)
	}

	// Maintain the per-base quote index so LookupCached can discover
	// which per-pair keys to OR for a given base. SADD is idempotent;
	// the Expire refreshes the set's TTL on every write so it drains
	// in lock-step with the value keys (a base whose pairs stop
	// refreshing loses its index after DivergenceTTL rather than
	// pinning dead quote members forever).
	idxKey := cachekeys.DivergenceBaseIndex(pair.Base)
	if err := s.cache.SAdd(ctx, idxKey, pair.Quote.String()).Err(); err != nil {
		return fmt.Errorf("divergence: index sadd %s: %w", idxKey, err)
	}
	if err := s.cache.Expire(ctx, idxKey, cachekeys.DivergenceTTL).Err(); err != nil {
		return fmt.Errorf("divergence: index expire %s: %w", idxKey, err)
	}

	// Durable per-reference mirror. Best-effort: a sink failure must
	// not surface to the caller because the Redis cache write — the
	// load-bearing operation that drives flags.divergence_warning
	// on the API response — has already succeeded.
	if s.sink != nil {
		s.flushObservations(ctx, pair, ourPrice, res, cached.ComputedAt)
	}

	// F-1249 (codex audit-2026-05-12): edge-triggered warning hook.
	// Only fires on `false → true` so the customer-webhook
	// delivery queue doesn't get one POST per refresh-while-firing.
	// Returns to "false" reset the latch so the next time the
	// pair re-crosses the threshold the customer gets a fresh
	// callback.
	if s.onWarning != nil {
		s.warningMu.Lock()
		prev := s.warningState[pair.String()]
		s.warningState[pair.String()] = cached.WarningFired
		s.warningMu.Unlock()
		if cached.WarningFired && !prev {
			s.onWarning(ctx, pair, cached)
		}
	}
	return nil
}

// flushObservations persists one durable row per (pair, reference)
// for the current refresh. Each successful reference contributes
// its observed price + delta + firing flag.
//
// Failures (references that errored out) are deliberately skipped
// — there's no observation to record, and the failure is already
// surfaced via the Redis cache's Failures map for operator
// dashboards.
func (s *Service) flushObservations(
	ctx context.Context,
	pair canonical.Pair,
	ourPrice float64,
	res Result,
	observedAt time.Time,
) {
	for refName, refPrice := range res.Sources {
		if refPrice == 0 {
			// Defensive: a reference reporting zero would produce a
			// divide-by-zero on delta. Skip with no log; this is
			// the comparator's job to surface.
			continue
		}
		deltaPct := (ourPrice - refPrice) / refPrice * 100.0
		firing := absFloat(deltaPct) > s.threshold
		if err := s.sink.RecordObservation(ctx, ObservationRecord{
			Pair:       pair,
			Reference:  refName,
			OurPrice:   ourPrice,
			RefPrice:   refPrice,
			DeltaPct:   deltaPct,
			Firing:     firing,
			ObservedAt: observedAt,
		}); err != nil && s.logger != nil {
			// Best-effort write — the Redis cache (load-bearing for
			// flags.divergence_warning) already succeeded. Log so
			// operators see the durable-mirror gap; pre-2026-05-10
			// this was a fully-silent drop.
			s.logger.Warn("divergence: sink RecordObservation failed",
				"pair", pair.String(),
				"reference", refName,
				"err", err)
		}
	}
}

// absFloat is a tiny helper kept here (math.Abs imports the heavier
// math package the rest of this file doesn't need).
func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// LookupCached returns the divergence verdict for a BASE asset,
// aggregated across every quote that asset trades against (F-1344).
// The returned CachedResult.WarningFired is the OR of the per-pair
// WarningFired flags — "firing if ANY quote diverges" — so the
// API's by-asset DivergenceFiringFor(asset) keeps working unchanged
// against the new per-pair key layout, and its verdict is
// independent of the order the orchestrator refreshes pairs in.
//
// The remaining fields (OurPrice / Median / DivergencePct / Sources
// / …) are copied from the FIRING pair with the largest divergence
// when any pair fires, else from the most-recently-computed pair, so
// operator dashboards that read the bundle still see a representative
// detail row. The PairID field identifies which pair the detail came
// from.
//
// Returns ([CachedResult{}], false, nil) when the base has no live
// per-pair entries (the worker hasn't run for it yet, or every
// pair's TTL has elapsed). API hot-path consumers call this when
// serving /v1/price to decide whether to set flags.divergence_warning.
//
// Cache-read errors other than redis.Nil are surfaced — callers
// should NOT silently set flags.divergence_warning=false on a
// transient cache outage; better to keep the previous response's
// flag value (or fail-open).
func (s *Service) LookupCached(ctx context.Context, asset canonical.Asset) (CachedResult, bool, error) {
	idxKey := cachekeys.DivergenceBaseIndex(asset)
	quotes, err := s.cache.SMembers(ctx, idxKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return CachedResult{}, false, fmt.Errorf("divergence: index smembers %s: %w", idxKey, err)
	}
	if len(quotes) == 0 {
		return CachedResult{}, false, nil
	}

	var (
		agg       CachedResult
		found     bool
		warning   bool
		bestFire  bool    // whether `agg` currently holds a firing pair
		bestDelta float64 // |DivergencePct| of the representative pair held in `agg`
	)
	for _, q := range quotes {
		quote, perr := canonical.ParseAsset(q)
		if perr != nil {
			// A malformed member can't be turned back into a key; skip
			// it rather than fail the whole lookup. The index is
			// worker-written from canonical assets, so this is a
			// defensive guard, not an expected path.
			continue
		}
		pair := canonical.Pair{Base: asset, Quote: quote}
		key := cachekeys.Divergence(pair)
		raw, gerr := s.cache.Get(ctx, key).Bytes()
		if errors.Is(gerr, redis.Nil) {
			// Value expired but the index member lingered (the set's
			// own TTL hasn't fired yet). Treat as "no contribution".
			continue
		}
		if gerr != nil {
			return CachedResult{}, false, fmt.Errorf("divergence: cache get %s: %w", key, gerr)
		}
		var cached CachedResult
		if uerr := json.Unmarshal(raw, &cached); uerr != nil {
			return CachedResult{}, false, fmt.Errorf("divergence: unmarshal cached result: %w", uerr)
		}
		if cached.WarningFired {
			warning = true
		}

		// Pick the representative detail row: prefer firing pairs over
		// non-firing, and within the same firing-status prefer the
		// larger |divergence|. The first contributing pair always
		// wins its slot (firstContrib short-circuits the comparison).
		delta := absFloat(cached.DivergencePct)
		switch {
		case !found:
			agg, bestFire, bestDelta = cached, cached.WarningFired, delta
		case cached.WarningFired && !bestFire:
			agg, bestFire, bestDelta = cached, true, delta
		case cached.WarningFired == bestFire && delta >= bestDelta:
			agg, bestDelta = cached, delta
		}
		found = true
	}
	if !found {
		return CachedResult{}, false, nil
	}
	agg.WarningFired = warning
	return agg, true, nil
}
