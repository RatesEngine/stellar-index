package reflector

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
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// These tests exercise the real (non-stub) SCVal decoder path with
// SDK-encoded fixtures. They cover the two Asset variants Reflector
// emits in each update_data pair — Asset::Stellar(Address) and
// Asset::Other(Symbol) — plus the topic[2] u64 timestamp. The SDK's
// encoder is treated as ground truth for the wire format; the
// golden bytes pinned in internal/scval/scval_test.go catch the
// scenario where an SDK upgrade quietly changes encoding.
//
// Once we have real mainnet captures from r1 (via
// scripts/capture-reflector-fixtures.sh), those replace the
// SDK-encoded fixtures here and this file becomes a pure
// regression harness. Fixture-capture path lives in
// test/fixtures/reflector/README.md.

// ─── Encoding helpers — build the exact xdr shapes Reflector emits ─

// Contract address example — a valid C-strkey we can decode back.
// All-zeros ContractId; strkey encode yields a deterministic value.
func zeroContractAddress(t *testing.T) xdr.ScAddress {
	t.Helper()
	var cid xdr.ContractId
	return xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &cid,
	}
}

func zeroContractStrkey(t *testing.T) string {
	t.Helper()
	var cid xdr.ContractId
	s, err := strkey.Encode(strkey.VersionByteContract, cid[:])
	if err != nil {
		t.Fatalf("strkey encode: %v", err)
	}
	return s
}

// encodeUpdateBody builds the base64-encoded SCVal for the Reflector
// UpdateEvent body. Real wire shape (verified against mainnet
// fixtures in test/fixtures/reflector/v6-2026-04-23/):
//
//	Map { "update_data": Vec<(Val, i128)> }
//
// `assets` and `prices` must be the same length; each entry
// produces one 2-tuple inside the inner Vec.
func encodeUpdateBody(t *testing.T, assets []xdr.ScVal, prices []*big.Int) string {
	t.Helper()
	if len(assets) != len(prices) {
		t.Fatalf("assets/prices length mismatch: %d vs %d", len(assets), len(prices))
	}
	tuples := make([]xdr.ScVal, len(assets))
	for i := range assets {
		hi, lo := splitBigInt128(prices[i])
		i128 := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
		priceSv := xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &i128}
		pair := xdr.ScVec{assets[i], priceSv}
		pp := &pair
		tuples[i] = xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pp}
	}
	vec := xdr.ScVec(tuples)
	pvec := &vec
	innerVec := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pvec}

	// Wrap in Map { "update_data": innerVec } to match the
	// soroban-sdk #[contractevent] macro's runtime serialization.
	updateDataKey := xdr.ScSymbol("update_data")
	keySv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &updateDataKey}
	scMap := xdr.ScMap{xdr.ScMapEntry{Key: keySv, Val: innerVec}}
	pmap := &scMap
	body := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pmap}

	b, err := body.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func encodeTimestampTopic(t *testing.T, ts uint64) string {
	t.Helper()
	u := xdr.Uint64(ts)
	sv := xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &u}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal ts: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// splitBigInt128 mirrors the helper in internal/scval/scval_test.go.
// Kept local so this file doesn't depend on test-package internals.
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

// ─── Real decoder: end-to-end tests ──────────────────────────────

func TestRealDecoder_stellarAndSymbolAssetsMix(t *testing.T) {
	// Build an UpdateEvent with two prices: one Asset::Stellar(contract)
	// (variant DEX path) and one Asset::Other(Symbol) → "USD" fiat
	// (variant CEX/FX path). Exercises both union arms.
	addrSv := xdr.ScVal{
		Type:    xdr.ScValTypeScvAddress,
		Address: ptrScAddr(zeroContractAddress(t)),
	}
	usd := xdr.ScSymbol("USD")
	symSv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &usd}

	bodyB64 := encodeUpdateBody(t,
		[]xdr.ScVal{addrSv, symSv},
		// 1.0 at 14 decimals, and 0.1242 at 14 decimals
		[]*big.Int{
			big.NewInt(100_000_000_000_000),
			big.NewInt(12_420_000_000_000),
		},
	)
	// Reflector events carry timestamp in u64 milliseconds.
	tsB64 := encodeTimestampTopic(t, 1_745_123_456_000)

	e := &events.Event{
		Topic:          []string{TopicSymbolReflector, TopicSymbolUpdate, tsB64},
		Value:          bodyB64,
		ContractID:     "CAFJZQWSED6YAWZU3GWRTOCNPPCGBN32L7QV43XX5LZLFTK6JLN34DLN",
		Ledger:         52_000_000,
		TxHash:         "a1b2c3",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, e.LedgerClosedAt)

	updates, err := decodeUpdate(e, VariantCEX, DefaultDecimals, "GRELAYER", closedAt)
	if err != nil {
		t.Fatalf("decodeUpdate: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}
	// Timestamp came from topic[2] (as ms), not ledger close.
	if updates[0].Timestamp.UnixMilli() != 1_745_123_456_000 {
		t.Errorf("timestamp not sourced from topic[2]: got %v (%d ms)",
			updates[0].Timestamp, updates[0].Timestamp.UnixMilli())
	}

	// First update is the contract-address asset. Decoded should
	// be NewSorobanAsset(<C-strkey of the zero contract>).
	wantAddr := zeroContractStrkey(t)
	wantSoroban, _ := canonical.NewSorobanAsset(wantAddr)
	if !updates[0].Asset.Equal(wantSoroban) {
		t.Errorf("updates[0].Asset = %+v want %+v", updates[0].Asset, wantSoroban)
	}
	// Second update is the fiat USD symbol.
	wantUSD, _ := canonical.NewFiatAsset("USD")
	if !updates[1].Asset.Equal(wantUSD) {
		t.Errorf("updates[1].Asset = %+v want %+v", updates[1].Asset, wantUSD)
	}

	// Prices round-tripped correctly.
	if updates[0].Price.BigInt().Cmp(big.NewInt(100_000_000_000_000)) != 0 {
		t.Errorf("updates[0].Price = %s", updates[0].Price)
	}
	if updates[1].Price.BigInt().Cmp(big.NewInt(12_420_000_000_000)) != 0 {
		t.Errorf("updates[1].Price = %s", updates[1].Price)
	}
}

func TestRealDecoder_unknownSymbolSkippedEmptyEventSurfaced(t *testing.T) {
	// An event whose ONLY entry is an unknown symbol decodes to
	// zero valid prices → surfaces as ErrEmptyPrices. (Partial
	// events with some valid + some unknown are covered below.)
	madeUp := xdr.ScSymbol("NOTACURRENCY")
	symSv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &madeUp}
	bodyB64 := encodeUpdateBody(t, []xdr.ScVal{symSv}, []*big.Int{big.NewInt(1)})

	e := &events.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, encodeTimestampTopic(t, 1)},
		Value:      bodyB64,
		ContractID: "CBKGPWGKSKZF52CFHMTRR23TBWTPMRDIYZ4O2P5VS65BMHYH4DXMCJZC",
	}
	_, err := decodeUpdate(e, VariantFX, DefaultDecimals, "", time.Now())
	if !errors.Is(err, ErrEmptyPrices) {
		t.Errorf("expected ErrEmptyPrices, got %v", err)
	}
}

