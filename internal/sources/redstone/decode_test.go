package redstone

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

// Build helpers ─────────────────────────────────────────────────

// encodeAddressArg builds the base64 SCVal::Address form of the
// relayer G-strkey — what the dispatcher would hand us in OpArgs[0].
func encodeAddressArg(t *testing.T, gStrkey string) string {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteAccountID, gStrkey)
	if err != nil {
		t.Fatalf("decode strkey: %v", err)
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
	sv := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// encodeStringVecArg builds the base64 SCVal::Vec<String> that the
// write_prices op arg feed_ids comes in as.
func encodeStringVecArg(t *testing.T, feedIDs []string) string {
	t.Helper()
	items := make([]xdr.ScVal, len(feedIDs))
	for i, id := range feedIDs {
		s := xdr.ScString(id)
		items[i] = xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &s}
	}
	vec := xdr.ScVec(items)
	pvec := &vec
	sv := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pvec}
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal vec: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// encodePayloadArg builds the base64 ScBytes we use as args[2]; the
// decoder doesn't inspect it, so a short sentinel is enough.
func encodePayloadArg(t *testing.T) string {
	t.Helper()
	b := xdr.ScBytes{0xAA, 0xBB}
	sv := xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &b}
	raw, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// encodeWritePricesBody builds the WritePrices event body:
//
//	Map { "updater": Address, "updated_feeds": Vec<PriceData> }
//
// prices are passed as *big.Int so tests can exercise the U256
// path (Redstone's canonical scale is 8 decimals; 1.0 at 8 dec = 1e8).
func encodeWritePricesBody(t *testing.T, updater string, prices []*big.Int, packageTs, writeTs uint64) string {
	t.Helper()
	// updater Address
	raw, err := strkey.Decode(strkey.VersionByteAccountID, updater)
	if err != nil {
		t.Fatalf("decode updater: %v", err)
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
	updaterSv := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}

	// Vec<PriceData>
	items := make([]xdr.ScVal, len(prices))
	for i, p := range prices {
		priceSv := u256ScVal(t, p)
		pkgU := xdr.Uint64(packageTs)
		wrU := xdr.Uint64(writeTs)
		pkgSv := xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &pkgU}
		wrSv := xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &wrU}

		pdKeys := []string{"price", "package_timestamp", "write_timestamp"}
		pdVals := []xdr.ScVal{priceSv, pkgSv, wrSv}
		m := make(xdr.ScMap, len(pdKeys))
		for j, k := range pdKeys {
			sym := xdr.ScSymbol(k)
			m[j] = xdr.ScMapEntry{
				Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
				Val: pdVals[j],
			}
		}
		pm := &m
		items[i] = xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}
	}
	vec := xdr.ScVec(items)
	pvec := &vec
	feedsSv := xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pvec}

	// outer map
	outerKeys := []string{"updated_feeds", "updater"}
	outerVals := []xdr.ScVal{feedsSv, updaterSv}
	outer := make(xdr.ScMap, len(outerKeys))
	for i, k := range outerKeys {
		sym := xdr.ScSymbol(k)
		outer[i] = xdr.ScMapEntry{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
			Val: outerVals[i],
		}
	}
	pouter := &outer
	body := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pouter}
	b, err := body.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// u256ScVal builds an ScVal::U256 from a non-negative *big.Int,
// splitting into the four 64-bit words the SDK expects.
func u256ScVal(t *testing.T, n *big.Int) xdr.ScVal {
	t.Helper()
	if n.Sign() < 0 {
		t.Fatalf("u256 does not accept negative: %s", n)
	}
	buf := n.Bytes() // big-endian, no leading zeros
	if len(buf) > 32 {
		t.Fatalf("value exceeds 256 bits: %s", n)
	}
	padded := make([]byte, 32)
	copy(padded[32-len(buf):], buf)
	hiHi := beUint64(padded[0:8])
	hiLo := beUint64(padded[8:16])
	loHi := beUint64(padded[16:24])
	loLo := beUint64(padded[24:32])
	parts := xdr.UInt256Parts{
		HiHi: xdr.Uint64(hiHi),
		HiLo: xdr.Uint64(hiLo),
		LoHi: xdr.Uint64(loHi),
		LoLo: xdr.Uint64(loLo),
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvU256, U256: &parts}
}

