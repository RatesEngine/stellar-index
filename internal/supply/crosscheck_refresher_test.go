package supply_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"sync"
	"testing"

	"github.com/RatesEngine/rates-engine/internal/supply"
)

// fakeSnapshotReader returns canned per-key supplies. Errors come
// from a parallel map so tests can wire ErrNoSnapshot / transient
// errors per asset_key.
type fakeSnapshotReader struct {
	mu       sync.Mutex
	supplies map[string]supply.Supply
	errs     map[string]error
}

func (f *fakeSnapshotReader) LatestSupply(_ context.Context, k string) (supply.Supply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.errs[k]; ok {
		return supply.Supply{}, e
	}
	if s, ok := f.supplies[k]; ok {
		return s, nil
	}
	return supply.Supply{}, supply.ErrNoSnapshot
}

// captureEmitter records every emission so tests can assert on the
// exact gauge + counter calls.
type captureEmitter struct {
	mu          sync.Mutex
	divergences []divergenceCall
	outcomes    []supply.CrossCheckOutcomeKind
}

type divergenceCall struct {
	ClassicKey string
	Stroops    float64
}

func (c *captureEmitter) Divergence(k string, s float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.divergences = append(c.divergences, divergenceCall{ClassicKey: k, Stroops: s})
}

func (c *captureEmitter) Outcome(k supply.CrossCheckOutcomeKind) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.outcomes = append(c.outcomes, k)
}

func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestCrossCheckRefresher_NoPairsIsNoOp — empty pairs slice returns
// nil and emits no metrics. No-op-when-unconfigured is the same
// pattern as the watched-set decoders (PR #411-#413).
func TestCrossCheckRefresher_NoPairsIsNoOp(t *testing.T) {
	t.Parallel()
	emitter := &captureEmitter{}
	r, err := supply.NewCrossCheckRefresher(nil, &fakeSnapshotReader{}, emitter, newSilentLogger())
	if err != nil {
		t.Fatalf("NewCrossCheckRefresher: %v", err)
	}
	if got := r.Tick(context.Background()); got != nil {
		t.Fatalf("Tick on empty pairs: got %v, want nil", got)
	}
	if len(emitter.outcomes) != 0 || len(emitter.divergences) != 0 {
		t.Fatalf("emitted on empty pairs: outcomes=%v divergences=%v", emitter.outcomes, emitter.divergences)
	}
}

