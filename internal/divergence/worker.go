package divergence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// Cache is the Redis subset the [Service] needs. Declared as an
// interface so tests can substitute miniredis or a fake without
// pulling the full redis.UniversalClient surface.
type Cache interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
}

// CachedResult is the wire shape stored at the `div:<asset>` Redis
// key per ADR-0007. Mirrors most of [Result] but with a couple of
// derived fields for API-side consumers that don't want to redo the
// threshold logic.
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
	// at div:<asset> keys. Required.
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
}

// Service wraps a set of References + a cache writer, exposing a
// single [Service.RefreshPair] method the aggregator hooks into
// after writing each fresh VWAP. Writes the cached result to
// Redis at the `div:<asset>` key per ADR-0007.
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
		refs:       opts.References,
		cache:      opts.Cache,
		threshold:  threshold,
		minSources: minSources,
		timeout:    timeout,
		sink:       opts.ObservationSink,
	}, nil
}

// RefreshPair runs one divergence check for the supplied pair +
// our-price, then writes the cached result to Redis at
// div:<base.String()>. The aggregator calls this from the
// bucket-close path AFTER the VWAP has been written to its own
// Redis key.
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

	key := cachekeys.Divergence(pair.Base)
	if err := s.cache.Set(ctx, key, body, cachekeys.DivergenceTTL).Err(); err != nil {
		return fmt.Errorf("divergence: cache set %s: %w", key, err)
	}

	// Durable per-reference mirror. Best-effort: a sink failure must
	// not surface to the caller because the Redis cache write — the
	// load-bearing operation that drives flags.divergence_warning
	// on the API response — has already succeeded.
	if s.sink != nil {
		s.flushObservations(ctx, pair, ourPrice, res, cached.ComputedAt)
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
		_ = s.sink.RecordObservation(ctx, ObservationRecord{
			Pair:       pair,
			Reference:  refName,
			OurPrice:   ourPrice,
			RefPrice:   refPrice,
			DeltaPct:   deltaPct,
			Firing:     firing,
			ObservedAt: observedAt,
		})
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

// LookupCached reads the most-recent cached divergence result for
// the asset. Returns ([CachedResult{}], false, nil) when no cached
// entry exists (the worker hasn't run for this asset yet, or the
// TTL has elapsed). API hot-path consumers call this when serving
// /v1/price to decide whether to set flags.divergence_warning.
//
// Cache-read errors other than redis.Nil are surfaced — callers
// should NOT silently set flags.divergence_warning=false on a
// transient cache outage; better to keep the previous response's
// flag value (or fail-open).
func (s *Service) LookupCached(ctx context.Context, asset canonical.Asset) (CachedResult, bool, error) {
	key := cachekeys.Divergence(asset)
	raw, err := s.cache.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return CachedResult{}, false, nil
	}
	if err != nil {
		return CachedResult{}, false, fmt.Errorf("divergence: cache get %s: %w", key, err)
	}
	var cached CachedResult
	if err := json.Unmarshal(raw, &cached); err != nil {
		return CachedResult{}, false, fmt.Errorf("divergence: unmarshal cached result: %w", err)
	}
	return cached, true, nil
}
