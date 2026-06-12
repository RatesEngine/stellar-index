package sep41_supply

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// Synthetic but checksum-valid C/G strkeys (zero/one byte
// patterns) so the test fixtures don't depend on real network
// addresses.
const (
	cWatched   = "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"
	cUnwatched = "CAAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQC526"
	gAdmin     = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"
	gHolder    = "GAAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQDZ7H"
)

func encodeScVal(t *testing.T, sv xdr.ScVal) string {
	t.Helper()
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func symbolScVal(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func addressScValG(t *testing.T, g string) xdr.ScVal {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteAccountID, g)
	if err != nil {
		t.Fatalf("decode %q: %v", g, err)
	}
	var pub xdr.Uint256
	copy(pub[:], raw)
	aid := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &pub}
	addr := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &aid}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}
}

func i128ScVal(n int64) xdr.ScVal {
	p := xdr.Int128Parts{Hi: 0, Lo: xdr.Uint64(n)}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p}
}

func mintEvent(t *testing.T, contract string, amount int64) events.Event {
	t.Helper()
	return events.Event{
		Type:           "contract",
		ContractID:     contract,
		Ledger:         42,
		LedgerClosedAt: "2026-04-30T12:00:00Z",
		TxHash:         "abcd",
		OperationIndex: 0,
		Topic: []string{
			encodeScVal(t, symbolScVal("mint")),
			encodeScVal(t, addressScValG(t, gAdmin)),
			encodeScVal(t, addressScValG(t, gHolder)),
		},
		Value: encodeScVal(t, i128ScVal(amount)),
	}
}

func burnEvent(t *testing.T, contract string, amount int64) events.Event {
	t.Helper()
	return events.Event{
		Type:           "contract",
		ContractID:     contract,
		Ledger:         42,
		LedgerClosedAt: "2026-04-30T12:00:00Z",
		TxHash:         "abcd",
		Topic: []string{
			encodeScVal(t, symbolScVal("burn")),
			encodeScVal(t, addressScValG(t, gHolder)),
		},
		Value: encodeScVal(t, i128ScVal(amount)),
	}
}

func clawbackEvent(t *testing.T, contract string, amount int64) events.Event {
	t.Helper()
	return events.Event{
		Type:           "contract",
		ContractID:     contract,
		Ledger:         42,
		LedgerClosedAt: "2026-04-30T12:00:00Z",
		TxHash:         "abcd",
		Topic: []string{
			encodeScVal(t, symbolScVal("clawback")),
			encodeScVal(t, addressScValG(t, gAdmin)),
			encodeScVal(t, addressScValG(t, gHolder)),
		},
		Value: encodeScVal(t, i128ScVal(amount)),
	}
}

func transferEvent(t *testing.T, contract string) events.Event {
	t.Helper()
	return events.Event{
		Type:       "contract",
		ContractID: contract,
		Topic: []string{
			encodeScVal(t, symbolScVal("transfer")),
			encodeScVal(t, addressScValG(t, gAdmin)),
			encodeScVal(t, addressScValG(t, gHolder)),
		},
		Value: encodeScVal(t, i128ScVal(1)),
	}
}

func TestNewDecoder_RejectsEmpty(t *testing.T) {
	if _, err := NewDecoder(nil); !errors.Is(err, ErrEmptyWatchSet) {
		t.Errorf("nil: err=%v want ErrEmptyWatchSet", err)
	}
	if _, err := NewDecoder([]string{""}); err == nil {
		t.Errorf("empty contract id should error")
	}
}

func TestDecoder_MatchesMintBurnClawback(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	cases := []struct {
		name string
		ev   events.Event
	}{
		{"mint", mintEvent(t, cWatched, 1)},
		{"burn", burnEvent(t, cWatched, 1)},
		{"clawback", clawbackEvent(t, cWatched, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !d.Matches(tc.ev) {
				t.Errorf("expected match on %s", tc.name)
			}
		})
	}
}

// TestDecoder_SkipsTransfer — transfers move ownership not
// supply; Match returns false even on a watched contract.
func TestDecoder_SkipsTransfer(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	if d.Matches(transferEvent(t, cWatched)) {
		t.Errorf("expected NO match on transfer")
	}
}

func TestDecoder_SkipsUnwatchedContract(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	if d.Matches(mintEvent(t, cUnwatched, 1)) {
		t.Errorf("expected NO match on unwatched contract")
	}
}

