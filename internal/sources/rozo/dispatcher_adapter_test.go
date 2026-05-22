package rozo

import (
	"math/big"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// paymentEvent builds a complete, well-formed v1 payment events.Event
// for adapter-level tests. contractID lets a test point it at a
// non-Rozo contract to exercise the Matches gate.
func paymentEvent(t *testing.T, contractID string) events.Event {
	t.Helper()
	from := makeAccountStrkey(t, 0x20)
	dest := makeAccountStrkey(t, 0x30)
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("amount"), Val: i128(big.NewInt(2_500_000))},
		xdr.ScMapEntry{Key: symbol("destination"), Val: accountAddrFromStrkey(t, dest)},
		xdr.ScMapEntry{Key: symbol("from"), Val: accountAddrFromStrkey(t, from)},
		xdr.ScMapEntry{Key: symbol("memo"), Val: scString("binance-tag-42")},
	))
	return events.Event{
		Type:           "contract",
		Ledger:         62_700_000,
		LedgerClosedAt: "2026-05-20T14:00:00Z",
		ContractID:     contractID,
		OperationIndex: 2,
		TxHash:         "rozo-tx-1",
		Topic: []string{
			TopicSymbolPayment,
			b64(t, accountAddrFromStrkey(t, from)),
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

func TestIsRozoContract(t *testing.T) {
	t.Parallel()
	for _, id := range MainnetPaymentContracts {
		if !IsRozoContract(id) {
			t.Errorf("IsRozoContract(%q) = false, want true", id)
		}
	}
	if IsRozoContract(makeContractStrkey(t, 0x99)) {
		t.Error("IsRozoContract on a foreign contract = true, want false")
	}
}

func TestDecoder_Matches(t *testing.T) {
	t.Parallel()
	d := NewDecoder()

	t.Run("payment from Rozo contract", func(t *testing.T) {
		t.Parallel()
		if !d.Matches(paymentEvent(t, MainnetPaymentContract)) {
			t.Error("want Matches=true for a payment from a Rozo v1 contract")
		}
	})

	t.Run("payment topic from non-Rozo contract", func(t *testing.T) {
		t.Parallel()
		if d.Matches(paymentEvent(t, makeContractStrkey(t, 0x99))) {
			t.Error("want Matches=false for a payment topic from a foreign contract")
		}
	})

	t.Run("unrecognised topic from Rozo contract", func(t *testing.T) {
		t.Parallel()
		ev := events.Event{
			ContractID: MainnetPaymentContract,
			Topic:      []string{b64(t, symbol("transfer"))},
		}
		if d.Matches(ev) {
			t.Error("want Matches=false for an unrecognised topic")
		}
	})
}

func TestDecoder_Decode_Payment(t *testing.T) {
	t.Parallel()
	out, err := NewDecoder().Decode(paymentEvent(t, MainnetPaymentContract))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Decode emitted %d events, want 1", len(out))
	}
	ev, ok := out[0].(Event)
	if !ok {
		t.Fatalf("emitted event is %T, want rozo.Event", out[0])
	}
	if ev.EventType != EventPayment {
		t.Errorf("EventType = %q, want %q", ev.EventType, EventPayment)
	}
	if ev.Amount != "2500000" {
		t.Errorf("Amount = %q, want 2500000", ev.Amount)
	}
	if ev.From == nil || ev.Memo == nil {
		t.Fatal("payment must populate From + Memo")
	}
	if *ev.Memo != "binance-tag-42" {
		t.Errorf("Memo = %q, want binance-tag-42", *ev.Memo)
	}
	if ev.Token != nil {
		t.Errorf("payment must not carry a Token, got %q", *ev.Token)
	}
	if ev.Destination == "" {
		t.Error("Destination should be populated")
	}
	if ev.ObservedAt.IsZero() {
		t.Error("ObservedAt should be parsed from LedgerClosedAt")
	}
	var _ consumer.Event = ev
	if ev.Source() != SourceName {
		t.Errorf("Source() = %q, want %q", ev.Source(), SourceName)
	}
}

func TestDecoder_Decode_Flush(t *testing.T) {
	t.Parallel()
	token := makeContractStrkey(t, 0x40)
	dest := makeAccountStrkey(t, 0x50)
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("amount"), Val: i128(big.NewInt(99))},
		xdr.ScMapEntry{Key: symbol("destination"), Val: accountAddrFromStrkey(t, dest)},
		xdr.ScMapEntry{Key: symbol("token"), Val: contractAddrFromStrkey(t, token)},
	))
	ev := events.Event{
		LedgerClosedAt: "2026-05-20T14:05:00Z",
		ContractID:     MainnetPaymentContract,
		Topic:          []string{TopicSymbolFlush},
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
	if got.EventType != EventFlush {
		t.Errorf("EventType = %q, want %q", got.EventType, EventFlush)
	}
	if got.Token == nil || *got.Token != token {
		t.Errorf("flush Token = %v, want %q", got.Token, token)
	}
	if got.From != nil || got.Memo != nil {
		t.Error("flush must not carry From / Memo")
	}
}

func TestDecoder_Decode_NonRozoContract(t *testing.T) {
	t.Parallel()
	out, err := NewDecoder().Decode(paymentEvent(t, makeContractStrkey(t, 0x99)))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Decode emitted %d events for a foreign contract, want 0", len(out))
	}
}

func TestDecoder_Decode_EmptyClosedAt(t *testing.T) {
	t.Parallel()
	ev := paymentEvent(t, MainnetPaymentContract)
	ev.LedgerClosedAt = ""
	if _, err := NewDecoder().Decode(ev); err == nil {
		t.Fatal("want an error when LedgerClosedAt is empty")
	}
}
