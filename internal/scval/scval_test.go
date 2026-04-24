package scval

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// ─── Symbol encode + decode ──────────────────────────────────────

func TestEncodeSymbol_roundtrip(t *testing.T) {
	cases := []string{"REFLECTOR", "update", "swap", "sync", "a", "a_b_1"}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			b64, err := EncodeSymbol(s)
			if err != nil {
				t.Fatalf("EncodeSymbol(%q): %v", s, err)
			}
			sv, err := Parse(b64)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got, err := AsSymbol(sv)
			if err != nil {
				t.Fatalf("AsSymbol: %v", err)
			}
			if got != s {
				t.Errorf("roundtrip: got %q want %q", got, s)
			}
		})
	}
}

func TestEncodeSymbol_rejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"too long (33)", "abcdefghijklmnopqrstuvwxyz0123456"},
		{"non-ascii-ident", "has-dash"},
		{"space", "has space"},
		{"unicode", "héllo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := EncodeSymbol(tc.in); err == nil {
				t.Errorf("EncodeSymbol(%q) unexpectedly accepted", tc.in)
			}
		})
	}
}

// Golden regression — the exact base64 bytes for Symbol("REFLECTOR")
// and Symbol("update") recorded once so an SDK upgrade that shifts
// the wire encoding surfaces as a test failure here before it ships.
// Re-generate via: `go test ./internal/scval/ -run TestGolden -v`.
func TestGolden_symbolBytes(t *testing.T) {
	cases := []struct {
		sym  string
		want string // base64
	}{
		// SCVal::Symbol("REFLECTOR"):
		//   disc=15 (Symbol, u32=0x0000000f)
		//   len=9   (u32=0x00000009)
		//   bytes  "REFLECTOR" padded to 4-byte boundary (3 pad bytes)
		//   → 20 bytes raw = base64 "AAAADwAAAAlSRUZMRUNUT1IAAAA="
		// "REFLECTOR" — 9 bytes → XDR pads to 12 → 4-byte disc + 4-byte len + 12 bytes = 20 bytes raw.
		{"REFLECTOR", "AAAADwAAAAlSRUZMRUNUT1IAAAA="},
		// "update" — 6 bytes → XDR pads to 8 → 4+4+8 = 16 bytes raw.
		{"update", "AAAADwAAAAZ1cGRhdGUAAA=="},
	}
	for _, tc := range cases {
		t.Run(tc.sym, func(t *testing.T) {
			got, err := EncodeSymbol(tc.sym)
			if err != nil {
				t.Fatalf("EncodeSymbol: %v", err)
			}
			if got != tc.want {
				t.Errorf("EncodeSymbol(%q)\n got:  %s\n want: %s", tc.sym, got, tc.want)
			}
		})
	}
}

// ─── I128 / U128 roundtrip ───────────────────────────────────────

func TestAsAmountFromI128(t *testing.T) {
	// The KALIEN-incident boundary: a value large enough that
	// truncating to int64(parts.Lo) would drop the hi word. Per
	// ADR-0003, we must not.
	big1 := new(big.Int)
	big1.SetString("123456789012345678901234567890", 10) // ~ 2^96, fits in i128
	bigNeg := new(big.Int).Neg(big1)

	cases := []struct {
		name string
		v    *big.Int
	}{
		{"zero", big.NewInt(0)},
		{"small pos", big.NewInt(42)},
		{"small neg", big.NewInt(-7)},
		{"int64 max", new(big.Int).SetInt64(1<<62 - 1)},
		{"above int64 (hi!=0)", big1},
		{"large neg", bigNeg},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sv := i128ScVal(tc.v)
			got, err := AsAmountFromI128(sv)
			if err != nil {
				t.Fatalf("AsAmountFromI128: %v", err)
			}
			if got.BigInt().Cmp(tc.v) != 0 {
				t.Errorf("got %s want %s", got.BigInt(), tc.v)
			}
		})
	}
}

func TestAsAmountFromI128_wrongType(t *testing.T) {
	sym := xdr.ScSymbol("not-an-i128")
	sv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
	_, err := AsAmountFromI128(sv)
	if !errors.Is(err, ErrScValType) {
		t.Errorf("expected ErrScValType, got %v", err)
	}
}

// ─── Address encoding ────────────────────────────────────────────

func TestAsAddressStrkey_account(t *testing.T) {
	// Valid pubnet-format account ID — all zeros. strkey encode
	// produces a legitimate G… address.
	var pub xdr.Uint256
	// zero-valued pub — encoded account ID is deterministic.
	accID := xdr.AccountId{
		Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
		Ed25519: &pub,
	}
	scAddr := xdr.ScAddress{
		Type:      xdr.ScAddressTypeScAddressTypeAccount,
		AccountId: &accID,
	}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &scAddr}
	got, err := AsAddressStrkey(sv)
	if err != nil {
		t.Fatalf("AsAddressStrkey: %v", err)
	}
	if len(got) != 56 || got[0] != 'G' {
		t.Errorf("got %q, expected 56-char G-strkey", got)
	}
	if !canonical.IsAccountID(got) {
		t.Errorf("got %q doesn't pass canonical.IsAccountID", got)
	}
}

func TestAsAddressStrkey_contract(t *testing.T) {
	var cid xdr.ContractId
	scAddr := xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &cid,
	}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &scAddr}
	got, err := AsAddressStrkey(sv)
	if err != nil {
		t.Fatalf("AsAddressStrkey: %v", err)
	}
	if len(got) != 56 || got[0] != 'C' {
		t.Errorf("got %q, expected 56-char C-strkey", got)
	}
	if !canonical.IsContractID(got) {
		t.Errorf("got %q doesn't pass canonical.IsContractID", got)
	}
}

