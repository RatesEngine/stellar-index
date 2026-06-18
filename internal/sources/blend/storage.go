package blend

import (
	"fmt"
	"math/big"

	"github.com/StellarIndex/stellar-index/internal/scval"
)

// This file decodes Blend pool CURRENT STATE from Soroban contract
// storage (ADR-0039) — distinct from the event path (decode.go /
// decode_money_market.go). The structs + field names mirror the Blend
// pool contract's `storage.rs` (ReserveData / ReserveConfig /
// PoolConfig); the rate/APY math lives in interest.go.
//
// Soroban `#[contracttype]` structs serialize as an ScVal::Map with
// the field names as Symbol keys, so every field is decoded BY NAME
// (scval.MapField) — resilient to field reordering across contract
// upgrades, the same discipline the event decoders use.
//
// Scales (from the contract): d_rate / b_rate are 12-decimal
// conversion rates; ir_mod / c_factor / l_factor / util / r_* /
// reactivity / bstop_rate are 7-decimal; b_supply / d_supply /
// backstop_credit / supply_cap are in the underlying token's decimals.

// ReserveData is a Blend reserve's per-asset on-chain state
// (PoolDataKey::ResData(asset) → persistent storage).
type ReserveData struct {
	DRate          *big.Int // dToken→underlying rate, 12 decimals
	BRate          *big.Int // bToken→underlying rate, 12 decimals
	IRMod          *big.Int // interest-rate curve modifier, 7 decimals
	BSupply        *big.Int // total bToken supply (underlying decimals)
	DSupply        *big.Int // total dToken supply (underlying decimals)
	BackstopCredit *big.Int // underlying owed to the backstop
	LastTime       uint64   // last block the data updated
}

// ReserveConfig is a Blend reserve's per-asset configuration
// (PoolDataKey::ResConfig(asset) → persistent storage). The interest
// model (interest.go) reads Util / RBase / ROne / RTwo / RThree.
type ReserveConfig struct {
	Index      uint32
	Decimals   uint32
	CFactor    uint32 // collateral factor, 7 decimals
	LFactor    uint32 // liability factor, 7 decimals
	Util       uint32 // target utilization, 7 decimals
	MaxUtil    uint32 // max utilization, 7 decimals
	RBase      uint32 // R0 base rate, 7 decimals
	ROne       uint32 // R1 slope, 7 decimals
	RTwo       uint32 // R2 slope, 7 decimals
	RThree     uint32 // R3 slope, 7 decimals
	Reactivity uint32 // reactivity constant, 7 decimals
	SupplyCap  *big.Int
	Enabled    bool
}

// PoolConfig is the pool-wide config (instance storage, Symbol
// "Config"). BstopRate is the backstop's cut of accrued debt interest
// (7 decimals) — the supply-APR computation needs it.
type PoolConfig struct {
	Oracle        string
	MinCollateral *big.Int
	BstopRate     uint32 // backstop take rate, 7 decimals
	Status        uint32
	MaxPositions  uint32
}

// DecodeReserveData decodes a ReserveData ScVal (the value of a
// ResData(asset) contract_data entry).
func DecodeReserveData(v scval.ScVal) (ReserveData, error) {
	m, err := scval.AsMap(v)
	if err != nil {
		return ReserveData{}, fmt.Errorf("blend: reserve data not a map: %w", err)
	}
	var rd ReserveData
	if rd.DRate, err = i128Field(m, "d_rate"); err != nil {
		return ReserveData{}, err
	}
	if rd.BRate, err = i128Field(m, "b_rate"); err != nil {
		return ReserveData{}, err
	}
	if rd.IRMod, err = i128Field(m, "ir_mod"); err != nil {
		return ReserveData{}, err
	}
	if rd.BSupply, err = i128Field(m, "b_supply"); err != nil {
		return ReserveData{}, err
	}
	if rd.DSupply, err = i128Field(m, "d_supply"); err != nil {
		return ReserveData{}, err
	}
	if rd.BackstopCredit, err = i128Field(m, "backstop_credit"); err != nil {
		return ReserveData{}, err
	}
	lastTime, err := u64Field(m, "last_time")
	if err != nil {
		return ReserveData{}, err
	}
	rd.LastTime = lastTime
	return rd, nil
}

