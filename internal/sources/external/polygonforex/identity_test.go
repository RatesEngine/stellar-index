package polygonforex

import (
	"testing"

	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// Name + Class must agree with the registry. Polygon Forex is the
// paid C-tier feed that registry.go classifies as ClassExchange so
// it lands in VWAP — divergence between this method and the
// registry would silently flip its inclusion.

func TestPoller_NameAndClass(t *testing.T) {
	p, err := NewPoller("key")
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	if got := p.Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
	if got := p.Class(); got != external.ClassExchange {
		t.Errorf("Class() = %v, want ClassExchange", got)
	}
}
