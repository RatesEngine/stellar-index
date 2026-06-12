package cctp

import (
	"math/big"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// depositForBurnEvent builds a complete, well-formed deposit_for_burn
// events.Event for adapter-level tests. contractID lets a test point
// it at a non-CCTP contract to exercise the Matches gate.
func depositForBurnEvent(t *testing.T, contractID string) events.Event {
	t.Helper()
	burnToken := makeContractStrkey(t, 0x10)
	depositor := makeAccountStrkey(t, 0x20)
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("amount"), Val: i128(big.NewInt(12_345_678))},
		xdr.ScMapEntry{Key: symbol("destination_caller"), Val: scBytes(makeBytesN32(0x50))},
		xdr.ScMapEntry{Key: symbol("destination_domain"), Val: u32(0)},
		xdr.ScMapEntry{Key: symbol("destination_token_messenger"), Val: scBytes(makeBytesN32(0x40))},
		xdr.ScMapEntry{Key: symbol("hook_data"), Val: scBytes([]byte("hook"))},
		xdr.ScMapEntry{Key: symbol("max_fee"), Val: i128(big.NewInt(500))},
		xdr.ScMapEntry{Key: symbol("mint_recipient"), Val: scBytes(makeBytesN32(0x30))},
	))
	return events.Event{
		Type:           "contract",
		Ledger:         62_700_000,
		LedgerClosedAt: "2026-05-20T14:00:00Z",
		ContractID:     contractID,
		OperationIndex: 1,
		TxHash:         "abc123",
		Topic: []string{
			TopicSymbolDepositForBurn,
			b64(t, contractAddrFromStrkey(t, burnToken)),
			b64(t, accountAddrFromStrkey(t, depositor)),
			b64(t, u32(2000)),
		},
		Value: body,
	}
}

func TestDecoder_Name(t *testing.T) {
	t.Parallel()
	if got := (&Decoder{}).Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
}

func TestDecoder_Matches(t *testing.T) {
	t.Parallel()
	d := NewDecoder()

	t.Run("CCTP topic from CCTP contract", func(t *testing.T) {
		t.Parallel()
		if !d.Matches(depositForBurnEvent(t, MainnetTokenMessengerMinter)) {
			t.Error("want Matches=true for a deposit_for_burn from TokenMessengerMinter")
		}
	})

	t.Run("CCTP topic from non-CCTP contract", func(t *testing.T) {
		t.Parallel()
		// Same topic bytes, foreign emitter — must be rejected so a
		// look-alike contract can't inject rows (CLAUDE.md "Comet
		// uses a shared topic").
		impostor := makeContractStrkey(t, 0x99)
		if d.Matches(depositForBurnEvent(t, impostor)) {
			t.Error("want Matches=false for a CCTP topic from a non-CCTP contract")
		}
	})

	t.Run("non-CCTP topic from CCTP contract", func(t *testing.T) {
		t.Parallel()
		ev := events.Event{
			ContractID: MainnetTokenMessengerMinter,
			Topic:      []string{b64(t, symbol("transfer"))},
		}
		if d.Matches(ev) {
			t.Error("want Matches=false for an unrecognised topic")
		}
	})
}

func TestIsCCTPContract(t *testing.T) {
	t.Parallel()
	for _, id := range []string{MainnetTokenMessengerMinter, MainnetMessageTransmitter, MainnetCctpForwarder} {
		if !IsCCTPContract(id) {
			t.Errorf("IsCCTPContract(%q) = false, want true", id)
		}
	}
	if IsCCTPContract(makeContractStrkey(t, 0x99)) {
		t.Error("IsCCTPContract on a foreign contract = true, want false")
	}
}

func TestDecoder_Decode_DepositForBurn(t *testing.T) {
	t.Parallel()
	out, err := NewDecoder().Decode(depositForBurnEvent(t, MainnetTokenMessengerMinter))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Decode emitted %d events, want 1", len(out))
	}
	ev, ok := out[0].(Event)
	if !ok {
		t.Fatalf("emitted event is %T, want cctp.Event", out[0])
	}
	if ev.EventType != EventDepositForBurn {
		t.Errorf("EventType = %q, want %q", ev.EventType, EventDepositForBurn)
	}
	if ev.Amount != "12345678" {
		t.Errorf("Amount = %q, want 12345678", ev.Amount)
	}
	if ev.Fee != "500" {
		t.Errorf("Fee = %q, want 500", ev.Fee)
	}
	if ev.CounterpartyDomain == nil || *ev.CounterpartyDomain != 0 {
		t.Errorf("CounterpartyDomain = %v, want 0", ev.CounterpartyDomain)
	}
	if ev.Token == "" {
		t.Error("Token (burn_token) should be populated")
	}
	if ev.ObservedAt.IsZero() {
		t.Error("ObservedAt should be parsed from LedgerClosedAt")
	}
	for _, k := range []string{"depositor", "mint_recipient", "hook_data", "min_finality_threshold"} {
		if _, present := ev.Attributes[k]; !present {
			t.Errorf("Attributes missing %q", k)
		}
	}
	// Compile-time + runtime confirmation it is a consumer.Event.
	var _ consumer.Event = ev
	if ev.Source() != SourceName {
		t.Errorf("Source() = %q, want %q", ev.Source(), SourceName)
	}
}