func TestRealDecoder_unknownSymbolSkippedPartialEventDecodes(t *testing.T) {
	// Mixed payload — one valid fiat symbol + one unknown. The
	// valid entry must come through; the unknown is skipped. This
	// is what protects us against losing real prices when a new
	// symbol shows up on the feed ahead of the canonical fiat list
	// catching up.
	usd := xdr.ScSymbol("USD")
	usdSv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &usd}
	madeUp := xdr.ScSymbol("NOTACURRENCY")
	unknownSv := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &madeUp}
	bodyB64 := encodeUpdateBody(t,
		[]xdr.ScVal{usdSv, unknownSv},
		[]*big.Int{big.NewInt(100_000_000_000_000), big.NewInt(42)},
	)

	e := &events.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, encodeTimestampTopic(t, 1)},
		Value:      bodyB64,
		ContractID: "CBKGPWGKSKZF52CFHMTRR23TBWTPMRDIYZ4O2P5VS65BMHYH4DXMCJZC",
	}
	updates, err := decodeUpdate(e, VariantFX, DefaultDecimals, "", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update (USD), got %d", len(updates))
	}
	usdAsset, _ := canonical.NewFiatAsset("USD")
	if !updates[0].Asset.Equal(usdAsset) {
		t.Errorf("updates[0].Asset = %+v want %+v", updates[0].Asset, usdAsset)
	}
}

func TestRealDecoder_rejectsWrongBodyShape(t *testing.T) {
	// Body is a bare I128, not a Vec — decoder must reject cleanly.
	hi, lo := splitBigInt128(big.NewInt(42))
	i128 := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &i128}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	e := &events.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate, encodeTimestampTopic(t, 1)},
		Value:      base64.StdEncoding.EncodeToString(b),
		ContractID: "C-unused",
	}
	_, err = decodeUpdate(e, VariantDEX, DefaultDecimals, "", time.Now())
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("expected ErrMalformedPayload, got %v", err)
	}
}

func TestRealDecoder_malformedTopicArity(t *testing.T) {
	// Only 2 topics — missing the timestamp slot. Must surface as
	// ErrMalformedPayload, not silently fall back to closedAt.
	addrSv := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: ptrScAddr(zeroContractAddress(t))}
	bodyB64 := encodeUpdateBody(t, []xdr.ScVal{addrSv}, []*big.Int{big.NewInt(1)})
	e := &events.Event{
		Topic:      []string{TopicSymbolReflector, TopicSymbolUpdate},
		Value:      bodyB64,
		ContractID: "C",
	}
	_, err := decodeUpdate(e, VariantDEX, DefaultDecimals, "", time.Now())
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("expected ErrMalformedPayload for missing timestamp topic, got %v", err)
	}
}

// ─── Real decoder: topic[0/1] symbols are byte-equal to what the
// contract emits ───────────────────────────────────────────────────

func TestTopicSymbolsMatchEncodedContractTopics(t *testing.T) {
	// The TopicSymbolReflector / TopicSymbolUpdate constants are
	// computed at package init via scval.MustEncodeSymbol. Verify
	// that an event built with the exact bytes the contract would
	// emit is classified() as a Reflector update.
	want0, err := scval.EncodeSymbol(EventTopic0)
	if err != nil {
		t.Fatal(err)
	}
	want1, err := scval.EncodeSymbol(EventTopic1)
	if err != nil {
		t.Fatal(err)
	}
	if want0 != TopicSymbolReflector {
		t.Errorf("TopicSymbolReflector drift: const=%q live=%q", TopicSymbolReflector, want0)
	}
	if want1 != TopicSymbolUpdate {
		t.Errorf("TopicSymbolUpdate drift: const=%q live=%q", TopicSymbolUpdate, want1)
	}

	e := &events.Event{
		Topic: []string{want0, want1, encodeTimestampTopic(t, 1)},
	}
	if !classify(e) {
		t.Error("event built with SDK-encoded REFLECTOR/update topics did not classify as Reflector")
	}
}

// Small helper — &zeroContractAddress(t) isn't addressable since it's
// a function return.
func ptrScAddr(a xdr.ScAddress) *xdr.ScAddress { return &a }
