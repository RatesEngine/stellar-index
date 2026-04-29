package baseline_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/aggregate/baseline"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// stubSource is a baseline.VWAPSource that returns a fixed slice
// (or err) per pair.
type stubSource struct {
	mu     sync.Mutex
	byPair map[string][]float64
	err    error
	calls  int
}

func newStubSource() *stubSource {
	return &stubSource{byPair: make(map[string][]float64)}
}

func (s *stubSource) set(pair canonical.Pair, vwaps []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byPair[pair.String()] = vwaps
}

func (s *stubSource) VWAPsForPair1m(_ context.Context, pair canonical.Pair, _, _ time.Time) ([]float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.byPair[pair.String()], nil
}

// stubSink captures upsert calls for assertion.
type stubSink struct {
	mu     sync.Mutex
	byPair map[string]baseline.Baseline
	err    error
	calls  int
}

func newStubSink() *stubSink {
	return &stubSink{byPair: make(map[string]baseline.Baseline)}
}

func (s *stubSink) UpsertBaseline(_ context.Context, pair canonical.Pair, _, _, _ time.Time, b baseline.Baseline) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return s.err
	}
	s.byPair[pair.String()] = b
	return nil
}

func mustPair(t *testing.T, base, quote string) canonical.Pair {
	t.Helper()
	b, err := canonical.ParseAsset(base)
	if err != nil {
		t.Fatalf("parse %s: %v", base, err)
	}
	q, err := canonical.ParseAsset(quote)
	if err != nil {
		t.Fatalf("parse %s: %v", quote, err)
	}
	p, err := canonical.NewPair(b, q)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	return p
}

// TestRefresher_HappyPath — source returns enough VWAPs, refresher
// computes a baseline and writes it.
func TestRefresher_HappyPath(t *testing.T) {
	pair := mustPair(t, "native", "fiat:USD")
	src := newStubSource()
	// 12 VWAPs → 11 returns → above MinSamples.
	src.set(pair, []float64{
		1.00, 1.01, 1.005, 1.02, 1.015, 1.018,
		1.020, 1.019, 1.022, 1.025, 1.024, 1.026,
	})
	sink := newStubSink()

	r := baseline.NewRefresher(src, sink, 30*24*time.Hour, nil)
	outcome, err := r.RefreshPair(context.Background(), pair)
	if err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}
	if outcome != baseline.OutcomeOK {
		t.Errorf("outcome = %v, want OutcomeOK", outcome)
	}
	if sink.calls != 1 {
		t.Errorf("sink.calls = %d, want 1", sink.calls)
	}
	got := sink.byPair[pair.String()]
	if got.N < baseline.MinSamples {
		t.Errorf("baseline.N = %d, want >= %d", got.N, baseline.MinSamples)
	}
}

// TestRefresher_NotEnoughSamples — fewer than 2 VWAPs → 0 returns →
// ErrNotEnoughSamples surfaces as OutcomeNotEnoughSamples without
// attempting to write.
func TestRefresher_NotEnoughSamples(t *testing.T) {
	pair := mustPair(t, "native", "fiat:USD")
	src := newStubSource()
	src.set(pair, []float64{1.0}) // single bucket → 0 returns
	sink := newStubSink()

	r := baseline.NewRefresher(src, sink, 30*24*time.Hour, nil)
	outcome, err := r.RefreshPair(context.Background(), pair)
	if !errors.Is(err, baseline.ErrNotEnoughSamples) {
		t.Errorf("err = %v, want ErrNotEnoughSamples", err)
	}
	if outcome != baseline.OutcomeNotEnoughSamples {
		t.Errorf("outcome = %v, want OutcomeNotEnoughSamples", outcome)
	}
	if sink.calls != 0 {
		t.Errorf("sink.calls = %d, want 0 (no write on bootstrap)", sink.calls)
	}
}

// TestRefresher_ReadError — VWAPSource fails → no compute, no write,
// OutcomeReadError.
func TestRefresher_ReadError(t *testing.T) {
	pair := mustPair(t, "native", "fiat:USD")
	src := newStubSource()
	src.err = errors.New("hypertable down")
	sink := newStubSink()

	r := baseline.NewRefresher(src, sink, 30*24*time.Hour, nil)
	outcome, err := r.RefreshPair(context.Background(), pair)
	if err == nil {
		t.Error("RefreshPair returned nil err, want non-nil")
	}
	if outcome != baseline.OutcomeReadError {
		t.Errorf("outcome = %v, want OutcomeReadError", outcome)
	}
	if sink.calls != 0 {
		t.Errorf("sink.calls = %d, want 0", sink.calls)
	}
}

// TestRefresher_WriteError — compute succeeds but Sink fails →
// OutcomeWriteError.
func TestRefresher_WriteError(t *testing.T) {
	pair := mustPair(t, "native", "fiat:USD")
	src := newStubSource()
	src.set(pair, []float64{
		1.00, 1.01, 1.005, 1.02, 1.015, 1.018, 1.020, 1.019,
	})
	sink := newStubSink()
	sink.err = errors.New("disk full")

	r := baseline.NewRefresher(src, sink, 30*24*time.Hour, nil)
	outcome, err := r.RefreshPair(context.Background(), pair)
	if err == nil {
		t.Error("RefreshPair returned nil err, want non-nil")
	}
	if outcome != baseline.OutcomeWriteError {
		t.Errorf("outcome = %v, want OutcomeWriteError", outcome)
	}
}

