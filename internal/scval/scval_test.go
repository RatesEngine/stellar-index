package scval

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
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

// TestAsAddressStrkey_muxed pins the CAP-67 / P23 muxed-account
// strkey encoding. SEP-41 transfers with a destination Muxed
// Account hit this path; pre-fix the decoder tripped
// "unknown ScAddress type 2" and dropped the row.
func TestAsAddressStrkey_muxed(t *testing.T) {
	var ed xdr.Uint256
	m := xdr.MuxedEd25519Account{Id: xdr.Uint64(0x80), Ed25519: ed}
	scAddr := xdr.ScAddress{
		Type:         xdr.ScAddressTypeScAddressTypeMuxedAccount,
		MuxedAccount: &m,
	}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &scAddr}
	got, err := AsAddressStrkey(sv)
	if err != nil {
		t.Fatalf("AsAddressStrkey: %v", err)
	}
	if len(got) == 0 || got[0] != 'M' {
		t.Errorf("got %q, expected M-strkey", got)
	}
}

// TestAsAddressStrkey_claimableBalance pins the CAP-67 / P23
// claimable-balance strkey encoding (B-…) — a SEP-41 transfer
// whose destination is a CB ID must round-trip cleanly.
func TestAsAddressStrkey_claimableBalance(t *testing.T) {
	var h xdr.Hash
	cb := xdr.ClaimableBalanceId{
		Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
		V0:   &h,
	}
	scAddr := xdr.ScAddress{
		Type:               xdr.ScAddressTypeScAddressTypeClaimableBalance,
		ClaimableBalanceId: &cb,
	}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &scAddr}
	got, err := AsAddressStrkey(sv)
	if err != nil {
		t.Fatalf("AsAddressStrkey: %v", err)
	}
	if len(got) == 0 || got[0] != 'B' {
		t.Errorf("got %q, expected B-strkey", got)
	}
}

// TestAsAddressStrkey_liquidityPool pins the CAP-67 / P23 LP
// strkey encoding (L-…). This was the live-r1 cascade-drain
// dry-run failure case on 2026-05-28: every SEP-41 transfer
// targeting an LP destination tripped "unknown ScAddress type 4"
// (LP is type 4 in the SDK enum despite the SEP-41 / strkey doc
// ordering).
func TestAsAddressStrkey_liquidityPool(t *testing.T) {
	var lp xdr.PoolId
	scAddr := xdr.ScAddress{
		Type:            xdr.ScAddressTypeScAddressTypeLiquidityPool,
		LiquidityPoolId: &lp,
	}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &scAddr}
	got, err := AsAddressStrkey(sv)
	if err != nil {
		t.Fatalf("AsAddressStrkey: %v", err)
	}
	if len(got) == 0 || got[0] != 'L' {
		t.Errorf("got %q, expected L-strkey", got)
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

// ─── String encode/decode ────────────────────────────────────────

func TestEncodeString_roundtrip(t *testing.T) {
	// String differs from Symbol: arbitrary bytes allowed (no
	// identifier-character restriction).
	cases := []string{"SoroswapPair", "hello world", "with-dash", ""}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			b64, err := EncodeString(s)
			if err != nil {
				t.Fatalf("EncodeString(%q): %v", s, err)
			}
			sv, err := Parse(b64)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got, err := AsString(sv)
			if err != nil {
				t.Fatalf("AsString: %v", err)
			}
			if got != s {
				t.Errorf("roundtrip: got %q want %q", got, s)
			}
		})
	}
}

func TestAsString_wrongType(t *testing.T) {
	_, err := AsString(symScVal("not-a-string"))
	if !errors.Is(err, ErrScValType) {
		t.Errorf("expected ErrScValType, got %v", err)
	}
}

// ─── U32 / U64 / Bytes ───────────────────────────────────────────

func TestNewU32_AsU32_roundtrip(t *testing.T) {
	for _, v := range []uint32{0, 1, 42, 4_294_967_295} {
		sv := NewU32(v)
		got, err := AsU32(sv)
		if err != nil {
			t.Fatalf("AsU32(%d): %v", v, err)
		}
		if got != v {
			t.Errorf("got %d want %d", got, v)
		}
	}
}

func TestAsU32_wrongType(t *testing.T) {
	_, err := AsU32(u64ScVal(123))
	if !errors.Is(err, ErrScValType) {
		t.Errorf("expected ErrScValType, got %v", err)
	}
}

func TestAsU64_wrongType(t *testing.T) {
	_, err := AsU64(symScVal("nope"))
	if !errors.Is(err, ErrScValType) {
		t.Errorf("expected ErrScValType, got %v", err)
	}
}

func TestAsBytes_roundtrip(t *testing.T) {
	want := []byte{0xde, 0xad, 0xbe, 0xef}
	b := xdr.ScBytes(want)
	sv := xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &b}
	got, err := AsBytes(sv)
	if err != nil {
		t.Fatalf("AsBytes: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("AsBytes = %x, want %x", got, want)
	}
}

func TestAsBytes_wrongType(t *testing.T) {
	_, err := AsBytes(symScVal("not-bytes"))
	if !errors.Is(err, ErrScValType) {
		t.Errorf("expected ErrScValType, got %v", err)
	}
}

// ParseBytes is the raw-bytes twin of Parse — used by decoders
// (e.g. Redstone) whose body is an ScVal::Bytes wrapping an
// inner XDR-encoded ScVal. Verify the round-trip.
func TestParseBytes_roundtrip(t *testing.T) {
	inner := symScVal("inner")
	raw, err := inner.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	got, err := ParseBytes(raw)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	gotSym, err := AsSymbol(got)
	if err != nil {
		t.Fatalf("AsSymbol: %v", err)
	}
	if gotSym != "inner" {
		t.Errorf("got %q, want \"inner\"", gotSym)
	}
}

