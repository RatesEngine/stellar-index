package blend

import (
	"math/big"
	"testing"
)

// interestRefConfig is the ReserveConfig from interest.rs's own unit
// tests (test_calc_accrual_*), so the borrow-rate vectors below are
// validated against the contract's ground truth.
func interestRefConfig() ReserveConfig {
	return ReserveConfig{
		Decimals: 7, CFactor: 7_500_000, LFactor: 7_500_000,
		Util: 7_500_000, MaxUtil: 9_500_000,
		RBase: 100_000, ROne: 500_000, RTwo: 5_000_000, RThree: 15_000_000,
		Reactivity: 20, SupplyCap: big.NewInt(1_000_000_000_000_000_000), Enabled: true,
	}
}

// BorrowRate must reproduce cur_ir from interest.rs::calc_accrual for
// each of its three utilization regimes (hand-derived from the contract
// formula; the under/over-target vectors also reconcile with the
// accrual assertions in interest.rs's own tests).
func TestBorrowRate_Vectors(t *testing.T) {
	cfg := interestRefConfig()
	irMod := big.NewInt(1_0000000) // 1.0
	cases := []struct {
		name string
		util int64
		want int64
	}{
		{"under_target", 6_565_656, 537_711},  // util ≤ 75%
		{"over_target", 7_979_797, 1_799_493}, // 75% < util ≤ 95%
		{"over_95", 9_700_000, 11_600_000},    // util > 95%
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cfg.BorrowRate(big.NewInt(c.util), irMod)
			if got.Cmp(big.NewInt(c.want)) != 0 {
				t.Errorf("BorrowRate(util=%d) = %s, want %d", c.util, got, c.want)
			}
		})
	}
}

// Utilization mirrors reserve.rs: liabilities/supply, capped, 0 on no
// debt. supplied = b_supply×b_rate/1e12; borrowed = d_supply×d_rate/1e12.
func TestUtilization(t *testing.T) {
	rate := big.NewInt(1_000_000_000_000) // 1.0 at 12 decimals
	// supplied = 2000, borrowed = 1000 → util 50%.
	rd := ReserveData{
		BSupply: big.NewInt(2000), BRate: rate,
		DSupply: big.NewInt(1000), DRate: rate,
		IRMod: big.NewInt(1_0000000),
	}
	if got := rd.Utilization(); got.Cmp(big.NewInt(5_000_000)) != 0 {
		t.Errorf("utilization = %s, want 5000000 (50%%)", got)
	}
	// No debt → 0.
	rd.DSupply = big.NewInt(0)
	if got := rd.Utilization(); got.Sign() != 0 {
		t.Errorf("utilization (no debt) = %s, want 0", got)
	}
	// Liabilities ≥ supply → capped at 100%.
	rd.DSupply = big.NewInt(5000)
	if got := rd.Utilization(); got.Cmp(big.NewInt(10_000_000)) != 0 {
		t.Errorf("utilization (over) = %s, want 10000000 (100%%)", got)
	}
}

// SupplyRate = borrow_rate × util × (1 − bstop_rate).
func TestSupplyRate(t *testing.T) {
	// borrow 10%, util 50%, bstop 10% → 0.10 × 0.50 × 0.90 = 0.045 = 450000.
	got := SupplyRate(big.NewInt(1_000_000), big.NewInt(5_000_000), 1_000_000)
	if got.Cmp(big.NewInt(450_000)) != 0 {
		t.Errorf("SupplyRate = %s, want 450000", got)
	}
}

// Metrics ties it together: a 50%-utilized reserve at the reference
// config, 10% backstop.
func TestMetrics(t *testing.T) {
	rate := big.NewInt(1_000_000_000_000)
	rd := ReserveData{
		BSupply: big.NewInt(2_000_000_0000000), BRate: rate,
		DSupply: big.NewInt(1_000_000_0000000), DRate: rate,
		IRMod: big.NewInt(1_0000000),
	}
	m := Metrics(rd, interestRefConfig(), 1_000_000)
	if m.UtilizationPct < 49.9 || m.UtilizationPct > 50.1 {
		t.Errorf("util pct = %v, want ~50", m.UtilizationPct)
	}
	if m.BorrowAPR <= 0 || m.SupplyAPR <= 0 || m.SupplyAPR >= m.BorrowAPR {
		t.Errorf("aprs: borrow=%v supply=%v (supply should be 0 < x < borrow)", m.BorrowAPR, m.SupplyAPR)
	}
	if m.SuppliedUnderlying.Sign() <= 0 || m.BorrowedUnderlying.Cmp(m.SuppliedUnderlying) >= 0 {
		t.Errorf("supplied=%s borrowed=%s", m.SuppliedUnderlying, m.BorrowedUnderlying)
	}
}
