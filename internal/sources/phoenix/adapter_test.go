package phoenix

import (
	"math/big"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// ─── consumer.go ──────────────────────────────────────────────────

func TestTradeEvent_implementsConsumerEvent(t *testing.T) {
	te := TradeEvent{}
	if got := te.EventKind(); got != "phoenix.trade" {
		t.Errorf("EventKind() = %q, want \"phoenix.trade\"", got)
	}
	if got := te.Source(); got != SourceName {
		t.Errorf("Source() = %q, want %q", got, SourceName)
	}
	var _ consumer.Event = te
}

// ─── dispatcher_adapter.go ────────────────────────────────────────

func TestDecoder_Name(t *testing.T) {
	if got := NewDecoder().Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
}

func TestDecoder_Matches_swapTopic(t *testing.T) {
	d := NewDecoder()
	good := events.Event{
		Topic: []string{TopicSymbolSwap, TopicSymbolSender},
	}
	if !d.Matches(good) {
		t.Error("Matches((swap, sender)) = false, want true")
	}
	bad := events.Event{
		Topic: []string{TopicSymbolSender, TopicSymbolSwap},
	}
	if d.Matches(bad) {
		t.Error("Matches((sender, swap)) = true, want false (wrong topic order)")
	}
	empty := events.Event{Topic: nil}
	if d.Matches(empty) {
		t.Error("Matches(empty topic) = true, want false")
	}
}

// makeFieldEvent builds one of the 8 swap-field events under a
// shared (ledger, txHash, opIndex) so the buffer groups them as
// one swap.
func makeFieldEvent(t *testing.T, fieldTopic, body string) events.Event {
	t.Helper()
	return events.Event{
		Topic:          []string{TopicSymbolSwap, fieldTopic},
		Value:          body,
		Ledger:         1_500_000,
		TxHash:         "phoenixtx0",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-23T12:00:00Z",
		ContractID:     "C-pool-strkey",
	}
}

func TestDecoder_Decode_completesAfterEighthField(t *testing.T) {
	// Feed 7 distinct field events; each should return (nil, nil)
	// (still buffering). The 8th completes the swap and emits one
	// TradeEvent.
	d := NewDecoder()

	sellToken := makeC(t, 0x20)
	buyToken := makeC(t, 0x30)
	sender := makeC(t, 0x10)
	offer := big.NewInt(1_000_000)
	received := big.NewInt(2_000_000)

	// Map: fieldTopic → body. SpreadAmount/ReturnAmount/ReferralFee
	// must be valid i128 even though decodeSwap doesn't read them —
	// the buffer's Complete() check requires all 8 slots populated.
	zeroI128 := i128Body(t, big.NewInt(0))
	fields := []struct{ topic, body string }{
		{TopicSymbolSender, addrBody(t, sender)},
		{TopicSymbolSellToken, addrBody(t, sellToken)},
		{TopicSymbolOfferAmount, i128Body(t, offer)},
		{TopicSymbolActualReceived, i128Body(t, received)},
		{TopicSymbolBuyToken, addrBody(t, buyToken)},
		{TopicSymbolReturnAmount, zeroI128},
		{TopicSymbolSpreadAmount, zeroI128},
		{TopicSymbolReferralFee, zeroI128},
	}

	for i, f := range fields {
		out, err := d.Decode(makeFieldEvent(t, f.topic, f.body))
		if err != nil {
			t.Fatalf("field %d (%s): unexpected error: %v", i, f.topic, err)
		}
		if i < 7 {
			if len(out) != 0 {
				t.Errorf("field %d (%s): got %d events, want 0 (still buffering)", i, f.topic, len(out))
			}
		} else {
			if len(out) != 1 {
				t.Fatalf("field 7 (%s): got %d events, want 1", f.topic, len(out))
			}
			te, ok := out[0].(TradeEvent)
			if !ok {
				t.Fatalf("expected TradeEvent, got %T", out[0])
			}
			if te.Trade.Source != SourceName {
				t.Errorf("Trade.Source = %q, want %q", te.Trade.Source, SourceName)
			}
			if te.Trade.BaseAmount.BigInt().Cmp(offer) != 0 {
				t.Errorf("BaseAmount = %s, want %s", te.Trade.BaseAmount, offer)
			}
		}
	}
}

func TestDecoder_Decode_fieldDecodeErrorPropagates(t *testing.T) {
	// Send a known-good Sender event followed by a malformed
	// SellToken (non-base64 body). Decode of the malformed one must
	// fail — buffer absorption signals the error to the caller.
	d := NewDecoder()
	d.Decode(makeFieldEvent(t, TopicSymbolSender, addrBody(t, makeC(t, 0x10))))
	// SellToken with a body the buffer's assign will accept structurally
	// but that decodeSwap can't parse — check error fan-out via decodeSwap.
	// Easier: send all 8 fields with the OfferAmount body NON-i128.
	d2 := NewDecoder()
	bad := []struct{ topic, body string }{
		{TopicSymbolSender, addrBody(t, makeC(t, 0x10))},
		{TopicSymbolSellToken, addrBody(t, makeC(t, 0x20))},
		// OfferAmount carrying an Address body — decodeSwap will reject.
		{TopicSymbolOfferAmount, addrBody(t, makeC(t, 0x40))},
		{TopicSymbolActualReceived, i128Body(t, big.NewInt(1))},
		{TopicSymbolBuyToken, addrBody(t, makeC(t, 0x30))},
		{TopicSymbolReturnAmount, i128Body(t, big.NewInt(0))},
		{TopicSymbolSpreadAmount, i128Body(t, big.NewInt(0))},
		{TopicSymbolReferralFee, i128Body(t, big.NewInt(0))},
	}
	var lastErr error
	for _, f := range bad {
		_, err := d2.Decode(makeFieldEvent(t, f.topic, f.body))
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		t.Error("expected decodeSwap error on malformed OfferAmount, got none")
	}
}

func TestDecoder_EvictedOrphans_initiallyZero(t *testing.T) {
	d := NewDecoder()
	if got := d.EvictedOrphans(); got != 0 {
		t.Errorf("EvictedOrphans() = %d on a fresh Decoder, want 0", got)
	}
}

func TestDecoder_EvictedOrphans_incrementsOnStaleEviction(t *testing.T) {
	// Drive the buffer's age-out by feeding two events whose
	// ClosedAt are >5 min apart. The first sits in the buffer
	// alone; the second's sweepStale should evict it.
	d := NewDecoder()

	// Event 1: t0, only sender, partial buffer entry.
	evOld := events.Event{
		Topic:          []string{TopicSymbolSwap, TopicSymbolSender},
		Value:          addrBody(t, makeC(t, 0x10)),
		Ledger:         1_500_000,
		TxHash:         "old-tx",
		OperationIndex: 0,
		LedgerClosedAt: "2026-01-01T00:00:00Z",
		ContractID:     "pool-A",
	}
	if _, err := d.Decode(evOld); err != nil {
		t.Fatalf("Decode evOld: %v", err)
	}

	// Event 2: t0 + 10min, distinct group_key — sweepStale runs and
	// evicts evOld since it's now > 5 min stale.
	evNew := events.Event{
		Topic:          []string{TopicSymbolSwap, TopicSymbolSender},
		Value:          addrBody(t, makeC(t, 0x10)),
		Ledger:         1_500_001, // different ledger ⇒ different groupKey
		TxHash:         "new-tx",
		OperationIndex: 0,
		LedgerClosedAt: "2026-01-01T00:10:00Z",
		ContractID:     "pool-A",
	}
	if _, err := d.Decode(evNew); err != nil {
		t.Fatalf("Decode evNew: %v", err)
	}

	if got := d.EvictedOrphans(); got != 1 {
		t.Errorf("EvictedOrphans() = %d, want 1 (the t0 entry should have aged out)", got)
	}
}