func beUint64(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}

// Deterministic test identifiers. `relayerG` is strkey-encoded
// from a fixed byte pattern so the checksum round-trips through
// strkey.Decode/Encode without depending on key generation.
// adapterC is the real mainnet adapter from
// docs/discovery/oracles/redstone.md.
const (
	adapterC  = "CA526Y2NQWGWVVQ7RFFPGAZMU66PSYJ3UC2MTVAV4ZU7OM5BOPHDXUSG"
	oneBTCAt8 = 50_000_000_000_000 // $500,000 at 8 decimals
	oneETHAt8 = 3_500_000_000_000  // $35,000 at 8 decimals
)

// relayerG is computed at init from a fixed 32-byte seed — a known
// public key bit pattern strkey-encodes to a valid G-address with a
// correct checksum. Hardcoding the strkey string invites checksum
// drift (what caught this test-suite the first time).
var relayerG = func() string {
	seed := [32]byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10,
		0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
		0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F, 0x20,
	}
	s, err := strkey.Encode(strkey.VersionByteAccountID, seed[:])
	if err != nil {
		panic("strkey encode of fixed seed failed: " + err.Error())
	}
	return s
}()

// ─── Tests ───────────────────────────────────────────────────────

func TestClassify_MatchesRedstone(t *testing.T) {
	e := &events.Event{Topic: []string{TopicSymbolRedstone}}
	if !classify(e) {
		t.Errorf("expected classify true for REDSTONE topic")
	}
	// non-matching
	e2 := &events.Event{Topic: []string{"AAAACwAAAAhTT1JPU1dBUAAAAAA="}}
	if classify(e2) {
		t.Errorf("expected classify false for non-REDSTONE")
	}
}

func TestDecode_HappyPath_TwoKnownFeeds(t *testing.T) {
	const pkgTs = uint64(1_745_000_000_000) // ms
	const wrTs = uint64(1_745_000_060_000)
	body := encodeWritePricesBody(t, relayerG,
		[]*big.Int{big.NewInt(oneBTCAt8), big.NewInt(oneETHAt8)},
		pkgTs, wrTs)

	args := []string{
		encodeAddressArg(t, relayerG),
		encodeStringVecArg(t, []string{"BTC", "ETH"}),
		encodePayloadArg(t),
	}

	ev := &events.Event{
		Topic:          []string{TopicSymbolRedstone},
		Value:          body,
		OpArgs:         args,
		ContractID:     adapterC,
		Ledger:         52_000_000,
		TxHash:         "abcd",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)

	updates, err := decodeWritePrices(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeWritePrices: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(updates))
	}

	btc, _ := canonical.NewCryptoAsset("BTC")
	if !updates[0].Asset.Equal(btc) {
		t.Errorf("updates[0].Asset = %+v want %+v", updates[0].Asset, btc)
	}
	if updates[0].Price.BigInt().Cmp(big.NewInt(oneBTCAt8)) != 0 {
		t.Errorf("updates[0].Price = %s want %d", updates[0].Price, oneBTCAt8)
	}
	if updates[0].Decimals != 8 {
		t.Errorf("decimals = %d want 8", updates[0].Decimals)
	}
	// Timestamp should come from PackageTimestamp, not ledger close.
	if updates[0].Timestamp.UnixMilli() != int64(pkgTs) {
		t.Errorf("timestamp not from package_timestamp: got %d want %d",
			updates[0].Timestamp.UnixMilli(), pkgTs)
	}
	if updates[0].Observer != relayerG {
		t.Errorf("observer = %q want %q", updates[0].Observer, relayerG)
	}
	// OpIndex fan-out: same OperationIndex=0, slots 0 and 1.
	if updates[0].OpIndex != 0 || updates[1].OpIndex != 1 {
		t.Errorf("OpIndex fanout wrong: [%d, %d]", updates[0].OpIndex, updates[1].OpIndex)
	}

	eth, _ := canonical.NewCryptoAsset("ETH")
	if !updates[1].Asset.Equal(eth) {
		t.Errorf("updates[1].Asset = %+v want %+v", updates[1].Asset, eth)
	}
}

