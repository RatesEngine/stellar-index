package blend

import (
	"math/big"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

func TestDecodeReserveData(t *testing.T) {
	// Alphabetical-by-symbol order mirrors soroban-sdk's encode path
	// (the decoder is order-independent — it reads by name).
	sv := mapScVal([]xdr.ScMapEntry{
		{Key: symbolScVal("b_rate"), Val: i128ScVal(t, big.NewInt(1_123_456_789_000))},
		{Key: symbolScVal("b_supply"), Val: i128ScVal(t, big.NewInt(5_000_000_0000000))},
		{Key: symbolScVal("backstop_credit"), Val: i128ScVal(t, big.NewInt(517357))},
		{Key: symbolScVal("d_rate"), Val: i128ScVal(t, big.NewInt(1_345_678_123_000))},
		{Key: symbolScVal("d_supply"), Val: i128ScVal(t, big.NewInt(2_000_000_0000000))},
		{Key: symbolScVal("ir_mod"), Val: i128ScVal(t, big.NewInt(1_0000000))},
		{Key: symbolScVal("last_time"), Val: u64ScVal(1_700_000_000)},
	})
	rd, err := DecodeReserveData(sv)
	if err != nil {
		t.Fatalf("DecodeReserveData: %v", err)
	}
	if rd.BRate.Cmp(big.NewInt(1_123_456_789_000)) != 0 {
		t.Errorf("b_rate = %s", rd.BRate)
	}
	if rd.DSupply.Cmp(big.NewInt(2_000_000_0000000)) != 0 {
		t.Errorf("d_supply = %s", rd.DSupply)
	}
	if rd.BackstopCredit.Cmp(big.NewInt(517357)) != 0 {
		t.Errorf("backstop_credit = %s", rd.BackstopCredit)
	}
	if rd.LastTime != 1_700_000_000 {
		t.Errorf("last_time = %d", rd.LastTime)
	}
}

func TestDecodeReserveConfig(t *testing.T) {
	rc, err := DecodeReserveConfig(reserveConfigScVal(t))
	if err != nil {
		t.Fatalf("DecodeReserveConfig: %v", err)
	}
	// Values from the existing reserveConfigScVal helper.
	if rc.Util != 8_000_000 || rc.RBase != 100_000 || rc.ROne != 500_000 ||
		rc.RTwo != 1_000_000 || rc.RThree != 2_000_000 || rc.Reactivity != 50_000 {
		t.Errorf("config rate params = %+v", rc)
	}
	if rc.Decimals != 7 || !rc.Enabled || rc.Index != 3 {
		t.Errorf("config meta = %+v", rc)
	}
}

func TestDecodePoolConfig(t *testing.T) {
	const oracle = "CCYHURAC5VTN2ZU663UUS5F24S4GURDPO4FHZ75JLN5DMLRTLCG44H44"
	sv := mapScVal([]xdr.ScMapEntry{
		{Key: symbolScVal("bstop_rate"), Val: u32ScVal(1_000_000)}, // 0.10
		{Key: symbolScVal("max_positions"), Val: u32ScVal(4)},
		{Key: symbolScVal("min_collateral"), Val: i128ScVal(t, big.NewInt(0))},
		{Key: symbolScVal("oracle"), Val: addressScVal(t, oracle)},
		{Key: symbolScVal("status"), Val: u32ScVal(0)},
	})
	pc, err := DecodePoolConfig(sv)
	if err != nil {
		t.Fatalf("DecodePoolConfig: %v", err)
	}
	if pc.BstopRate != 1_000_000 || pc.Oracle != oracle || pc.MaxPositions != 4 {
		t.Errorf("pool config = %+v", pc)
	}
}
