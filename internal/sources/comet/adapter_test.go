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

// F-1242 (audit-2026-05-12): Comet's `("POOL", "swap")` topic shape
// is the Balancer-v1 contract event family, not the Soroban-Balancer
// contract address. Any future pubnet contract deployed from the same
// Balancer-v1 WASM (or any contract that mimics the topic + body
// shape) will match this decoder. CLAUDE.md "Things that will surprise
// you" calls this out and the operator-side mitigation is downstream
// filter on `(Trade.Source = "comet", contract_address)` — but the
// decoder itself does NOT discriminate by contract address. This test
// makes the contract explicit: a synthetic event from a *different*
// contract address with the Comet topic + body shape will be decoded
// to a Comet TradeEvent. Any future change that adds a contract-id
// allow-list at the decoder layer (rather than downstream) MUST flip
// this test's expectation.
func TestDecoder_Decode_NoContractIDDiscrimination(t *testing.T) {
	d := NewDecoder()
	caller := accountStrkeyFromSeed(t, 0x10)
	tokenIn := contractStrkeyFromSeed(t, 0x20)
	tokenOut := contractStrkeyFromSeed(t, 0x30)
	body := encodeSwapBody(t, caller, tokenIn, tokenOut,
		big.NewInt(1_000_000), big.NewInt(2_500_000))

	// Synthetic event from a contract that is NOT the documented Blend
	// backstop pool — same topic shape, same body shape, different
	// emitting contract.
	otherContract := contractStrkeyFromSeed(t, 0xFF)
	ev := events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolSwap},
		Value:          body,
		ContractID:     otherContract,
		Ledger:         52_000_000,
		TxHash:         "non-blend-tx",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	}

	out, err := d.Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1 (decoder matches by topic, not contract)", len(out))
	}
	te := out[0].(TradeEvent)
	if te.Trade.Source != SourceName {
		t.Errorf("Trade.Source = %q, want %q", te.Trade.Source, SourceName)
	}
	// Operator filter happens DOWNSTREAM (at aggregator class lookup
	// + per-pair allow-listing); the decoder doesn't carry the
	// contract identity in the canonical Trade beyond the topic
	// match. This is the documented contract per CLAUDE.md.
}

func TestDecoder_Decode_FailsClosedOnMissingClosedAt(t *testing.T) {
	// LedgerClosedAt is empty — the adapter must FAIL CLOSED (return
	// the error) rather than substituting time.Now(). A wall-clock
	// fallback would mis-timestamp the row during a backfill replay,
	// stamping every event with the replay run's time instead of the
	// historical ledger close. Matches the blend/phoenix/defindex
	// siblings. Production always populates LedgerClosedAt.
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
	if err == nil {
		t.Fatalf("Decode: expected an error on empty LedgerClosedAt, got nil (out=%v)", out)
	}
	if out != nil {
		t.Errorf("Decode: expected nil events on error, got %v", out)
	}
}
