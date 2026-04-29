// Package baseline provides robust statistical primitives for the
// per-asset volatility baselines defined in ADR-0019 Phase 2:
//
//   - [Median] / [MAD] over a slice of returns
//   - [Baseline] — combined robust-stats result with an attached
//     [Baseline.ZScore] method for "how anomalous is this new
//     return"
//   - [ReturnsFromVWAPs] — convert a sequence of bucket VWAPs into
//     bucket-to-bucket percent changes (the input the baseline
//     summarises)
//
// The math is intentionally split off from the storage layer (the
// `volatility_baseline_1m` CAGG that feeds it) and from the
// orchestrator integration. This keeps the math pure-Go and easy
// to fuzz without dragging in DB fixtures.
//
// Why MAD instead of standard deviation: per ADR-0019, σ is itself
// sensitive to outliers — a single attack in the training window
// inflates σ and hides the next attack. MAD (median absolute
// deviation) is computed from medians and is robust against
// outliers in the training data, which is exactly the failure mode
// our anomaly detector needs to survive. Mature oracles (Pyth,
// MakerDAO OSM) make the same substitution for the same reason.
//
// Output of MAD is scaled by 1.4826 so it is σ-equivalent for
// normally-distributed data — the consistency factor that makes
// "5σ is anomalous" match clinicians' / quants' intuition without
// requiring a separate scale conversion at every call site.
package baseline