func TestDecoder_Decode_NonCCTPContract(t *testing.T) {
	t.Parallel()
	// A genuine CCTP-shaped event from a foreign contract: Decode
	// returns nothing rather than minting a row.
	out, err := NewDecoder().Decode(depositForBurnEvent(t, makeContractStrkey(t, 0x99)))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Decode emitted %d events for a foreign contract, want 0", len(out))
	}
}

func TestDecoder_Decode_EmptyClosedAt(t *testing.T) {
	t.Parallel()
	ev := depositForBurnEvent(t, MainnetTokenMessengerMinter)
	ev.LedgerClosedAt = "" // EventClosedAt fails closed
	_, err := NewDecoder().Decode(ev)
	if err == nil {
		t.Fatal("want an error when LedgerClosedAt is empty")
	}
}

func TestDecoder_Decode_MintAndWithdraw(t *testing.T) {
	t.Parallel()
	mintRecipient := makeAccountStrkey(t, 0x60)
	mintToken := makeContractStrkey(t, 0x70)
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("amount"), Val: i128(big.NewInt(1_000_000_000))},
		xdr.ScMapEntry{Key: symbol("fee_collected"), Val: i128(big.NewInt(50))},
	))
	ev := events.Event{
		Ledger:         62_700_005,
		LedgerClosedAt: "2026-05-20T14:00:25Z",
		ContractID:     MainnetTokenMessengerMinter,
		Topic: []string{
			TopicSymbolMintAndWithdraw,
			b64(t, accountAddrFromStrkey(t, mintRecipient)),
			b64(t, contractAddrFromStrkey(t, mintToken)),
		},
		Value: body,
	}
	out, err := NewDecoder().Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Decode emitted %d events, want 1", len(out))
	}
	got := out[0].(Event)
	if got.EventType != EventMintAndWithdraw {
		t.Errorf("EventType = %q, want %q", got.EventType, EventMintAndWithdraw)
	}
	if got.Amount != "1000000000" || got.Fee != "50" {
		t.Errorf("Amount/Fee = %q/%q, want 1000000000/50", got.Amount, got.Fee)
	}
	// mint_and_withdraw carries no domain.
	if got.CounterpartyDomain != nil {
		t.Errorf("CounterpartyDomain = %v, want nil", *got.CounterpartyDomain)
	}
}

func TestDecoder_Decode_MessageReceived(t *testing.T) {
	t.Parallel()
	caller := makeAccountStrkey(t, 0x80)
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("message_body"), Val: scBytes([]byte("payload"))},
		xdr.ScMapEntry{Key: symbol("sender"), Val: scBytes(makeBytesN32(0xA0))},
		xdr.ScMapEntry{Key: symbol("source_domain"), Val: u32(7)}, // Solana
	))
	ev := events.Event{
		LedgerClosedAt: "2026-05-20T14:01:00Z",
		ContractID:     MainnetMessageTransmitter,
		Topic: []string{
			TopicSymbolMessageReceived,
			b64(t, accountAddrFromStrkey(t, caller)),
			b64(t, scBytes(makeBytesN32(0x90))),
			b64(t, u32(2000)),
		},
		Value: body,
	}
	out, err := NewDecoder().Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Decode emitted %d events, want 1", len(out))
	}
	got := out[0].(Event)
	if got.EventType != EventMessageReceived {
		t.Errorf("EventType = %q, want %q", got.EventType, EventMessageReceived)
	}
	if got.CounterpartyDomain == nil || *got.CounterpartyDomain != 7 {
		t.Errorf("CounterpartyDomain = %v, want 7 (source_domain)", got.CounterpartyDomain)
	}
}

func TestDecoder_Decode_MessageSent(t *testing.T) {
	t.Parallel()
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("message"), Val: scBytes([]byte("envelope"))},
	))
	ev := events.Event{
		LedgerClosedAt: "2026-05-20T14:00:01Z",
		ContractID:     MainnetMessageTransmitter,
		Topic:          []string{TopicSymbolMessageSent},
		Value:          body,
	}
	out, err := NewDecoder().Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Decode emitted %d events, want 1", len(out))
	}
	got := out[0].(Event)
	if got.EventType != EventMessageSent {
		t.Errorf("EventType = %q, want %q", got.EventType, EventMessageSent)
	}
	if got.Amount != "" || got.Token != "" || got.CounterpartyDomain != nil {
		t.Error("message_sent should carry no amount/token/domain")
	}
	if _, present := got.Attributes["message"]; !present {
		t.Error("Attributes missing 'message'")
	}
}
