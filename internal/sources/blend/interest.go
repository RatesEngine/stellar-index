package blend

import "math/big"

// Blend interest-rate model + reserve arithmetic, ported faithfully
// from the pool contract's `interest.rs` (calc_accrual) and
// `reserve.rs` (utilization / total_supply / total_liabilities). All
// fixed-point ops mirror soroban-fixed-point-math's mul/div ceil/floor
// rounding so the Go output matches the on-chain values bit-for-bit
// (verified against the contract's own unit-test vectors in
// interest_test.go).
//
// Scales: SCALAR_7 (1e7) for rates/factors/utilization; SCALAR_12
// (1e12) for b_rate/d_rate conversion rates.

var (
	scalar7  = big.NewInt(10_000_000)        // 1e7
	scalar12 = big.NewInt(1_000_000_000_000) // 1e12
	util95   = big.NewInt(9_500_000)         // 0.95 in 7 decimals
	util05   = big.NewInt(500_000)           // 0.05 in 7 decimals
)

// fixedMulFloor = floor(a*b / scalar).
func fixedMulFloor(a, b, scalar *big.Int) *big.Int {
	n := new(big.Int).Mul(a, b)
	return n.Quo(n, scalar) // big.Int Quo truncates toward zero; inputs are non-negative here
}

// fixedMulCeil = ceil(a*b / scalar).
func fixedMulCeil(a, b, scalar *big.Int) *big.Int {
	n := new(big.Int).Mul(a, b)
	return ceilDiv(n, scalar)
}

// fixedDivCeil = ceil(a*scalar / b).
func fixedDivCeil(a, b, scalar *big.Int) *big.Int {
	n := new(big.Int).Mul(a, scalar)
	return ceilDiv(n, b)
}

// ceilDiv = ceil(n / d) for non-negative n, positive d.
func ceilDiv(n, d *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(n, d, new(big.Int))
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

// SuppliedUnderlying returns the reserve's total supplied amount in the
// underlying token's smallest unit (reserve.rs::total_supply =
// b_supply.fixed_mul_floor(b_rate, SCALAR_12)).
func (rd ReserveData) SuppliedUnderlying() *big.Int {
	return fixedMulFloor(rd.BSupply, rd.BRate, scalar12)
}

// BorrowedUnderlying returns the reserve's total borrowed amount in the
// underlying token's smallest unit (reserve.rs::total_liabilities =
// d_supply.fixed_mul_ceil(d_rate, SCALAR_12)).
func (rd ReserveData) BorrowedUnderlying() *big.Int {
	return fixedMulCeil(rd.DSupply, rd.DRate, scalar12)
}

// Utilization returns the reserve utilization in 7 decimals, exactly
// per reserve.rs::utilization: liabilities/supply, 0 when no debt,
// capped at SCALAR_7 (100%) when liabilities ≥ supply.
func (rd ReserveData) Utilization() *big.Int {
	liabilities := rd.BorrowedUnderlying()
	supply := rd.SuppliedUnderlying()
	if liabilities.Sign() == 0 {
		return big.NewInt(0)
	}
	if liabilities.Cmp(supply) >= 0 {
		return new(big.Int).Set(scalar7)
	}
	return fixedDivCeil(liabilities, supply, scalar7)
}

// BorrowRate returns the current annual borrow interest rate (cur_ir)
// in 7 decimals, ported from interest.rs::calc_accrual's rate branch.
// util is 7-decimal utilization (from ReserveData.Utilization); irMod
// is the reserve's current interest-rate modifier (ReserveData.IRMod,
// 7 decimals).
func (rc ReserveConfig) BorrowRate(util, irMod *big.Int) *big.Int {
	rBase := big.NewInt(int64(rc.RBase))
	rOne := big.NewInt(int64(rc.ROne))
	rTwo := big.NewInt(int64(rc.RTwo))
	rThree := big.NewInt(int64(rc.RThree))
	target := big.NewInt(int64(rc.Util))

	switch {
	case util.Cmp(target) <= 0:
		utilScalar := fixedDivCeil(util, target, scalar7)
		baseRate := new(big.Int).Add(fixedMulCeil(utilScalar, rOne, scalar7), rBase)
		return fixedMulCeil(baseRate, irMod, scalar7)
	case util.Cmp(util95) <= 0:
		denom := new(big.Int).Sub(util95, target)
		utilScalar := fixedDivCeil(new(big.Int).Sub(util, target), denom, scalar7)
		baseRate := fixedMulCeil(utilScalar, rTwo, scalar7)
		baseRate.Add(baseRate, rOne)
		baseRate.Add(baseRate, rBase)
		return fixedMulCeil(baseRate, irMod, scalar7)
	default:
		utilScalar := fixedDivCeil(new(big.Int).Sub(util, util95), util05, scalar7)
		extraRate := fixedMulCeil(utilScalar, rThree, scalar7)
		sum := new(big.Int).Add(rTwo, rOne)
		sum.Add(sum, rBase)
		intersection := fixedMulCeil(irMod, sum, scalar7)
		return new(big.Int).Add(extraRate, intersection)
	}
}

// SupplyRate returns the supplier interest rate in 7 decimals: the
// borrow rate scaled by utilization (only borrowed capital earns
// interest) minus the backstop's take. bstopRate is 7-decimal.
//
//	supply_rate = borrow_rate × util × (1 − bstop_rate)
func SupplyRate(borrowRate, util *big.Int, bstopRate uint32) *big.Int {
	gross := fixedMulFloor(borrowRate, util, scalar7)
	keep := new(big.Int).Sub(scalar7, big.NewInt(int64(bstopRate)))
	if keep.Sign() < 0 {
		keep = big.NewInt(0)
	}
	return fixedMulFloor(gross, keep, scalar7)
}

// rate7ToFloat converts a 7-decimal fixed-point rate to a float
// fraction (e.g. 537711 → 0.0537711).
func rate7ToFloat(rate *big.Int) float64 {
	f := new(big.Float).SetInt(rate)
	f.Quo(f, new(big.Float).SetInt(scalar7))
	v, _ := f.Float64()
	return v
}

// ReserveMetrics bundles the derived current-state metrics for one
// reserve, ready for the API.
type ReserveMetrics struct {
	SuppliedUnderlying *big.Int // smallest unit of the underlying token
	BorrowedUnderlying *big.Int
	UtilizationPct     float64 // 0..100
	BorrowAPR          float64 // fraction (0.05 = 5%)
	SupplyAPR          float64
}

// Metrics derives the per-reserve current-state metrics from the
// decoded reserve state + the pool's backstop take rate.
func Metrics(rd ReserveData, rc ReserveConfig, bstopRate uint32) ReserveMetrics {
	util := rd.Utilization()
	borrowRate := rc.BorrowRate(util, rd.IRMod)
	supplyRate := SupplyRate(borrowRate, util, bstopRate)
	return ReserveMetrics{
		SuppliedUnderlying: rd.SuppliedUnderlying(),
		BorrowedUnderlying: rd.BorrowedUnderlying(),
		UtilizationPct:     rate7ToFloat(util) * 100,
		BorrowAPR:          rate7ToFloat(borrowRate),
		SupplyAPR:          rate7ToFloat(supplyRate),
	}
}
