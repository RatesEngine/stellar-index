package sep41_transfers

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/events"
)

const (
	cWatched   = "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4"
	cUnwatched = "CAAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQC526"
	gFrom      = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"
	gTo        = "GAAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQDZ7H"
	gSpender   = "GABAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEAQCAIBAEJXA"
)

func encScVal(t *testing.T, sv xdr.ScVal) string {
	t.Helper()
	b, err := sv.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func sym(s string) xdr.ScVal {
	x := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &x}
}

func addr(t *testing.T, g string) xdr.ScVal {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteAccountID, g)
	if err != nil {
		t.Fatalf("decode %q: %v", g, err)
	}
	var pub xdr.Uint256
	copy(pub[:], raw)
	aid := xdr.AccountId{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &pub}
	a := xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &aid}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &a}
}

func i128(n int64) xdr.ScVal {
	p := xdr.Int128Parts{Hi: 0, Lo: xdr.Uint64(n)}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p}
}

func u32(n uint32) xdr.ScVal {
	v := xdr.Uint32(n)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &v}
}

func boolVal(b bool) xdr.ScVal {
	v := b
	return xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &v}
}

func vec(elems ...xdr.ScVal) xdr.ScVal {
	v := xdr.ScVec(elems)
	pp := &v
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &pp}
}

func transferEvent(t *testing.T, contract string, amount int64) events.Event {
	t.Helper()
	return events.Event{
		Type:           "contract",
		ContractID:     contract,
		Ledger:         100,
		LedgerClosedAt: "2026-05-27T12:00:00Z",
		TxHash:         "tx_transfer",
		Topic: []string{
			encScVal(t, sym("transfer")),
			encScVal(t, addr(t, gFrom)),
			encScVal(t, addr(t, gTo)),
		},
		Value: encScVal(t, i128(amount)),
	}
}

func approveEvent(t *testing.T, contract string, amount int64, liveUntil uint32) events.Event {
	t.Helper()
	return events.Event{
		Type:           "contract",
		ContractID:     contract,
		Ledger:         101,
		LedgerClosedAt: "2026-05-27T12:00:01Z",
		TxHash:         "tx_approve",
		Topic: []string{
			encScVal(t, sym("approve")),
			encScVal(t, addr(t, gFrom)),
			encScVal(t, addr(t, gSpender)),
		},
		Value: encScVal(t, vec(i128(amount), u32(liveUntil))),
	}
}

func setAdminEvent(t *testing.T, contract string, withAdminTopic bool) events.Event {
	t.Helper()
	topic := []string{encScVal(t, sym("set_admin"))}
	if withAdminTopic {
		topic = append(topic, encScVal(t, addr(t, gFrom)))
	}
	return events.Event{
		Type:           "contract",
		ContractID:     contract,
		Ledger:         102,
		LedgerClosedAt: "2026-05-27T12:00:02Z",
		TxHash:         "tx_set_admin",
		Topic:          topic,
		Value:          encScVal(t, addr(t, gTo)),
	}
}

func setAuthorizedEvent(t *testing.T, contract string, authorize bool) events.Event {
	t.Helper()
	return events.Event{
		Type:           "contract",
		ContractID:     contract,
		Ledger:         103,
		LedgerClosedAt: "2026-05-27T12:00:03Z",
		TxHash:         "tx_set_authorized",
		Topic: []string{
			encScVal(t, sym("set_authorized")),
			encScVal(t, addr(t, gTo)),
		},
		Value: encScVal(t, boolVal(authorize)),
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

func TestDecoder_MatchesAllFourKinds(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	cases := []struct {
		name string
		ev   events.Event
	}{
		{"transfer", transferEvent(t, cWatched, 1_000)},
		{"approve", approveEvent(t, cWatched, 500, 999_999)},
		{"set_admin", setAdminEvent(t, cWatched, true)},
		{"set_authorized", setAuthorizedEvent(t, cWatched, true)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !d.Matches(tc.ev) {
				t.Errorf("expected match on %s", tc.name)
			}
		})
	}
}

func TestDecoder_SkipsSupplyKinds(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	for _, supplyKind := range []string{"mint", "burn", "clawback"} {
		ev := transferEvent(t, cWatched, 1)
		ev.Topic[0] = encScVal(t, sym(supplyKind))
		if d.Matches(ev) {
			t.Errorf("%s: expected NO match (belongs to sep41_supply)", supplyKind)
		}
	}
}

func TestDecoder_SkipsUnwatchedContract(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	if d.Matches(transferEvent(t, cUnwatched, 1)) {
		t.Errorf("expected NO match on unwatched contract")
	}
}

func TestDecoder_DecodeTransfer(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	outs, err := d.Decode(transferEvent(t, cWatched, 1_000_000))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out := outs[0].(Event)
	if out.Kind != SymbolTransfer {
		t.Errorf("Kind = %q, want %q", out.Kind, SymbolTransfer)
	}
	if out.FromAddr != gFrom {
		t.Errorf("From = %q, want %q", out.FromAddr, gFrom)
	}
	if out.ToAddr != gTo {
		t.Errorf("To = %q, want %q", out.ToAddr, gTo)
	}
	if out.Amount == nil || out.Amount.Int64() != 1_000_000 {
		t.Errorf("Amount = %v, want 1_000_000", out.Amount)
	}
	want := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if !out.ObservedAt.Equal(want) {
		t.Errorf("ObservedAt = %v, want %v", out.ObservedAt, want)
	}
}

// TestDecoder_DecodeTransferMapBody pins the post-CAP-67 muxed-
// recipient form: data is a Map with `amount` + `to_muxed_id`.
func TestDecoder_DecodeTransferMapBody(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	amtKey := xdr.ScSymbol("amount")
	muxKey := xdr.ScSymbol("to_muxed_id")
	muxVal := xdr.Uint64(42)
	mapVal := xdr.ScMap{
		{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &amtKey}, Val: i128(2_000_000)},
		{Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &muxKey}, Val: xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &muxVal}},
	}
	pm := &mapVal
	sv := xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &pm}
	ev := transferEvent(t, cWatched, 0)
	ev.Value = encScVal(t, sv)
	outs, err := d.Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out := outs[0].(Event)
	if out.Amount == nil || out.Amount.Int64() != 2_000_000 {
		t.Errorf("Amount = %v, want 2_000_000 (extracted from map.amount)", out.Amount)
	}
}