func TestDecode_FeedIDCountMismatch(t *testing.T) {
	// 2 prices, 1 feed id — simulates the freshness verifier dropping
	// one submitted feed.
	body := encodeWritePricesBody(t, relayerG,
		[]*big.Int{big.NewInt(oneBTCAt8), big.NewInt(oneETHAt8)},
		1, 2)
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeStringVecArg(t, []string{"BTC"}),
		encodePayloadArg(t),
	}
	ev := &events.Event{
		Topic:  []string{TopicSymbolRedstone},
		Value:  body,
		OpArgs: args,
		TxHash: "abcd",
	}
	_, err := decodeWritePrices(ev, time.Now())
	if !errors.Is(err, ErrFeedIDCountMismatch) {
		t.Errorf("expected ErrFeedIDCountMismatch, got %v", err)
	}
}

func TestDecode_MissingOpArgs(t *testing.T) {
	body := encodeWritePricesBody(t, relayerG,
		[]*big.Int{big.NewInt(1)}, 1, 2)
	ev := &events.Event{
		Topic:  []string{TopicSymbolRedstone},
		Value:  body,
		OpArgs: nil,
		TxHash: "abcd",
	}
	_, err := decodeWritePrices(ev, time.Now())
	if !errors.Is(err, ErrMissingOpArgs) {
		t.Errorf("expected ErrMissingOpArgs, got %v", err)
	}
}

func TestDecode_UnknownFeedSkipped_KnownLands(t *testing.T) {
	// Three feeds: BTC (known), NOTAFEED (outside the ADR-0028
	// registry — e.g. a 20th feed RedStone deployed), ETH (known).
	// Middle entry must be skipped while outer two land.
	body := encodeWritePricesBody(t, relayerG,
		[]*big.Int{
			big.NewInt(oneBTCAt8),
			big.NewInt(9_000_000), // synthetic unknown-feed price
			big.NewInt(oneETHAt8),
		}, 1, 2)
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeStringVecArg(t, []string{"BTC", "NOTAFEED", "ETH"}),
		encodePayloadArg(t),
	}
	ev := &events.Event{
		Topic:  []string{TopicSymbolRedstone},
		Value:  body,
		OpArgs: args,
		TxHash: "abcd",
	}
	updates, err := decodeWritePrices(ev, time.Now())
	if err != nil {
		t.Fatalf("decodeWritePrices: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("expected 2 updates (BTC+ETH), got %d", len(updates))
	}
	btc, _ := canonical.NewCryptoAsset("BTC")
	eth, _ := canonical.NewCryptoAsset("ETH")
	if !updates[0].Asset.Equal(btc) {
		t.Errorf("updates[0].Asset = %+v want BTC", updates[0].Asset)
	}
	if !updates[1].Asset.Equal(eth) {
		t.Errorf("updates[1].Asset = %+v want ETH", updates[1].Asset)
	}
	// OpIndex preserves original-slot positions: BTC=0, ETH=2.
	if updates[0].OpIndex != 0 {
		t.Errorf("BTC OpIndex = %d, want 0", updates[0].OpIndex)
	}
	if updates[1].OpIndex != 2 {
		t.Errorf("ETH OpIndex = %d, want 2 (NOTAFEED slot 1 skipped)", updates[1].OpIndex)
	}
}

