package pipeline

import (
	"testing"

	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/sources/blend"
	"github.com/RatesEngine/rates-engine/internal/sources/childgate"
)

func TestGatedMetaFor_blend(t *testing.T) {
	m, ok := GatedMetaFor(blend.SourceName)
	if !ok {
		t.Fatal("blend should be a gated source")
	}
	if m.Factory != blend.MainnetPoolFactory {
		t.Errorf("factory=%q want %q", m.Factory, blend.MainnetPoolFactory)
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
	if GatedFactory("not-a-source") != "" {
		t.Error("GatedFactory of unknown source should be empty")
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
