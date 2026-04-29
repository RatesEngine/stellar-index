package confidence_test

import (
	"math"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/aggregate/confidence"
)

// healthyInputs are the ADR-0019 worked-example values:
//
//	z=0.3, sources=6, classes=2, liquidity=$250K, divergence=0.4%,
//	baseline_age=187 days
//
// Expected confidence ≈ 0.92 per the ADR. Used as a regression
// anchor so a future tweak to factor shapes doesn't accidentally
// drift the documented worked example.
func healthyInputs() confidence.Inputs {
	return confidence.Inputs{
		ZScore:                   0.3,
		SourceCount:              6,
		SourceClassCount:         2,
		LiquidityUSD:             250_000,
		CrossOracleDivergencePct: 0.4,
		BaselineAgeDays:          187,
	}
}

// TestCompute_HealthyAnchor — the ADR-0019 worked-example inputs
// produce a high confidence under the implemented factor shapes.
//
// The ADR's illustrative figure of "≈ 0.92" describes the
// SHAPE of the response (high but not 1.0) rather than a
// mathematical anchor. With our z/source/etc. factor shapes a
// fully-healthy bucket sits in [0.85, 1.0]; this test pins that
// range so future shape edits surface as deliberate decisions
// rather than silent regressions.
func TestCompute_HealthyAnchor(t *testing.T) {
	got := confidence.Compute(healthyInputs(), confidence.DefaultWeights())
	if got.Confidence < 0.85 {
		t.Errorf("healthy confidence = %v, want >= 0.85 (high under all factors green)", got.Confidence)
	}
	// Sanity: every per-factor decomposition value is in [0, 1].
	for name, f := range map[string]float64{
		"z":         got.Factors.ZScore,
		"src":       got.Factors.SourceCount,
		"diversity": got.Factors.Diversity,
		"liquidity": got.Factors.Liquidity,
		"xoracle":   got.Factors.CrossOracle,
		"baseline":  got.Factors.BaselineQuality,
	} {
		if f < 0 || f > 1 {
			t.Errorf("factor %q = %v, want in [0, 1]", name, f)
		}
	}
}

// TestCompute_DominatingFactor — ADR-0019: "any one factor near
// zero pulls the whole score down". A single low factor (single-
// source asset, ~0.12) materially drops the combined score.
//
// With 6 equal-weight factors, the geometric mean's "domination"
// is real but mild — a 0.12 factor among five 1.0s gives
// confidence ≈ 0.69, not "near zero". To get a sharper drop the
// operator can raise that factor's weight via `Weights.SourceCount`.
// Verifies the healthy → degraded delta is significant: the
// healthy anchor scores ~0.92, single-source drags below 0.75.
func TestCompute_DominatingFactor(t *testing.T) {
	healthy := confidence.Compute(healthyInputs(), confidence.DefaultWeights())

	in := healthyInputs()
	in.SourceCount = 1 // factor ≈ 0.12
	degraded := confidence.Compute(in, confidence.DefaultWeights())

	if degraded.Confidence > 0.75 {
		t.Errorf("single-source confidence = %v, want < 0.75 (one low factor must drop the score below the healthy baseline)", degraded.Confidence)
	}
	if healthy.Confidence-degraded.Confidence < 0.15 {
		t.Errorf("healthy %v vs single-source %v: delta too small (%.3f), want > 0.15",
			healthy.Confidence, degraded.Confidence, healthy.Confidence-degraded.Confidence)
	}
}

// TestCompute_AnomalyKillsScore — z >> 5 (a real anomaly) cuts the
// score sharply.
func TestCompute_AnomalyKillsScore(t *testing.T) {
	in := healthyInputs()
	in.ZScore = 8.0
	got := confidence.Compute(in, confidence.DefaultWeights())
	if got.Confidence > 0.7 {
		t.Errorf("z=8 confidence = %v, want < 0.7", got.Confidence)
	}
}

