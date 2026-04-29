package confidence_test

import (
	"math"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/aggregate/confidence"
)

func near(a, b, tol float64) bool {
	return math.Abs(a-b) < tol
}

// ─── ZScoreFactor ────────────────────────────────────────────────

// TestZScoreFactor_AnchorPoints — the ADR's documented shape
// (1.0 at z=0, ~0.5 at z=5 (the 5σ trigger), ~0 at z=10).
func TestZScoreFactor_AnchorPoints(t *testing.T) {
	cases := []struct {
		z, want, tol float64
	}{
		{z: 0, want: 1.0, tol: 0.01},
		{z: 5, want: 0.5, tol: 0.01},
		{z: 10, want: 0.01, tol: 0.01},
	}
	for _, c := range cases {
		got := confidence.ZScoreFactor(c.z)
		if !near(got, c.want, c.tol) {
			t.Errorf("ZScoreFactor(%v) = %v, want ~%v (±%v)", c.z, got, c.want, c.tol)
		}
	}
}

func TestZScoreFactor_Monotonic(t *testing.T) {
	// Higher z must NEVER produce a higher confidence factor.
	prev := math.Inf(1)
	for z := 0.0; z <= 15; z += 0.5 {
		got := confidence.ZScoreFactor(z)
		if got > prev {
			t.Errorf("ZScoreFactor not monotonic at z=%v: %v > prev %v", z, got, prev)
		}
		prev = got
	}
}

func TestZScoreFactor_GuardsBadInputs(t *testing.T) {
	for _, in := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1.0} {
		got := confidence.ZScoreFactor(in)
		if got != 0 {
			t.Errorf("ZScoreFactor(%v) = %v, want 0 (defensive)", in, got)
		}
	}
}

// ─── SourceCountFactor ────────────────────────────────────────────

func TestSourceCountFactor_AnchorPoints(t *testing.T) {
	cases := []struct {
		n         int
		want, tol float64
	}{
		{n: 1, want: 0.12, tol: 0.05}, // ADR: "single-source caps at ~0.3" — sigmoid gives 0.119
		{n: 3, want: 0.5, tol: 0.01},
		{n: 6, want: 0.95, tol: 0.05}, // ADR: "n≥6 reaches near-1.0"
	}
	for _, c := range cases {
		got := confidence.SourceCountFactor(c.n)
		if !near(got, c.want, c.tol) {
			t.Errorf("SourceCountFactor(%d) = %v, want ~%v (±%v)", c.n, got, c.want, c.tol)
		}
	}
}

func TestSourceCountFactor_GuardsNegative(t *testing.T) {
	if got := confidence.SourceCountFactor(-1); got != 0 {
		t.Errorf("SourceCountFactor(-1) = %v, want 0", got)
	}
}

// ─── DiversityFactor ──────────────────────────────────────────────

func TestDiversityFactor(t *testing.T) {
	cases := []struct {
		classCount int
		want       float64
	}{
		{0, 0},
		{1, 0.5},
		{2, 1.0},
		{5, 1.0}, // any number ≥ 2 is full credit
	}
	for _, c := range cases {
		got := confidence.DiversityFactor(c.classCount)
		if got != c.want {
			t.Errorf("DiversityFactor(%d) = %v, want %v", c.classCount, got, c.want)
		}
	}
}

// ─── LiquidityFactor ──────────────────────────────────────────────

func TestLiquidityFactor_AnchorPoints(t *testing.T) {
	cases := []struct {
		usd       float64
		want, tol float64
	}{
		{usd: 100, want: 0, tol: 0.01},         // below floor → 0
		{usd: 1_000, want: 0, tol: 0.01},       // exactly floor → 0
		{usd: 100_000, want: 1.0, tol: 0.01},   // exactly ceiling → 1
		{usd: 1_000_000, want: 1.0, tol: 0.01}, // above ceiling → 1
	}
	for _, c := range cases {
		got := confidence.LiquidityFactor(c.usd)
		if !near(got, c.want, c.tol) {
			t.Errorf("LiquidityFactor(%v) = %v, want ~%v", c.usd, got, c.want)
		}
	}
}

func TestLiquidityFactor_LogShape(t *testing.T) {
	// Geometric midpoint between $1K and $100K is $10K (log midpoint);
	// the factor at $10K should be ~0.5.
	got := confidence.LiquidityFactor(10_000)
	if !near(got, 0.5, 0.05) {
		t.Errorf("LiquidityFactor(10000) = %v, want ~0.5 (log-midpoint)", got)
	}
}

func TestLiquidityFactor_GuardsBadInputs(t *testing.T) {
	for _, in := range []float64{math.NaN(), -1.0, 0.0, math.Inf(-1)} {
		got := confidence.LiquidityFactor(in)
		if got != 0 {
			t.Errorf("LiquidityFactor(%v) = %v, want 0", in, got)
		}
	}
}

// ─── CrossOracleFactor ────────────────────────────────────────────

func TestCrossOracleFactor_AnchorPoints(t *testing.T) {
	if got := confidence.CrossOracleFactor(0); !near(got, 1.0, 0.01) {
		t.Errorf("0%% divergence = %v, want ~1.0", got)
	}
	if got := confidence.CrossOracleFactor(5); !near(got, 0.5, 0.05) {
		t.Errorf("5%% divergence = %v, want ~0.5", got)
	}
	if got := confidence.CrossOracleFactor(50); got > 0.05 {
		t.Errorf("50%% divergence = %v, want near-0", got)
	}
}

func TestCrossOracleFactor_NoDataReturnsNeutral(t *testing.T) {
	// Negative input is the "no cross-oracle data" sentinel.
	got := confidence.CrossOracleFactor(-1)
	if got != 0.7 {
		t.Errorf("no-data sentinel = %v, want 0.7 (ADR-0019 worked example)", got)
	}
}

func TestCrossOracleFactor_GuardsNaN(t *testing.T) {
	if got := confidence.CrossOracleFactor(math.NaN()); got != 0 {
		t.Errorf("NaN = %v, want 0", got)
	}
}

// ─── BaselineQualityFactor ────────────────────────────────────────

func TestBaselineQualityFactor(t *testing.T) {
	cases := []struct {
		days      float64
		want, tol float64
	}{
		{0, 0.5, 0.001},
		{15, 0.75, 0.001}, // halfway through ramp
		{30, 1.0, 0.001},
		{100, 1.0, 0.001}, // capped
	}
	for _, c := range cases {
		got := confidence.BaselineQualityFactor(c.days)
		if !near(got, c.want, c.tol) {
			t.Errorf("BaselineQualityFactor(%v days) = %v, want %v", c.days, got, c.want)
		}
	}
}

func TestBaselineQualityFactor_NegativeOrNaN_ReturnsBootstrap(t *testing.T) {
	// Negative or NaN treated as bootstrap, not failure — clock skew
	// shouldn't crater confidence.
	for _, in := range []float64{-1, math.NaN()} {
		got := confidence.BaselineQualityFactor(in)
		if got != 0.5 {
			t.Errorf("BaselineQualityFactor(%v) = %v, want 0.5", in, got)
		}
	}
}
