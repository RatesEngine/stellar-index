package comet

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// ─── Fixture helpers ─────────────────────────────────────────────

// contractStrkeyFromSeed produces a valid C-strkey from a 32-byte
// seed so every fixture passes strkey checksum without depending on
// real mainnet addresses we'd then need to pin.
func contractStrkeyFromSeed(t *testing.T, tag byte) string {
	t.Helper()
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = tag ^ byte(i)
	}
	s, err := strkey.Encode(strkey.VersionByteContract, seed)
	if err != nil {
		t.Fatalf("strkey encode: %v", err)
	}
	return s
}

func accountStrkeyFromSeed(t *testing.T, tag byte) string {
	t.Helper()
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = tag | byte(i)
	}
	s, err := strkey.Encode(strkey.VersionByteAccountID, seed)
	if err != nil {
		t.Fatalf("strkey encode: %v", err)
	}
	return s
}

// addressScValFromStrkey turns a G-strkey / C-strkey into an
// ScVal::Address.
func addressScValFromStrkey(t *testing.T, s string) xdr.ScVal {
	t.Helper()
	if len(s) == 0 {
		t.Fatal("empty strkey")
	}
	switch s[0] {
	case 'G':
		raw, err := strkey.Decode(strkey.VersionByteAccountID, s)
		if err != nil {
			t.Fatalf("decode G: %v", err)
		}
		var pub xdr.Uint256
		copy(pub[:], raw)
		aid := xdr.AccountId{
			Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
			Ed25519: &pub,
		}
		addr := xdr.ScAddress{
			Type:      xdr.ScAddressTypeScAddressTypeAccount,
			AccountId: &aid,
		}
		return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
	case 'C':
		raw, err := strkey.Decode(strkey.VersionByteContract, s)
		if err != nil {
			t.Fatalf("decode C: %v", err)
		}
		var cid xdr.ContractId
		copy(cid[:], raw)
		addr := xdr.ScAddress{
			Type:       xdr.ScAddressTypeScAddressTypeContract,
			ContractId: &cid,
		}
		return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
	default:
		t.Fatalf("unknown strkey prefix: %s", s)
		return xdr.ScVal{}
	}
}

// i128ScVal splits a signed *big.Int into the Hi/Lo parts.
func i128ScVal(t *testing.T, n *big.Int) xdr.ScVal {
	t.Helper()
	hi, lo := splitBigInt128(n)
	p := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p}
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

// encodeSwapBody assembles a (caller, token_in, token_out,
// token_amount_in, token_amount_out) Map body and returns its base64
// SCVal blob.
func encodeSwapBody(t *testing.T, caller, tokenIn, tokenOut string, amountIn, amountOut *big.Int) string {
	t.Helper()
	callerSv := addressScValFromStrkey(t, caller)
	tokenInSv := addressScValFromStrkey(t, tokenIn)
	tokenOutSv := addressScValFromStrkey(t, tokenOut)
	amountInSv := i128ScVal(t, amountIn)
	amountOutSv := i128ScVal(t, amountOut)

	keys := []string{"caller", "token_amount_in", "token_amount_out", "token_in", "token_out"}
	vals := []xdr.ScVal{callerSv, amountInSv, amountOutSv, tokenInSv, tokenOutSv}
	m := make(xdr.ScMap, len(keys))
	for i, k := range keys {
		sym := xdr.ScSymbol(k)
		m[i] = xdr.ScMapEntry{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
			Val: vals[i],
		}
	}
	pm := &m
	body := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}
	b, err := body.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// ─── Tests ───────────────────────────────────────────────────────

func TestClassify_MatchesPoolSwap(t *testing.T) {
	e := &events.Event{Topic: []string{TopicSymbolPool, TopicSymbolSwap}}
	if !classifySwap(e) {
		t.Errorf("expected classify true")
	}
	if classifySwap(&events.Event{Topic: []string{TopicSymbolPool}}) {
		t.Errorf("expected false for single-topic event")
	}
	if classifySwap(&events.Event{Topic: []string{TopicSymbolSwap, TopicSymbolPool}}) {
		t.Errorf("expected false for swapped-order topics")
	}
}

