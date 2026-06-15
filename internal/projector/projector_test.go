package projector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/StellarIndex/stellar-index/internal/config"
	"github.com/StellarIndex/stellar-index/internal/consumer"
	"github.com/StellarIndex/stellar-index/internal/events"
)

// fakeDecoder is a configurable dispatcher.Decoder for the X9
// panic-isolation tests.
type fakeDecoder struct {
	matches  bool
	panics   bool
	err      error
	outs     []consumer.Event
	decodeHi int // count of Decode calls
}

func (f *fakeDecoder) Name() string              { return "fake" }
func (f *fakeDecoder) Matches(events.Event) bool { return f.matches }
func (f *fakeDecoder) Decode(events.Event) ([]consumer.Event, error) {
	f.decodeHi++
	if f.panics {
		panic("boom: poison / upgraded-WASM row")
	}
	return f.outs, f.err
}

// fakeEvent is a minimal consumer.Event for sink-counting in tests.
type fakeEvent struct{}

func (fakeEvent) EventKind() string { return "fake.event" }
func (fakeEvent) Source() string    { return "fake" }

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestProcessEventSafely_RecoversDecoderPanic pins X9 (audit-2026-06-14):
// a decoder panic on one poison lake row must be recovered + counted as a
// soft-fail, NOT crash the live indexer.
func TestProcessEventSafely_RecoversDecoderPanic(t *testing.T) {
	src := Source{Name: "x", Decoder: &fakeDecoder{matches: true, panics: true}}
	sinked := 0
	emitted, softFail := processEventSafely(src, events.Event{Ledger: 42},
		func(consumer.Event) { sinked++ }, discardLog())
	if !softFail {
		t.Error("a decoder panic must be a soft-fail (counted), not propagate")
	}
	if emitted != 0 || sinked != 0 {
		t.Errorf("emitted=%d sinked=%d, want 0/0 on panic", emitted, sinked)
	}
}

// TestProcessEventSafely_DecodeErrorIsSoftFail — a returned decode error is
// the existing soft-fail path; it must not sink and must flag softFail.
func TestProcessEventSafely_DecodeErrorIsSoftFail(t *testing.T) {
	src := Source{Name: "x", Decoder: &fakeDecoder{matches: true, err: errors.New("bad row")}}
	sinked := 0
	emitted, softFail := processEventSafely(src, events.Event{}, func(consumer.Event) { sinked++ }, discardLog())
	if !softFail || emitted != 0 || sinked != 0 {
		t.Errorf("decode error: softFail=%v emitted=%d sinked=%d, want true/0/0", softFail, emitted, sinked)
	}
}

// TestProcessEventSafely_HappyPathSinks — a clean decode sinks each output and
// reports no soft-fail.
func TestProcessEventSafely_HappyPathSinks(t *testing.T) {
	src := Source{Name: "x", Decoder: &fakeDecoder{matches: true, outs: []consumer.Event{fakeEvent{}, fakeEvent{}}}}
	sinked := 0
	emitted, softFail := processEventSafely(src, events.Event{}, func(consumer.Event) { sinked++ }, discardLog())
	if softFail || emitted != 2 || sinked != 2 {
		t.Errorf("happy: softFail=%v emitted=%d sinked=%d, want false/2/2", softFail, emitted, sinked)
	}
}

// TestProcessEventSafely_NonMatchSkips — a non-matching row is neither sinked
// nor a soft-fail (and Decode is never called).
func TestProcessEventSafely_NonMatchSkips(t *testing.T) {
	d := &fakeDecoder{matches: false}
	sinked := 0
	emitted, softFail := processEventSafely(Source{Name: "x", Decoder: d}, events.Event{},
		func(consumer.Event) { sinked++ }, discardLog())
	if softFail || emitted != 0 || sinked != 0 || d.decodeHi != 0 {
		t.Errorf("non-match: softFail=%v emitted=%d sinked=%d decodeCalls=%d, want false/0/0/0", softFail, emitted, sinked, d.decodeHi)
	}
}

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
	names := []string{"aquarius", "phoenix", "comet", "blend", "blend_backstop", "cctp", "rozo", "soroswap", "defindex"}
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
