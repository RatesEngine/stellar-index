package supply_test

import (
	"errors"
	"math/big"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/supply"
)

const (
	usdcClassicKey = "USDC:GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	usdcSACKey     = "CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUOZWS4HG3B5UPHHC2QQA"
)

// supplyWithTotal is a small helper to build a Supply with just the
// fields CrossCheck reads — keeps tests focused on the comparison.
func supplyWithTotal(key string, total int64) supply.Supply {
	return supply.Supply{
		AssetKey:    key,
		TotalSupply: big.NewInt(total),
	}
}

// TestCrossCheck_ExactMatch — equal totals report DivergenceStroops=0
// and WithinTolerance=true.
func TestCrossCheck_ExactMatch(t *testing.T) {
	classic := supplyWithTotal(usdcClassicKey, 1_000_000_000)
	sac := supplyWithTotal(usdcSACKey, 1_000_000_000)

	got, err := supply.CrossCheck(classic, sac)
	if err != nil {
		t.Fatalf("CrossCheck: %v", err)
	}
	if got.DivergenceStroops.Sign() != 0 {
		t.Errorf("DivergenceStroops = %s, want 0", got.DivergenceStroops)
	}
	if !got.WithinTolerance {
		t.Errorf("WithinTolerance = false on exact match, want true")
	}
}

// TestCrossCheck_OneStroopDriftIsTolerated — exactly 1 stroop is
// the documented tolerance; alert MUST NOT fire.
func TestCrossCheck_OneStroopDriftIsTolerated(t *testing.T) {
	classic := supplyWithTotal(usdcClassicKey, 1_000_000_001)
	sac := supplyWithTotal(usdcSACKey, 1_000_000_000)

	got, _ := supply.CrossCheck(classic, sac)
	if got.DivergenceStroops.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("DivergenceStroops = %s, want 1", got.DivergenceStroops)
	}
	if !got.WithinTolerance {
		t.Errorf("1-stroop drift must be tolerated; got WithinTolerance=false")
	}
}

// TestCrossCheck_TwoStroopDriftFires — > tolerance triggers an
// out-of-tolerance result; this is what feeds the alert.
func TestCrossCheck_TwoStroopDriftFires(t *testing.T) {
	classic := supplyWithTotal(usdcClassicKey, 1_000_000_002)
	sac := supplyWithTotal(usdcSACKey, 1_000_000_000)

	got, _ := supply.CrossCheck(classic, sac)
	if got.DivergenceStroops.Cmp(big.NewInt(2)) != 0 {
		t.Errorf("DivergenceStroops = %s, want 2", got.DivergenceStroops)
	}
	if got.WithinTolerance {
		t.Errorf("2-stroop drift must trigger alert; got WithinTolerance=true")
	}
}

// TestCrossCheck_AsymmetricSign — divergence is absolute, regardless
// of which side is larger.
func TestCrossCheck_AsymmetricSign(t *testing.T) {
	// SAC reports a higher total than classic — could happen if
	// the SAC has been minted-into via cross-contract calls the
	// classic indexer hasn't observed yet.
	classic := supplyWithTotal(usdcClassicKey, 1_000_000_000)
	sac := supplyWithTotal(usdcSACKey, 1_000_000_005)

	got, _ := supply.CrossCheck(classic, sac)
	if got.DivergenceStroops.Cmp(big.NewInt(5)) != 0 {
		t.Errorf("DivergenceStroops = %s, want 5", got.DivergenceStroops)
	}
	if got.DivergenceStroops.Sign() < 0 {
		t.Errorf("DivergenceStroops = %s, must be non-negative", got.DivergenceStroops)
	}
	if got.WithinTolerance {
		t.Error("5-stroop drift must trigger alert")
	}
}

// TestCrossCheck_PreservesInputs — ClassicTotal / SACTotal on the
// result are independent copies of the inputs (so log lines and
// dashboards can quote them without re-querying), AND mutating them
// later does not corrupt the originals.
func TestCrossCheck_PreservesInputs(t *testing.T) {
	classic := supplyWithTotal(usdcClassicKey, 100)
	sac := supplyWithTotal(usdcSACKey, 100)

	got, _ := supply.CrossCheck(classic, sac)
	if got.ClassicTotal.Cmp(big.NewInt(100)) != 0 {
		t.Errorf("ClassicTotal = %s, want 100", got.ClassicTotal)
	}
	if got.SACTotal.Cmp(big.NewInt(100)) != 0 {
		t.Errorf("SACTotal = %s, want 100", got.SACTotal)
	}
	// Mutate the result-side copies; original Supply must remain.
	got.ClassicTotal.SetInt64(0)
	got.SACTotal.SetInt64(0)
	if classic.TotalSupply.Cmp(big.NewInt(100)) != 0 {
		t.Errorf("classic.TotalSupply mutated by result mutation")
	}
	if sac.TotalSupply.Cmp(big.NewInt(100)) != 0 {
		t.Errorf("sac.TotalSupply mutated by result mutation")
	}
}

// TestCrossCheck_RejectsNilTotalSupply — defensive: a caller passing
// a zero-value Supply (no TotalSupply) must trip a typed error rather
// than nil-pointer the Sub call.
func TestCrossCheck_RejectsNilTotalSupply(t *testing.T) {
	classic := supply.Supply{AssetKey: usdcClassicKey} // TotalSupply nil
	sac := supplyWithTotal(usdcSACKey, 100)
	_, err := supply.CrossCheck(classic, sac)
	if !errors.Is(err, supply.ErrCrossCheckNilSupply) {
		t.Errorf("err = %v, want ErrCrossCheckNilSupply", err)
	}
	// Also when the SAC side is nil.
	_, err = supply.CrossCheck(supplyWithTotal(usdcClassicKey, 100), supply.Supply{AssetKey: usdcSACKey})
	if !errors.Is(err, supply.ErrCrossCheckNilSupply) {
		t.Errorf("err = %v, want ErrCrossCheckNilSupply (sac-side)", err)
	}
}

// TestCrossCheck_KeysCarriedThroughResult — the result's
// ClassicKey + SACKey echo the inputs so log lines + alert labels
// can identify the asset without separate plumbing.
func TestCrossCheck_KeysCarriedThroughResult(t *testing.T) {
	got, _ := supply.CrossCheck(
		supplyWithTotal(usdcClassicKey, 100),
		supplyWithTotal(usdcSACKey, 100),
	)
	if got.ClassicKey != usdcClassicKey {
		t.Errorf("ClassicKey = %q", got.ClassicKey)
	}
	if got.SACKey != usdcSACKey {
		t.Errorf("SACKey = %q", got.SACKey)
	}
}

// TestCrossCheckTolerance_Value — the documented tolerance is
// exactly 1 stroop per ADR-0011. Pinning the constant guards against
// an accidental nudge.
func TestCrossCheckTolerance_Value(t *testing.T) {
	if supply.CrossCheckTolerance.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("CrossCheckTolerance = %s, want 1 (per ADR-0011)",
			supply.CrossCheckTolerance)
	}
}
