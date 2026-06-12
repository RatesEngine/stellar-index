package baseline_test

import (
	"errors"
	"math"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate/baseline"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// TestMedian_OddCount — clean odd-length case picks the middle.
func TestMedian_OddCount(t *testing.T) {
	got := baseline.Median([]float64{5, 1, 3, 4, 2})
	if !almostEqual(got, 3) {
		t.Errorf("Median = %v, want 3", got)
	}
}

// TestMedian_EvenCount — even length averages the two middle values.
func TestMedian_EvenCount(t *testing.T) {
	got := baseline.Median([]float64{1, 2, 3, 4})
	if !almostEqual(got, 2.5) {
		t.Errorf("Median = %v, want 2.5", got)
	}
}

// TestMedian_DoesNotMutateInput — the input slice must remain in
// its original order after Median runs (we copy internally).
func TestMedian_DoesNotMutateInput(t *testing.T) {
	in := []float64{3, 1, 4, 1, 5, 9, 2, 6}
	cp := make([]float64, len(in))
	copy(cp, in)
	_ = baseline.Median(in)
	for i := range in {
		if in[i] != cp[i] {
			t.Fatalf("Median mutated input at index %d: got %v, want %v", i, in[i], cp[i])
		}
	}
}

// TestMedian_Empty — well-defined zero rather than panic.
func TestMedian_Empty(t *testing.T) {
	got := baseline.Median(nil)
	if got != 0 {
		t.Errorf("Median(nil) = %v, want 0", got)
	}
}

// TestMAD_KnownInput — manual computation:
//
//	xs = [1,2,3,4,5], median = 3
//	deviations = [2,1,0,1,2], median(devs) = 1
//	MAD = 1.4826 * 1 = 1.4826
func TestMAD_KnownInput(t *testing.T) {
	got := baseline.MAD([]float64{1, 2, 3, 4, 5})
	want := baseline.MADScale * 1.0
	if !almostEqual(got, want) {
		t.Errorf("MAD = %v, want %v", got, want)
	}
}

// TestMAD_AllIdentical — degenerate-but-valid: zero spread → MAD=0.
func TestMAD_AllIdentical(t *testing.T) {
	got := baseline.MAD([]float64{7, 7, 7, 7, 7})
	if got != 0 {
		t.Errorf("MAD(identical) = %v, want 0", got)
	}
}

// TestMAD_RobustToOutlier — a single outlier moves σ a lot but
// barely moves MAD. The whole reason ADR-0019 picked MAD.
//
//	clean   = [1,2,3,4,5]                MAD = 1.4826
//	tainted = [1,2,3,4,5, 1000]          MAD should be barely larger
func TestMAD_RobustToOutlier(t *testing.T) {
	clean := baseline.MAD([]float64{1, 2, 3, 4, 5})
	tainted := baseline.MAD([]float64{1, 2, 3, 4, 5, 1000})
	if math.Abs(tainted-clean) > 1.0 {
		t.Errorf("MAD jumped %v with one outlier — too sensitive", tainted-clean)
	}
}

// TestFromReturns_ErrorOnTooFew — n < MinSamples returns the
// well-known sentinel rather than nonsense.
func TestFromReturns_ErrorOnTooFew(t *testing.T) {
	for _, in := range [][]float64{nil, {}, {0.5}} {
		_, err := baseline.FromReturns(in)
		if !errors.Is(err, baseline.ErrNotEnoughSamples) {
			t.Errorf("FromReturns(%v) err = %v, want ErrNotEnoughSamples", in, err)
		}
	}
}

// TestFromReturns_StoresN — the count of samples that fed the
// computation is reported back so downstream `baseline_quality`
// math can use it.
func TestFromReturns_StoresN(t *testing.T) {
	b, err := baseline.FromReturns([]float64{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatal(err)
	}
	if b.N != 5 {
		t.Errorf("N = %d, want 5", b.N)
	}
}

// TestZScore_KnownInputs — round-trip the canonical example from
// ADR-0019: a return of 0.10 (10%) on a baseline with median=0,
// MAD≈0.0148 (a stablecoin-class baseline) should fire well above
// z=5.
func TestZScore_KnownInputs(t *testing.T) {
	// Synthesise a stablecoin-class baseline: 11 returns clustered
	// tightly around zero. MAD will end up ~0.0148.
	stable := []float64{
		0.0001, -0.0002, 0.0001, 0.00, -0.0001,
		0.0002, -0.0001, 0.00, 0.0001, -0.0002, 0.00,
	}
	b, err := baseline.FromReturns(stable)
	if err != nil {
		t.Fatalf("FromReturns: %v", err)
	}
	if b.MAD == 0 {
		t.Fatal("expected non-zero MAD on near-stable input")
	}

	// 10% move on a stablecoin baseline ⇒ massive z-score.
	z := b.ZScore(0.10)
	if z < 5 {
		t.Errorf("z-score for a 10%% move on stable baseline = %v, want > 5", z)
	}
	// 0.01% move ⇒ in-band, z below the 5σ threshold.
	z = b.ZScore(0.0001)
	if z >= 5 {
		t.Errorf("z-score for 0.01%% move = %v, expected below 5σ trigger", z)
	}
}

// TestZScore_ZeroMADExactMatch — when the baseline has zero spread
// and the new observation matches the median exactly, the z-score
// is 0 (not NaN, not Inf).
func TestZScore_ZeroMADExactMatch(t *testing.T) {
	b := baseline.Baseline{Median: 1.5, MAD: 0, N: 10}
	if z := b.ZScore(1.5); z != 0 {
		t.Errorf("ZScore on zero-MAD exact match = %v, want 0", z)
	}
}

// TestZScore_ZeroMADAnyDeviation — when the baseline has zero spread
// and the observation differs even slightly, the z-score is +Inf —
// any threshold check (z >= 5) fires. Important: not NaN.
func TestZScore_ZeroMADAnyDeviation(t *testing.T) {
	b := baseline.Baseline{Median: 1.5, MAD: 0, N: 10}
	if z := b.ZScore(1.50001); !math.IsInf(z, 1) {
		t.Errorf("ZScore on zero-MAD deviation = %v, want +Inf", z)
	}
}

// TestZScore_SymmetricAroundMedian — by construction
// |x - median| is symmetric, so z(median + d) == z(median - d).
func TestZScore_SymmetricAroundMedian(t *testing.T) {
	b, err := baseline.FromReturns([]float64{0.01, -0.01, 0.02, -0.02, 0.0, 0.005})
	if err != nil {
		t.Fatal(err)
	}
	left := b.ZScore(b.Median - 0.05)
	right := b.ZScore(b.Median + 0.05)
	if !almostEqual(left, right) {
		t.Errorf("z asymmetric: left=%v right=%v", left, right)
	}
}

// TestReturnsFromVWAPs_BasicRollthrough — three VWAPs → two returns.
func TestReturnsFromVWAPs_BasicRollthrough(t *testing.T) {
	r := baseline.ReturnsFromVWAPs([]float64{100, 102, 99})
	if len(r) != 2 {
		t.Fatalf("got %d returns, want 2", len(r))
	}
	// (102 - 100) / 100 = 0.02
	if !almostEqual(r[0], 0.02) {
		t.Errorf("returns[0] = %v, want 0.02", r[0])
	}
	// (99 - 102) / 102 ≈ -0.0294117647
	if !almostEqual(r[1], -0.029411764705882) {
		t.Errorf("returns[1] = %v, want ≈ -0.0294", r[1])
	}
}

// TestReturnsFromVWAPs_ShortInput — fewer than 2 inputs → nil.
func TestReturnsFromVWAPs_ShortInput(t *testing.T) {
	if got := baseline.ReturnsFromVWAPs(nil); got != nil {
		t.Errorf("nil input → %v, want nil", got)
	}
	if got := baseline.ReturnsFromVWAPs([]float64{42}); got != nil {
		t.Errorf("single input → %v, want nil", got)
	}
}

// TestReturnsFromVWAPs_SkipsZeroPrev — a bucket where prev VWAP is
// zero (e.g. a bucket with no liquidity) is skipped rather than
// producing a +/-Inf return that would poison downstream stats.
func TestReturnsFromVWAPs_SkipsZeroPrev(t *testing.T) {
	r := baseline.ReturnsFromVWAPs([]float64{0, 100, 102})
	// First return would have been (100 - 0) / 0 = Inf — skipped.
	// Remaining: (102 - 100) / 100 = 0.02
	if len(r) != 1 {
		t.Fatalf("got %d returns, want 1 (zero-prev skipped)", len(r))
	}
	if !almostEqual(r[0], 0.02) {
		t.Errorf("returns[0] = %v, want 0.02", r[0])
	}
}

// TestEndToEnd_StableCoinClassThreshold — sanity-check the
// ADR-0019 §"typical return_mad" claim: ~0.05% MAD on a stablecoin,
// ~0.25% 5σ trigger. Round-trip a synthetic stable series of
// realistic minute-to-minute jitter and assert MAD is in the right
// ballpark.
func TestEndToEnd_StableCoinClassThreshold(t *testing.T) {
	// 30 returns with 1-bp σ — plausible for USDC during a quiet
	// market. Manually constructed (not random) so the test is
	// deterministic.
	jitter := []float64{
		+0.0001, -0.0001, +0.00005, -0.00005, +0.00015, -0.00015,
		0, +0.0001, -0.0001, +0.0002, -0.0002, +0.0001, -0.0001,
		+0.00005, -0.00005, 0, +0.0001, -0.0001, +0.00015, -0.00015,
		+0.0001, -0.0001, +0.00005, -0.00005, +0.0001, -0.0001,
		+0.0002, -0.0002, 0, +0.0001,
	}
	b, err := baseline.FromReturns(jitter)
	if err != nil {
		t.Fatal(err)
	}
	// MAD should be on the order of the input spread (a few bp),
	// not orders of magnitude off.
	if b.MAD < 1e-5 || b.MAD > 1e-2 {
		t.Errorf("stablecoin-class MAD = %v, want in [1e-5, 1e-2]", b.MAD)
	}
	// 5% move (massively de-pegging) should fire well above z=5.
	z := b.ZScore(0.05)
	if z < 5 {
		t.Errorf("5%% depeg z = %v, want > 5σ", z)
	}
}