// TestRefresher_RefreshAll_AggregatesOutcomes — three pairs with
// three different outcomes; the summary counts each.
func TestRefresher_RefreshAll_AggregatesOutcomes(t *testing.T) {
	pHappy := mustPair(t, "native", "fiat:USD")
	pBootstrap := mustPair(t, "native", "fiat:EUR")
	pReadErr := mustPair(t, "native", "fiat:GBP")

	src := newStubSource()
	src.set(pHappy, []float64{
		1.00, 1.01, 1.005, 1.02, 1.015, 1.018, 1.020, 1.019, 1.022,
	})
	src.set(pBootstrap, []float64{1.0}) // not enough samples
	// pReadErr left absent → returns nil — but we want a real error,
	// not just an empty slice. Switch to a per-pair error router:
	src2 := &perPairErrSource{base: src, errFor: pReadErr.String()}
	sink := newStubSink()

	r := baseline.NewRefresher(src2, sink, 30*24*time.Hour, nil)
	sum := r.RefreshAll(context.Background(),
		[]canonical.Pair{pHappy, pBootstrap, pReadErr}, 2)

	if sum.OK != 1 {
		t.Errorf("OK = %d, want 1", sum.OK)
	}
	if sum.NotEnoughSamples != 1 {
		t.Errorf("NotEnoughSamples = %d, want 1", sum.NotEnoughSamples)
	}
	if sum.ReadErrors != 1 {
		t.Errorf("ReadErrors = %d, want 1", sum.ReadErrors)
	}
	if sum.WriteErrors != 0 {
		t.Errorf("WriteErrors = %d, want 0", sum.WriteErrors)
	}
	if sink.calls != 1 {
		t.Errorf("sink.calls = %d, want 1 (only pHappy upserted)", sink.calls)
	}
}

// perPairErrSource lets a test return an error only for one specific
// pair while delegating the rest to the underlying stub.
type perPairErrSource struct {
	base   *stubSource
	errFor string
}

func (s *perPairErrSource) VWAPsForPair1m(ctx context.Context, pair canonical.Pair, from, to time.Time) ([]float64, error) {
	if pair.String() == s.errFor {
		return nil, errors.New("transient db error")
	}
	return s.base.VWAPsForPair1m(ctx, pair, from, to)
}

// TestRefresher_RefreshAll_ConcurrencyClamp — concurrency <= 0
// falls back to 1 (serial). Confirm by passing 0 and asserting
// the call still completes (not by inspecting goroutine count,
// which is implementation-dependent).
func TestRefresher_RefreshAll_ConcurrencyClamp(t *testing.T) {
	pair := mustPair(t, "native", "fiat:USD")
	src := newStubSource()
	src.set(pair, []float64{1.0, 1.01, 1.02, 1.015, 1.018, 1.02, 1.019, 1.022})
	sink := newStubSink()

	r := baseline.NewRefresher(src, sink, 30*24*time.Hour, nil)
	sum := r.RefreshAll(context.Background(),
		[]canonical.Pair{pair, pair, pair}, 0) // 0 → clamps to 1
	if sum.OK != 3 {
		t.Errorf("OK = %d, want 3", sum.OK)
	}
}

// TestRefresher_DefaultWindowAppliesWhenZero — passing window<=0
// to NewRefresher uses [DefaultWindow]. Indirect probe: a 0-length
// from/to range would have triggered the source's "to <= from"
// guard if the refresher had passed it through.
func TestRefresher_DefaultWindowAppliesWhenZero(t *testing.T) {
	pair := mustPair(t, "native", "fiat:USD")

	var capturedFrom, capturedTo time.Time
	src := &captureSource{
		onCall: func(_ canonical.Pair, from, to time.Time) {
			capturedFrom, capturedTo = from, to
		},
		vwaps: []float64{1.0, 1.01, 1.02, 1.015, 1.018, 1.02, 1.019, 1.022},
	}
	sink := newStubSink()
	r := baseline.NewRefresher(src, sink, 0, nil) // 0 → DefaultWindow

	_, err := r.RefreshPair(context.Background(), pair)
	if err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}
	delta := capturedTo.Sub(capturedFrom)
	// Should be the default 30 days. Allow some slack for clock
	// jitter inside the call.
	if delta < baseline.DefaultWindow-time.Minute || delta > baseline.DefaultWindow+time.Minute {
		t.Errorf("window = %v, want %v", delta, baseline.DefaultWindow)
	}
}

// captureSource records the time-range arguments without inspecting
// stub state — simpler than threading a method onto stubSource.
type captureSource struct {
	onCall func(canonical.Pair, time.Time, time.Time)
	vwaps  []float64
}

func (s *captureSource) VWAPsForPair1m(_ context.Context, pair canonical.Pair, from, to time.Time) ([]float64, error) {
	s.onCall(pair, from, to)
	return s.vwaps, nil
}