// DecodeReserveConfig decodes a ReserveConfig ScVal (the value of a
// ResConfig(asset) contract_data entry).
func DecodeReserveConfig(v scval.ScVal) (ReserveConfig, error) {
	m, err := scval.AsMap(v)
	if err != nil {
		return ReserveConfig{}, fmt.Errorf("blend: reserve config not a map: %w", err)
	}
	var rc ReserveConfig
	for _, f := range []struct {
		name string
		dst  *uint32
	}{
		{"index", &rc.Index},
		{"decimals", &rc.Decimals},
		{"c_factor", &rc.CFactor},
		{"l_factor", &rc.LFactor},
		{"util", &rc.Util},
		{"max_util", &rc.MaxUtil},
		{"r_base", &rc.RBase},
		{"r_one", &rc.ROne},
		{"r_two", &rc.RTwo},
		{"r_three", &rc.RThree},
		{"reactivity", &rc.Reactivity},
	} {
		v, err := u32Field(m, f.name)
		if err != nil {
			return ReserveConfig{}, err
		}
		*f.dst = v
	}
	if rc.SupplyCap, err = i128Field(m, "supply_cap"); err != nil {
		return ReserveConfig{}, err
	}
	enabled, err := boolField(m, "enabled")
	if err != nil {
		return ReserveConfig{}, err
	}
	rc.Enabled = enabled
	return rc, nil
}

// DecodePoolConfig decodes a PoolConfig ScVal (instance storage,
// Symbol "Config").
func DecodePoolConfig(v scval.ScVal) (PoolConfig, error) {
	m, err := scval.AsMap(v)
	if err != nil {
		return PoolConfig{}, fmt.Errorf("blend: pool config not a map: %w", err)
	}
	var pc PoolConfig
	oracleV, err := scval.MustMapField(m, "oracle")
	if err != nil {
		return PoolConfig{}, err
	}
	if pc.Oracle, err = scval.AsAddressStrkey(oracleV); err != nil {
		return PoolConfig{}, fmt.Errorf("blend: pool config oracle: %w", err)
	}
	if pc.MinCollateral, err = i128Field(m, "min_collateral"); err != nil {
		return PoolConfig{}, err
	}
	if pc.BstopRate, err = u32Field(m, "bstop_rate"); err != nil {
		return PoolConfig{}, err
	}
	if pc.Status, err = u32Field(m, "status"); err != nil {
		return PoolConfig{}, err
	}
	if pc.MaxPositions, err = u32Field(m, "max_positions"); err != nil {
		return PoolConfig{}, err
	}
	return pc, nil
}

func i128Field(m []scval.ScMapEntry, name string) (*big.Int, error) {
	v, err := scval.MustMapField(m, name)
	if err != nil {
		return nil, err
	}
	amt, err := scval.AsAmountFromI128(v)
	if err != nil {
		return nil, fmt.Errorf("blend: field %q: %w", name, err)
	}
	return amt.BigInt(), nil
}

func u32Field(m []scval.ScMapEntry, name string) (uint32, error) {
	v, err := scval.MustMapField(m, name)
	if err != nil {
		return 0, err
	}
	n, err := scval.AsU32(v)
	if err != nil {
		return 0, fmt.Errorf("blend: field %q: %w", name, err)
	}
	return n, nil
}

func u64Field(m []scval.ScMapEntry, name string) (uint64, error) {
	v, err := scval.MustMapField(m, name)
	if err != nil {
		return 0, err
	}
	n, err := scval.AsU64(v)
	if err != nil {
		return 0, fmt.Errorf("blend: field %q: %w", name, err)
	}
	return n, nil
}

func boolField(m []scval.ScMapEntry, name string) (bool, error) {
	v, err := scval.MustMapField(m, name)
	if err != nil {
		return false, err
	}
	b, err := scval.AsBool(v)
	if err != nil {
		return false, fmt.Errorf("blend: field %q: %w", name, err)
	}
	return b, nil
}
