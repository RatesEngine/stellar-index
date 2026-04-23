package canonical_test

import (
	"encoding/json"
	"math/big"
	"testing"
	"time"

	c "github.com/RatesEngine/rates-engine/internal/canonical"
)

func validOracle() c.OracleUpdate {
	// Reflector Pulse shape: price at 14 decimals, i128 payload.
	// Value here: 12.42 USDC per XLM = 1242000000000000 (at 10^14 scale).
	// We use the real USDC classic asset as the Quote so the fixture
	// JSON-round-trips; the separate fiat-sentinel test exercises the
	// placeholder path.
	v, _ := new(big.Int).SetString("1242000000000000", 10)
	return c.OracleUpdate{
		Source:     "reflector-dex",
		ContractID: "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA",
		Ledger:     52_430_001,
		TxHash:     goodTxHash,
		OpIndex:    0,
		Timestamp:  time.Unix(1745000000, 0).UTC(),
		Asset:      c.NativeAsset(),
		Quote:      mustClassic("USDC", usdcIssuer),
		Price:      c.NewAmount(v),
		Decimals:   14,
		Confidence: 0.95,
	}
}

func TestOracle_ID(t *testing.T) {
	u := validOracle()
	want := "reflector-dex:52430001:" + goodTxHash + ":0"
	if got := u.ID(); got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

func TestOracle_Validate_happy(t *testing.T) {
	if err := validOracle().Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestOracle_Validate_errors(t *testing.T) {
	cases := map[string]func(*c.OracleUpdate){
		"empty source":      func(u *c.OracleUpdate) { u.Source = "" },
		"short tx_hash":     func(u *c.OracleUpdate) { u.TxHash = "cafe" },
		"bad hex tx_hash":   func(u *c.OracleUpdate) { u.TxHash = "z" + goodTxHash[1:] },
		"zero timestamp":    func(u *c.OracleUpdate) { u.Timestamp = time.Time{} },
		"bad asset":         func(u *c.OracleUpdate) { u.Asset = c.Asset{Type: "weird"} },
		"zero price":        func(u *c.OracleUpdate) { u.Price = c.NewAmount(big.NewInt(0)) },
		"neg price":         func(u *c.OracleUpdate) { u.Price = c.NewAmount(big.NewInt(-1)) },
		"too many decimals": func(u *c.OracleUpdate) { u.Decimals = 40 },
		"negative conf":     func(u *c.OracleUpdate) { u.Confidence = -0.1 },
		"conf > 1":          func(u *c.OracleUpdate) { u.Confidence = 1.5 },
		// Observer, when present, MUST be a valid G-strkey.
		"short observer":    func(u *c.OracleUpdate) { u.Observer = "GSHORT" },
		"bad observer":      func(u *c.OracleUpdate) { u.Observer = "NOTG" + "A" + "BCDEFGHIJKLMNOPQRSTUVWXYZ234567ABCDEFGHIJKLMNOPQRSTUVW" },
		// ContractID, when present, MUST be a valid C-strkey.
		"bad contract_id":   func(u *c.OracleUpdate) { u.ContractID = "not-a-c-key" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			u := validOracle()
			mutate(&u)
			if err := u.Validate(); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestOracle_FiatQuoteAccepted(t *testing.T) {
	// Per ADR-0010 fiat is a first-class AssetType variant, not a
	// sentinel. The USD fiat quote should validate cleanly via the
	// ordinary Asset.Validate path.
	usd, err := c.NewFiatAsset("USD")
	if err != nil {
		t.Fatalf("NewFiatAsset: %v", err)
	}
	u := validOracle()
	u.Quote = usd
	if err := u.Validate(); err != nil {
		t.Fatalf("USD fiat quote rejected: %v", err)
	}
	// Unknown fiat codes still rejected (allow-list in ADR-0010).
	_, err = c.NewFiatAsset("XYZ")
	if err == nil {
		t.Fatal("unknown fiat code should have been rejected")
	}
	// And a code-only classic asset (the former sentinel shape) is
	// now legitimately invalid.
	u.Quote = c.Asset{Type: c.AssetClassic, Code: "USD"}
	if err := u.Validate(); err == nil {
		t.Fatal("classic-with-empty-issuer should fail after ADR-0010")
	}
}

func TestOracle_PriceFloat(t *testing.T) {
	u := validOracle()
	got := u.PriceFloat()
	want, _ := new(big.Float).SetString("12.42")
	// big.Float comparison: diff should round to ~0 at 10^-10 precision.
	diff := new(big.Float).Sub(got, want)
	diff.Abs(diff)
	if diff.Cmp(big.NewFloat(1e-10)) > 0 {
		t.Fatalf("PriceFloat = %s, want 12.42", got.Text('f', 10))
	}

	// Zero decimals path returns the raw big.Int as a float.
	u.Decimals = 0
	raw := u.PriceFloat()
	if raw.Cmp(new(big.Float).SetInt(u.Price.BigInt())) != 0 {
		t.Fatal("PriceFloat(decimals=0) should equal raw price as float")
	}
}

func TestOracle_Equal_identityOnly(t *testing.T) {
	a := validOracle()
	b := validOracle()
	if !a.Equal(b) {
		t.Fatal("identical updates should be equal")
	}
	b.Confidence = 0.01
	b.Observer = "GRELAYER..."
	if !a.Equal(b) {
		t.Fatal("observational fields shouldn't affect identity")
	}
	b.OpIndex = 1
	if a.Equal(b) {
		t.Fatal("OpIndex difference should break identity")
	}
}

func TestOracle_JSON_roundTrip(t *testing.T) {
	u := validOracle()
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	var got c.OracleUpdate
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Equal(u) {
		t.Fatalf("identity lost: %+v", got)
	}
	if got.Price.Cmp(u.Price) != 0 {
		t.Fatalf("price lost precision: got %s, want %s", got.Price, u.Price)
	}
	if got.Decimals != u.Decimals {
		t.Errorf("decimals mismatch: %d vs %d", got.Decimals, u.Decimals)
	}
}

func TestOracle_JSON_i128Scale(t *testing.T) {
	// 128-bit-scale price preserved through JSON — the same
	// invariant our Trade tests enforce, now extended to OracleUpdate.
	u := validOracle()
	big128, ok := new(big.Int).SetString("340282366920938463463374607431768211455", 10) // 2^128 - 1
	if !ok {
		t.Fatal("bad test data")
	}
	u.Price = c.NewAmount(big128)

	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	var got c.OracleUpdate
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Price.String() != "340282366920938463463374607431768211455" {
		t.Fatalf("128-bit value lost: got %s", got.Price.String())
	}
}
