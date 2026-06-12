package comet

import (
	"math/big"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// ─── Matches now claims four additional kinds ────────────────────

func TestDecoder_Matches_AllFivePoolKinds(t *testing.T) {
	d := NewDecoder()
	for _, topic1 := range []string{
		TopicSymbolSwap, TopicSymbolJoinPool, TopicSymbolExitPool,
		TopicSymbolDeposit, TopicSymbolWithdraw,
	} {
		ev := events.Event{Topic: []string{TopicSymbolPool, topic1}}
		if !d.Matches(ev) {
			t.Errorf("Matches((POOL, %q)) = false, want true", topic1)
		}
	}
}

func TestDecoder_Matches_RejectsUnknownPoolKind(t *testing.T) {
	// A hypothetical contract upgrade emits (POOL, "bind") — until
	// the decoder grows a handler for it the dispatcher should NOT
	// claim it. That keeps the orphan-events counter honest and
	// surfaces the schema drift to operators.
	d := NewDecoder()
	bind := mustEncodeSymbolForTest(t, "bind")
	ev := events.Event{Topic: []string{TopicSymbolPool, bind}}
	if d.Matches(ev) {
		t.Error("Matches((POOL, bind)) = true, want false (unknown kind)")
	}
}

// ─── Decode dispatches kind → correct event type ────────────────

func TestDecoder_Decode_JoinPoolProducesLiquidityEvent(t *testing.T) {
	d := NewDecoder()
	caller := accountStrkeyFromSeed(t, 0x60)
	token := contractStrkeyFromSeed(t, 0x61)
	amount := big.NewInt(900_000)

	body := encodeLiquidityBody(t, caller, token, "token_in", "token_amount_in", amount)
	ev := events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolJoinPool},
		Value:          body,
		Ledger:         52_000_500,
		TxHash:         "beef01",
		OperationIndex: 1,
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	out, err := d.Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	le, ok := out[0].(LiquidityEvent)
	if !ok {
		t.Fatalf("got %T, want LiquidityEvent", out[0])
	}
	if le.Kind != LiquidityJoinPool {
		t.Errorf("Kind = %q, want %q", le.Kind, LiquidityJoinPool)
	}
	if le.EventKind() != "comet.liquidity" {
		t.Errorf("EventKind() = %q, want comet.liquidity", le.EventKind())
	}
	if le.Source() != SourceName {
		t.Errorf("Source() = %q, want %q", le.Source(), SourceName)
	}
}

func TestDecoder_Decode_WithdrawProducesLiquidityEventWithPoolAmount(t *testing.T) {
	d := NewDecoder()
	caller := accountStrkeyFromSeed(t, 0x62)
	token := contractStrkeyFromSeed(t, 0x63)
	body := encodeWithdrawBody(t, caller, token, big.NewInt(1_000), big.NewInt(50))
	ev := events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolWithdraw},
		Value:          body,
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	out, err := d.Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	le := out[0].(LiquidityEvent)
	if le.PoolAmountIn.BigInt().Int64() != 50 {
		t.Errorf("PoolAmountIn = %s, want 50", le.PoolAmountIn)
	}
}

// TestDecoder_Decode_SwapStillProducesTradeEvent ensures the
// adapter's broadened Matches predicate doesn't accidentally route
// swap events through the liquidity path.
func TestDecoder_Decode_SwapStillProducesTradeEvent(t *testing.T) {
	d := NewDecoder()
	caller := accountStrkeyFromSeed(t, 0x64)
	tokenIn := contractStrkeyFromSeed(t, 0x65)
	tokenOut := contractStrkeyFromSeed(t, 0x66)
	body := encodeSwapBody(t, caller, tokenIn, tokenOut,
		big.NewInt(1_000_000), big.NewInt(2_500_000))
	ev := events.Event{
		Topic:          []string{TopicSymbolPool, TopicSymbolSwap},
		Value:          body,
		LedgerClosedAt: "2026-05-26T12:00:00Z",
	}
	out, err := d.Decode(ev)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if _, ok := out[0].(TradeEvent); !ok {
		t.Errorf("got %T, want TradeEvent", out[0])
	}
}

func TestDecoder_Decode_UnknownPoolKindReturnsErrNotCometEvent(t *testing.T) {
	d := NewDecoder()
	bind := mustEncodeSymbolForTest(t, "bind")
	ev := events.Event{Topic: []string{TopicSymbolPool, bind}}
	_, err := d.Decode(ev)
	if err == nil {
		t.Error("expected error on unknown (POOL, bind), got nil")
	}
}
