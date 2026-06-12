package pipeline

import (
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/blend"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/childgate"
)

func TestGatedMetaFor_blend(t *testing.T) {
	m, ok := GatedMetaFor(blend.SourceName)
	if !ok {
		t.Fatal("blend should be a gated source")
	}
	// Blend has MORE THAN ONE factory (it was redeployed) — both must be
	// present, else the missing factory's pools are silently dropped.
	if len(m.Factories) < 2 {
		t.Errorf("blend factories=%v, want at least 2 (V1 + V2)", m.Factories)
	}
	wantFac := map[string]bool{blend.MainnetPoolFactory: false, blend.MainnetPoolFactoryV1: false}
	for _, f := range m.Factories {
		if _, ok := wantFac[f]; ok {
			wantFac[f] = true
		}
	}
	for f, seen := range wantFac {
		if !seen {
			t.Errorf("blend factory set missing %q", f)
		}
	}
	if m.CreationSym != blend.EventDeploy {
		t.Errorf("creationSym=%q want %q", m.CreationSym, blend.EventDeploy)
	}
	if m.Genesis != blend.FactoryGenesisLedger {
		t.Errorf("genesis=%d want %d", m.Genesis, blend.FactoryGenesisLedger)
	}
	if m.NewDecoder == nil {
		t.Fatal("NewDecoder must be non-nil")
	}
	// The constructor must forward childgate options to a real, gated
	// decoder: a seeded pool's event matches, an unseeded one does not.
	const pool = "CDPOOLSEEDEDAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	dec := m.NewDecoder(childgate.WithSeed([]string{pool}))
	if !dec.Matches(events.Event{Topic: []string{blend.TopicSymbolSupply}, ContractID: pool}) {
		t.Error("seeded pool event should match through the constructed decoder")
	}
	if dec.Matches(events.Event{Topic: []string{blend.TopicSymbolSupply}, ContractID: "COTHERAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}) {
		t.Error("unseeded contract event must not match")
	}
}

func TestGatedMetaFor_unknown(t *testing.T) {
	if _, ok := GatedMetaFor("not-a-source"); ok {
		t.Error("unknown source should not be gated")
	}
	if GatedFactories("not-a-source") != nil {
		t.Error("GatedFactories of unknown source should be nil")
	}
}

func TestGatedSourceNames_includesBlend(t *testing.T) {
	found := false
	for _, n := range GatedSourceNames() {
		if n == blend.SourceName {
			found = true
		}
	}
	if !found {
		t.Errorf("GatedSourceNames %v should include blend", GatedSourceNames())
	}
}
