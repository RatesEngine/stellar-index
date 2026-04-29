package confidence

import "math"

// BootstrapDays is the age threshold below which the confidence
// score is hard-capped at [BootstrapConfidenceCap], regardless of
// other factor values. Per ADR-0019 §"Bootstrap (warmup) policy
// for new assets":
//
//	"For an asset with < 30 days of history: ... cap confidence at
//	 0.5 regardless of other factors."
//
// The cap exists because a freshly-listed asset's per-asset
// baseline isn't trustworthy yet — even with multi-source
// agreement and tight liquidity, we lack the historical signal
// to know what's normal for THIS asset. 0.5 says "we can serve
// the price, but consumers should treat it as provisional".
const (
	BootstrapDays          = 30.0
	BootstrapConfidenceCap = 0.5
)

// Inputs are the raw observations a single bucket carries. The
// orchestrator populates this from the bucket's stats + the per-
// asset baseline; this package converts to a [Score] without any
// further IO.
//
// Field shapes deliberately match the data the orchestrator
// already has at confidence-compute time — no new accessors needed
// at the call site.
type Inputs struct {
	// ZScore — the largest z-score across the multi-window
	// baselines (`baseline.MultiBaseline.MaxZScore`). Pass 0 when
	// the asset is in full bootstrap (no baseline at any window).
	ZScore float64

	// SourceCount — distinct contributing sources in the bucket.
	SourceCount int

	// SourceClassCount — distinct source CLASSES (CEX / DEX /
	// oracle / aggregator). 0 when no sources contributed.
	SourceClassCount int

	// LiquidityUSD — bucket volume in USD. Negative or zero means
	// "no liquidity signal"; the [LiquidityFactor] handles that
	// branch.
	LiquidityUSD float64

	// CrossOracleDivergencePct — % absolute deviation between our
	// price and the cross-oracle median. Pass a negative value to
	// signal "no cross-oracle data" (returns the neutral factor
	// per ADR-0019 worked example).
	CrossOracleDivergencePct float64

	// BaselineAgeDays — days since the per-asset baseline was first
	// computed. 0 for a brand-new pair (bootstrap penalty). Use
	// negative to mean "no baseline yet"; the factor returns 0.5.
	BaselineAgeDays float64
}

// Factors holds the per-factor decomposition that ships on the
// wire alongside the combined confidence score. Customers and
// operators look at this to understand WHY confidence dropped:
// "z=1.0 (ok), src=0.3 (single-source), div=0.5 (one class)" tells
// you the issue is source coverage, not staleness.
type Factors struct {
	ZScore          float64 `json:"z_score"`
	SourceCount     float64 `json:"source_count"`
	Diversity       float64 `json:"diversity"`
	Liquidity       float64 `json:"liquidity"`
	CrossOracle     float64 `json:"cross_oracle"`
	BaselineQuality float64 `json:"baseline_quality"`
}

// Weights are the per-factor exponents in the weighted geometric
// mean. ADR-0019 specifies these as operator-tunable but defaults
// them all to 1.0 (unweighted geometric mean). A weight of 0
// effectively removes that factor from the product.
//
// Operators tune these via [anomaly.weights] in TOML — that wiring
// lands with the orchestrator slice. This struct is just the math
// surface.
type Weights struct {
	ZScore          float64
	SourceCount     float64
	Diversity       float64
	Liquidity       float64
	CrossOracle     float64
	BaselineQuality float64
}

// DefaultWeights returns all-ones — the ADR-0019 default
// (unweighted geometric mean). Use directly when an operator
// hasn't supplied a [Weights] override.
func DefaultWeights() Weights {
	return Weights{
		ZScore:          1.0,
		SourceCount:     1.0,
		Diversity:       1.0,
		Liquidity:       1.0,
		CrossOracle:     1.0,
		BaselineQuality: 1.0,
	}
}

// Score is the combined confidence score plus its decomposition.
// The wire response carries this whole struct — Confidence on the
// envelope, Factors on a sibling field for transparency.
type Score struct {
	Confidence float64 `json:"confidence"`
	Factors    Factors `json:"factors"`
}

// Compute returns the [Score] for a bucket given its raw inputs and
// per-factor weights. Pass [DefaultWeights] to compute with the
// ADR-0019 default unweighted shape.
//
// The combined score is the weighted geometric mean:
//
//	confidence = prod(factor_i ^ weight_i) ^ (1 / sum(weights))
//
// The 1/sum(weights) normalisation keeps the final value in [0, 1]
// regardless of weight magnitude. Without it, doubling every weight
// would square the result.
//
// Edge cases:
//
//   - All weights = 0: returns a neutral 0.5 with the per-factor
//     decomposition still populated. (Useful for diagnostics:
//     "compute the factors but ignore the combiner".)
//   - Any factor returns exactly 0 with non-zero weight: the
//     geometric mean is 0 (the dominating-factor behaviour the
//     ADR explicitly wants).
func Compute(in Inputs, w Weights) Score {
	f := Factors{
		ZScore:          ZScoreFactor(in.ZScore),
		SourceCount:     SourceCountFactor(in.SourceCount),
		Diversity:       DiversityFactor(in.SourceClassCount),
		Liquidity:       LiquidityFactor(in.LiquidityUSD),
		CrossOracle:     CrossOracleFactor(in.CrossOracleDivergencePct),
		BaselineQuality: BaselineQualityFactor(in.BaselineAgeDays),
	}

	totalWeight := w.ZScore + w.SourceCount + w.Diversity + w.Liquidity + w.CrossOracle + w.BaselineQuality
	if totalWeight <= 0 {
		return Score{Confidence: 0.5, Factors: f}
	}

	// Sum log-factors instead of multiplying directly — keeps the
	// arithmetic numerically stable when any factor is very small
	// (log(small) is large negative; the exp at the end recovers
	// the result without underflow).
	logSum := safeLog(f.ZScore)*w.ZScore +
		safeLog(f.SourceCount)*w.SourceCount +
		safeLog(f.Diversity)*w.Diversity +
		safeLog(f.Liquidity)*w.Liquidity +
		safeLog(f.CrossOracle)*w.CrossOracle +
		safeLog(f.BaselineQuality)*w.BaselineQuality

	conf := math.Exp(logSum / totalWeight)
	conf = applyBootstrapCap(conf, in.BaselineAgeDays)
	return Score{Confidence: clamp01(conf), Factors: f}
}

// applyBootstrapCap caps the final confidence at
// [BootstrapConfidenceCap] when the asset is still in bootstrap
// (BaselineAgeDays known and below [BootstrapDays]).
//
// A negative BaselineAgeDays is the "no baseline yet" sentinel —
// stricter than bootstrap, so we apply the cap there too. Callers
// who pass an unknown age via NaN get no cap (the BaselineQuality
// factor already returns 0.5 for NaN, dragging the combiner down
// without a hard ceiling).
func applyBootstrapCap(c, ageDays float64) float64 {
	if math.IsNaN(ageDays) {
		return c
	}
	if ageDays >= BootstrapDays {
		return c
	}
	if c > BootstrapConfidenceCap {
		return BootstrapConfidenceCap
	}
	return c
}

// safeLog returns log(x) with log(0) → -Inf clamped through Exp;
// log(NaN) and log(<0) return -Inf so the geometric mean dominates
// to zero. Defensive: factor outputs are already clamped to [0, 1]
// but the math would produce NaN on log(0) so we route through
// math.Inf(-1) explicitly.
func safeLog(x float64) float64 {
	if x <= 0 || math.IsNaN(x) {
		return math.Inf(-1)
	}
	return math.Log(x)
}
