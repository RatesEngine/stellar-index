// Package anomaly implements the Phase-1 component of [ADR-0019] —
// per-asset-class threshold-based anomaly detection. It runs alongside
// Phase 2 (per-asset MAD baselines + multi-factor confidence) rather
// than being superseded by it: both layers vote into the orchestrator's
// freeze decision via the [Phase2FreezeConfig] AND-of-three-signals
// rule.
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
// Phase 2, shipped, lives at [internal/aggregate/baseline]
// (per-asset MAD baselines + z-score) and
// [internal/aggregate/confidence] (six-factor weighted-geomean
// confidence). The aggregator orchestrator wires both — Phase 1 here
// gates "is this movement large for this asset class" while Phase 2
// gates "is this movement statistically anomalous AND under-confident
// AND under-corroborated". Both must agree before the orchestrator
// flips ActionFreeze.
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
// Per-asset behaviour layers on top via Phase 2's
// [internal/aggregate/baseline] (volatility profile observed from
// the `volatility_baseline_1m` CAGG); the per-class table here
// remains operator-curated as a coarse safety net for assets
// without enough trades to build a baseline.
//
// # Why this lives separate from internal/aggregate
//
// The aggregate package computes VWAP/TWAP from raw trade slices;
// it doesn't know about wire policy. The anomaly package consumes
// that output and decides whether to publish it. Keeping them
// separate lets Phase 1's class thresholds and Phase 2's
// per-asset baselines + confidence ([internal/aggregate/baseline],
// [internal/aggregate/confidence]) layer cleanly on top of an
// untouched math layer.
//
// [ADR-0019]: ../../docs/adr/0019-anomaly-response-and-confidence-scoring.md
package anomaly