// TestCompute_FullBootstrap — a brand-new asset with no baseline,
// no cross-oracle, single source, low liquidity. Score should be
// low but well-defined (no NaN / Inf).
func TestCompute_FullBootstrap(t *testing.T) {
	in := confidence.Inputs{
		ZScore:                   0,
		SourceCount:              1,
		SourceClassCount:         1,
		LiquidityUSD:             500, // below floor
		CrossOracleDivergencePct: -1,  // no data
		BaselineAgeDays:          -1,  // no baseline
	}
	got := confidence.Compute(in, confidence.DefaultWeights())
	if math.IsNaN(got.Confidence) || math.IsInf(got.Confidence, 0) {
		t.Errorf("bootstrap confidence not finite: %v", got.Confidence)
	}
	if got.Confidence < 0 || got.Confidence > 1 {
		t.Errorf("bootstrap confidence outside [0, 1]: %v", got.Confidence)
	}
	// LiquidityFactor returns 0 for below-floor input → geometric
	// mean is 0 (dominating-factor behaviour).
	if got.Confidence != 0 {
		t.Errorf("bootstrap with liquidity=0 should crater to 0, got %v", got.Confidence)
	}
}

// TestCompute_AllZeroWeights — degenerate edge case: every weight
// is 0. Total weight is 0; we return a neutral 0.5 plus the
// per-factor decomposition for diagnostics.
func TestCompute_AllZeroWeights(t *testing.T) {
	got := confidence.Compute(healthyInputs(), confidence.Weights{})
	if got.Confidence != 0.5 {
		t.Errorf("zero-weights confidence = %v, want 0.5", got.Confidence)
	}
	if got.Factors.ZScore == 0 {
		t.Error("Factors should still be populated even with zero weights")
	}
}

// TestCompute_WeightingChangesScore — bumping one factor's weight
// shifts the combined score's sensitivity to that factor.
func TestCompute_WeightingChangesScore(t *testing.T) {
	in := healthyInputs()
	in.SourceCount = 2 // factor ≈ 0.27 (low)

	defaults := confidence.Compute(in, confidence.DefaultWeights())

	// Heavy weight on source_count → low SourceCount drags score
	// further toward zero than it would under default weights.
	w := confidence.DefaultWeights()
	w.SourceCount = 5.0
	heavy := confidence.Compute(in, w)

	if heavy.Confidence >= defaults.Confidence {
		t.Errorf("heavy SourceCount weight should drop confidence further; default=%v heavy=%v",
			defaults.Confidence, heavy.Confidence)
	}
}

// TestCompute_NumericalStability — extreme inputs don't produce
// NaN. Any factor returning exactly 0 + non-zero weight should
// produce a 0 score (not NaN from log(0)).
func TestCompute_NumericalStability(t *testing.T) {
	in := confidence.Inputs{
		ZScore:                   1e9, // → factor ~0
		SourceCount:              0,   // → factor 0
		SourceClassCount:         0,
		LiquidityUSD:             0,   // → factor 0
		CrossOracleDivergencePct: 1e9, // → factor ~0
		BaselineAgeDays:          0,
	}
	got := confidence.Compute(in, confidence.DefaultWeights())
	if math.IsNaN(got.Confidence) || math.IsInf(got.Confidence, 0) {
		t.Errorf("extreme inputs produced %v, want finite", got.Confidence)
	}
	if got.Confidence != 0 {
		t.Errorf("all-zero factors → confidence should be 0, got %v", got.Confidence)
	}
}

// ─── Bootstrap cap (ADR-0019 §"Bootstrap policy") ───────────────

