package changesummary

import (
	"strconv"
	"testing"
	"time"
)

// TestComputeSummary_DeltasAndATH walks computeSummary against a
// hand-constructed series and checks the multi-window deltas, ATH/ATL,
// and current value all line up.
func TestComputeSummary_DeltasAndATH(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	series := []TimedValue{
		{At: now.Add(-30 * 24 * time.Hour), Value: "100.00"}, // 30 days ago
		{At: now.Add(-7 * 24 * time.Hour), Value: "110.00"},  // 7 days ago
		{At: now.Add(-24 * time.Hour), Value: "120.00"},      // 24h ago
		{At: now.Add(-1 * time.Hour), Value: "125.00"},       // 1h ago
		{At: now, Value: "130.00"},                           // current
	}
	row := computeSummary(Entity{Type: "coin", ID: "stellar"}, series, now)

	if row.CurrentValue != 130.0 {
		t.Errorf("CurrentValue = %v, want 130", row.CurrentValue)
	}
	// (130 - 125) / 125 * 100 = 4.0
	if row.H1DeltaPct == nil || *row.H1DeltaPct < 3.99 || *row.H1DeltaPct > 4.01 {
		t.Errorf("H1DeltaPct = %v, want ~4.0", row.H1DeltaPct)
	}
	// (130 - 120) / 120 * 100 ≈ 8.33
	if row.H24DeltaPct == nil || *row.H24DeltaPct < 8.32 || *row.H24DeltaPct > 8.34 {
		t.Errorf("H24DeltaPct = %v, want ~8.33", row.H24DeltaPct)
	}
	// (130 - 110) / 110 * 100 ≈ 18.18
	if row.D7DeltaPct == nil || *row.D7DeltaPct < 18.17 || *row.D7DeltaPct > 18.19 {
		t.Errorf("D7DeltaPct = %v, want ~18.18", row.D7DeltaPct)
	}
	// (130 - 100) / 100 * 100 = 30
	if row.D30DeltaPct == nil || *row.D30DeltaPct < 29.99 || *row.D30DeltaPct > 30.01 {
		t.Errorf("D30DeltaPct = %v, want 30", row.D30DeltaPct)
	}

	// ATH = 130, ATL = 100 (across the 30d window).
	if row.ATHValue == nil || *row.ATHValue != 130 {
		t.Errorf("ATH = %v, want 130", row.ATHValue)
	}
	if row.ATLValue == nil || *row.ATLValue != 100 {
		t.Errorf("ATL = %v, want 100", row.ATLValue)
	}
}

// TestComputeSummary_SparseHistoryGracefulNullables — an entity
// with only a few hours of history must leave d7 / d30 unset rather
// than fabricating zeros.
func TestComputeSummary_SparseHistoryGracefulNullables(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	series := []TimedValue{
		{At: now.Add(-30 * time.Minute), Value: "100"},
		{At: now, Value: "105"},
	}
	row := computeSummary(Entity{Type: "coin", ID: "freshasset"}, series, now)

	if row.H1Value != nil {
		t.Errorf("H1Value = %v, want nil (no observation 1h ago)", row.H1Value)
	}
	if row.H24Value != nil {
		t.Errorf("H24Value = %v, want nil (entity is <1h old)", row.H24Value)
	}
	if row.D7Value != nil {
		t.Errorf("D7Value = %v, want nil", row.D7Value)
	}
	if row.D30Value != nil {
		t.Errorf("D30Value = %v, want nil", row.D30Value)
	}
}

// TestValueAt_BinarySearch — pin the at-or-before semantic.
func TestValueAt_BinarySearch(t *testing.T) {
	t0 := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	series := []TimedValue{
		{At: t0.Add(0 * time.Hour), Value: "1"},
		{At: t0.Add(1 * time.Hour), Value: "2"},
		{At: t0.Add(2 * time.Hour), Value: "3"},
		{At: t0.Add(3 * time.Hour), Value: "4"},
	}
	for _, tc := range []struct {
		target time.Time
		want   float64
		ok     bool
	}{
		{t0.Add(-1 * time.Hour), 0, false}, // before any data
		{t0.Add(0 * time.Hour), 1, true},
		{t0.Add(90 * time.Minute), 2, true},
		{t0.Add(2 * time.Hour), 3, true},
		{t0.Add(10 * time.Hour), 4, true}, // after last
	} {
		got, ok := valueAt(series, tc.target)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("valueAt(%v) = (%v, %v), want (%v, %v)", tc.target, got, ok, tc.want, tc.ok)
		}
	}
}

