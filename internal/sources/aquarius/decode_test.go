package aquarius

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// Decoder tests using SDK-encoded fixtures. Complement
// real_fixture_test.go (mainnet captures): this file covers shapes
// that are hard to produce on mainnet — large negative i128s,
// wrong body arity, topic slot with wrong SCVal type.

// ─── SDK helpers ────────────────────────────────────────────────

func makeContractStrkey(t *testing.T, seed byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seed
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

func makeAccountStrkey(t *testing.T, seed byte) string {
	t.Helper()
	var raw [32]byte
	raw[0] = seed
	s, err := strkey.Encode(strkey.VersionByteAccountID, raw[:])
	if err != nil {
		t.Fatalf("strkey.Encode: %v", err)
	}
	return s
}

func encodeSymbol(t *testing.T, s string) string {
	t.Helper()
	sym := xdr.ScSymbol(s)
	sv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func encodeContractAddrFromStrkey(t *testing.T, c string) string {
	t.Helper()
	var cid xdr.ContractId
	raw, err := strkey.Decode(strkey.VersionByteContract, c)
	if err != nil {
		t.Fatalf("decode strkey: %v", err)
	}
	copy(cid[:], raw)
	addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func encodeAccountAddrFromStrkey(t *testing.T, g string) string {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteAccountID, g)
	if err != nil {
		t.Fatalf("decode strkey: %v", err)
	}
	var pub xdr.Uint256
	copy(pub[:], raw)
	accID := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &pub}
	addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &accID}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func encodeTradeBody(t *testing.T, sold, bought, fee *big.Int) string {
	t.Helper()
	build := func(n *big.Int) xdr.ScVal {
		hi, lo := splitBigInt128(n)
		p := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
		return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p}
	}
	vec := xdr.ScVec([]xdr.ScVal{build(sold), build(bought), build(fee)})
	pvec := &vec
	body := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pvec}
	b, err := body.MarshalBinary()
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

// ─── Real decoder: end-to-end through sdk helpers ────────────────

func TestRealDecoder_endToEnd(t *testing.T) {
	tokenIn := makeContractStrkey(t, 0x01)
	tokenOut := makeContractStrkey(t, 0x02)
	user := makeAccountStrkey(t, 0x03)

	e := &events.Event{
		Topic: []string{
			encodeSymbol(t, "trade"),
			encodeContractAddrFromStrkey(t, tokenIn),
			encodeContractAddrFromStrkey(t, tokenOut),
			encodeAccountAddrFromStrkey(t, user),
		},
		Value:          encodeTradeBody(t, big.NewInt(1_000_000_000), big.NewInt(12_420_000), big.NewInt(1_000)),
		Ledger:         62_000_000,
		TxHash:         "abc123",
		OperationIndex: 3,
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, e.LedgerClosedAt)

	tr, err := decodeTrade(e, closedAt)
	if err != nil {
		t.Fatalf("decodeTrade: %v", err)
	}
	wantBase, _ := canonical.NewSorobanAsset(tokenIn)
	wantQuote, _ := canonical.NewSorobanAsset(tokenOut)
	if !tr.Pair.Base.Equal(wantBase) {
		t.Errorf("base = %+v, want %+v", tr.Pair.Base, wantBase)
	}
	if !tr.Pair.Quote.Equal(wantQuote) {
		t.Errorf("quote = %+v, want %+v", tr.Pair.Quote, wantQuote)
	}
	if tr.Taker != user {
		t.Errorf("taker = %q, want %q", tr.Taker, user)
	}
	if tr.BaseAmount.BigInt().Cmp(big.NewInt(1_000_000_000)) != 0 {
		t.Errorf("base amount = %s", tr.BaseAmount)
	}
	if tr.QuoteAmount.BigInt().Cmp(big.NewInt(12_420_000)) != 0 {
		t.Errorf("quote amount = %s", tr.QuoteAmount)
	}
}

