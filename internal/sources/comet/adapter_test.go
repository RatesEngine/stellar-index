package comet

import (
	"math/big"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// ─── consumer.go ──────────────────────────────────────────────────

func TestTradeEvent_implementsConsumerEvent(t *testing.T) {
	te := TradeEvent{}
	if got := te.EventKind(); got != "comet.trade" {
		t.Errorf("EventKind() = %q, want \"comet.trade\"", got)
	}
	if got := te.Source(); got != SourceName {
		t.Errorf("Source() = %q, want %q", got, SourceName)
	}
	// Compile-time check is in consumer.go (var _ consumer.Event = TradeEvent{}),
	// but assert at runtime too so a future field rename can't quietly drop it.
	var _ consumer.Event = te
}

// ─── dispatcher_adapter.go ────────────────────────────────────────

func TestDecoder_Name(t *testing.T) {
	d := NewDecoder()
	if got := d.Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
}

func TestDecoder_Matches_TopicShape(t *testing.T) {
	d := NewDecoder()

	swap := events.Event{Topic: []string{TopicSymbolPool, TopicSymbolSwap}}
	if !d.Matches(swap) {
		t.Error("Matches((POOL, swap)) = false, want true")
	}

	// Wrong topic[0]: not a pool event.
	other := events.Event{Topic: []string{TopicSymbolSwap, TopicSymbolPool}}
	if d.Matches(other) {
		t.Error("Matches((swap, POOL)) = true, want false (wrong topic order)")
	}

	empty := events.Event{Topic: nil}
	if d.Matches(empty) {
		t.Error("Matches(empty topic) = true, want false")
	}
}

func TestDecoder_Decode_HappyPathProducesOneTradeEvent(t *testing.T) {
	d := NewDecoder()
	caller := accountStrkeyFromSeed(t, 0x10)
	tokenIn := contractStrkeyFromSeed(t, 0x20)
	tokenOut := contractStrkeyFromSeed(t, 0x30)
	body := encodeSwapBody(t, caller, tokenIn, tokenOut,
		big.NewInt(1_000_000), big.NewInt(2_500_000))
	ev := events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolSwap},
		Value:          body,
		Ledger:         52_000_000,
		TxHash:         "deadbeef",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	}
	out, err := d.Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1", len(out))
	}
	te, ok := out[0].(TradeEvent)
	if !ok {
		t.Fatalf("expected TradeEvent, got %T", out[0])
	}
	if te.Trade.Source != SourceName {
		t.Errorf("Trade.Source = %q, want %q", te.Trade.Source, SourceName)
	}
	wantBase, _ := canonical.NewSorobanAsset(tokenIn)
	if !te.Trade.Pair.Base.Equal(wantBase) {
		t.Errorf("Pair.Base = %+v, want %+v", te.Trade.Pair.Base, wantBase)
	}
}

func TestDecoder_Decode_MalformedBodyReturnsError(t *testing.T) {
	d := NewDecoder()
	ev := events.Event{
		Topic: []string{TopicSymbolPool, TopicSymbolSwap},
		Value: "not-base64",
	}
	_, err := d.Decode(ev)
	if err == nil {
		t.Error("expected decode error on malformed body, got nil")
	}
}

func TestDecoder_Decode_FallbackTimestampOnMissingClosedAt(t *testing.T) {
	// LedgerClosedAt is empty — the adapter must still return a valid
	// trade by falling back to time.Now(). The fallback path is only
	// exercised here; production always populates LedgerClosedAt.
	d := NewDecoder()
	caller := accountStrkeyFromSeed(t, 0x10)
	tokenIn := contractStrkeyFromSeed(t, 0x20)
	tokenOut := contractStrkeyFromSeed(t, 0x30)
	body := encodeSwapBody(t, caller, tokenIn, tokenOut,
		big.NewInt(100), big.NewInt(200))
	ev := events.Event{
		Topic: []string{TopicSymbolPool, TopicSymbolSwap},
		Value: body,
		// LedgerClosedAt deliberately empty.
	}
	out, err := d.Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	te := out[0].(TradeEvent)
	if te.Trade.Timestamp.IsZero() {
		t.Error("Timestamp is zero — fallback should have set time.Now()")
	}
}
