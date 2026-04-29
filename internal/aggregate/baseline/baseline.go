package baseline

import (
	"errors"
	"math"
	"sort"
)

// MADScale is the consistency factor that maps MAD to σ-equivalent
// units for normally-distributed data. Multiplying raw MAD by
// 1.4826 means a 5-σ-equivalent anomaly is at z=5 (matching the
// industry convention) regardless of whether the underlying
// distribution is actually Gaussian.
//
// Source: 1 / Φ⁻¹(0.75) ≈ 1.4826, where Φ⁻¹ is the inverse standard
// normal CDF.
const MADScale = 1.4826

// MinSamples is the floor below which Median + MAD aren't meaningful.
// At n=1 there's no variance to measure; at n=0 there's nothing at
// all. ADR-0019 §"Bootstrap (warmup) policy" handles the
// not-yet-trained case at a higher layer; this package just refuses
// to compute.
const MinSamples = 2

// ErrNotEnoughSamples is what [FromReturns] returns when the input
// has fewer than [MinSamples] elements. Callers translate this into
// "use the bootstrap policy" rather than treating it as an error.
var ErrNotEnoughSamples = errors.New("baseline: not enough samples (need >= 2)")

// Baseline is the robust-statistics summary of a rolling window of
// returns, ready to score a new observation against.
//
// Population semantics: Median is the 50th percentile; MAD is the
// 1.4826-scaled median absolute deviation. N is the count that fed
// the computation — useful for downstream baseline_quality factors
// (ADR-0019 §"Multi-factor confidence score").
//
// MAD == 0 is a real outcome — it means every observation in the
// window was identical (think a tightly-pegged stablecoin during a
// quiet period). [Baseline.ZScore] handles that explicitly.
type Baseline struct {
	Median float64
	MAD    float64
	N      int
}

// FromReturns computes the robust-stats summary of a slice of
// bucket-to-bucket percent changes. The input is NOT mutated.
//
// Returns [ErrNotEnoughSamples] when len(returns) < [MinSamples].
// Caller is responsible for filtering NaN / Inf out of the input
// — this function does not silently drop pathological inputs
// because that would mask data-pipeline bugs upstream.
func FromReturns(returns []float64) (Baseline, error) {
	if len(returns) < MinSamples {
		return Baseline{}, ErrNotEnoughSamples
	}

	median := Median(returns)
	mad := MAD(returns) // already scaled by MADScale internally

	return Baseline{
		Median: median,
		MAD:    mad,
		N:      len(returns),
	}, nil
}

// ZScore returns the standardised distance from the baseline
// median, in σ-equivalent units:
//
//	z = |x - Median| / MAD
//
// Special cases:
//
//   - When MAD is 0 and x equals Median: returns 0 (the new
//     observation matches the baseline exactly; not anomalous).
//   - When MAD is 0 and x differs from Median: returns +Inf (any
//     deviation from a no-spread baseline is by definition
//     infinitely many σ; the caller will see "anomalous" if it has
//     a sane threshold like z>=5).
//
// Callers compare against threshold = 5 per ADR-0019 §"5σ trigger"
// to gate confidence-score factors and freeze decisions.
func (b Baseline) ZScore(x float64) float64 {
	delta := math.Abs(x - b.Median)
	if b.MAD == 0 {
		if delta == 0 {
			return 0
		}
		return math.Inf(1)
	}
	return delta / b.MAD
}

// Median returns the 50th percentile of `xs`. The input is NOT
// mutated; an internal copy is sorted. Empty input returns 0
// (callers should use [FromReturns] which checks [MinSamples]
// before delegating here).
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]float64, len(xs))
	copy(cp, xs)
	sort.Float64s(cp)

	n := len(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	// Even count: arithmetic mean of the two middle values. Done
	// in float64 because the inputs are already float64 — adding
	// big.Rat would be precision theatre at this layer.
	return (cp[n/2-1] + cp[n/2]) / 2
}

// MAD returns the 1.4826-scaled median absolute deviation:
//
//	MAD = 1.4826 * median( |xs[i] - median(xs)| )
//
// The 1.4826 factor (see [MADScale]) converts to σ-equivalent for
// normal data — so [Baseline.ZScore] reads as "σ-equivalents from
// median" without further conversion.
//
// Empty input returns 0. Single-element input also returns 0
// (no spread to measure — degenerate but well-defined).
func MAD(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	med := Median(xs)
	devs := make([]float64, len(xs))
	for i, x := range xs {
		devs[i] = math.Abs(x - med)
	}
	return MADScale * Median(devs)
}

// ReturnsFromVWAPs converts a chronologically-ordered slice of
// bucket VWAP values into bucket-to-bucket percent changes:
//
//	returns[i] = (vwaps[i+1] - vwaps[i]) / vwaps[i]
//
// Returns nil when len(vwaps) < 2 — there's no return to compute
// for a single observation. Buckets with vwaps[i] == 0 are skipped
// (division by zero); the resulting slice may be shorter than
// len(vwaps)-1.
//
// Used as the input to [FromReturns] when building a baseline from
// a stored window of 1m VWAP buckets.
func ReturnsFromVWAPs(vwaps []float64) []float64 {
	if len(vwaps) < 2 {
		return nil
	}
	out := make([]float64, 0, len(vwaps)-1)
	for i := 0; i < len(vwaps)-1; i++ {
		prev := vwaps[i]
		if prev == 0 {
			continue
		}
		out = append(out, (vwaps[i+1]-prev)/prev)
	}
	return out
}