func TestDecoder_DecodeApprove(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	outs, err := d.Decode(approveEvent(t, cWatched, 500, 999_999))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out := outs[0].(Event)
	if out.Kind != SymbolApprove {
		t.Errorf("Kind = %q, want %q", out.Kind, SymbolApprove)
	}
	if out.FromAddr != gFrom {
		t.Errorf("From = %q, want %q", out.FromAddr, gFrom)
	}
	if out.ToAddr != gSpender {
		t.Errorf("ToAddr (spender) = %q, want %q", out.ToAddr, gSpender)
	}
	if out.Amount == nil || out.Amount.Int64() != 500 {
		t.Errorf("Amount = %v, want 500", out.Amount)
	}
	if out.LiveUntilLedger != 999_999 {
		t.Errorf("LiveUntilLedger = %d, want 999999", out.LiveUntilLedger)
	}
}

func TestDecoder_DecodeSetAdminWithTopic(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	outs, err := d.Decode(setAdminEvent(t, cWatched, true))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out := outs[0].(Event)
	if out.Kind != SymbolSetAdmin {
		t.Errorf("Kind = %q, want %q", out.Kind, SymbolSetAdmin)
	}
	if out.FromAddr != gFrom {
		t.Errorf("FromAddr (admin) = %q, want %q", out.FromAddr, gFrom)
	}
	if out.ToAddr != gTo {
		t.Errorf("ToAddr (new_admin) = %q, want %q", out.ToAddr, gTo)
	}
	if out.Amount != nil {
		t.Errorf("Amount = %v, want nil (set_admin has no amount)", out.Amount)
	}
}

func TestDecoder_DecodeSetAdminWithoutTopic(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	outs, err := d.Decode(setAdminEvent(t, cWatched, false))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	out := outs[0].(Event)
	if out.FromAddr != "" {
		t.Errorf("FromAddr = %q, want empty", out.FromAddr)
	}
	if out.ToAddr != gTo {
		t.Errorf("ToAddr (new_admin) = %q, want %q", out.ToAddr, gTo)
	}
}

func TestDecoder_DecodeSetAuthorized(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	for _, authorize := range []bool{true, false} {
		outs, err := d.Decode(setAuthorizedEvent(t, cWatched, authorize))
		if err != nil {
			t.Fatalf("Decode(authorize=%v): %v", authorize, err)
		}
		out := outs[0].(Event)
		if out.Kind != SymbolSetAuthorized {
			t.Errorf("Kind = %q, want %q", out.Kind, SymbolSetAuthorized)
		}
		if out.ToAddr != gTo {
			t.Errorf("ToAddr (id) = %q, want %q", out.ToAddr, gTo)
		}
		if out.Authorized == nil || *out.Authorized != authorize {
			t.Errorf("Authorized = %v, want &%v", out.Authorized, authorize)
		}
	}
}

func TestDecoder_RejectsShortTopic(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	ev := approveEvent(t, cWatched, 1, 100)
	ev.Topic = ev.Topic[:2]
	_, err := d.Decode(ev)
	if !errors.Is(err, ErrShortTopic) {
		t.Errorf("err = %v, want wrapping ErrShortTopic", err)
	}
}

func TestDecoder_RejectsBadApproveValue(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	ev := approveEvent(t, cWatched, 1, 100)
	ev.Value = encScVal(t, i128(1))
	_, err := d.Decode(ev)
	if !errors.Is(err, ErrBadValue) {
		t.Errorf("err = %v, want wrapping ErrBadValue", err)
	}
}

func TestDecoder_HasI128SafeAmount(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	huge := new(big.Int).Mul(big.NewInt(5_000_000_000), big.NewInt(1_000_000_000))
	p := xdr.Int128Parts{Hi: 0, Lo: xdr.Uint64(huge.Uint64())}
	ev := transferEvent(t, cWatched, 0)
	ev.Value = encScVal(t, xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p})
	outs, err := d.Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if outs[0].(Event).Amount.Cmp(huge) != 0 {
		t.Errorf("Amount = %s, want %s", outs[0].(Event).Amount, huge)
	}
}

func TestDecoder_NameAndSource(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})
	if d.Name() != SourceName {
		t.Errorf("Name() = %q, want %q", d.Name(), SourceName)
	}
	e := Event{Kind: SymbolTransfer}
	if e.Source() != SourceName {
		t.Errorf("Event.Source() = %q, want %q", e.Source(), SourceName)
	}
	if e.EventKind() != EventKind {
		t.Errorf("Event.EventKind() = %q, want %q", e.EventKind(), EventKind)
	}
}
