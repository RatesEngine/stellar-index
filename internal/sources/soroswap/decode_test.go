package soroswap

import (
	"encoding/base64"
	"math/big"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Decoder tests using SDK-encoded fixtures. Complements
// real_fixture_test.go (live mainnet captures): this file covers
// shapes that are hard to provoke on mainnet — large negative
// i128s, missing map fields, wrong top-level SCVal kind,
// contract-version drift scenarios.
//
// Capturing new_pair events from the factory requires waiting for
// someone to deploy a pair on mainnet (infrequent). Until a real
// fixture lands in test/fixtures/soroswap/<wasm_hash>/new_pair_*.json,
// the new_pair decoder is covered exclusively by the SDK-encoded
// fixture below. TODO: refresh from real capture when available.

// ─── SDK-encode helpers for building well-formed fixtures ────────

func symbol(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func i128(n *big.Int) xdr.ScVal {
	hi, lo := splitBigInt128(n)
	p := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p}
}

func scMap(entries ...xdr.ScMapEntry) xdr.ScVal {
	m := xdr.ScMap(entries)
	pm := &m
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}
}

// makeContractStrkey builds a valid C-strkey from a 32-byte seed
// (the first byte of seed fills the raw contract id for uniqueness;
// checksum is computed by strkey.Encode).
func makeContractStrkey(t *testing.T, seedByte byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seedByte
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

func contractAddrFromStrkey(t *testing.T, strk string) xdr.ScVal {
	t.Helper()
	var cid xdr.ContractId
	raw, err := strkey.Decode(strkey.VersionByteContract, strk)
	if err != nil {
		t.Fatalf("strkey.Decode(%q): %v", strk, err)
	}
	copy(cid[:], raw)
	scAddr := xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &cid,
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &scAddr}
}

func b64(t *testing.T, sv xdr.ScVal) string {
	t.Helper()
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func splitBigInt128(n *big.Int) (hi int64, lo uint64) {
	twoTo64 := new(big.Int).Lsh(big.NewInt(1), 64)
	mask64 := new(big.Int).Sub(twoTo64, big.NewInt(1))
	if n.Sign() >= 0 {
		loBig := new(big.Int).And(n, mask64)
		hiBig := new(big.Int).Rsh(n, 64)
		return hiBig.Int64(), loBig.Uint64()
	}
	twoTo128 := new(big.Int).Lsh(big.NewInt(1), 128)
	u := new(big.Int).Add(twoTo128, n)
	loBig := new(big.Int).And(u, mask64)
	hiBig := new(big.Int).Rsh(u, 64)
	return int64(hiBig.Uint64()), loBig.Uint64()
}

// ─── SwapEvent body decoder ──────────────────────────────────────

func TestSdkDecodeSwapAmounts_happy(t *testing.T) {
	// All four amounts populated, like a real pair swap body.
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("amount_0_in"), Val: i128(big.NewInt(123_456))},
		xdr.ScMapEntry{Key: symbol("amount_0_out"), Val: i128(big.NewInt(0))},
		xdr.ScMapEntry{Key: symbol("amount_1_in"), Val: i128(big.NewInt(0))},
		xdr.ScMapEntry{Key: symbol("amount_1_out"), Val: i128(big.NewInt(42))},
		xdr.ScMapEntry{Key: symbol("to"), Val: contractAddrFromStrkey(t, makeContractStrkey(t, 0x01))},
	))
	out, err := sdkDecodeSwapAmounts(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Amount0In.BigInt().Cmp(big.NewInt(123_456)) != 0 {
		t.Errorf("amount_0_in = %s", out.Amount0In)
	}
	if out.Amount1Out.BigInt().Cmp(big.NewInt(42)) != 0 {
		t.Errorf("amount_1_out = %s", out.Amount1Out)
	}
}

func TestSdkDecodeSwapAmounts_largeI128(t *testing.T) {
	// Amount above int64 range (ADR-0003 boundary). Catches the
	// classic bug where hi-word truncation drops significant bits.
	big1 := new(big.Int)
	big1.SetString("123456789012345678901234567890", 10) // > 2^96
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("amount_0_in"), Val: i128(big1)},
		xdr.ScMapEntry{Key: symbol("amount_0_out"), Val: i128(big.NewInt(0))},
		xdr.ScMapEntry{Key: symbol("amount_1_in"), Val: i128(big.NewInt(0))},
		xdr.ScMapEntry{Key: symbol("amount_1_out"), Val: i128(big.NewInt(1))},
	))
	out, err := sdkDecodeSwapAmounts(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Amount0In.BigInt().Cmp(big1) != 0 {
		t.Errorf("large i128 wrong: got %s want %s", out.Amount0In, big1)
	}
}

