package supply

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
)

// SnapshotReader is the storage primitive the [CrossCheckRefresher]
// uses to fetch the most-recent snapshot for an asset_key. Production
// impl is timescale.Store.LatestSupply; the asset_key form is the
// supply-package canonical key (see [AssetKey]).
//
// Implementations MUST return a wrapped sentinel that satisfies
// `errors.Is(err, ErrNoSnapshot)` when the asset has no recorded
// snapshot — the refresher distinguishes that from transient read
// errors so the bootstrap state (no snapshot yet) doesn't surface as
// a read failure on the dashboard.
type SnapshotReader interface {
	LatestSupply(ctx context.Context, assetKey string) (Supply, error)
}

// ErrNoSnapshot is the sentinel a [SnapshotReader] returns when the
// asset has no rows in `asset_supply_history` yet. Callers wrap this
// (e.g. `fmt.Errorf("…: %w", ErrNoSnapshot)`) so `errors.Is` keeps
// working through the wrap chain.
//
// Production storage maps timescale.ErrNotFound → ErrNoSnapshot via
// the supplyStorageReader adapter in cmd/ratesengine-aggregator —
// the supply package itself doesn't import timescale.
var ErrNoSnapshot = errors.New("supply: no snapshot for asset_key")

// CrossCheckPair binds a classic asset's supply.AssetKey form (the
// "CODE:ISSUER" colon-separated key — produced by [AssetKey] for a
// classic asset) to its SAC-wrapper contract id (a bare C-strkey).
// Both must already be in the watched-set so the per-asset refreshers
// produce snapshots for them; the cross-check refresher reads those
// snapshots back.
type CrossCheckPair struct {
	ClassicKey string
	SACKey     string
}

// CrossCheckOutcomeKind is the per-pair outcome of one tick. Stable
// metric-label strings; the aggregator-level counter uses these
// values directly as the `outcome` label per
// `ratesengine_supply_cross_check_total`.
type CrossCheckOutcomeKind string

const (
	// CrossCheckOutcomeWithin — both snapshots loaded, divergence ≤
	// CrossCheckTolerance. The gauge is set to the divergence value
	// (≥0 stroops, almost always 0).
	CrossCheckOutcomeWithin CrossCheckOutcomeKind = "within"

	// CrossCheckOutcomeOver — both snapshots loaded, divergence >
	// CrossCheckTolerance. The gauge is set to the divergence; the
	// supply_cross_check_divergence alert fires after `for: 5m`.
	CrossCheckOutcomeOver CrossCheckOutcomeKind = "over"

	// CrossCheckOutcomeMissing — at least one of (classic, SAC) has
	// no snapshot in storage yet. Common during early bring-up
	// before either side's refresher has produced its first row.
	// Gauge is NOT updated — operators see the bootstrap signal
	// via the missing-side counter rather than a misleading zero
	// gauge that would imply "checked, agreed".
	CrossCheckOutcomeMissing CrossCheckOutcomeKind = "missing_snapshot"

	// CrossCheckOutcomeReadError — a transient storage error fired
	// while reading either side. Gauge is NOT updated; the surface
	// is the counter so operators can chart sustained read failure
	// rate on this pair.
	CrossCheckOutcomeReadError CrossCheckOutcomeKind = "read_error"
)

// CrossCheckOutcome is the per-pair result of one tick. The
// refresher emits one Outcome per configured pair regardless of
// success/failure — the per-tick slice has stable length so
// aggregator-level counter cardinality stays bounded by the pair
// count.
type CrossCheckOutcome struct {
	Pair   CrossCheckPair
	Kind   CrossCheckOutcomeKind
	Result CrossCheckResult // populated on Within / Over
	Err    error            // populated on Missing / ReadError
}

// CrossCheckEmitter is the metric-emission seam — kept as an
// interface so the refresher stays Prometheus-agnostic and unit
// tests can capture emitted values without a registry.
//
// Production impl wraps obs.SupplyCrossCheckDivergenceStroops +
// obs.SupplyCrossCheckTotal; the wiring lives in
// cmd/ratesengine-aggregator/main.go where the supply package
// can stay free of the obs dependency.
type CrossCheckEmitter interface {
	// Divergence sets the per-asset gauge to the stroop divergence.
	// Called only on Within / Over outcomes. Negative values are a
	// caller bug ([CrossCheck] always returns a non-negative
	// abs-difference).
	Divergence(classicKey string, stroops float64)

	// Outcome increments the per-outcome counter. Called for every
	// outcome (including Missing / ReadError) so operators see the
	// "is the cross-checker even running" signal.
	Outcome(kind CrossCheckOutcomeKind)
}

// CrossCheckRefresher runs one cross-check cycle per [Tick] call.
// Loads the most-recent snapshot for each side of every configured
// pair, runs [CrossCheck] on the pair, and emits the result.
//
// The refresher is policy-free: it neither chooses pairs nor decides
// tolerance. Pairs come from the aggregator at construction time
// (derived from `[supply].sac_wrappers` ∩ watched-sets) and the
// tolerance is the package-level [CrossCheckTolerance].
type CrossCheckRefresher struct {
	pairs   []CrossCheckPair
	reader  SnapshotReader
	emitter CrossCheckEmitter
	logger  *slog.Logger
}

