package coingecko

import (
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/external"
)

// Coingecko is an aggregator (composite index over upstream venues),
// NOT an exchange. Class must be ClassAggregator so the aggregator's
// VWAP filter excludes it — including a composite index against
// retail liquidity would double-count the same upstream trades.

func TestPoller_NameAndClass(t *testing.T) {
	p := NewPoller()
	if got := p.Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
	if got := p.Class(); got != external.ClassAggregator {
		t.Errorf("Class() = %v, want ClassAggregator (CoinGecko is an index, not an exchange)", got)
	}
}