func TestSdkDecodeSwapAmounts_missingField(t *testing.T) {
	// Drop amount_1_out from the map — must surface missing-key.
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("amount_0_in"), Val: i128(big.NewInt(1))},
		xdr.ScMapEntry{Key: symbol("amount_0_out"), Val: i128(big.NewInt(0))},
		xdr.ScMapEntry{Key: symbol("amount_1_in"), Val: i128(big.NewInt(0))},
	))
	_, err := sdkDecodeSwapAmounts(body)
	if err == nil {
		t.Fatal("expected error on missing amount_1_out")
	}
}

func TestSdkDecodeSwapAmounts_wrongTopKind(t *testing.T) {
	// Body is an i128, not a Map — schema violation.
	body := b64(t, i128(big.NewInt(42)))
	_, err := sdkDecodeSwapAmounts(body)
	if err == nil {
		t.Fatal("expected error on non-Map body")
	}
}

// ─── NewPairEvent body decoder ───────────────────────────────────

func TestSdkDecodeNewPair_happy(t *testing.T) {
	// Build a NewPairEvent-shaped Map. Three distinct C-strkeys —
	// generated with valid checksums via strkey.Encode so they
	// round-trip through sdkDecodeNewPair's strkey.Decode.
	token0 := makeContractStrkey(t, 0x10)
	token1 := makeContractStrkey(t, 0x11)
	pair := makeContractStrkey(t, 0x20)

	npL := xdr.Uint32(7)
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("new_pairs_length"), Val: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &npL}},
		xdr.ScMapEntry{Key: symbol("pair"), Val: contractAddrFromStrkey(t, pair)},
		xdr.ScMapEntry{Key: symbol("token_0"), Val: contractAddrFromStrkey(t, token0)},
		xdr.ScMapEntry{Key: symbol("token_1"), Val: contractAddrFromStrkey(t, token1)},
	))
	fields, err := sdkDecodeNewPair(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if fields.Pair != pair {
		t.Errorf("pair = %q want %q", fields.Pair, pair)
	}
	if fields.Token0.ContractID != token0 {
		t.Errorf("token_0 = %q want %q", fields.Token0.ContractID, token0)
	}
	if fields.Token1.ContractID != token1 {
		t.Errorf("token_1 = %q want %q", fields.Token1.ContractID, token1)
	}
}

func TestSdkDecodeNewPair_missingPair(t *testing.T) {
	token0 := makeContractStrkey(t, 0x30)
	token1 := makeContractStrkey(t, 0x31)

	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("token_0"), Val: contractAddrFromStrkey(t, token0)},
		xdr.ScMapEntry{Key: symbol("token_1"), Val: contractAddrFromStrkey(t, token1)},
	))
	_, err := sdkDecodeNewPair(body)
	if err == nil {
		t.Fatal("expected error on missing pair field")
	}
}

// ─── Byte-level drift guard ──────────────────────────────────────

func TestTopicConstantsMatchEncoderOutput(t *testing.T) {
	// If the scval encoder's wire format shifts, our TopicPrefix*
	// constants drift from what real events emit. Verify at build
	// time by re-computing and comparing.
	cases := map[string]string{
		TopicPrefixPair:    PrefixPair,
		TopicPrefixFactory: PrefixFactory,
	}
	for got, src := range cases {
		// We can't re-import scval.MustEncodeString here without
		// cycling; instead, the golden test in internal/scval/
		// covers the encoder itself. This test just verifies the
		// length + prefix discriminator is String (14 = 0x0E).
		raw, err := base64.StdEncoding.DecodeString(got)
		if err != nil {
			t.Fatalf("decode %q: %v", src, err)
		}
		if len(raw) < 4 {
			t.Fatalf("too short for %q: %d bytes", src, len(raw))
		}
		disc := uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3])
		if disc != uint32(xdr.ScValTypeScvString) {
			t.Errorf("%q: disc=%d, want %d (ScvString)", src, disc, xdr.ScValTypeScvString)
		}
	}
}
