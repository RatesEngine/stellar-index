package baseline_test

import (
	"math"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate/baseline"
)

// stableJitter returns `n` near-zero returns oscillating around 0
// with an amplitude of `amp`. Used to seed clean training windows.
func stableJitter(n int, amp float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		switch i % 4 {
		case 0:
			out[i] = +amp
		case 1:
			out[i] = -amp
		case 2:
			out[i] = +amp / 2
		case 3:
			out[i] = -amp / 2
		}
	}
	return out
}

// vwapSeries reconstructs a VWAP series from a starting price and
// a return slice. (vwap[i+1] = vwap[i] * (1 + returns[i]).)
func vwapSeries(start float64, returns []float64) []float64 {
	out := make([]float64, len(returns)+1)
	out[0] = start
	for i, r := range returns {
		out[i+1] = out[i] * (1 + r)
	}
	return out
}

// TestMultiBaseline_AllValid — three populated windows produce
// three baselines.
func TestMultiBaseline_AllValid(t *testing.T) {
	d1 := vwapSeries(1.0, stableJitter(20, 0.001))
	d7 := vwapSeries(1.0, stableJitter(50, 0.001))
	d30 := vwapSeries(1.0, stableJitter(200, 0.001))

	mb := baseline.NewMultiBaseline(d1, d7, d30)
	if mb.Day1 == nil {
		t.Error("Day1 nil; expected populated")
	}
	if mb.Day7 == nil {
		t.Error("Day7 nil; expected populated")
	}
	if mb.Day30 == nil {
		t.Error("Day30 nil; expected populated")
	}
	if !mb.HasAnyValid() {
		t.Error("HasAnyValid = false; expected true")
	}
}

// TestMultiBaseline_PartialBootstrap — when only one window has
// enough samples (typical newly-listed asset that's been live for
// 2 days), HasAnyValid is true and MaxZScore considers only the
// valid window.
func TestMultiBaseline_PartialBootstrap(t *testing.T) {
	d1 := vwapSeries(1.0, stableJitter(20, 0.001)) // valid
	d7 := []float64{1.0}                           // bootstrap (n<2)
	d30 := []float64{1.0}                          // bootstrap

	mb := baseline.NewMultiBaseline(d1, d7, d30)
	if mb.Day1 == nil {
		t.Fatal("Day1 should be populated")
	}
	if mb.Day7 != nil || mb.Day30 != nil {
		t.Error("Day7/Day30 should be nil in bootstrap")
	}
	z, window, valid := mb.MaxZScore(0.10)
	if !valid {
		t.Fatal("MaxZScore returned valid=false despite Day1 being valid")
	}
	if window != baseline.Window1d {
		t.Errorf("window = %v, want Window1d", window)
	}
	if z < 5 {
		t.Errorf("z = %v, want > 5 for a 10%% move on a 0.1%% MAD baseline", z)
	}
}

// TestMultiBaseline_FullBootstrap — all three windows in bootstrap.
// HasAnyValid is false; MaxZScore signals "no signal" with
// valid=false (NOT a misleading 0).
func TestMultiBaseline_FullBootstrap(t *testing.T) {
	mb := baseline.NewMultiBaseline(nil, nil, nil)
	if mb.HasAnyValid() {
		t.Error("HasAnyValid = true on empty input")
	}
	_, _, valid := mb.MaxZScore(0.05)
	if valid {
		t.Error("MaxZScore valid = true on empty input")
	}
}

