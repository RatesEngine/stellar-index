package sdex

import (
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

// TradeEvent.EventKind / Source and Decoder.Name are trivial
// accessors but they're load-bearing — the consumer-layer event
// sink type-switches on EventKind to route trades to the right
// metric label, and the dispatcher maps Decoder.Name to the
// per-source decode-error counter. A future rename that breaks
// the value would silently misroute prod telemetry.
func TestTradeEvent_implementsConsumerEvent(t *testing.T) {
	te := TradeEvent{}
	if got := te.EventKind(); got != "sdex.trade" {
		t.Errorf("EventKind() = %q, want \"sdex.trade\"", got)
	}
	if got := te.Source(); got != SourceName {
		t.Errorf("Source() = %q, want %q", got, SourceName)
	}
	var _ consumer.Event = te
}

func TestDecoder_Name(t *testing.T) {
	if got := NewDecoder().Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
}