func TestDecode_AllUnknown_ErrEmptyUpdates(t *testing.T) {
	body := encodeWritePricesBody(t, relayerG,
		[]*big.Int{big.NewInt(1), big.NewInt(2)},
		1, 2)
	args := []string{
		encodeAddressArg(t, relayerG),
		// Both outside the ADR-0028 registry. Note "BENJI" alone is
		// NOT a real feed_id — the real one is
		// "BENJI_ETHEREUM_FUNDAMENTAL" (see TestDecode_RWAFeeds).
		encodeStringVecArg(t, []string{"BENJI", "NOTAFEED"}),
		encodePayloadArg(t),
	}
	ev := &events.Event{
		Topic:  []string{TopicSymbolRedstone},
		Value:  body,
		OpArgs: args,
		TxHash: "abcd",
	}
	_, err := decodeWritePrices(ev, time.Now())
	if !errors.Is(err, ErrEmptyUpdates) {
		t.Errorf("expected ErrEmptyUpdates, got %v", err)
	}
}

func TestDecode_RWAandQuoteCurrency(t *testing.T) {
	// Exercises the ADR-0028 feed registry: an RWA feed whose
	// feed_id ≠ display name, the EUR-quoted EUROC feed (the pre-#53
	// USD-hardcode bug), a plain RWA feed, and a tokenized-BTC crypto
	// feed.
	feedIDs := []string{
		"BENJI_ETHEREUM_FUNDAMENTAL", // → rwa:BENJI, USD
		"EUROC/EUR",                  // → crypto:EUROC, EUR
		"GILTS",                      // → rwa:GILTS, USD
		"SolvBTC",                    // → crypto:SolvBTC, USD
	}
	body := encodeWritePricesBody(t, relayerG,
		[]*big.Int{
			big.NewInt(1_00000000),
			big.NewInt(1_05000000),
			big.NewInt(100_00000000),
			big.NewInt(95000_00000000),
		}, 1, 2)
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeStringVecArg(t, feedIDs),
		encodePayloadArg(t),
	}
	ev := &events.Event{
		Topic:  []string{TopicSymbolRedstone},
		Value:  body,
		OpArgs: args,
		TxHash: "abcd",
	}
	updates, err := decodeWritePrices(ev, time.Now())
	if err != nil {
		t.Fatalf("decodeWritePrices: %v", err)
	}
	if len(updates) != 4 {
		t.Fatalf("expected 4 updates, got %d", len(updates))
	}

	wantBenji, _ := canonical.NewRWAAsset("BENJI")
	if !updates[0].Asset.Equal(wantBenji) {
		t.Errorf("feed_id BENJI_ETHEREUM_FUNDAMENTAL → %s, want rwa:BENJI", updates[0].Asset)
	}
	if updates[0].Quote.String() != "fiat:USD" {
		t.Errorf("BENJI quote = %s, want fiat:USD", updates[0].Quote)
	}

	wantEUROC, _ := canonical.NewCryptoAsset("EUROC")
	if !updates[1].Asset.Equal(wantEUROC) {
		t.Errorf("feed_id EUROC/EUR → %s, want crypto:EUROC", updates[1].Asset)
	}
	if updates[1].Quote.String() != "fiat:EUR" {
		t.Errorf("EUROC quote = %s, want fiat:EUR (pre-#53 this was mislabelled USD)", updates[1].Quote)
	}

	wantGILTS, _ := canonical.NewRWAAsset("GILTS")
	if !updates[2].Asset.Equal(wantGILTS) {
		t.Errorf("feed_id GILTS → %s, want rwa:GILTS", updates[2].Asset)
	}

	wantSolv, _ := canonical.NewCryptoAsset("SolvBTC")
	if !updates[3].Asset.Equal(wantSolv) {
		t.Errorf("feed_id SolvBTC → %s, want crypto:SolvBTC", updates[3].Asset)
	}
}