func TestDecodeSwap_HappyPath(t *testing.T) {
	caller := accountStrkeyFromSeed(t, 0x10)
	tokenIn := contractStrkeyFromSeed(t, 0x20)
	tokenOut := contractStrkeyFromSeed(t, 0x30)
	amountIn := big.NewInt(1_000_000_000)  // 1.0 at 9 decimals
	amountOut := big.NewInt(3_500_000_000) // 3.5 at 9 decimals

	body := encodeSwapBody(t, caller, tokenIn, tokenOut, amountIn, amountOut)
	ev := &events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolSwap},
		Value:          body,
		Ledger:         52_000_000,
		TxHash:         "deadbeef",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	trade, err := decodeSwap(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeSwap: %v", err)
	}
	if trade.Source != SourceName {
		t.Errorf("Source = %q want %q", trade.Source, SourceName)
	}
	if trade.BaseAmount.BigInt().Cmp(amountIn) != 0 {
		t.Errorf("BaseAmount = %s want %s", trade.BaseAmount, amountIn)
	}
	if trade.QuoteAmount.BigInt().Cmp(amountOut) != 0 {
		t.Errorf("QuoteAmount = %s want %s", trade.QuoteAmount, amountOut)
	}
	if trade.Taker != caller {
		t.Errorf("Taker = %q want %q", trade.Taker, caller)
	}
	// Pair base == token_in, quote == token_out.
	wantBase, _ := canonical.NewSorobanAsset(tokenIn)
	if !trade.Pair.Base.Equal(wantBase) {
		t.Errorf("Pair.Base = %+v want %+v", trade.Pair.Base, wantBase)
	}
}

func TestDecodeSwap_NonPositiveAmounts_Rejects(t *testing.T) {
	caller := accountStrkeyFromSeed(t, 0x10)
	tokenIn := contractStrkeyFromSeed(t, 0x20)
	tokenOut := contractStrkeyFromSeed(t, 0x30)
	body := encodeSwapBody(t, caller, tokenIn, tokenOut, big.NewInt(0), big.NewInt(5))
	ev := &events.Event{
		Topic: []string{TopicSymbolPool, TopicSymbolSwap},
		Value: body,
	}
	_, err := decodeSwap(ev, time.Now())
	if !errors.Is(err, ErrNonPositiveAmounts) {
		t.Errorf("expected ErrNonPositiveAmounts, got %v", err)
	}
}

func TestDecodeSwap_WrongTopic_Rejects(t *testing.T) {
	ev := &events.Event{Topic: []string{TopicSymbolPool, TopicSymbolPool}}
	_, err := decodeSwap(ev, time.Now())
	if !errors.Is(err, ErrNotCometSwap) {
		t.Errorf("expected ErrNotCometSwap, got %v", err)
	}
}

func TestDecodeSwap_MissingBodyField_Malformed(t *testing.T) {
	// Build a map missing token_out.
	caller := accountStrkeyFromSeed(t, 0x10)
	callerSv := addressScValFromStrkey(t, caller)
	amountInSv := i128ScVal(t, big.NewInt(1))
	amountOutSv := i128ScVal(t, big.NewInt(2))
	tokenIn := contractStrkeyFromSeed(t, 0x20)
	tokenInSv := addressScValFromStrkey(t, tokenIn)

	keys := []string{"caller", "token_amount_in", "token_amount_out", "token_in"}
	vals := []xdr.ScVal{callerSv, amountInSv, amountOutSv, tokenInSv}
	m := make(xdr.ScMap, len(keys))
	for i, k := range keys {
		sym := xdr.ScSymbol(k)
		m[i] = xdr.ScMapEntry{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
			Val: vals[i],
		}
	}
	pm := &m
	body := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}
	b, _ := body.MarshalBinary()
	bodyB64 := base64.StdEncoding.EncodeToString(b)

	ev := &events.Event{
		Topic: []string{TopicSymbolPool, TopicSymbolSwap},
		Value: bodyB64,
	}
	_, err := decodeSwap(ev, time.Now())
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("expected ErrMalformedPayload, got %v", err)
	}
}
