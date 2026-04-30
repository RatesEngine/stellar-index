package blend

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/scval"
)

// ─── Fixture helpers ─────────────────────────────────────────────

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

func addressScVal(t *testing.T, s string) xdr.ScVal {
	t.Helper()
	switch s[0] {
	case 'G':
		raw, err := strkey.Decode(strkey.VersionByteAccountID, s)
		if err != nil {
			t.Fatalf("decode G: %v", err)
		}
		var pub xdr.Uint256
		copy(pub[:], raw)
		aid := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &pub}
		addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &aid}
		return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
	case 'C':
		raw, err := strkey.Decode(strkey.VersionByteContract, s)
		if err != nil {
			t.Fatalf("decode C: %v", err)
		}
		var cid xdr.ContractId
		copy(cid[:], raw)
		addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &cid}
		return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
	}
	t.Fatalf("unexpected strkey prefix: %s", s)
	return xdr.ScVal{}
}

func i128ScVal(t *testing.T, n *big.Int) xdr.ScVal {
	t.Helper()
	twoTo64 := new(big.Int).Lsh(big.NewInt(1), 64)
	mask64 := new(big.Int).Sub(twoTo64, big.NewInt(1))
	var hi int64
	var lo uint64
	if n.Sign() >= 0 {
		loBig := new(big.Int).And(n, mask64)
		hiBig := new(big.Int).Rsh(n, 64)
		hi = hiBig.Int64()
		lo = loBig.Uint64()
	} else {
		twoTo128 := new(big.Int).Lsh(big.NewInt(1), 128)
		u := new(big.Int).Add(twoTo128, n)
		loBig := new(big.Int).And(u, mask64)
		hiBig := new(big.Int).Rsh(u, 64)
		hi = int64(hiBig.Uint64())
		lo = loBig.Uint64()
	}
	p := xdr.Int128Parts{Hi: xdr.Int64(hi), Lo: xdr.Uint64(lo)}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p}
}

func u32ScVal(n uint32) xdr.ScVal {
	x := xdr.Uint32(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &x}
}

func symbolScVal(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

// mapScVal builds an ScvMap with the given (sorted-by-key) entries.
// Caller must pre-sort keys to mirror what soroban-sdk emits.
func mapScVal(entries []xdr.ScMapEntry) xdr.ScVal {
	m := xdr.ScMap(entries)
	pm := &m
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}
}

// addressKeyedMap builds a Map<Address, i128> for AuctionData
// bid/lot fields.
func addressKeyedMap(t *testing.T, pairs ...struct {
	Asset  string
	Amount *big.Int
},
) xdr.ScVal {
	t.Helper()
	entries := make([]xdr.ScMapEntry, len(pairs))
	for i, p := range pairs {
		entries[i] = xdr.ScMapEntry{Key: addressScVal(t, p.Asset), Val: i128ScVal(t, p.Amount)}
	}
	return mapScVal(entries)
}

// vecScVal builds an ScvVec from the given values. Used for tuple-
// shaped event bodies (soroban-sdk emits unnamed tuples as Vec).
func vecScVal(vals ...xdr.ScVal) xdr.ScVal {
	v := xdr.ScVec(vals)
	pv := &v
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pv}
}

func encodeScVal(t *testing.T, sv xdr.ScVal) string {
	t.Helper()
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// auctionDataScVal builds an ScvMap with sorted-by-symbol keys
// matching the Blend AuctionData wire format.
func auctionDataScVal(bid, lot xdr.ScVal, block uint32) xdr.ScVal {
	// sorted: bid < block < lot
	return mapScVal([]xdr.ScMapEntry{
		{Key: symbolScVal("bid"), Val: bid},
		{Key: symbolScVal("block"), Val: u32ScVal(block)},
		{Key: symbolScVal("lot"), Val: lot},
	})
}

// ─── classify ───────────────────────────────────────────────────

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		top0 string
		want string
	}{
		{"new_auction", scval.MustEncodeSymbol("new_auction"), EventNewAuction},
		{"fill_auction", scval.MustEncodeSymbol("fill_auction"), EventFillAuction},
		{"delete_auction", scval.MustEncodeSymbol("delete_auction"), EventDeleteAuction},
		{"unrelated", scval.MustEncodeSymbol("borrow"), ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			topics := []string{tc.top0}
			if tc.top0 == "" {
				topics = nil
			}
			got := classify(&events.Event{Topic: topics})
			if got != tc.want {
				t.Errorf("classify=%q want %q", got, tc.want)
			}
		})
	}
}