// ─── Map-field lookup ────────────────────────────────────────────

func TestMapField_byName(t *testing.T) {
	// Construct an ScMap with keys: "price" (i128), "timestamp" (u64).
	// Confirm name-based lookup retrieves each correctly and that
	// missing keys return false.
	entries := []xdr.ScMapEntry{
		{
			Key: symScVal("price"),
			Val: i128ScVal(big.NewInt(42)),
		},
		{
			Key: symScVal("timestamp"),
			Val: u64ScVal(1_745_000_000),
		},
	}

	p, ok := MapField(entries, "price")
	if !ok {
		t.Fatal("MapField(price) not found")
	}
	amt, err := AsAmountFromI128(p)
	if err != nil || amt.BigInt().Cmp(big.NewInt(42)) != 0 {
		t.Errorf("price decode: %v %v", amt, err)
	}

	ts, ok := MapField(entries, "timestamp")
	if !ok {
		t.Fatal("MapField(timestamp) not found")
	}
	u, err := AsU64(ts)
	if err != nil || u != 1_745_000_000 {
		t.Errorf("ts decode: %v %v", u, err)
	}

	_, ok = MapField(entries, "absent")
	if ok {
		t.Errorf("absent key unexpectedly found")
	}
}

func TestMustMapField_missingIsError(t *testing.T) {
	_, err := MustMapField(nil, "nope")
	if !errors.Is(err, ErrScValMissingKey) {
		t.Errorf("expected ErrScValMissingKey, got %v", err)
	}
}

// ─── Vec + tuple shape ───────────────────────────────────────────

func TestAsTupleN(t *testing.T) {
	// Soroban "tuples" are Vecs at runtime. Vec<(Address, i128)>
	// — the exact Reflector body shape — is a Vec where each
	// element is itself a 2-element Vec.
	pair := vecScVal([]xdr.ScVal{
		symScVal("BTC"),
		i128ScVal(big.NewInt(100_000_000_000_000)), // 1.0 at E14
	})
	elts, err := AsTupleN(pair, 2)
	if err != nil {
		t.Fatalf("AsTupleN(2): %v", err)
	}
	sym, err := AsSymbol(elts[0])
	if err != nil {
		t.Fatalf("tuple[0] as symbol: %v", err)
	}
	if sym != "BTC" {
		t.Errorf("tuple[0] = %q want BTC", sym)
	}
	amt, err := AsAmountFromI128(elts[1])
	if err != nil {
		t.Fatalf("tuple[1] as i128: %v", err)
	}
	want := big.NewInt(100_000_000_000_000)
	if amt.BigInt().Cmp(want) != 0 {
		t.Errorf("tuple[1] = %s want %s", amt, want)
	}
}

func TestAsTupleN_wrongArity(t *testing.T) {
	vec := vecScVal([]xdr.ScVal{symScVal("x"), symScVal("y"), symScVal("z")})
	if _, err := AsTupleN(vec, 2); !errors.Is(err, ErrScValType) {
		t.Errorf("expected ErrScValType on arity mismatch, got %v", err)
	}
}

// ─── Parse on bad input ──────────────────────────────────────────

func TestParse_badBase64(t *testing.T) {
	_, err := Parse("not-base64!!!")
	if !errors.Is(err, ErrScValDecode) {
		t.Errorf("expected ErrScValDecode, got %v", err)
	}
}

func TestParse_truncated(t *testing.T) {
	// Valid-looking base64, but too short to be a full SCVal.
	_, err := Parse(base64.StdEncoding.EncodeToString([]byte{0x00, 0x00}))
	if !errors.Is(err, ErrScValDecode) {
		t.Errorf("expected ErrScValDecode, got %v", err)
	}
}

// ─── Test helpers: build well-formed ScVals for fixtures ────────

func symScVal(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func u64ScVal(v uint64) xdr.ScVal {
	u := xdr.Uint64(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &u}
}

func i128ScVal(n *big.Int) xdr.ScVal {
	// Split into sign-aware hi (int64) + lo (uint64).
	hi, lo := splitBigInt128(n)
	p := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p}
}

func vecScVal(elts []xdr.ScVal) xdr.ScVal {
	sv := xdr.ScVec(elts)
	pv := &sv
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pv}
}

// splitBigInt128 decomposes a 128-bit-fitting big.Int into
// (hi int64, lo uint64) in two's-complement form — the inverse of
// canonical.FromInt128Parts.
func splitBigInt128(n *big.Int) (hi int64, lo uint64) {
	twoTo64 := new(big.Int).Lsh(big.NewInt(1), 64)
	mask64 := new(big.Int).Sub(twoTo64, big.NewInt(1))

	if n.Sign() >= 0 {
		loBig := new(big.Int).And(n, mask64)
		hiBig := new(big.Int).Rsh(n, 64)
		return hiBig.Int64(), loBig.Uint64()
	}
	// Negative: encode as two's complement across 128 bits.
	// Equivalent to: add 2^128 then split.
	twoTo128 := new(big.Int).Lsh(big.NewInt(1), 128)
	u := new(big.Int).Add(twoTo128, n) // two's complement 128-bit
	loBig := new(big.Int).And(u, mask64)
	hiBig := new(big.Int).Rsh(u, 64)
	return int64(hiBig.Uint64()), loBig.Uint64()
}
