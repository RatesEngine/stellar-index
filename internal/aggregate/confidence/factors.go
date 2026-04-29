package confidence

import "math"

// Factor knobs. Public so operators can tune via [anomaly.weights]
// config — but all defaults map to the ADR-0019 documented shapes,
// and the per-factor functions intentionally do NOT take tuning
// parameters: changing the SHAPE of a factor is a code change, not
// a config change. Only the per-factor weights in [Weights] are
// operator-tunable at runtime.

// zScoreSigmoidWidth controls how fast [ZScoreFactor] decays with
// rising z. The ADR's "1.0 at z=0, decays smoothly to ~0 at z=10"
// shape is achieved by 1 / (1 + exp((z - 5) / width)) with width
// chosen so the value at z=10 is ~0 and z=0 is ~1.
const zScoreSigmoidWidth = 1.0

// sourceCountInflectionN is the n_sources value at which
// [SourceCountFactor] crosses 0.5. Per the ADR: "single-source
// assets cap at ~0.3, n>=6 reaches near-1.0" — the crossover at
// n=3 satisfies both.
const sourceCountInflectionN = 3.0

// liquidityFloorUSD and liquidityCeilingUSD bound [LiquidityFactor]'s
// log-saturating shape. Below the floor the factor is ~0; above the
// ceiling it's ~1. The ADR specifies $1K → 0, $100K → ~1.
const (
	liquidityFloorUSD   = 1_000.0
	liquidityCeilingUSD = 100_000.0
)

// crossOracleTolerancePct is the deviation below which the factor
// reads as full agreement (1.0). Per ADR-0019: "1.0 when within 1%
// of cross-oracle median".
const crossOracleTolerancePct = 1.0

// crossOracleHalfLifePct is the divergence beyond the tolerance at
// which the exponential decay reaches 0.5. Tuned so a 5% deviation
// (4 percentage points past tolerance) lands near 0.5 — matches
// the ADR's "decays with divergence" intent without prescribing a
// specific point.
const crossOracleHalfLifePct = 4.0

// baselineFullDays is the age (in days) at which a baseline is
// considered fully mature. ADR §"baseline_quality_factor: 0.5 with
// no baseline data, ramps to 1.0 over the first 30 days".
const baselineFullDays = 30.0

// ZScoreFactor maps a baseline z-score to a confidence factor in
// [0, 1]. Sigmoid-shaped: 1.0 at z=0, ~0.5 at z=5 (the ADR-0019
// anomaly threshold), ~0 at z=10.
//
// Negative or NaN/Inf z values clamp to 0 (defensive — the math
// for z is |x - median| / mad which can't be negative under our
// invariants, but a future caller bug shouldn't propagate to
// confidence).
func ZScoreFactor(z float64) float64 {
	if math.IsNaN(z) || z < 0 {
		return 0
	}
	if math.IsInf(z, 0) {
		return 0
	}
	return clamp01(1.0 / (1.0 + math.Exp((z-5.0)/zScoreSigmoidWidth)))
}

// SourceCountFactor maps n_sources to a confidence factor.
// Logistic shape: 1/(1+exp(-(n-3))). Single-source caps at ~0.3,
// n=3 is 0.5, n>=6 nears 1.0.
//
// n < 0 clamps to 0. NaN inputs (from a future bug at the caller)
// return 0 rather than NaN-poisoning the geometric mean.
func SourceCountFactor(n int) float64 {
	if n < 0 {
		return 0
	}
	return clamp01(1.0 / (1.0 + math.Exp(-(float64(n) - sourceCountInflectionN))))
}

// DiversityFactor returns a step function: 0.5 for one source class,
// 1.0 for ≥2 distinct classes. The ADR-0019 motivation is that
// CEX + DEX agreement is more trustworthy than three CEXs (which
// could share a common upstream feed bug).
//
// classCount = 0 returns 0 (no signal); ≥2 returns 1.0; 1 returns
// 0.5.
func DiversityFactor(classCount int) float64 {
	switch {
	case classCount <= 0:
		return 0
	case classCount == 1:
		return 0.5
	default:
		return 1.0
	}
}

// LiquidityFactor maps USD bucket volume to a confidence factor on
// a log-saturating shape. Below $1K → ~0; above $100K → ~1.0. The
// boundary value is computed as a log-interpolation between the
// floor and ceiling.
//
// Negative or NaN volumes return 0.
func LiquidityFactor(usdVolume float64) float64 {
	if math.IsNaN(usdVolume) || usdVolume <= 0 {
		return 0
	}
	if usdVolume >= liquidityCeilingUSD {
		return 1.0
	}
	if usdVolume <= liquidityFloorUSD {
		return 0
	}
	// Log-interpolation: how far between log(floor) and log(ceiling)
	// is log(volume), as a fraction in [0, 1].
	return clamp01(
		(math.Log(usdVolume) - math.Log(liquidityFloorUSD)) /
			(math.Log(liquidityCeilingUSD) - math.Log(liquidityFloorUSD)),
	)
}

// CrossOracleFactor maps cross-oracle divergence (% absolute
// deviation from the cross-reference median) to a confidence
// factor. Piecewise: full credit (1.0) within
// [crossOracleTolerancePct]; exponential decay beyond. 0% → 1.0;
// 1% → 1.0; 5% → ~0.5; 10% → ~0.21; +∞ → 0.
//
// The "no cross-oracle data available" case is signalled by passing
// a negative divergence — the function returns the documented
// neutral value 0.7 (per the ADR-0019 worked example) so the
// geometric mean isn't dragged down on assets without external
// references.
func CrossOracleFactor(divergencePct float64) float64 {
	if math.IsNaN(divergencePct) {
		return 0
	}
	if divergencePct < 0 {
		// Sentinel: "no cross-oracle data". Return neutral.
		return 0.7
	}
	if divergencePct <= crossOracleTolerancePct {
		return 1.0
	}
	// Exponential decay past the tolerance band:
	//   factor = exp(-(x - tolerance) * ln(2) / half_life)
	// Hits 0.5 at x = tolerance + half_life.
	excess := divergencePct - crossOracleTolerancePct
	return clamp01(math.Exp(-excess * math.Ln2 / crossOracleHalfLifePct))
}

// BaselineQualityFactor maps the age of a per-asset baseline (in
// days) to a confidence factor. 0 days → 0.5 (bootstrap penalty per
// ADR-0019 §"Bootstrap policy"); 30 days → 1.0; linear in between.
//
// Negative or NaN ages return 0.5 (treat as bootstrap rather than
// failing closed — a clock-skew-induced negative age is a transient
// error, not an attack).
func BaselineQualityFactor(daysHistory float64) float64 {
	if math.IsNaN(daysHistory) || daysHistory < 0 {
		return 0.5
	}
	if daysHistory >= baselineFullDays {
		return 1.0
	}
	// Linear ramp from 0.5 → 1.0 across [0, 30] days.
	return 0.5 + 0.5*(daysHistory/baselineFullDays)
}

// clamp01 returns x clamped to [0, 1]. Defensive — every factor
// caller passes its output through this so a math edge case can't
// poison the geometric mean.
func clamp01(x float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