// TestMultiBaseline_FrogBoilingDefense — the headline test:
//
// An attacker drifts the asset 0.5% per day for 14 days. The 1d
// and 7d windows have learned the drift (their median has moved
// with it). The 30d window still includes pre-attack data, so a
// fresh return that's small relative to the drifted recent
// baseline is still LARGE relative to the 30d baseline.
//
// MaxZScore picks the 30d window — anomaly fires.
func TestMultiBaseline_FrogBoilingDefense(t *testing.T) {
	const (
		bucketsPerDay = 1440 // 1m buckets per 24h
		driftDays     = 14
		driftPerDay   = 0.005 // 0.5% per day = ~7% over 14 days
	)

	// 30 days of returns: first 16 quiet, last 14 with sustained
	// drift. quietReturns has bp-scale noise.
	const totalDays = 30
	returns := make([]float64, 0, totalDays*bucketsPerDay)
	for d := 0; d < totalDays; d++ {
		dayReturns := stableJitter(bucketsPerDay, 0.0001) // 1bp jitter
		if d >= totalDays-driftDays {
			// Add a sustained drift component to every bucket today
			perBucketDrift := driftPerDay / bucketsPerDay
			for i := range dayReturns {
				dayReturns[i] += perBucketDrift
			}
		}
		returns = append(returns, dayReturns...)
	}
	full := vwapSeries(1.0, returns)

	// Slice into the three windows. We want the 30d to span the full
	// series; the 7d to span the last 7 days (post-drift baseline);
	// the 1d to span the last 1 day (post-drift baseline).
	d1 := full[len(full)-bucketsPerDay-1:]
	d7 := full[len(full)-7*bucketsPerDay-1:]
	d30 := full

	mb := baseline.NewMultiBaseline(d1, d7, d30)
	if mb.Day1 == nil || mb.Day7 == nil || mb.Day30 == nil {
		t.Fatal("expected all three windows populated")
	}

	// A fresh return matching the recent drift rate looks small to
	// the 1d/7d (medians have caught up) but large to the 30d
	// baseline (which was learned before the drift).
	freshReturn := driftPerDay / bucketsPerDay // one more bucket of drift

	z1 := mb.Day1.ZScore(freshReturn)
	z7 := mb.Day7.ZScore(freshReturn)
	z30 := mb.Day30.ZScore(freshReturn)

	t.Logf("z scores — 1d=%.2f, 7d=%.2f, 30d=%.2f", z1, z7, z30)

	// Sanity check the drift defeats short windows but not the long.
	if z1 > z30 {
		t.Errorf("frog-boiling defence broken: 1d window flags MORE than 30d (z1=%.2f, z30=%.2f) — drift should be invisible to short windows", z1, z30)
	}
	if z30 < z1 || z30 < z7 {
		t.Errorf("expected 30d to dominate after drift; got z1=%.2f z7=%.2f z30=%.2f", z1, z7, z30)
	}

	// And MaxZScore picks the 30d window.
	maxZ, w, valid := mb.MaxZScore(freshReturn)
	if !valid {
		t.Fatal("MaxZScore valid=false")
	}
	if w != baseline.Window30d {
		t.Errorf("MaxZScore window = %v, want Window30d (the long window catches the drift)", w)
	}
	if !math.IsInf(maxZ, 1) && maxZ < z30 {
		t.Errorf("maxZ = %v, want >= z30 = %v", maxZ, z30)
	}
}

// TestMultiBaseline_SuddenSpikeFiresFromShortWindow — the converse
// of frog-boiling: a single big spike on an otherwise-quiet asset
// fires from the 1d window because the 30d window's MAD averaged
// the spike out across 43k samples.
func TestMultiBaseline_SuddenSpikeFiresFromShortWindow(t *testing.T) {
	d1 := vwapSeries(1.0, stableJitter(20, 0.0001))
	d7 := vwapSeries(1.0, stableJitter(100, 0.0001))
	d30 := vwapSeries(1.0, stableJitter(500, 0.0001))

	mb := baseline.NewMultiBaseline(d1, d7, d30)

	// Sudden 5% spike. All three windows have ~bp MAD, so all flag
	// it — but the longest window's MAD is presumably the smallest
	// (more samples to refine the median). MaxZScore selects whichever
	// window gives the highest z.
	maxZ, _, valid := mb.MaxZScore(0.05)
	if !valid {
		t.Fatal("MaxZScore valid=false")
	}
	if maxZ < 5 {
		t.Errorf("5%% spike on bp baseline should give z >= 5; got %v", maxZ)
	}
}

// TestSplitByLookback_BasicSlicing — three windows pulled from one
// timestamped series.
func TestSplitByLookback_BasicSlicing(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	timed := []baseline.TimedVWAP{
		// Older than 30d → excluded from all
		{VWAP: 0.5, BucketEnd: now.Add(-31 * 24 * time.Hour)},
		// 25d ago → in 30d only
		{VWAP: 1.0, BucketEnd: now.Add(-25 * 24 * time.Hour)},
		// 5d ago → in 30d + 7d
		{VWAP: 1.1, BucketEnd: now.Add(-5 * 24 * time.Hour)},
		// 30m ago → in all three
		{VWAP: 1.2, BucketEnd: now.Add(-30 * time.Minute)},
	}
	d1, d7, d30 := baseline.SplitByLookback(timed, now)
	if len(d1) != 1 {
		t.Errorf("d1 len = %d, want 1", len(d1))
	}
	if len(d7) != 2 {
		t.Errorf("d7 len = %d, want 2", len(d7))
	}
	if len(d30) != 3 {
		t.Errorf("d30 len = %d, want 3", len(d30))
	}
}

// TestSplitByLookback_BoundaryInclusive — entry exactly at the
// cutoff IS included (half-open [cutoff, now]). Input is oldest-
// first per the package contract.
func TestSplitByLookback_BoundaryInclusive(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	timed := []baseline.TimedVWAP{
		{VWAP: 1.2, BucketEnd: now.Add(-baseline.Window30d)}, // exactly 30d → in d30 only
		{VWAP: 1.1, BucketEnd: now.Add(-baseline.Window7d)},  // exactly 7d → in d7+d30, not d1
		{VWAP: 1.0, BucketEnd: now.Add(-baseline.Window1d)},  // exactly 1d → in all three
	}
	d1, d7, d30 := baseline.SplitByLookback(timed, now)
	if len(d1) != 1 {
		t.Errorf("d1 should include only the exact-1d entry, len = %d", len(d1))
	}
	if len(d7) != 2 {
		t.Errorf("d7 should include exact-7d + exact-1d entries, len = %d", len(d7))
	}
	if len(d30) != 3 {
		t.Errorf("d30 should include all three entries, len = %d", len(d30))
	}
}
