// Package anomaly implements the Phase-1 stop-gap from
// [ADR-0019] — per-asset-class threshold-based anomaly detection.
//
// # Scope
//
// Phase 1 is an operator-set TOML threshold table per asset class.
// It is deliberately crude:
//
//   - One pair of (warn_pct, freeze_pct) thresholds per asset class
//   - Decision is a function of (asset class, prev VWAP, curr VWAP, source count)
//   - No statistical baseline; no z-score; no confidence score
//
// Phase 2 (planned per ADR-0019 §Phase 2) replaces this with
// per-asset MAD-based statistical baselines + multi-factor confidence
// scoring. Phase 1 is the safety-net we ship before Phase 2 lands so
// the API has SOME anomaly protection during the gap.
//
// # The decision algorithm
//
// For each closed bucket the aggregator computes a VWAP. Before
// publishing, it calls [Checker.Evaluate] with:
//
//   - the asset's previous closed-bucket VWAP
//   - the new VWAP it's about to publish
//   - how many sources contributed
//
// Evaluate returns a [Decision] with one of three actions:
//
//   - [ActionAllow]  — publish normally
//   - [ActionWarn]   — publish with `flags.divergence_warning: true`
//   - [ActionFreeze] — DO NOT publish; serve the previous bucket's
//     LKG with `flags.frozen: true` (caller's
//     responsibility to maintain the LKG slot)
//
// The Phase-1 freeze condition is the AND of two signals:
//
//	deviation_pct >= thresholds[class].freeze_pct
//	source_count <= 1
//
// Both must trip. A 100x movement on a multi-source asset (real
// flash crash, news event) gets WARN, not FREEZE — because
// multi-source agreement provides its own corroboration.
//
// # Asset classification
//
// Operator config maps each asset to a class via
// `[anomaly_detection.classifications]`. Anything not explicitly
// classified falls through to [ClassDefault] with conservative
// thresholds.
//
// Phase 2 will auto-classify based on observed volatility profile;
// Phase 1 is operator-curated.
//
// # Why this lives separate from internal/aggregate
//
// The aggregate package computes VWAP/TWAP from raw trade slices;
// it doesn't know about wire policy. The anomaly package consumes
// that output and decides whether to publish it. Keeping them
// separate lets Phase 1 ship without changing the math layer, and
// later lets Phase 2's confidence scoring layer cleanly on top.
//
// [ADR-0019]: ../../docs/adr/0019-anomaly-response-and-confidence-scoring.md
package anomaly