func TestRealDecoder_largeI128(t *testing.T) {
	// 2^96-range amount — the ADR-0003 boundary. Catches classic
	// truncate-to-int64 bugs in the hi-word path.
	big1 := new(big.Int)
	big1.SetString("123456789012345678901234567890", 10)

	tokenIn := makeContractStrkey(t, 0x10)
	tokenOut := makeContractStrkey(t, 0x11)
	user := makeContractStrkey(t, 0x12) // C-strkey user (router contract)

	e := &events.Event{
		Topic: []string{
			encodeSymbol(t, "trade"),
			encodeContractAddrFromStrkey(t, tokenIn),
			encodeContractAddrFromStrkey(t, tokenOut),
			encodeContractAddrFromStrkey(t, user),
		},
		Value:  encodeTradeBody(t, big1, big.NewInt(1), big.NewInt(0)),
		Ledger: 1, TxHash: "x", OperationIndex: 0,
		LedgerClosedAt: "2026-04-23T00:00:00Z",
	}
	tr, err := decodeTrade(e, time.Now())
	if err != nil {
		t.Fatalf("decodeTrade: %v", err)
	}
	if tr.BaseAmount.BigInt().Cmp(big1) != 0 {
		t.Errorf("large i128 lost bits: got %s, want %s", tr.BaseAmount, big1)
	}
}

func TestRealDecoder_wrongBodyArity(t *testing.T) {
	// Body has 2 i128s instead of 3. Must reject cleanly.
	tokenIn := makeContractStrkey(t, 0x20)
	tokenOut := makeContractStrkey(t, 0x21)
	user := makeContractStrkey(t, 0x22)

	// Hand-construct a 2-element Vec body (should have been 3).
	build := func(n *big.Int) xdr.ScVal {
		hi, lo := splitBigInt128(n)
		return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}}
	}
	vec := xdr.ScVec([]xdr.ScVal{build(big.NewInt(1)), build(big.NewInt(2))})
	pvec := &vec
	body := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pvec}
	b, _ := body.MarshalBinary()
	bodyB64 := base64.StdEncoding.EncodeToString(b)

	e := &events.Event{
		Topic: []string{
			encodeSymbol(t, "trade"),
			encodeContractAddrFromStrkey(t, tokenIn),
			encodeContractAddrFromStrkey(t, tokenOut),
			encodeContractAddrFromStrkey(t, user),
		},
		Value: bodyB64,
	}
	_, err := decodeTrade(e, time.Now())
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("expected ErrMalformedPayload, got %v", err)
	}
}

func TestRealDecoder_wrongTopicType(t *testing.T) {
	// topic[1] is a Symbol instead of Address — schema violation.
	tokenOut := makeContractStrkey(t, 0x30)
	user := makeContractStrkey(t, 0x31)

	e := &events.Event{
		Topic: []string{
			encodeSymbol(t, "trade"),
			encodeSymbol(t, "not-an-address"),
			encodeContractAddrFromStrkey(t, tokenOut),
			encodeContractAddrFromStrkey(t, user),
		},
		Value: encodeTradeBody(t, big.NewInt(1), big.NewInt(1), big.NewInt(0)),
	}
	_, err := decodeTrade(e, time.Now())
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("expected ErrMalformedPayload, got %v", err)
	}
}

// ─── Topic-constant drift guard ──────────────────────────────────

func TestTopicSymbolTradeMatchesEncoderOutput(t *testing.T) {
	// Confirm the init-time TopicSymbolTrade equals what
	// scval.MustEncodeSymbol("trade") produces. The byte-level
	// golden is already guarded in internal/scval/scval_test.go;
	// this test catches a regression in the init wiring.
	if got := encodeSymbol(t, EventTrade); got != TopicSymbolTrade {
		t.Errorf("TopicSymbolTrade drift:\n got  %s\n want %s", TopicSymbolTrade, got)
	}
}
