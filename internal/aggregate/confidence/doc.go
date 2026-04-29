// Package confidence implements the multi-factor confidence score
// per ADR-0019 §"Multi-factor confidence score".
//
// Each price published by the aggregator carries a `confidence ∈ [0, 1]`
// value computed by combining six independent factors via weighted
// geometric mean:
//
//   - [ZScoreFactor]: how anomalous the bucket's return is vs the
//     per-asset statistical baseline (ADR-0019 Phase 2; see
//     [internal/aggregate/baseline]).
//   - [SourceCountFactor]: how many distinct sources contributed.
//   - [DiversityFactor]: did multiple source CLASSES contribute (CEX
//     vs DEX vs oracle) — orthogonal to count.
//   - [LiquidityFactor]: USD volume in the bucket.
//   - [CrossOracleFactor]: agreement with external reference oracles
//     (Reflector, Chainlink, etc.).
//   - [BaselineQualityFactor]: how mature the per-asset baseline is.
//
// Weighted geometric mean (`prod(factor_i ^ weight_i)`) is the right
// shape because it gives DOMINATING-FACTOR behaviour: any one
// near-zero factor pulls the whole score toward zero. That matches
// the operational intent — a single missing signal is enough to
// down-rank the score.
//
// Each factor returns a value strictly in [0, 1]; this package
// guards inputs against NaN/Inf and clamps outputs at the bounds so
// the geometric mean never produces NaN.
//
// The wire response carries `confidence` plus its raw [Factors]
// decomposition so customers and operators can see WHY confidence
// dropped (per the ADR's worked example). [Factors] is the
// debug-friendly view; [Score] is the combined float.
package confidence
