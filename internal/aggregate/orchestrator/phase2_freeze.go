package orchestrator

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate/anomaly"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
)

// Phase 2 freeze thresholds per ADR-0019 §"Freeze policy". All
// three signals must agree to fire — keeps the freeze decision
// robust against legitimate market events (those have multi-source
// corroboration even if z is high) while still catching the
// USTRY-shape attack pattern (single source, large deviation,
// confidence-killing combination of factors).
//
// The package-level defaults match the ADR-0019 documented values
// and are used when [Config.Phase2Thresholds] is the zero value
// (operator hasn't overridden via TOML). Operators tune via the
// `[anomaly.phase2]` config block.
const (
	DefaultPhase2ConfidenceMaxFreeze  = 0.10 // freeze when confidence < this
	DefaultPhase2ZScoreMinFreeze      = 5.0  // freeze when z > this
	DefaultPhase2SourceCountMaxFreeze = 1    // freeze when source_count <= this
)

// Phase2Thresholds is the operator-tunable shape of the Phase 2
// freeze condition. The orchestrator's [Config.Phase2Thresholds]
// holds an instance of this struct; zero values fall back to the
// `Default*` package constants so unset fields do the right thing.
type Phase2Thresholds struct {
	ConfidenceMaxFreeze  float64
	ZScoreMinFreeze      float64
	SourceCountMaxFreeze int
}

// withDefaults returns a Phase2Thresholds with any zero-valued
// field replaced by the package default. Lets the orchestrator
// merge an operator's partial override over the documented
// stop-gap without forcing the operator to repeat every value.
//
// The zero value sentinel: ConfidenceMaxFreeze == 0 means "use
// default" because a literal 0.0 would mean "never freeze on
// confidence" (any value < 0 is impossible since confidence is
// clamped [0, 1]) — equivalent to disabling that condition. We
// treat the zero value as "use default"; operators who genuinely
// want to disable the confidence check set a value >= 1.0 (always
// fires the confidence sub-condition; freeze still requires z +
// source_count to also pass).
func (p Phase2Thresholds) withDefaults() Phase2Thresholds {
	if p.ConfidenceMaxFreeze == 0 {
		p.ConfidenceMaxFreeze = DefaultPhase2ConfidenceMaxFreeze
	}
	if p.ZScoreMinFreeze == 0 {
		p.ZScoreMinFreeze = DefaultPhase2ZScoreMinFreeze
	}
	// SourceCountMaxFreeze: zero is a valid threshold ("freeze only
	// when zero contributing sources") — we can't use the zero
	// sentinel here. Operators who want to override this MUST set a
	// non-zero value; the documented default fires below.
	if p.SourceCountMaxFreeze == 0 {
		p.SourceCountMaxFreeze = DefaultPhase2SourceCountMaxFreeze
	}
	return p
}

// phase2FreezeFires reports whether the Phase 2 freeze condition
// (3-signal AND) holds for a bucket given its confidence score,
// raw z-score, and contributing source count, evaluated against
// the supplied (or default-merged) thresholds.
//
// Per ADR-0019 §"Freeze policy":
//
//	freeze_condition = (
//	  confidence  < ConfidenceMaxFreeze
//	  AND z_score > ZScoreMinFreeze
//	  AND source_count <= SourceCountMaxFreeze
//	)
//
// All three must be true. A two-of-three pattern (e.g. anomalous
// z + low confidence but multi-source) does NOT freeze — those
// scenarios surface via flags.divergence_warning instead, set by
// the API's read-side divergence check.
func phase2FreezeFires(c confidenceWithSourceCount, t Phase2Thresholds) bool {
	t = t.withDefaults()
	return c.Confidence < t.ConfidenceMaxFreeze &&
		c.ZScore > t.ZScoreMinFreeze &&
		c.SourceCount <= t.SourceCountMaxFreeze
}

