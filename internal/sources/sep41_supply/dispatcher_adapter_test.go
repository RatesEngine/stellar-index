package sep41_supply

import (
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarIndex/stellar-index/internal/events"
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

// stringScVal builds an ScvString — the sep0011_asset topic carries a
// SEP-11 asset STRING (e.g. "USDC:GA5..."), not a Symbol. Matches the
// on-wire shape (lake-verified: topic[2] ScValType = String/0x0E).
func stringScVal(s string) xdr.ScVal {
	str := xdr.ScString(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &str}
}

// mintEventCAP67 builds the REAL post-P23 (CAP-67 / Whisk) mint shape:
// ["mint", to(Addr), sep0011_asset(String)] — `to` at topic[1], the asset
// String at topic[2]. This is the dominant mainnet shape (99.96% of recent
// mints per the r1 lake) and is DISTINCT from the legacy mintEvent helper,
// which prefixes the admin (`to` at topic[2]). The old decoder dropped this.
func mintEventCAP67(t *testing.T, contract string, amount int64) events.Event {
	t.Helper()
	ev := mintEvent(t, contract, amount)
	ev.Topic = []string{
		encodeScVal(t, symbolScVal("mint")),
		encodeScVal(t, addressScValG(t, gHolder)),   // to @ topic[1]
		encodeScVal(t, stringScVal("USDC:"+gAdmin)), // sep0011_asset @ topic[2]
	}
	return ev
}

// clawbackEventCAP67 builds the REAL CAP-67 clawback shape:
// ["clawback", from(Addr), sep0011_asset(String)] — `from` at topic[1].
func clawbackEventCAP67(t *testing.T, contract string, amount int64) events.Event {
	t.Helper()
	ev := clawbackEvent(t, contract, amount)
	ev.Topic = []string{
		encodeScVal(t, symbolScVal("clawback")),
		encodeScVal(t, addressScValG(t, gHolder)),   // from @ topic[1]
		encodeScVal(t, stringScVal("USDC:"+gAdmin)), // sep0011_asset @ topic[2]
	}
	return ev
}

// mintEventBareSpec builds the bare SEP-41 spec mint: ["mint", to] (2 topics,
// no admin, no asset) — `to` at topic[1].
func mintEventBareSpec(t *testing.T, contract string, amount int64) events.Event {
	t.Helper()
	ev := mintEvent(t, contract, amount)
	ev.Topic = []string{
		encodeScVal(t, symbolScVal("mint")),
		encodeScVal(t, addressScValG(t, gHolder)), // to @ topic[1]
	}
	return ev
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

// TestDecoder_CounterpartyAcrossShapes pins the shape-aware counterparty
// decode across EVERY mainnet-observed topic shape (lake-verified on r1
// 2026-06-15). The counterparty position is NOT fixed by topic count: the
// legacy SAC form prefixes `admin` (counterparty at topic[2]), while the
// CAP-67 / Whisk form (mainnet 2025-09-03, 99.96% of recent mints + 100% of
// clawbacks) puts the counterparty at topic[1] and the sep0011_asset STRING at
// topic[2]. The discriminator is the TYPE of topic[2] (Address ⇒ legacy).
//
// This supersedes the old back-compat test, whose "post-P23" fixture appended
// sep0011 to the LEGACY admin-prefixed form (`["mint", admin, to, sep0011]`) —
// a shape mainnet never emits — and so passed while the decoder silently
// dropped every real CAP-67 mint + clawback (F-13xx, audit-2026-06-14).
func TestDecoder_CounterpartyAcrossShapes(t *testing.T) {
	d, _ := NewDecoder([]string{cWatched})

	cases := []struct {
		name       string
		buildEvent func(t *testing.T) events.Event
		wantKind   string
		wantCpty   string
	}{
		// mint
		{"mint legacy SAC [mint,admin,to]", func(t *testing.T) events.Event { return mintEvent(t, cWatched, 100) }, SymbolMint, gHolder},
		{"mint CAP-67 [mint,to,sep0011]", func(t *testing.T) events.Event { return mintEventCAP67(t, cWatched, 100) }, SymbolMint, gHolder},
		{"mint bare-spec [mint,to]", func(t *testing.T) events.Event { return mintEventBareSpec(t, cWatched, 100) }, SymbolMint, gHolder},
		// clawback
		{"clawback legacy [clawback,admin,from]", func(t *testing.T) events.Event { return clawbackEvent(t, cWatched, 100) }, SymbolClawback, gHolder},
		{"clawback CAP-67 [clawback,from,sep0011]", func(t *testing.T) events.Event { return clawbackEventCAP67(t, cWatched, 100) }, SymbolClawback, gHolder},
		// burn (topic[1]=from in every shape)
		{"burn spec [burn,from]", func(t *testing.T) events.Event { return burnEvent(t, cWatched, 100) }, SymbolBurn, gHolder},
		{"burn CAP-67 [burn,from,sep0011]", func(t *testing.T) events.Event {
			ev := burnEvent(t, cWatched, 100)
			ev.Topic = append(ev.Topic, encodeScVal(t, stringScVal("USDC:"+gAdmin)))
			return ev
		}, SymbolBurn, gHolder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outs, err := d.Decode(tc.buildEvent(t))
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
			if out.Counterparty != tc.wantCpty {
				t.Errorf("Counterparty = %q, want %q", out.Counterparty, tc.wantCpty)
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