// ─── decodeNewAuction ───────────────────────────────────────────

func TestDecodeNewAuction_HappyPath(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x01)
	user := accountStrkeyFromSeed(t, 0x10)
	collat := contractStrkeyFromSeed(t, 0x20)
	debt := contractStrkeyFromSeed(t, 0x30)

	bid := addressKeyedMap(t, struct {
		Asset  string
		Amount *big.Int
	}{debt, big.NewInt(500_000_000)})

	lot := addressKeyedMap(t, struct {
		Asset  string
		Amount *big.Int
	}{collat, big.NewInt(750_000_000)})

	body := vecScVal(
		u32ScVal(80), // percent
		auctionDataScVal(bid, lot, 12345),
	)

	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolNewAuction,
			encodeScVal(t, u32ScVal(AuctionTypeUserLiquidation)),
			encodeScVal(t, addressScVal(t, user)),
		},
		Value:          encodeScVal(t, body),
		Ledger:         55_000_000,
		TxHash:         "abc123",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-29T12:00:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeNewAuction(ev, closedAt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Pool != pool {
		t.Errorf("Pool=%q want %q", out.Pool, pool)
	}
	if out.AuctionType != AuctionTypeUserLiquidation {
		t.Errorf("AuctionType=%d want %d", out.AuctionType, AuctionTypeUserLiquidation)
	}
	if out.User != user {
		t.Errorf("User=%q want %q", out.User, user)
	}
	if out.Percent != 80 {
		t.Errorf("Percent=%d want 80", out.Percent)
	}
	if out.Data.Block != 12345 {
		t.Errorf("Block=%d want 12345", out.Data.Block)
	}
	if len(out.Data.Bid) != 1 || out.Data.Bid[0].Amount.Cmp(big.NewInt(500_000_000)) != 0 {
		t.Errorf("Bid mismatch: %+v", out.Data.Bid)
	}
	if len(out.Data.Lot) != 1 || out.Data.Lot[0].Amount.Cmp(big.NewInt(750_000_000)) != 0 {
		t.Errorf("Lot mismatch: %+v", out.Data.Lot)
	}
}

func TestDecodeNewAuction_UnknownAuctionType(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x01)
	user := accountStrkeyFromSeed(t, 0x10)

	body := vecScVal(u32ScVal(50), auctionDataScVal(
		mapScVal(nil), mapScVal(nil), 0,
	))
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolNewAuction,
			encodeScVal(t, u32ScVal(99)), // unknown type
			encodeScVal(t, addressScVal(t, user)),
		},
		Value:          encodeScVal(t, body),
		LedgerClosedAt: "2026-04-29T12:00:00Z",
	}
	_, err := decodeNewAuction(ev, time.Now())
	if err == nil {
		t.Fatal("expected error for unknown auction_type=99")
	}
	if !errors.Is(err, ErrUnknownAuctionType) {
		t.Errorf("err=%v not wrapping ErrUnknownAuctionType", err)
	}
}

// ─── decodeFillAuction ──────────────────────────────────────────