// TestCrossCheckRefresher_RejectsBadInput — empty keys and duplicate
// classic_keys are operator-config bugs caught at construction.
func TestCrossCheckRefresher_RejectsBadInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		pairs []supply.CrossCheckPair
	}{
		{"empty classic", []supply.CrossCheckPair{{ClassicKey: "", SACKey: "C..."}}},
		{"empty sac", []supply.CrossCheckPair{{ClassicKey: "USDC:G...", SACKey: ""}}},
		{"duplicate classic", []supply.CrossCheckPair{
			{ClassicKey: "USDC:G...", SACKey: "C1"},
			{ClassicKey: "USDC:G...", SACKey: "C2"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := supply.NewCrossCheckRefresher(tc.pairs, &fakeSnapshotReader{}, &captureEmitter{}, newSilentLogger())
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

// TestCrossCheckRefresher_WithinTolerance — equal totals → "within"
// outcome + gauge=0.
func TestCrossCheckRefresher_WithinTolerance(t *testing.T) {
	t.Parallel()
	classic := supply.Supply{
		AssetKey:    "USDC:G...",
		TotalSupply: big.NewInt(100_000_000_000),
	}
	sac := supply.Supply{
		AssetKey:    "CCONTRACT",
		TotalSupply: big.NewInt(100_000_000_000),
	}
	reader := &fakeSnapshotReader{supplies: map[string]supply.Supply{
		"USDC:G...": classic,
		"CCONTRACT": sac,
	}}
	emitter := &captureEmitter{}
	r, err := supply.NewCrossCheckRefresher(
		[]supply.CrossCheckPair{{ClassicKey: "USDC:G...", SACKey: "CCONTRACT"}},
		reader, emitter, newSilentLogger(),
	)
	if err != nil {
		t.Fatalf("NewCrossCheckRefresher: %v", err)
	}
	got := r.Tick(context.Background())
	if len(got) != 1 || got[0].Kind != supply.CrossCheckOutcomeWithin {
		t.Fatalf("Tick: got %#v, want one Within", got)
	}
	if len(emitter.outcomes) != 1 || emitter.outcomes[0] != supply.CrossCheckOutcomeWithin {
		t.Fatalf("outcomes: %v", emitter.outcomes)
	}
	if len(emitter.divergences) != 1 || emitter.divergences[0].Stroops != 0 {
		t.Fatalf("divergence: %v", emitter.divergences)
	}
}

// TestCrossCheckRefresher_OverTolerance — 2-stroop divergence → "over"
// outcome and the gauge gets the absolute value.
func TestCrossCheckRefresher_OverTolerance(t *testing.T) {
	t.Parallel()
	classic := supply.Supply{
		AssetKey:    "USDC:G...",
		TotalSupply: big.NewInt(100_000_000_002),
	}
	sac := supply.Supply{
		AssetKey:    "CCONTRACT",
		TotalSupply: big.NewInt(100_000_000_000),
	}
	reader := &fakeSnapshotReader{supplies: map[string]supply.Supply{
		"USDC:G...": classic,
		"CCONTRACT": sac,
	}}
	emitter := &captureEmitter{}
	r, _ := supply.NewCrossCheckRefresher(
		[]supply.CrossCheckPair{{ClassicKey: "USDC:G...", SACKey: "CCONTRACT"}},
		reader, emitter, newSilentLogger(),
	)
	got := r.Tick(context.Background())
	if len(got) != 1 || got[0].Kind != supply.CrossCheckOutcomeOver {
		t.Fatalf("Tick: got %#v, want one Over", got)
	}
	if got[0].Result.DivergenceStroops.Cmp(big.NewInt(2)) != 0 {
		t.Fatalf("divergence stroops: got %s, want 2", got[0].Result.DivergenceStroops)
	}
	if emitter.divergences[0].Stroops != 2 {
		t.Fatalf("emitted divergence: got %v, want 2", emitter.divergences[0].Stroops)
	}
}

// TestCrossCheckRefresher_MissingSnapshot — no rows yet for either
// side → "missing_snapshot" outcome and NO gauge update (the
// bootstrap state must not look like "checked, agreed").
func TestCrossCheckRefresher_MissingSnapshot(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		supplies map[string]supply.Supply
	}{
		{
			name:     "neither side has a snapshot",
			supplies: map[string]supply.Supply{},
		},
		{
			name: "only classic has a snapshot",
			supplies: map[string]supply.Supply{
				"USDC:G...": {AssetKey: "USDC:G...", TotalSupply: big.NewInt(1)},
			},
		},
		{
			name: "only sac has a snapshot",
			supplies: map[string]supply.Supply{
				"CCONTRACT": {AssetKey: "CCONTRACT", TotalSupply: big.NewInt(1)},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := &fakeSnapshotReader{supplies: tc.supplies}
			emitter := &captureEmitter{}
			r, _ := supply.NewCrossCheckRefresher(
				[]supply.CrossCheckPair{{ClassicKey: "USDC:G...", SACKey: "CCONTRACT"}},
				reader, emitter, newSilentLogger(),
			)
			got := r.Tick(context.Background())
			if len(got) != 1 || got[0].Kind != supply.CrossCheckOutcomeMissing {
				t.Fatalf("Tick: got %#v, want one Missing", got)
			}
			if len(emitter.divergences) != 0 {
				t.Fatalf("expected no gauge update on missing, got %v", emitter.divergences)
			}
		})
	}
}

// TestCrossCheckRefresher_ReadError — non-ErrNoSnapshot read failure
// surfaces as "read_error" without updating the gauge.
func TestCrossCheckRefresher_ReadError(t *testing.T) {
	t.Parallel()
	reader := &fakeSnapshotReader{
		errs: map[string]error{"USDC:G...": errors.New("connection refused")},
	}
	emitter := &captureEmitter{}
	r, _ := supply.NewCrossCheckRefresher(
		[]supply.CrossCheckPair{{ClassicKey: "USDC:G...", SACKey: "CCONTRACT"}},
		reader, emitter, newSilentLogger(),
	)
	got := r.Tick(context.Background())
	if len(got) != 1 || got[0].Kind != supply.CrossCheckOutcomeReadError {
		t.Fatalf("Tick: got %#v, want one ReadError", got)
	}
	if len(emitter.divergences) != 0 {
		t.Fatalf("expected no gauge update on read error, got %v", emitter.divergences)
	}
}

// TestCrossCheckRefresher_PerPairIsolation — one pair failing
// doesn't drop the next pair's cross-check.
func TestCrossCheckRefresher_PerPairIsolation(t *testing.T) {
	t.Parallel()
	reader := &fakeSnapshotReader{
		supplies: map[string]supply.Supply{
			"BTC:G...": {AssetKey: "BTC:G...", TotalSupply: big.NewInt(50)},
			"CSACBTC":  {AssetKey: "CSACBTC", TotalSupply: big.NewInt(50)},
		},
		errs: map[string]error{
			"USDC:G...": errors.New("transient"),
		},
	}
	emitter := &captureEmitter{}
	r, _ := supply.NewCrossCheckRefresher(
		[]supply.CrossCheckPair{
			{ClassicKey: "USDC:G...", SACKey: "CSACUSDC"},
			{ClassicKey: "BTC:G...", SACKey: "CSACBTC"},
		},
		reader, emitter, newSilentLogger(),
	)
	got := r.Tick(context.Background())
	if len(got) != 2 {
		t.Fatalf("Tick: want 2 outcomes, got %d", len(got))
	}
	// Sorted by ClassicKey: BTC:G... before USDC:G...
	if got[0].Pair.ClassicKey != "BTC:G..." || got[0].Kind != supply.CrossCheckOutcomeWithin {
		t.Fatalf("got[0]: %#v", got[0])
	}
	if got[1].Pair.ClassicKey != "USDC:G..." || got[1].Kind != supply.CrossCheckOutcomeReadError {
		t.Fatalf("got[1]: %#v", got[1])
	}
}