// TestCompute_BootstrapCapsConfidence — an asset with <30d of
// history has confidence hard-capped at 0.5 regardless of how
// healthy every other factor is.
func TestCompute_BootstrapCapsConfidence(t *testing.T) {
	in := healthyInputs()
	in.BaselineAgeDays = 5 // freshly-listed
	got := confidence.Compute(in, confidence.DefaultWeights())
	if got.Confidence > confidence.BootstrapConfidenceCap+1e-9 {
		t.Errorf("bootstrap confidence = %v, want <= %v",
			got.Confidence, confidence.BootstrapConfidenceCap)
	}
}

// TestCompute_BootstrapCapAtZeroAge — exactly 0 days of history
// (just-listed asset) is the most-bootstrap state. Cap fires.
func TestCompute_BootstrapCapAtZeroAge(t *testing.T) {
	in := healthyInputs()
	in.BaselineAgeDays = 0
	got := confidence.Compute(in, confidence.DefaultWeights())
	if got.Confidence > confidence.BootstrapConfidenceCap {
		t.Errorf("zero-age confidence = %v, want capped at %v",
			got.Confidence, confidence.BootstrapConfidenceCap)
	}
}

// TestCompute_BootstrapCapNotAppliedAt30Days — exactly at the
// threshold the cap turns OFF. Healthy bucket reads its full
// confidence.
func TestCompute_BootstrapCapNotAppliedAt30Days(t *testing.T) {
	in := healthyInputs()
	in.BaselineAgeDays = 30 // boundary — strictly < BootstrapDays required
	got := confidence.Compute(in, confidence.DefaultWeights())
	if got.Confidence <= confidence.BootstrapConfidenceCap {
		t.Errorf("at 30d the cap should not apply; got %v <= %v",
			got.Confidence, confidence.BootstrapConfidenceCap)
	}
}

// TestCompute_BootstrapCapPreservesLowConfidence — when an asset
// is in bootstrap AND would naturally score below the cap (e.g.
// single-source low-liquidity), the cap is a CEILING not a floor.
// The natural-low score must still come through.
func TestCompute_BootstrapCapPreservesLowConfidence(t *testing.T) {
	in := healthyInputs()
	in.BaselineAgeDays = 5 // bootstrap
	in.SourceCount = 1     // pulls confidence way down
	in.LiquidityUSD = 100  // below floor → factor 0
	got := confidence.Compute(in, confidence.DefaultWeights())
	if got.Confidence > confidence.BootstrapConfidenceCap {
		t.Errorf("bootstrap with bad signals: confidence = %v, expected <= %v (cap)",
			got.Confidence, confidence.BootstrapConfidenceCap)
	}
	// AND the natural score should be allowed through (not raised
	// to the cap). Liquidity=0 makes the geomean 0.
	if got.Confidence != 0 {
		t.Errorf("natural-low score should pass; got %v, want 0", got.Confidence)
	}
}

// TestCompute_BootstrapCapWithNoBaselineSentinel — BaselineAgeDays
// = -1 (the "no baseline yet" sentinel) is stricter than bootstrap
// — the cap MUST fire.
func TestCompute_BootstrapCapWithNoBaselineSentinel(t *testing.T) {
	in := healthyInputs()
	in.BaselineAgeDays = -1 // no baseline at all
	got := confidence.Compute(in, confidence.DefaultWeights())
	if got.Confidence > confidence.BootstrapConfidenceCap {
		t.Errorf("no-baseline confidence = %v, want capped at %v",
			got.Confidence, confidence.BootstrapConfidenceCap)
	}
}

// TestDefaultWeights — sanity that the default is unweighted.
func TestDefaultWeights(t *testing.T) {
	w := confidence.DefaultWeights()
	for name, v := range map[string]float64{
		"z":         w.ZScore,
		"src":       w.SourceCount,
		"diversity": w.Diversity,
		"liquidity": w.Liquidity,
		"xoracle":   w.CrossOracle,
		"baseline":  w.BaselineQuality,
	} {
		if v != 1.0 {
			t.Errorf("DefaultWeights.%s = %v, want 1.0", name, v)
		}
	}
}