// confidenceWithSourceCount is the Phase 2 input bundle. Pulled
// out of [confidenceComputation] so [phase2FreezeFires] is a pure
// function on three floats — easy to unit-test exhaustively.
type confidenceWithSourceCount struct {
	Confidence  float64
	ZScore      float64
	SourceCount int
}

// markPhase2Freeze records a Phase 2 freeze decision via the
// configured FreezeWriter (when wired) and emits the orchestrator's
// freeze-engaged Prometheus counter. Reuses the [anomaly.Decision]
// shape so downstream readers (the API's freeze-flag lookup) don't
// need to distinguish Phase 1 vs Phase 2 — both look identical on
// the wire.
//
// The [anomaly.Decision] carries Reason="phase2:3_signal_AND" so
// log lines + Redis marker JSON make the source legible without
// adding a new wire field.
func (o *Orchestrator) markPhase2Freeze(
	ctx context.Context,
	pair canonical.Pair,
	window time.Duration,
	c confidenceWithSourceCount,
	prevVWAP *big.Rat,
) {
	o.mu.Lock()
	o.freezesEngaged++
	o.mu.Unlock()

	// Class label uses the same Phase 1 checker's classifier when
	// it's wired (so the per-class metric stays consistent across
	// both phases). When Phase 1 isn't configured, default class.
	class := anomaly.ClassDefault
	if o.cfg.Anomaly != nil {
		class = o.cfg.Anomaly.ClassOf(pair.Base)
	}
	obs.AnomalyFreezeEngagedTotal.WithLabelValues(string(class)).Inc()

	// Visibility for the operator: until 2026-05-13 Phase 2 freeze
	// decisions only manifested as a Prometheus counter +
	// (optional) Redis marker. Phase 1 logs the same event at
	// WARN level (orchestrator.go:851). The Phase 2 path stayed
	// silent for ergonomic reasons (3-signal AND can fire many
	// times in a tick) but that hid an actionable signal — when
	// baselines aren't mature (insufficient historical samples)
	// Phase 2 false-fires across many pairs and the operator's
	// only signal is the alert summary "freeze sustained" with no
	// way to triage which pairs / how confidently. This INFO line
	// gives the alert runbook something to grep.
	o.logger.Info("phase2 freeze engaged",
		"pair", pair.String(),
		"class", class,
		"confidence", c.Confidence,
		"zscore", c.ZScore,
		"source_count", c.SourceCount,
		"writer_wired", o.cfg.FreezeWriter != nil,
	)

	// F-1345 (G13-03): a Phase 2 freeze also skips the VWAP cache
	// write, so the prior bucket's value must be kept alive for as
	// long as the freeze marker. Refresh its TTL regardless of whether
	// a FreezeWriter is wired — the LKG keeps serving either way.
	o.keepFrozenVWAPAlive(ctx, pair, window)

	if o.cfg.FreezeWriter == nil {
		return
	}
	decision := anomaly.Decision{
		Action: anomaly.ActionFreeze,
		Class:  class,
		// Phase 2 doesn't compute a raw class deviation — the per-
		// asset baseline replaces the per-class threshold. Keep
		// DeviationPct zero; the Reason field carries the source.
		Reason: fmt.Sprintf("phase2:3_signal_AND confidence=%.3f z=%.2f sources=%d",
			c.Confidence, c.ZScore, c.SourceCount),
	}
	// LKG VWAP we're freezing on: the prior bucket's value (which
	// stays in cache because the Phase 2 caller skips the cache
	// write below). Empty string when no prior bucket exists.
	var frozenValue string
	if prevVWAP != nil {
		frozenValue = formatRatFixed(prevVWAP, 12)
	}
	if err := o.cfg.FreezeWriter.Mark(ctx, pair.Base, pair.Quote, frozenValue, decision); err != nil {
		o.logger.Warn("phase2 freeze marker write failed",
			"pair", pair.String(), "err", err)
	}
}