// TestDecoder_SkipsNonContractEventType — system / diagnostic
// events (Type != "contract") never match.
func TestDecoder_SkipsNonContractEventType(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	ev := mintEvent(t, cWatched, 1)
	ev.Type = "diagnostic"
	if d.Matches(ev) {
		t.Errorf("expected NO match on diagnostic event")
	}
}

func TestDecoder_DecodeMint(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	outs, err := d.Decode(mintEvent(t, cWatched, 1_000_000))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out := outs[0].(Event)
	if out.Kind != SymbolMint {
		t.Errorf("Kind=%q want %q", out.Kind, SymbolMint)
	}
	if out.Amount.Int64() != 1_000_000 {
		t.Errorf("Amount=%s want 1000000", out.Amount)
	}
	if out.Counterparty != gHolder {
		t.Errorf("Counterparty=%q want %q (mint→to)", out.Counterparty, gHolder)
	}
	if out.ContractID != cWatched {
		t.Errorf("ContractID=%q want %q", out.ContractID, cWatched)
	}
	if out.Ledger != 42 {
		t.Errorf("Ledger=%d want 42", out.Ledger)
	}
	want := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	if !out.ObservedAt.Equal(want) {
		t.Errorf("ObservedAt=%v want %v", out.ObservedAt, want)
	}
}

func TestDecoder_DecodeBurnCounterpartyAtTopic1(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	outs, err := d.Decode(burnEvent(t, cWatched, 500))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out := outs[0].(Event)
	if out.Counterparty != gHolder {
		t.Errorf("burn Counterparty=%q want %q (topic[1]=from)", out.Counterparty, gHolder)
	}
}

func TestDecoder_DecodeClawbackCounterpartyAtTopic2(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	outs, err := d.Decode(clawbackEvent(t, cWatched, 300))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out := outs[0].(Event)
	if out.Counterparty != gHolder {
		t.Errorf("clawback Counterparty=%q want %q (topic[2]=from)", out.Counterparty, gHolder)
	}
}

// TestDecoder_DecodeShortBurnTopic — older SEP-41 spec variants
// might emit shorter topic vectors. The decoder surfaces
// ErrShortTopic so the caller can drop the event rather than
// write garbage.
func TestDecoder_DecodeShortBurnTopic(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	ev := burnEvent(t, cWatched, 1)
	ev.Topic = ev.Topic[:1] // strip everything except topic[0]
	_, err := d.Decode(ev)
	if !errors.Is(err, ErrShortTopic) {
		t.Errorf("err=%v want wrapping ErrShortTopic", err)
	}
}

// TestDecoder_DecodeRejectsNonI128Value — a malformed Value
// (not i128) is upstream contract-bug; surface it loudly.
func TestDecoder_DecodeRejectsNonI128Value(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	ev := mintEvent(t, cWatched, 1)
	// Replace Value with a u32 instead of i128.
	x := xdr.Uint32(1)
	ev.Value = encodeScVal(t, xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &x})
	_, err := d.Decode(ev)
	if !errors.Is(err, ErrAmountNotI128) {
		t.Errorf("err=%v want wrapping ErrAmountNotI128", err)
	}
}

func TestDecoder_DecodeNegativeAmount(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	ev := mintEvent(t, cWatched, 1)
	// Construct a negative i128 — Hi has top bit set in two's-complement.
	p := xdr.Int128Parts{Hi: -1, Lo: 0xFFFFFFFFFFFFFFFE}
	ev.Value = encodeScVal(t, xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p})
	_, err := d.Decode(ev)
	if err == nil {
		t.Fatal("expected error on negative amount")
	}
}

