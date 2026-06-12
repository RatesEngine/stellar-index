package main

import (
	"context"
	"io"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/obstest"
	"github.com/StellarAtlas/stellar-atlas/internal/supply"
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

// TestRunSupplyRefresh_DurationMetricRecorded pins the wave-90
// (2026-05-13) latency-histogram wiring on the supply-refresh
// loop. Final entry in the wave-92/93/94 regression-test series.
//
// Setup: build a real *supply.Refresher with stub
// LedgerLookup/SnapshotComputer/SnapshotInserter (the supply
// package's own interfaces — production impls are timescale-
// backed, the test ones are in-memory). Pre-cancel the context
// so the immediate first tick runs once and the ticker loop
// exits via <-ctx.Done() without firing.
func TestRunSupplyRefresh_DurationMetricRecorded(t *testing.T) {
	r := supply.NewRefresher(
		stubSupplyLedgers{ledger: 50_000_000, observedAt: time.Unix(1_770_000_000, 0).UTC()},
		stubSupplyComputer{out: supply.Supply{
			AssetKey:          "TEST",
			TotalSupply:       big.NewInt(1_000_000),
			CirculatingSupply: big.NewInt(900_000),
			Basis:             supply.BasisXLMSDFReserveExclusion,
		}},
		&stubSupplyInserter{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	before := obstest.HistogramSampleCount(t, obs.AggregatorSupplyRefreshDurationSeconds, "outcome", "ok")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediate-first-tick runs; for-loop sees ctx.Done() and returns
	runSupplyRefresh(ctx, r, time.Hour, "TEST")

	after := obstest.HistogramSampleCount(t, obs.AggregatorSupplyRefreshDurationSeconds, "outcome", "ok")
	if after <= before {
		t.Errorf("supply refresh duration histogram did not advance: before=%d after=%d", before, after)
	}
}

// ─── stubs for TestRunSupplyRefresh_DurationMetricRecorded ──────
//
// Mirror the (unexported) stubs in internal/supply/refresher_test.go.
// Re-implemented here since the supply package's stubs are
// package-private; the cost of duplicating ~25 lines beats either
// exporting test fixtures or adding a separate testfixture
// subpackage.

type stubSupplyLedgers struct {
	ledger     uint32
	observedAt time.Time
}

func (s stubSupplyLedgers) LatestKnownLedger(_ context.Context) (uint32, time.Time, error) {
	return s.ledger, s.observedAt, nil
}

type stubSupplyComputer struct {
	out supply.Supply
}

func (s stubSupplyComputer) Compute(_ context.Context, ledger uint32, observedAt time.Time) (supply.Supply, error) {
	out := s.out
	out.LedgerSequence = ledger
	out.ObservedAt = observedAt
	return out, nil
}

type stubSupplyInserter struct{}

func (*stubSupplyInserter) InsertSupply(_ context.Context, _ supply.Supply) error { return nil }
