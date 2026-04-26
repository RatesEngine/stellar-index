package cryptocompare

import (
	"testing"

	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// CryptoCompare is an aggregator (composite index across upstream
// venues), NOT an exchange. Class must be ClassAggregator so the
// aggregator's VWAP filter excludes it — including a composite
// index against retail liquidity would double-count the same
// upstream trades.

func TestPoller_NameAndClass(t *testing.T) {
	p, err := NewPoller("key")
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	if got := p.Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
	if got := p.Class(); got != external.ClassAggregator {
		t.Errorf("Class() = %v, want ClassAggregator (CryptoCompare is an index, not an exchange)", got)
	}
}
