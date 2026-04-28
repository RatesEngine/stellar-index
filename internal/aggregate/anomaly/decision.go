package anomaly

// Action is the recommended publishing behaviour for a bucket.
// Stable string values appear in metric labels; renaming is a wire
// break.
type Action string

const (
	// ActionAllow — publish the bucket normally. No flags fire.
	ActionAllow Action = "allow"

	// ActionWarn — publish the bucket but set
	// `flags.divergence_warning: true`. The deviation is large
	// enough to surface to consumers but not extreme enough (or
	// has multi-source corroboration) to refuse to publish.
	ActionWarn Action = "warn"

	// ActionFreeze — DO NOT publish this bucket. Serve the
	// previous bucket's last-known-good VWAP with
	// `flags.frozen: true` and `flags.single_source: true`.
	// Caller maintains the LKG slot; this package only signals
	// the recommended action.
	ActionFreeze Action = "freeze"
)

// Decision is the result of [Checker.Evaluate]. Carries the chosen
// [Action] plus enough context for the caller to populate response
// flags, log lines, and Prometheus labels.
type Decision struct {
	// Action — what the caller should do with the bucket.
	Action Action

	// Class — which [AssetClass] was used to look up thresholds.
	Class AssetClass

	// Thresholds — the per-class thresholds that were checked.
	// Useful for log lines + ops dashboards.
	Thresholds Thresholds

	// DeviationPct — the computed deviation of CurrVWAP from
	// PrevVWAP, as an absolute percentage.
	DeviationPct float64

	// Reason — short human-readable explanation. Used by
	// runbooks + log lines; not a wire-shape field.
	Reason string
}

// IsFrozen reports whether the decision says to freeze. Convenience
// for callers that only need the boolean.
func (d Decision) IsFrozen() bool { return d.Action == ActionFreeze }

// IsWarn reports whether the decision says to warn (publish but
// flag).
func (d Decision) IsWarn() bool { return d.Action == ActionWarn }
