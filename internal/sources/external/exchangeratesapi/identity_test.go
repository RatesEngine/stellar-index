package exchangeratesapi

import (
	"testing"

	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// Name + Class must agree with the entry in
// internal/sources/external/registry.go — divergence would cause
// the aggregator's class-based VWAP filter to treat this venue
// inconsistently across the call sites.

func TestPoller_NameAndClass(t *testing.T) {
	p, err := NewPoller("key")
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	if got := p.Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
	if got := p.Class(); got != external.ClassExchange {
		t.Errorf("Class() = %v, want ClassExchange (registry treats paid FX feeds as exchange-equivalent)", got)
	}
}