// TestComputeStreak_Up — three consecutive daily increases → "up", 3.
func TestComputeStreak_Up(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	series := make([]TimedValue, 5)
	for i := range series {
		series[i] = TimedValue{
			At:    now.Add(-time.Duration(4-i) * 24 * time.Hour),
			Value: strconv.Itoa(100 + i*10),
		}
	}
	dir, days := computeStreak(series)
	if dir != "up" || days == nil || *days != 4 {
		t.Errorf("got dir=%q days=%v, want up/4", dir, days)
	}
}

// TestComputeSummary_ATLSkipsZero — a zero (or unparseable) point
// mid-series must NOT corrupt ATL. Regression for G14-02: the old
// `|| atlValue == 0` reset turned [100,5,0,90] into ATL=90.
func TestComputeSummary_ATLSkipsZero(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	series := []TimedValue{
		{At: now.Add(-3 * time.Hour), Value: "100"},
		{At: now.Add(-2 * time.Hour), Value: "5"},
		{At: now.Add(-1 * time.Hour), Value: "0"}, // bad/zero point
		{At: now, Value: "90"},
	}
	row := computeSummary(Entity{Type: "coin", ID: "x"}, series, now)
	if row.ATLValue == nil || *row.ATLValue != 5 {
		t.Errorf("ATL = %v, want 5 (zero point must be skipped, not adopted)", row.ATLValue)
	}
	if row.ATHValue == nil || *row.ATHValue != 100 {
		t.Errorf("ATH = %v, want 100", row.ATHValue)
	}
}

// TestComputeAcceleration — direction-agnostic momentum. A steepening
// downtrend must read "increasing" (momentum building), not "decreasing".
// Regression for G14-01: signed comparison inverted for negative deltas.
func TestComputeAcceleration(t *testing.T) {
	base := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	// build constructs a series from per-step values starting at v0.
	build := func(v0 float64, steps ...float64) []TimedValue {
		vals := []float64{v0}
		for _, s := range steps {
			vals = append(vals, vals[len(vals)-1]+s)
		}
		out := make([]TimedValue, len(vals))
		for i, v := range vals {
			out[i] = TimedValue{
				At:    base.Add(time.Duration(i) * time.Minute),
				Value: strconv.FormatFloat(v, 'f', -1, 64),
			}
		}
		return out
	}

	// computeAcceleration compares the last quarter's mean step against
	// the previous quarter's. With 9 points (8 steps) and q=2 that is
	// the step ending the series vs the step two-quarters back, so the
	// cases below vary the LAST step's magnitude relative to an earlier
	// one. The key regression: a steepening DOWNtrend (more-negative
	// last step) must read "increasing", not "decreasing".
	for _, tc := range []struct {
		name   string
		series []TimedValue
		want   string
	}{
		{
			// Last step (+10) much bigger than the earlier baseline (+1).
			name:   "steepening uptrend → increasing",
			series: build(100, 1, 1, 1, 1, 1, 1, 1, 10),
			want:   "increasing",
		},
		{
			// Last step (-10) much more negative than the earlier (-1):
			// the signed-comparison bug labelled this "decreasing".
			name:   "steepening downtrend → increasing",
			series: build(100, -1, -1, -1, -1, -1, -1, -1, -10),
			want:   "increasing",
		},
		{
			// Earlier step (-10) bigger than the last (-1): momentum waning.
			name:   "softening downtrend → decreasing",
			series: build(100, -1, -1, -1, -1, -1, -10, -1, -1),
			want:   "decreasing",
		},
		{
			// Constant slope — neither accelerating nor decelerating.
			name:   "steady uptrend → flat",
			series: build(100, 5, 5, 5, 5, 5, 5, 5, 5),
			want:   "flat",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeAcceleration(tc.series); got != tc.want {
				t.Errorf("computeAcceleration = %q, want %q", got, tc.want)
			}
		})
	}
}
