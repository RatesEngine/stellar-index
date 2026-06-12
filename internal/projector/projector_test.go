package projector

import (
	"context"
	"log/slog"
	"testing"

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

func oracleConfigEmpty() config.OracleConfig { return config.OracleConfig{} }

// TestNew_DefaultsLogger checks the nil-logger branch picks up
// slog.Default rather than panicking on the first Info call.
func TestNew_DefaultsLogger(t *testing.T) {
	p := New(nil, Registry{}, func(context.Context, consumer.Event) {}, nil)
	if p == nil {
		t.Fatal("New returned nil")
	}
	if p.logger == nil {
		t.Fatal("expected logger to default to slog.Default")
	}
}

// TestRun_NilStoreReturnsError checks the guard in Run: a Projector
// constructed with a nil store should reject Run immediately rather
// than panicking later when a goroutine touches the store.
func TestRun_NilStoreReturnsError(t *testing.T) {
	p := New(nil, Registry{Sources: []Source{{Name: "x"}}},
		func(context.Context, consumer.Event) {}, slog.Default())
	if err := p.Run(context.Background()); err == nil {
		t.Fatal("expected non-nil error from Run with nil store")
	}
}

// TestRun_NilSinkReturnsError checks the guard in Run rejects a
// nil sink before launching any goroutines.
func TestRun_NilSinkReturnsError(t *testing.T) {
	p := &Projector{
		registry: Registry{Sources: []Source{{Name: "x"}}},
		logger:   slog.Default(),
	}
	if err := p.Run(context.Background()); err == nil {
		t.Fatal("expected non-nil error from Run with nil sink")
	}
}

// TestBuildRegistry_UnknownSourceIsSilent confirms enabled-sources
// names that aren't in the projector's dispatch table (sdex, band,
// external CEX/FX) are silently skipped — they're handled
// elsewhere per ADR-0032 § "Out of scope".
func TestBuildRegistry_UnknownSourceIsSilent(t *testing.T) {
	reg, err := BuildRegistry([]string{"sdex", "binance", "kraken", "band"}, oracleConfigEmpty(), nil, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: unexpected error: %v", err)
	}
	if len(reg.Sources) != 0 {
		t.Fatalf("expected 0 in-scope sources for sdex/binance/kraken/band, got %d", len(reg.Sources))
	}
}

// TestBuildRegistry_SEP41NeedsWatchedSet pins F-1316: the sep41 projector
// sources reproduce the dispatcher's WATCHED set, not a firehose. With no
// watched contracts they're skipped (the dispatcher writes nothing
// either); with a watched set they're registered.
func TestBuildRegistry_SEP41NeedsWatchedSet(t *testing.T) {
	names := []string{"sep41_transfers", "sep41_supply"}

	reg, err := BuildRegistry(names, oracleConfigEmpty(), nil, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if len(reg.Sources) != 0 {
		t.Fatalf("no watched sep41 contracts → expected 0 sources, got %d", len(reg.Sources))
	}

	reg, err = BuildRegistry(names, oracleConfigEmpty(), []string{"CWATCHEDCONTRACT0000000000000000000000000000000000000000"}, nil)
	if err != nil {
		t.Fatalf("BuildRegistry (watched): %v", err)
	}
	if len(reg.Sources) != 2 {
		t.Fatalf("watched sep41 contracts → expected 2 sources, got %d", len(reg.Sources))
	}
}

// TestBuildRegistry_IncludesInScopeSources confirms an enabled-
// sources list with on-chain Soroban protocols produces matching
// projector.Source entries. Order-dependent so we map names.
func TestBuildRegistry_IncludesInScopeSources(t *testing.T) {
	names := []string{"aquarius", "phoenix", "comet", "blend", "cctp", "rozo", "soroswap", "defindex"}
	reg, err := BuildRegistry(names, oracleConfigEmpty(), nil, nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}
	if len(reg.Sources) != len(names) {
		t.Fatalf("expected %d sources, got %d", len(names), len(reg.Sources))
	}
	got := map[string]bool{}
	for _, s := range reg.Sources {
		got[s.Name] = true
	}
	for _, n := range names {
		if !got[n] {
			t.Errorf("expected registry to include source %q", n)
		}
	}
}
