package phoenix

import (
	"encoding/base64"
	"math/big"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Decoder tests using SDK-encoded single-value SCVal bodies.
// Complements real_fixture_test.go (mainnet captures) by covering
// shapes that rarely appear on mainnet: i128 above the 2^63
// boundary (ADR-0003), wrong body kind, unknown field topic.

func makeC(t *testing.T, seed byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seed
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

func b64Marshal(t *testing.T, sv xdr.ScVal) string {
	t.Helper()
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func i128Body(t *testing.T, n *big.Int) string {
	t.Helper()
	hi, lo := splitI128(n)
	parts := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
	return b64Marshal(t, xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &parts})
}

func addrBody(t *testing.T, c string) string {
	t.Helper()
	var cid xdr.ContractId
	raw, err := strkey.Decode(strkey.VersionByteContract, c)
	if err != nil {
		t.Fatalf("strkey.Decode: %v", err)
	}
	copy(cid[:], raw)
	addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid}
	return b64Marshal(t, xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr})
}

func splitI128(n *big.Int) (hi int64, lo uint64) {
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

// ─── sdkDecodeI128 ──────────────────────────────────────────────

func TestSdkDecodeI128_range(t *testing.T) {
	big1 := new(big.Int)
	big1.SetString("123456789012345678901234567890", 10) // ~2^96, needs hi word
	cases := []struct {
		name string
		v    *big.Int
	}{
		{"zero", big.NewInt(0)},
		{"small pos", big.NewInt(42)},
		{"int64-max boundary", new(big.Int).SetInt64(1<<62 - 1)},
		{"above int64 (hi!=0)", big1},
		{"large neg", new(big.Int).Neg(big1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := i128Body(t, tc.v)
			got, err := sdkDecodeI128(body)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.BigInt().Cmp(tc.v) != 0 {
				t.Errorf("got %s, want %s", got.BigInt(), tc.v)
			}
		})
	}
}

func TestSdkDecodeI128_wrongKind(t *testing.T) {
	// Address body, not I128 — schema violation.
	body := addrBody(t, makeC(t, 0x01))
	_, err := sdkDecodeI128(body)
	if err == nil {
		t.Fatal("expected error on non-I128 body")
	}
}

// ─── sdkDecodeAddress / sdkDecodeAsset ──────────────────────────

func TestSdkDecodeAsset_contract(t *testing.T) {
	c := makeC(t, 0x20)
	body := addrBody(t, c)
	asset, err := sdkDecodeAsset(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if asset.ContractID != c {
		t.Errorf("asset.ContractID = %q, want %q", asset.ContractID, c)
	}
}

func TestSdkDecodeAddress_wrongKind(t *testing.T) {
	// I128 body, not Address — schema violation.
	body := i128Body(t, big.NewInt(1))
	_, err := sdkDecodeAddress(body)
	if err == nil {
		t.Fatal("expected error on non-Address body")
	}
}

// ─── Classify drift guard ──────────────────────────────────────

func TestTopicConstantsMatchEncoderOutput(t *testing.T) {
	// Each TopicSymbol* is computed at init via scval.MustEncodeString.
	// Verify their discriminators are all ScvString (0x0E) — if a
	// future refactor accidentally switches one to Symbol (0x0F),
	// classify() would stop matching on real events.
	cases := []string{
		TopicSymbolSwap,
		TopicSymbolSender,
		TopicSymbolSellToken,
		TopicSymbolOfferAmount,
		TopicSymbolActualReceived,
		TopicSymbolBuyToken,
		TopicSymbolReturnAmount,
		TopicSymbolSpreadAmount,
		TopicSymbolReferralFee,
	}
	for i, c := range cases {
		raw, err := base64.StdEncoding.DecodeString(c)
		if err != nil {
			t.Fatalf("decode[%d]: %v", i, err)
		}
		if len(raw) < 4 {
			t.Fatalf("too short [%d]", i)
		}
		disc := uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3])
		if disc != uint32(xdr.ScValTypeScvString) {
			t.Errorf("topic[%d]: disc=%d, want %d (ScvString)", i, disc, xdr.ScValTypeScvString)
		}
	}
}