func TestParseBytes_truncated(t *testing.T) {
	_, err := ParseBytes([]byte{0x00, 0x00})
	if !errors.Is(err, ErrScValDecode) {
		t.Errorf("expected ErrScValDecode, got %v", err)
	}
}

// ─── U128 / U256 ────────────────────────────────────────────────

func TestAsAmountFromU128(t *testing.T) {
	// 2^65: split into (Hi=2, Lo=0). Tests the full unsigned range,
	// past int64.
	hi := uint64(2)
	lo := uint64(0)
	p := xdr.UInt128Parts{Hi: xdr.Uint64(hi), Lo: xdr.Uint64(lo)}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvU128, U128: &p}
	got, err := AsAmountFromU128(sv)
	if err != nil {
		t.Fatalf("AsAmountFromU128: %v", err)
	}
	want := new(big.Int).Lsh(big.NewInt(1), 65)
	if got.BigInt().Cmp(want) != 0 {
		t.Errorf("got %s want %s", got.BigInt(), want)
	}
}

func TestAsAmountFromU128_wrongType(t *testing.T) {
	_, err := AsAmountFromU128(u64ScVal(1))
	if !errors.Is(err, ErrScValType) {
		t.Errorf("expected ErrScValType, got %v", err)
	}
}

func TestAsAmountFromU256(t *testing.T) {
	// 2^130: split as (HiHi=0, HiLo=4, LoHi=0, LoLo=0).
	// 2^130 = 4 * 2^128, so the second word from the top = 4.
	p := xdr.UInt256Parts{
		HiHi: 0, HiLo: 4, LoHi: 0, LoLo: 0,
	}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvU256, U256: &p}
	got, err := AsAmountFromU256(sv)
	if err != nil {
		t.Fatalf("AsAmountFromU256: %v", err)
	}
	want := new(big.Int).Lsh(big.NewInt(1), 130)
	if got.BigInt().Cmp(want) != 0 {
		t.Errorf("got %s want %s", got.BigInt(), want)
	}
}

func TestAsAmountFromU256_wrongType(t *testing.T) {
	_, err := AsAmountFromU256(symScVal("nope"))
	if !errors.Is(err, ErrScValType) {
		t.Errorf("expected ErrScValType, got %v", err)
	}
}

// ─── Vec / Map / DecodeAddressOrSymbol ──────────────────────────

func TestAsVec_normalAndNil(t *testing.T) {
	full := vecScVal([]xdr.ScVal{symScVal("a"), symScVal("b")})
	got, err := AsVec(full)
	if err != nil {
		t.Fatalf("AsVec: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d elts, want 2", len(got))
	}

	// Present-but-nil Vec — must yield empty slice, not nil.
	var nilVec *xdr.ScVec
	emptySV := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &nilVec}
	got, err = AsVec(emptySV)
	if err != nil {
		t.Fatalf("AsVec (nil-inner): %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("nil-inner Vec: got %v, want empty slice", got)
	}
}

func TestAsVec_wrongType(t *testing.T) {
	_, err := AsVec(symScVal("not-vec"))
	if !errors.Is(err, ErrScValType) {
		t.Errorf("expected ErrScValType, got %v", err)
	}
}

func TestAsMap_normalAndNil(t *testing.T) {
	// Map with one entry: { sym "k" -> sym "v" }.
	entries := xdr.ScMap{
		{Key: symScVal("k"), Val: symScVal("v")},
	}
	pmap := &entries
	full := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pmap}
	got, err := AsMap(full)
	if err != nil {
		t.Fatalf("AsMap: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d entries, want 1", len(got))
	}

	// Present-but-nil Map — same empty-not-nil contract as AsVec.
	var nilMap *xdr.ScMap
	emptySV := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &nilMap}
	got, err = AsMap(emptySV)
	if err != nil {
		t.Fatalf("AsMap (nil-inner): %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("nil-inner Map: got %v, want empty slice", got)
	}
}

func TestAsMap_wrongType(t *testing.T) {
	_, err := AsMap(symScVal("not-map"))
	if !errors.Is(err, ErrScValType) {
		t.Errorf("expected ErrScValType, got %v", err)
	}
}

func TestDecodeAddressOrSymbol(t *testing.T) {
	// Symbol form (Reflector's Asset::Other variant).
	got, err := DecodeAddressOrSymbol(symScVal("XLM"))
	if err != nil {
		t.Fatalf("DecodeAddressOrSymbol(symbol): %v", err)
	}
	if got.Symbol != "XLM" || got.Address != "" {
		t.Errorf("symbol case: got %+v", got)
	}

	// Address form — build a contract-typed ScAddress (no need to
	// know a real account; the strkey codec handles encoding).
	var contractID xdr.ContractId
	for i := range contractID {
		contractID[i] = byte(i)
	}
	addr := xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &contractID,
	}
	addrSV := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
	got, err = DecodeAddressOrSymbol(addrSV)
	if err != nil {
		t.Fatalf("DecodeAddressOrSymbol(address): %v", err)
	}
	if got.Address == "" || got.Symbol != "" {
		t.Errorf("address case: got %+v", got)
	}
	// Strkey-encoded contract IDs always start with 'C'.
	if got.Address[0] != 'C' {
		t.Errorf("expected C-prefix strkey, got %q", got.Address)
	}
}

func TestDecodeAddressOrSymbol_wrongType(t *testing.T) {
	_, err := DecodeAddressOrSymbol(u64ScVal(123))
	if !errors.Is(err, ErrScValType) {
		t.Errorf("expected ErrScValType, got %v", err)
	}
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
