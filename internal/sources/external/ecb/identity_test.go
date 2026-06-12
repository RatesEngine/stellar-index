package ecb

import (
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/external"
)

// Name + Class must agree with the registry. ECB is the
// authority-sanity feed: NOT in VWAP, used only as a sanity
// check vs computed FX. Reclassifying ECB as ClassExchange would
// double-count central-bank reference rates against retail
// liquidity — a serious aggregator hazard.

func TestPoller_NameAndClass(t *testing.T) {
	p := NewPoller()
	if got := p.Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
	if got := p.Class(); got != external.ClassAuthoritySanity {
		t.Errorf("Class() = %v, want ClassAuthoritySanity (ECB rates are reference, not retail)", got)
	}
}