// NewCrossCheckRefresher constructs the refresher. Empty pairs is
// valid (the operator hasn't configured any SAC wrappers in the
// watched set yet) — Tick is a no-op and emits no outcomes in that
// case. Returns an error on duplicate pairs to surface operator
// config bugs early; sorts the input so per-tick emission order is
// stable across process restarts.
func NewCrossCheckRefresher(pairs []CrossCheckPair, reader SnapshotReader, emitter CrossCheckEmitter, logger *slog.Logger) (*CrossCheckRefresher, error) {
	if reader == nil {
		return nil, errors.New("supply: cross-check refresher needs a SnapshotReader")
	}
	if emitter == nil {
		return nil, errors.New("supply: cross-check refresher needs a CrossCheckEmitter")
	}
	if logger == nil {
		return nil, errors.New("supply: cross-check refresher needs a logger")
	}
	for i, p := range pairs {
		if p.ClassicKey == "" {
			return nil, fmt.Errorf("supply: cross-check pair[%d] has empty ClassicKey", i)
		}
		if p.SACKey == "" {
			return nil, fmt.Errorf("supply: cross-check pair[%d] (%s) has empty SACKey", i, p.ClassicKey)
		}
	}
	sorted := make([]CrossCheckPair, len(pairs))
	copy(sorted, pairs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ClassicKey < sorted[j].ClassicKey })
	for i := 1; i < len(sorted); i++ {
		if sorted[i].ClassicKey == sorted[i-1].ClassicKey {
			return nil, fmt.Errorf("supply: cross-check pairs duplicate ClassicKey %q", sorted[i].ClassicKey)
		}
	}
	return &CrossCheckRefresher{
		pairs:   sorted,
		reader:  reader,
		emitter: emitter,
		logger:  logger,
	}, nil
}

// Tick runs one cycle across every configured pair. Per-pair errors
// don't bubble up — they're logged and surfaced via the outcome
// slice so a single transient storage hiccup doesn't drop the
// remaining pairs' cross-checks. The slice has stable length =
// len(pairs) so callers can size dashboards accordingly.
func (r *CrossCheckRefresher) Tick(ctx context.Context) []CrossCheckOutcome {
	if len(r.pairs) == 0 {
		return nil
	}
	out := make([]CrossCheckOutcome, 0, len(r.pairs))
	for _, p := range r.pairs {
		outcome := r.tickOne(ctx, p)
		r.emitter.Outcome(outcome.Kind)
		switch outcome.Kind {
		case CrossCheckOutcomeWithin, CrossCheckOutcomeOver:
			stroops, _ := outcome.Result.DivergenceStroops.Float64()
			r.emitter.Divergence(p.ClassicKey, stroops)
		}
		out = append(out, outcome)
	}
	return out
}

func (r *CrossCheckRefresher) tickOne(ctx context.Context, p CrossCheckPair) CrossCheckOutcome {
	classic, err := r.reader.LatestSupply(ctx, p.ClassicKey)
	if err != nil {
		if errors.Is(err, ErrNoSnapshot) {
			r.logger.Debug("cross-check: no classic snapshot yet",
				"classic_key", p.ClassicKey, "sac_key", p.SACKey)
			return CrossCheckOutcome{Pair: p, Kind: CrossCheckOutcomeMissing, Err: err}
		}
		r.logger.Warn("cross-check: classic read failed",
			"classic_key", p.ClassicKey, "err", err)
		return CrossCheckOutcome{Pair: p, Kind: CrossCheckOutcomeReadError, Err: err}
	}
	sac, err := r.reader.LatestSupply(ctx, p.SACKey)
	if err != nil {
		if errors.Is(err, ErrNoSnapshot) {
			r.logger.Debug("cross-check: no sac snapshot yet",
				"classic_key", p.ClassicKey, "sac_key", p.SACKey)
			return CrossCheckOutcome{Pair: p, Kind: CrossCheckOutcomeMissing, Err: err}
		}
		r.logger.Warn("cross-check: sac read failed",
			"sac_key", p.SACKey, "err", err)
		return CrossCheckOutcome{Pair: p, Kind: CrossCheckOutcomeReadError, Err: err}
	}
	result, err := CrossCheck(classic, sac)
	if err != nil {
		r.logger.Warn("cross-check: compare failed",
			"classic_key", p.ClassicKey, "sac_key", p.SACKey, "err", err)
		return CrossCheckOutcome{Pair: p, Kind: CrossCheckOutcomeReadError, Err: err}
	}
	if result.WithinTolerance {
		return CrossCheckOutcome{Pair: p, Kind: CrossCheckOutcomeWithin, Result: result}
	}
	r.logger.Warn("cross-check: divergence over tolerance",
		"classic_key", p.ClassicKey,
		"sac_key", p.SACKey,
		"divergence_stroops", result.DivergenceStroops.String())
	return CrossCheckOutcome{Pair: p, Kind: CrossCheckOutcomeOver, Result: result}
}
