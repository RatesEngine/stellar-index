package aquarius

import (
	"math/big"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// ─── consumer.go ──────────────────────────────────────────────────

func TestTradeEvent_implementsConsumerEvent(t *testing.T) {
	te := TradeEvent{}
	if got := te.EventKind(); got != "aquarius.trade" {
		t.Errorf("EventKind() = %q, want \"aquarius.trade\"", got)
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

func TestDecoder_Matches(t *testing.T) {
	d := NewDecoder()
	tokenIn := makeContractStrkey(t, 0x01)
	tokenOut := makeContractStrkey(t, 0x02)
	user := makeAccountStrkey(t, 0x03)

	good := events.Event{
		Topic: []string{
			TopicSymbolTrade,
			encodeContractAddrFromStrkey(t, tokenIn),
			encodeContractAddrFromStrkey(t, tokenOut),
			encodeAccountAddrFromStrkey(t, user),
		},
	}
	if !d.Matches(good) {
		t.Error("Matches(trade event) = false, want true")
	}

	for _, tc := range []struct {
		name string
		ev   events.Event
	}{
		{"empty topic", events.Event{}},
		{"non-trade topic[0]", events.Event{Topic: []string{
			encodeSymbol(t, "deposit"),
			encodeContractAddrFromStrkey(t, tokenIn),
			encodeContractAddrFromStrkey(t, tokenOut),
			encodeAccountAddrFromStrkey(t, user),
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if d.Matches(tc.ev) {
				t.Errorf("Matches(%s) = true, want false", tc.name)
			}
		})
	}
}

func TestDecoder_Decode_HappyPathProducesOneTradeEvent(t *testing.T) {
	d := NewDecoder()
	tokenIn := makeContractStrkey(t, 0x01)
	tokenOut := makeContractStrkey(t, 0x02)
	user := makeAccountStrkey(t, 0x03)
	ev := events.Event{
		Topic: []string{
			TopicSymbolTrade,
			encodeContractAddrFromStrkey(t, tokenIn),
			encodeContractAddrFromStrkey(t, tokenOut),
			encodeAccountAddrFromStrkey(t, user),
		},
		Value:          encodeTradeBody(t, big.NewInt(1_000_000), big.NewInt(2_000_000), big.NewInt(0)),
		Ledger:         62_000_000,
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
	if te.Trade.Taker != user {
		t.Errorf("Trade.Taker = %q, want %q", te.Trade.Taker, user)
	}
}

func TestDecoder_Decode_MalformedClosedAtReturnsError(t *testing.T) {
	d := NewDecoder()
	tokenIn := makeContractStrkey(t, 0x01)
	tokenOut := makeContractStrkey(t, 0x02)
	user := makeAccountStrkey(t, 0x03)
	ev := events.Event{
		Topic: []string{
			TopicSymbolTrade,
			encodeContractAddrFromStrkey(t, tokenIn),
			encodeContractAddrFromStrkey(t, tokenOut),
			encodeAccountAddrFromStrkey(t, user),
		},
		Value:          encodeTradeBody(t, big.NewInt(1), big.NewInt(1), big.NewInt(0)),
		LedgerClosedAt: "not-a-timestamp",
	}
	if _, err := d.Decode(ev); err == nil {
		t.Error("expected EventClosedAt error on malformed timestamp, got nil")
	}
}

func TestDecoder_Decode_MalformedBodyReturnsError(t *testing.T) {
	d := NewDecoder()
	tokenIn := makeContractStrkey(t, 0x01)
	tokenOut := makeContractStrkey(t, 0x02)
	user := makeAccountStrkey(t, 0x03)
	ev := events.Event{
		Topic: []string{
			TopicSymbolTrade,
			encodeContractAddrFromStrkey(t, tokenIn),
			encodeContractAddrFromStrkey(t, tokenOut),
			encodeAccountAddrFromStrkey(t, user),
		},
		Value:          "not-base64",
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	}
	if _, err := d.Decode(ev); err == nil {
		t.Error("expected decode error on malformed body, got nil")
	}
}