func TestFeedRegistry_Has19Feeds(t *testing.T) {
	// The ADR-0028 registry must cover exactly the 19 mainnet feeds.
	// A drift here means a feed was added/removed without updating
	// the ADR + this registry in lock-step.
	if len(feedRegistry) != 19 {
		t.Errorf("feedRegistry has %d feeds, want 19 (ADR-0028)", len(feedRegistry))
	}
	for feedID, entry := range feedRegistry {
		if err := entry.Base.Validate(); err != nil {
			t.Errorf("feed %q base asset invalid: %v", feedID, err)
		}
		if err := entry.Quote.Validate(); err != nil {
			t.Errorf("feed %q quote asset invalid: %v", feedID, err)
		}
	}
}

func TestDecode_NonRedstoneTopic_Rejects(t *testing.T) {
	ev := &events.Event{Topic: []string{"AAAADwAAAAhTT1JPU1dBUAAAAAA="}}
	_, err := decodeWritePrices(ev, time.Now())
	if !errors.Is(err, ErrNotRedstoneEvent) {
		t.Errorf("expected ErrNotRedstoneEvent, got %v", err)
	}
}

// TestDecode_RealMainnetEvent_BytesWrappedBody exercises the
// exact on-wire body shape RedStone's adapter contract emits: an
// `ScVal::Bytes` wrapping the XDR-encoded WritePrices struct
// (the Rust impl uses `self.to_xdr(env).to_val()` — see
// redstone-public-contracts/packages/stellar-connector/.../event.rs).
// Earlier helper tests built the Map directly, which masked a bug
// where the decoder's Map-assertion ran against the outer Bytes
// wrapper and silently rejected every real event.
//
// Fixture: event pulled from mainnet ledger 62265977 (tx
// 349bd590…c7a8b) on 2026-04-24 via sorobanrpc.com getEvents.
// Contains one XLM price update at package_timestamp
// 2026-04-23T12:30:06Z. Feed id from the tx's write_prices args.
func TestDecode_RealMainnetEvent_BytesWrappedBody(t *testing.T) {
	// The actual event body from ledger 62265977. Outer wrapper is
	// ScVal::Bytes(248); inner parse yields Map{updater, updated_feeds}.
	body := "AAAADQAAAPgAAAARAAAAAQAAAAIAAAAPAAAADXVwZGF0ZWRfZmVlZHMAAAAAAAAQAAAAAQAAAAEAAAARAAAAAQAAAAMAAAAPAAAAEXBhY2thZ2VfdGltZXN0YW1wAAAAAAAABQAAAZ2/ru8wAAAADwAAAAVwcmljZQAAAAAAAAsAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABA0AaYAAAAA8AAAAPd3JpdGVfdGltZXN0YW1wAAAAAAUAAAGdv68KiAAAAA8AAAAHdXBkYXRlcgAAAAASAAAAAAAAAAAk1wP6EQQ6Z6YlFesVDZAkQ3o7tjIdDJoRh0/hHzC1Bw=="

	// OpArgs built to match the real tx's write_prices(updater,
	// feed_ids, payload) call with feed_ids=["XLM"] — matching the
	// single entry in updated_feeds.
	args := []string{
		encodeAddressArg(t, relayerG),
		encodeStringVecArg(t, []string{"XLM"}),
		encodePayloadArg(t),
	}

	ev := &events.Event{
		Topic:          []string{TopicSymbolRedstone},
		Value:          body,
		OpArgs:         args,
		ContractID:     adapterC,
		Ledger:         62_265_977,
		TxHash:         "349bd590c679a9d69ac0ff3eb49a673f95cf9d77016fc3d019eb654c772c7a8b",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-24T13:30:13Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)

	updates, err := decodeWritePrices(ev, closedAt)
	if err != nil {
		t.Fatalf("decodeWritePrices: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}

	xlm, _ := canonical.NewCryptoAsset("XLM")
	if !updates[0].Asset.Equal(xlm) {
		t.Errorf("Asset = %+v want XLM", updates[0].Asset)
	}
	// Price from the event body: 4349500000 (0x103401A60 = U256).
	if updates[0].Price.BigInt().Cmp(big.NewInt(4_349_500_000)) != 0 {
		t.Errorf("Price = %s want 4349500000", updates[0].Price)
	}
}