func TestDecodeFillAuction_HappyPath(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x02)
	user := accountStrkeyFromSeed(t, 0x11)
	filler := accountStrkeyFromSeed(t, 0x12)
	asset := contractStrkeyFromSeed(t, 0x40)

	bid := addressKeyedMap(t, struct {
		Asset  string
		Amount *big.Int
	}{asset, big.NewInt(100)})
	lot := addressKeyedMap(t, struct {
		Asset  string
		Amount *big.Int
	}{asset, big.NewInt(200)})

	body := vecScVal(
		addressScVal(t, filler),
		i128ScVal(t, big.NewInt(50)), // fill_percent (i128)
		auctionDataScVal(bid, lot, 99),
	)

	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolFillAuction,
			encodeScVal(t, u32ScVal(AuctionTypeBadDebt)),
			encodeScVal(t, addressScVal(t, user)),
		},
		Value:          encodeScVal(t, body),
		Ledger:         56_000_000,
		LedgerClosedAt: "2026-04-29T12:01:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeFillAuction(ev, closedAt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Filler != filler {
		t.Errorf("Filler=%q want %q", out.Filler, filler)
	}
	if out.FillPercent.Cmp(big.NewInt(50)) != 0 {
		t.Errorf("FillPercent=%s want 50", out.FillPercent)
	}
	if out.AuctionType != AuctionTypeBadDebt {
		t.Errorf("AuctionType=%d want %d", out.AuctionType, AuctionTypeBadDebt)
	}
}

// ─── decodeDeleteAuction ────────────────────────────────────────

func TestDecodeDeleteAuction_HappyPath(t *testing.T) {
	pool := contractStrkeyFromSeed(t, 0x03)
	user := accountStrkeyFromSeed(t, 0x13)
	ev := &events.Event{
		ContractID: pool,
		Topic: []string{
			TopicSymbolDeleteAuction,
			encodeScVal(t, u32ScVal(AuctionTypeInterest)),
			encodeScVal(t, addressScVal(t, user)),
		},
		Value:          encodeScVal(t, xdr.ScVal{Type: xdr.ScValTypeScvVoid}), // unit ()
		LedgerClosedAt: "2026-04-29T12:02:00Z",
	}
	closedAt, _ := time.Parse(time.RFC3339, ev.LedgerClosedAt)
	out, err := decodeDeleteAuction(ev, closedAt)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Pool != pool || out.User != user || out.AuctionType != AuctionTypeInterest {
		t.Errorf("decoded wrong: %+v", out)
	}
}

// ─── dispatcher.Decoder boundary ───────────────────────────────

func TestDecoder_Matches(t *testing.T) {
	d := NewDecoder()
	cases := []struct {
		top0 string
		want bool
	}{
		{TopicSymbolNewAuction, true},
		{TopicSymbolFillAuction, true},
		{TopicSymbolDeleteAuction, true},
		{TopicSymbolBorrow, false}, // money-market, not in this PR
		{TopicSymbolDeploy, false}, // factory event, separate adapter
		{"", false},
	}
	for _, tc := range cases {
		topics := []string{tc.top0}
		if tc.top0 == "" {
			topics = nil
		}
		got := d.Matches(events.Event{Topic: topics})
		if got != tc.want {
			t.Errorf("topic[0]=%q Matches=%v want %v", tc.top0, got, tc.want)
		}
	}
}

func TestDecoder_NameAndKind(t *testing.T) {
	d := NewDecoder()
	if d.Name() != SourceName {
		t.Errorf("Name=%q want %q", d.Name(), SourceName)
	}
	if (NewAuctionEvent{}).Source() != SourceName {
		t.Errorf("NewAuctionEvent.Source mismatch")
	}
	if (NewAuctionEvent{}).EventKind() != NewAuctionEventKind {
		t.Errorf("NewAuctionEvent.EventKind mismatch")
	}
	if (FillAuctionEvent{}).EventKind() != FillAuctionEventKind {
		t.Errorf("FillAuctionEvent.EventKind mismatch")
	}
	if (DeleteAuctionEvent{}).EventKind() != DeleteAuctionEventKind {
		t.Errorf("DeleteAuctionEvent.EventKind mismatch")
	}
}
