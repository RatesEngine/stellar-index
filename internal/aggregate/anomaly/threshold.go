package anomaly

import (
	"fmt"
	"math/big"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// Thresholds is the per-class threshold pair for Phase-1 anomaly
// detection.
//
//   - WarnPct: deviation above this triggers [ActionWarn] (publish
//     with `flags.divergence_warning: true`).
//   - FreezePct: deviation above this AND `source_count <= 1`
//     triggers [ActionFreeze] (do not publish; serve LKG).
//
// Both are absolute percentages (e.g. `1.0` means 1 %). Must be > 0
// and FreezePct > WarnPct.
type Thresholds struct {
	WarnPct   float64
	FreezePct float64
}

// Validate checks that the thresholds are well-formed.
func (t Thresholds) Validate() error {
	if t.WarnPct <= 0 {
		return fmt.Errorf("anomaly: WarnPct must be > 0, got %g", t.WarnPct)
	}
	if t.FreezePct <= 0 {
		return fmt.Errorf("anomaly: FreezePct must be > 0, got %g", t.FreezePct)
	}
	if t.FreezePct <= t.WarnPct {
		return fmt.Errorf("anomaly: FreezePct (%g) must be > WarnPct (%g)",
			t.FreezePct, t.WarnPct)
	}
	return nil
}

// DefaultThresholds is the recommended Phase-1 threshold table. Used
// when an operator config omits explicit thresholds for a class.
//
// Numbers reflect the per-class baselines from [ADR-0019]:
//
//   - Stablecoin / Treasury: 1 % warn / 3 % freeze
//   - Crypto: 20 % warn / 50 % freeze
//   - Governance: 50 % warn / 100 % freeze
//   - Default: 30 % warn / 75 % freeze
func DefaultThresholds() map[AssetClass]Thresholds {
	return map[AssetClass]Thresholds{
		ClassStablecoin: {WarnPct: 1.0, FreezePct: 3.0},
		ClassTreasury:   {WarnPct: 1.0, FreezePct: 3.0},
		ClassCrypto:     {WarnPct: 20.0, FreezePct: 50.0},
		ClassGovernance: {WarnPct: 50.0, FreezePct: 100.0},
		ClassDefault:    {WarnPct: 30.0, FreezePct: 75.0},
	}
}

// Checker evaluates whether a new bucket's VWAP is anomalous given
// the prior bucket's VWAP and the source count for the new bucket.
//
// Checker is safe for concurrent use after construction. Internal
// maps are not mutated.
type Checker struct {
	thresholds map[AssetClass]Thresholds
	classifier *Classifier
}

// NewChecker constructs a Checker. thresholds is the per-class
// threshold table; missing entries fall through to [ClassDefault]'s
// row. classifier maps assets to classes.
//
// Returns an error if any threshold entry fails [Thresholds.Validate].
func NewChecker(thresholds map[AssetClass]Thresholds, classifier *Classifier) (*Checker, error) {
	if classifier == nil {
		return nil, fmt.Errorf("anomaly: classifier is required")
	}
	if _, ok := thresholds[ClassDefault]; !ok {
		return nil, fmt.Errorf("anomaly: thresholds map must include ClassDefault (the fallback)")
	}
	for cls, t := range thresholds {
		if err := t.Validate(); err != nil {
			return nil, fmt.Errorf("anomaly: thresholds[%s]: %w", cls, err)
		}
	}
	cp := make(map[AssetClass]Thresholds, len(thresholds))
	for k, v := range thresholds {
		cp[k] = v
	}
	return &Checker{thresholds: cp, classifier: classifier}, nil
}

// ClassOf returns the asset's class. Pass-through to the wrapped
// [Classifier] — exposed so the orchestrator's Phase 2 freeze
// path can label its metrics with the same per-class breakdown
// that Phase 1 emits.
func (c *Checker) ClassOf(asset canonical.Asset) AssetClass {
	return c.classifier.ClassOf(asset)
}

// Observation is the input to [Checker.Evaluate]. The aggregator
// fills this in for each bucket-close before publishing.
type Observation struct {
	// Pair is the asset pair being evaluated. The Checker uses
	// Pair.Base.String() to look up the asset's class.
	Pair canonical.Pair

	// PrevVWAP is the previous closed bucket's VWAP. Nil means
	// "no prior bucket" — first observation for this pair, or
	// after a long gap. Treated as ActionAllow (we have nothing
	// to compare against).
	PrevVWAP *big.Rat

	// CurrVWAP is the new bucket's VWAP about to be published.
	// Nil is invalid — the caller must compute SOMETHING before
	// asking whether to publish it.
	CurrVWAP *big.Rat

	// SourceCount is how many distinct sources contributed to
	// CurrVWAP. The Phase-1 freeze condition fires only when
	// SourceCount <= 1 (single-source signature of manipulation).
	SourceCount int
}

// thresholdsFor returns the threshold row for the asset's class,
// falling back to ClassDefault if the class isn't in the table.
func (c *Checker) thresholdsFor(class AssetClass) Thresholds {
	if t, ok := c.thresholds[class]; ok {
		return t
	}
	return c.thresholds[ClassDefault]
}

// Evaluate returns a [Decision] for the supplied observation.
//
// Algorithm (Phase-1, ADR-0019):
//
//  1. If obs.PrevVWAP is nil → ActionAllow (nothing to compare).
//  2. Compute deviation_pct = |curr - prev| / prev * 100.
//  3. Look up thresholds for the asset's class.
//  4. Decision rules:
//     - deviation < WarnPct                           → ActionAllow
//     - WarnPct <= deviation < FreezePct              → ActionWarn
//     - deviation >= FreezePct AND source_count <= 1  → ActionFreeze
//     - deviation >= FreezePct AND source_count >  1  → ActionWarn
//
// The asymmetry in the last two rules is deliberate: a large
// deviation with multi-source corroboration is a real market move
// (not a freeze candidate); the same deviation with a single source
// is the manipulation signature.
func (c *Checker) Evaluate(obs Observation) Decision {
	class := c.classifier.ClassOf(obs.Pair.Base)
	thresholds := c.thresholdsFor(class)

	if obs.PrevVWAP == nil {
		return Decision{
			Action:       ActionAllow,
			Class:        class,
			Thresholds:   thresholds,
			DeviationPct: 0,
			Reason:       "no prior bucket — first observation for pair",
		}
	}
	if obs.CurrVWAP == nil {
		// Caller bug — the aggregator should never publish a nil
		// VWAP. Fail-safe to ActionFreeze so the upstream code
		// notices.
		return Decision{
			Action:       ActionFreeze,
			Class:        class,
			Thresholds:   thresholds,
			DeviationPct: 0,
			Reason:       "nil CurrVWAP — caller bug",
		}
	}

	deviation := computeDeviationPct(obs.PrevVWAP, obs.CurrVWAP)

	switch {
	case deviation < thresholds.WarnPct:
		return Decision{
			Action:       ActionAllow,
			Class:        class,
			Thresholds:   thresholds,
			DeviationPct: deviation,
			Reason:       "deviation within normal range",
		}
	case deviation < thresholds.FreezePct:
		return Decision{
			Action:       ActionWarn,
			Class:        class,
			Thresholds:   thresholds,
			DeviationPct: deviation,
			Reason: fmt.Sprintf("deviation %.2f%% above warn threshold %.2f%% for class %s",
				deviation, thresholds.WarnPct, class),
		}
	case obs.SourceCount > 1:
		return Decision{
			Action:       ActionWarn,
			Class:        class,
			Thresholds:   thresholds,
			DeviationPct: deviation,
			Reason: fmt.Sprintf("deviation %.2f%% above freeze threshold %.2f%% but %d sources corroborate (real market move)",
				deviation, thresholds.FreezePct, obs.SourceCount),
		}
	default:
		return Decision{
			Action:       ActionFreeze,
			Class:        class,
			Thresholds:   thresholds,
			DeviationPct: deviation,
			Reason: fmt.Sprintf("deviation %.2f%% above freeze threshold %.2f%% on single source — possible manipulation",
				deviation, thresholds.FreezePct),
		}
	}
}

// computeDeviationPct returns abs(curr - prev) / prev * 100. Both
// inputs must be non-nil and prev must be non-zero (caller's
// responsibility — Evaluate guards this).
func computeDeviationPct(prev, curr *big.Rat) float64 {
	if prev.Sign() == 0 {
		// A zero prev VWAP shouldn't happen in practice (CAGGs
		// don't materialise empty buckets), but guard anyway:
		// any non-zero curr is "infinite" deviation. Caller will
		// see an actionable Reason in the returned Decision via
		// Evaluate's wrapper.
		if curr.Sign() == 0 {
			return 0
		}
		return 1e9
	}
	delta := new(big.Rat).Sub(curr, prev)
	delta.Abs(delta)
	delta.Quo(delta, prev)
	delta.Mul(delta, big.NewRat(100, 1))
	f, _ := delta.Float64()
	return f
}