// TestDecoder_CAP67_FourTopic_BackCompat pins the CLAUDE.md surprise:
// post-P23 (Whisk, mainnet 2025-09-03) SEP-41 supply events grew a
// fourth topic carrying the SEP-11 asset string
// (`sep0011_asset`). The decoder reads counterparty positionally
// (topic[1] for burn, topic[2] for mint/clawback) and IGNORES the
// optional 4th topic — but a future contributor reading the
// shorter pre-P23 spec might naively assert topic length and
// reject the post-P23 shape.
//
// Lock the behaviour with explicit fixtures for both arities.
// F-1242 (audit-2026-05-12).
func TestDecoder_CAP67_FourTopic_BackCompat(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})

	// Use XLM's pubnet SEP-11 representation as a realistic
	// 4th-topic value (the field carries a SEP-11 asset string).
	sep0011 := encodeScVal(t, symbolScVal("native"))

	cases := []struct {
		name            string
		buildEvent      func(t *testing.T) events.Event
		wantKind        string
		wantCounterPty  string
		wantOrigArity   int
		extendArityWith string // the optional 4th topic
	}{
		{
			name:            "mint pre-P23 (3 topics)",
			buildEvent:      func(t *testing.T) events.Event { return mintEvent(t, cWatched, 100) },
			wantKind:        SymbolMint,
			wantCounterPty:  gHolder,
			wantOrigArity:   3,
			extendArityWith: "",
		},
		{
			name: "mint post-P23 (4 topics inc. sep0011_asset)",
			buildEvent: func(t *testing.T) events.Event {
				ev := mintEvent(t, cWatched, 100)
				ev.Topic = append(ev.Topic, sep0011)
				return ev
			},
			wantKind:       SymbolMint,
			wantCounterPty: gHolder,
			wantOrigArity:  4,
		},
		{
			name:           "burn pre-P23 (2 topics)",
			buildEvent:     func(t *testing.T) events.Event { return burnEvent(t, cWatched, 100) },
			wantKind:       SymbolBurn,
			wantCounterPty: gHolder,
			wantOrigArity:  2,
		},
		{
			name: "burn post-P23 (3 topics inc. sep0011_asset)",
			buildEvent: func(t *testing.T) events.Event {
				ev := burnEvent(t, cWatched, 100)
				ev.Topic = append(ev.Topic, sep0011)
				return ev
			},
			wantKind:       SymbolBurn,
			wantCounterPty: gHolder,
			wantOrigArity:  3,
		},
		{
			name:           "clawback pre-P23 (3 topics)",
			buildEvent:     func(t *testing.T) events.Event { return clawbackEvent(t, cWatched, 100) },
			wantKind:       SymbolClawback,
			wantCounterPty: gHolder,
			wantOrigArity:  3,
		},
		{
			name: "clawback post-P23 (4 topics inc. sep0011_asset)",
			buildEvent: func(t *testing.T) events.Event {
				ev := clawbackEvent(t, cWatched, 100)
				ev.Topic = append(ev.Topic, sep0011)
				return ev
			},
			wantKind:       SymbolClawback,
			wantCounterPty: gHolder,
			wantOrigArity:  4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := tc.buildEvent(t)
			if len(ev.Topic) != tc.wantOrigArity {
				t.Fatalf("test fixture arity = %d, expected %d", len(ev.Topic), tc.wantOrigArity)
			}
			outs, err := d.Decode(ev)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if len(outs) != 1 {
				t.Fatalf("Decode returned %d events, want 1", len(outs))
			}
			out := outs[0].(Event)
			if out.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", out.Kind, tc.wantKind)
			}
			if out.Counterparty != tc.wantCounterPty {
				t.Errorf("Counterparty = %q, want %q (positional decode must ignore the 4th topic)",
					out.Counterparty, tc.wantCounterPty)
			}
		})
	}
}

// TestDecoder_HasI128SafeAmount — ensure the decoder preserves
// large values that exceed int64.
func TestDecoder_HasI128SafeAmount(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	// 5 × 10^18 > int64 max (~9.2 × 10^18 fits, but smaller test
	// value still exercises the i128 → big.Int path).
	huge := new(big.Int).Mul(big.NewInt(5_000_000_000), big.NewInt(1_000_000_000))
	p := xdr.Int128Parts{Hi: 0, Lo: xdr.Uint64(huge.Uint64())}
	ev := mintEvent(t, cWatched, 1)
	ev.Value = encodeScVal(t, xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p})
	outs, err := d.Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if outs[0].(Event).Amount.Cmp(huge) != 0 {
		t.Errorf("Amount=%s want %s", outs[0].(Event).Amount, huge)
	}
}

// TestDecoder_PopulatesEventIndex pins F-1324: EventIndex must be
// carried onto the row so multiple supply events emitted by one op
// (mint-to-many, or a burn + clawback in one call) don't collapse on
// the sep41_supply_events PK (migration 0057) via ON CONFLICT.
func TestDecoder_PopulatesEventIndex(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	ev := mintEvent(t, cWatched, 1_000_000)
	ev.EventIndex = 4
	outs, err := d.Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("emitted %d events, want 1", len(outs))
	}
	got := outs[0].(Event)
	if got.EventIndex != 4 {
		t.Errorf("EventIndex = %d, want 4 (F-1324)", got.EventIndex)
	}
}
