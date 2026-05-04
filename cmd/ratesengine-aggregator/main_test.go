package main

import (
	"strings"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
)

// TestDefaultPairs_IncludesBothXLMForms guards against regression of
// the on-r1 launch finding: the abstract `crypto:XLM` ticker and the
// Stellar-protocol `native` form are different cache keys, and the
// aggregator must publish for both so a customer query under either
// form lands on a populated key. On-chain DEX/SDEX trades store
// `native` quote-asset; off-chain CEX trades emit `crypto:XLM`.
func TestDefaultPairs_IncludesBothXLMForms(t *testing.T) {
	got := defaultPairs()

	hasNativeUSD := false
	hasCryptoXLMUSD := false
	for _, p := range got {
		if p.Quote.Type != canonical.AssetFiat || p.Quote.Code != "USD" {
			continue
		}
		switch p.Base.Type {
		case canonical.AssetNative:
			hasNativeUSD = true
		case canonical.AssetCrypto:
			if p.Base.Code == "XLM" {
				hasCryptoXLMUSD = true
			}
		}
	}
	if !hasNativeUSD {
		t.Error("defaultPairs missing native/fiat:USD — on-chain XLM trades will publish to a key the API never queries")
	}
	if !hasCryptoXLMUSD {
		t.Error("defaultPairs missing crypto:XLM/fiat:USD — CEX/FX XLM trades will publish to a key the API never queries")
	}
}

// TestBuildTriangulations_RespectsTriangulationEnabled pins down the
// aggregate.triangulation_enabled master switch — pre-2026-05-02 the
// field existed but no production code consulted it, so an operator
// setting it false still got triangulation. The wiring lives in
// buildTriangulations: when the switch is false, return nil so the
// orchestrator's `len(cfg.Triangulations) == 0` short-circuit skips
// the triangulation tick. Validation still runs first so a malformed
// row is caught regardless of the switch state.
func TestBuildTriangulations_RespectsTriangulationEnabled(t *testing.T) {
	row := config.TriangulationChainConfig{
		Target: "crypto:XLM/fiat:EUR",
		Legs:   []string{"crypto:XLM/fiat:USD", "fiat:USD/fiat:EUR"},
	}

	t.Run("enabled returns the configured chains", func(t *testing.T) {
		cfg := config.AggregateConfig{
			TriangulationEnabled: true,
			Triangulations:       []config.TriangulationChainConfig{row},
		}
		out, err := buildTriangulations(cfg)
		if err != nil {
			t.Fatalf("buildTriangulations: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("len(out) = %d, want 1", len(out))
		}
		if got := out[0].Target.String(); got != row.Target {
			t.Errorf("Target = %q, want %q", got, row.Target)
		}
	})

	t.Run("disabled returns nil even with rows configured", func(t *testing.T) {
		cfg := config.AggregateConfig{
			TriangulationEnabled: false,
			Triangulations:       []config.TriangulationChainConfig{row},
		}
		out, err := buildTriangulations(cfg)
		if err != nil {
			t.Fatalf("buildTriangulations: %v", err)
		}
		if out != nil {
			t.Errorf("len(out) = %d, want nil — switch is OFF", len(out))
		}
	})

	t.Run("disabled still validates rows so flip-on doesn't surprise", func(t *testing.T) {
		bad := config.TriangulationChainConfig{
			Target: "crypto:XLM/fiat:EUR",
			Legs:   []string{"crypto:XLM/fiat:USD"}, // < 2 legs — invalid
		}
		cfg := config.AggregateConfig{
			TriangulationEnabled: false,
			Triangulations:       []config.TriangulationChainConfig{bad},
		}
		_, err := buildTriangulations(cfg)
		if err == nil {
			t.Fatal("buildTriangulations: want error for malformed row, got nil")
		}
		if !strings.Contains(err.Error(), "triangulations[0]") {
			t.Errorf("err = %v; want substring 'triangulations[0]'", err)
		}
	})
}
