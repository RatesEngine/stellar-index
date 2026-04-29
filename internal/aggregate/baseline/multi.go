package baseline

import (
	"time"
)

// Window-length constants used by the multi-window safeguard
// per ADR-0019. The aggregator's storage layer uses these as the
// query lookback for each per-window VWAP fetch.
const (
	Window1d  = 24 * time.Hour
	Window7d  = 7 * 24 * time.Hour
	Window30d = 30 * 24 * time.Hour
)

// MultiBaseline is the per-pair statistical baseline computed at
// three time scales (1d / 7d / 30d) per ADR-0019 §"Multi-window
// safeguard against frog-boiling".
//
// The defence: a coordinated attacker who slowly drifts an asset
// over weeks would defeat the 1d window (the median + MAD have
// drifted with the attack), and possibly the 7d window, but the
// 30d window — which still includes pre-attack data — would still
// flag the drifted price as anomalous.
//
// Conversely, a legitimate regime change (asset matures, gains
// liquidity) shows up proportionally across all three windows, so
// no single window fires a false positive.
//
// Each window field is a pointer so a "not enough data" outcome at
// one scale doesn't invalidate the whole struct — the 1d and 7d
// can be `nil` early in an asset's lifetime while the 30d is
// already valid (or vice versa during data-gap recovery).
type MultiBaseline struct {
	Day1  *Baseline
	Day7  *Baseline
	Day30 *Baseline
}

// NewMultiBaseline builds a [MultiBaseline] from three pre-sliced
// VWAP series — one per window. The caller is responsible for
// slicing to the right duration; this constructor just runs each
// slice through [ReturnsFromVWAPs] → [FromReturns] and stores the
// result (or `nil` when [ErrNotEnoughSamples] fires).
//
// Callers that have a single time-stamped VWAP series can use
// [SplitByLookback] to derive the three sub-slices.
func NewMultiBaseline(vwapsDay1, vwapsDay7, vwapsDay30 []float64) MultiBaseline {
	return MultiBaseline{
		Day1:  buildOrNil(vwapsDay1),
		Day7:  buildOrNil(vwapsDay7),
		Day30: buildOrNil(vwapsDay30),
	}
}

// buildOrNil runs FromReturns(ReturnsFromVWAPs(vwaps)) and returns
// either a pointer to the Baseline or nil if the window had too few
// samples. Used internally so each window's bootstrap state is
// observable on the wrapper struct.
func buildOrNil(vwaps []float64) *Baseline {
	b, err := FromReturns(ReturnsFromVWAPs(vwaps))
	if err != nil {
		return nil
	}
	return &b
}

// HasAnyValid reports whether at least one window had enough
// samples to compute a baseline. A multi-baseline with all three
// windows nil is in full bootstrap (ADR-0019 §"Bootstrap policy")
// and the caller should not consult it for anomaly detection.
func (m MultiBaseline) HasAnyValid() bool {
	return m.Day1 != nil || m.Day7 != nil || m.Day30 != nil
}

// MaxZScore returns the largest z-score for `x` across every
// window that has a valid baseline, plus the window's lookback
// duration so callers can attribute "which window detected this".
//
// "Fires when any window flags as anomalous" (ADR-0019) maps to
// `MaxZScore(x) >= threshold`. The 30d window dominates for
// drifted-attack detection (it sees pre-drift baseline); the 1d
// window dominates for sudden-spike detection (the longer windows
// average a single spike out among 30k samples).
//
// The third return value is `valid` — false when [HasAnyValid]
// would return false. Callers branch to the bootstrap policy on
// !valid rather than treating the zero z-score as a real reading.
func (m MultiBaseline) MaxZScore(x float64) (z float64, window time.Duration, valid bool) {
	if b := m.Day1; b != nil {
		zb := b.ZScore(x)
		if !valid || zb > z {
			z, window, valid = zb, Window1d, true
		}
	}
	if b := m.Day7; b != nil {
		zb := b.ZScore(x)
		if !valid || zb > z {
			z, window, valid = zb, Window7d, true
		}
	}
	if b := m.Day30; b != nil {
		zb := b.ZScore(x)
		if !valid || zb > z {
			z, window, valid = zb, Window30d, true
		}
	}
	return z, window, valid
}

// SplitByLookback partitions a chronologically-ordered (oldest-first)
// series of (vwap, bucketEnd) pairs into three slices: one for each
// rolling lookback window ending at `now`.
//
// The 1d slice contains entries with bucketEnd >= now-1d; the 7d
// slice >= now-7d; the 30d slice >= now-30d. Slices share backing
// memory with the input — callers MUST NOT mutate. A bucket
// timestamp equal to the cutoff IS included (half-open `[start, now]`).
//
// This is a helper for callers who store the full 30d series and
// want to derive sub-window views without three round trips to the
// storage layer.
func SplitByLookback(timed []TimedVWAP, now time.Time) (day1, day7, day30 []float64) {
	cut1 := now.Add(-Window1d)
	cut7 := now.Add(-Window7d)
	cut30 := now.Add(-Window30d)

	// Find the first index >= each cutoff. Linear scans are fine —
	// the 30-day series is bounded at 43,200 entries (1m buckets) and
	// runs once per refresh cycle, not per request.
	idx1 := firstAtOrAfter(timed, cut1)
	idx7 := firstAtOrAfter(timed, cut7)
	idx30 := firstAtOrAfter(timed, cut30)

	day1 = vwapsFrom(timed, idx1)
	day7 = vwapsFrom(timed, idx7)
	day30 = vwapsFrom(timed, idx30)
	return day1, day7, day30
}

// TimedVWAP is one bucketed VWAP with its window-end timestamp. The
// timestamp is the bucket end (when the price was "as of"), so
// SplitByLookback's cutoffs are wall-clock comparable.
type TimedVWAP struct {
	VWAP      float64
	BucketEnd time.Time
}

// firstAtOrAfter returns the index of the first entry in `timed`
// with BucketEnd >= cutoff. Returns len(timed) when every entry is
// older than the cutoff.
func firstAtOrAfter(timed []TimedVWAP, cutoff time.Time) int {
	for i := range timed {
		if !timed[i].BucketEnd.Before(cutoff) {
			return i
		}
	}
	return len(timed)
}

// vwapsFrom extracts the VWAP column from a TimedVWAP slice
// starting at `from`. Allocates — sub-windows aren't large enough
// to warrant unsafe slicing tricks.
func vwapsFrom(timed []TimedVWAP, from int) []float64 {
	if from >= len(timed) {
		return nil
	}
	out := make([]float64, len(timed)-from)
	for i := range out {
		out[i] = timed[from+i].VWAP
	}
	return out
}
