package coinmarketcap

import (
	"testing"

	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// Name + Class are the metadata the aggregator's external.Registry
// reads to decide whether a poller's trades count toward VWAP.
// CMC must report ClassAggregator (NOT ClassExchange) — getting this
// wrong would let CMC's index price double-count upstream venues.

func TestPoller_NameAndClass(t *testing.T) {
	p, err := NewPoller("test-key")
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	if got := p.Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
	if got := p.Class(); got != external.ClassAggregator {
		t.Errorf("Class() = %v, want ClassAggregator (CMC is an index, not an exchange)", got)
	}
}
