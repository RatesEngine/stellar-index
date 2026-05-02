package main

import (
	"strings"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/config"
)

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
