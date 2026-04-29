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

// stubSource is a baseline.TimedVWAPSource that returns a per-pair
// fixture (or err). The fixture is built relative to a `now` time
// captured per-test so SplitByLookback's window cutoffs land where
// we expect.
type stubSource struct {
	mu     sync.Mutex
	byPair map[string][]baseline.TimedVWAP
	err    error
}

func newStubSource() *stubSource {
	return &stubSource{byPair: make(map[string][]baseline.TimedVWAP)}
}

func (s *stubSource) set(pair canonical.Pair, vwaps []baseline.TimedVWAP) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byPair[pair.String()] = vwaps
}

func (s *stubSource) TimedVWAPsForPair1m(_ context.Context, pair canonical.Pair, _, _ time.Time) ([]baseline.TimedVWAP, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return s.byPair[pair.String()], nil
}

// stubSink captures upsert calls for assertion.
type stubSink struct {
	mu     sync.Mutex
	byPair map[string]baseline.MultiBaseline
	err    error
	calls  int
}

func newStubSink() *stubSink {
	return &stubSink{byPair: make(map[string]baseline.MultiBaseline)}
}

func (s *stubSink) UpsertBaseline(_ context.Context, pair canonical.Pair, _, _, _ time.Time, m baseline.MultiBaseline) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.err != nil {
		return s.err
	}
	s.byPair[pair.String()] = m
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

// stableTimedSeries builds `n` evenly-spaced timed VWAPs with bp
// jitter, ending at `now`. Bucket spacing is 1 minute (matching the
// 1m CAGG cadence).
func stableTimedSeries(now time.Time, n int) []baseline.TimedVWAP {
	out := make([]baseline.TimedVWAP, n)
	price := 1.0
	for i := 0; i < n; i++ {
		// 0.0001 jitter alternating sign
		shift := 0.0001
		if i%2 == 0 {
			shift = -shift
		}
		price += shift
		out[i] = baseline.TimedVWAP{
			VWAP:      price,
			BucketEnd: now.Add(-time.Duration(n-1-i) * time.Minute),
		}
	}
	return out
}

// TestRefresher_HappyPath_30dWindowFills — 30+ days of timed VWAPs
// produces a populated 30d (and 7d, 1d) baseline.
func TestRefresher_HappyPath_30dWindowFills(t *testing.T) {
	pair := mustPair(t, "native", "fiat:USD")
	now := time.Now().UTC()
	src := newStubSource()
	// 30d * 1440 buckets/day = 43,200 — overkill for tests, use a
	// smaller stable series that still spans >7d so all three
	// windows have valid data.
	src.set(pair, stableTimedSeries(now, 8*1440)) // 8 days
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
	if got.Day30 == nil {
		t.Error("Day30 nil after refresh — should be populated with 8d of data")
	}
	if got.Day7 == nil {
		t.Error("Day7 nil — 8d of data should easily fill 7d")
	}
	if got.Day1 == nil {
		t.Error("Day1 nil — 8d of data covers 1d")
	}
}

// TestRefresher_30dBootstrap_NotEnoughSamples — fewer than 2 buckets
// available → can't compute even one return → OutcomeNotEnoughSamples
// and no write.
func TestRefresher_30dBootstrap_NotEnoughSamples(t *testing.T) {
	pair := mustPair(t, "native", "fiat:USD")
	src := newStubSource()
	// One bucket → 0 returns → no baseline at any window.
	src.set(pair, []baseline.TimedVWAP{
		{VWAP: 1.0, BucketEnd: time.Now().UTC()},
	})
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
		t.Errorf("sink.calls = %d, want 0", sink.calls)
	}
}

// TestRefresher_PartialBootstrap_OnlyDay30Valid — input spans more
// than 1d but bucketing density is too thin for the 1d window to
// have MinSamples. The 30d window upserts; the 1d slot is null.
func TestRefresher_PartialBootstrap_OnlyDay30Valid(t *testing.T) {
	pair := mustPair(t, "native", "fiat:USD")
	now := time.Now().UTC()
	// Three buckets spread over 8 days. Each window subset:
	//   1d  → just the most-recent (1 bucket → 0 returns → bootstrap)
	//   7d  → most-recent two (1 return → still < MinSamples=2 → bootstrap)
	//   30d → all three (2 returns → MinSamples=2 → valid)
	src := newStubSource()
	src.set(pair, []baseline.TimedVWAP{
		{VWAP: 1.0, BucketEnd: now.Add(-8 * 24 * time.Hour)},
		{VWAP: 1.01, BucketEnd: now.Add(-2 * 24 * time.Hour)},
		{VWAP: 1.02, BucketEnd: now.Add(-30 * time.Minute)},
	})
	sink := newStubSink()

	r := baseline.NewRefresher(src, sink, 30*24*time.Hour, nil)
	outcome, err := r.RefreshPair(context.Background(), pair)
	if err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}
	if outcome != baseline.OutcomeOK {
		t.Errorf("outcome = %v, want OutcomeOK (Day30 valid)", outcome)
	}
	got := sink.byPair[pair.String()]
	if got.Day30 == nil {
		t.Error("Day30 nil; expected populated")
	}
	if got.Day7 != nil {
		t.Errorf("Day7 expected nil (bootstrap); got %v", got.Day7)
	}
	if got.Day1 != nil {
		t.Errorf("Day1 expected nil (bootstrap); got %v", got.Day1)
	}
}

// TestRefresher_ReadError — TimedVWAPSource fails → no compute,
// no write, OutcomeReadError.
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
	src.set(pair, stableTimedSeries(time.Now().UTC(), 100))
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
	now := time.Now().UTC()
	pHappy := mustPair(t, "native", "fiat:USD")
	pBootstrap := mustPair(t, "native", "fiat:EUR")
	pReadErr := mustPair(t, "native", "fiat:GBP")

	src := newStubSource()
	src.set(pHappy, stableTimedSeries(now, 100))
	src.set(pBootstrap, []baseline.TimedVWAP{{VWAP: 1.0, BucketEnd: now}}) // not enough
	// pReadErr handled by perPairErrSource below.
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

type perPairErrSource struct {
	base   *stubSource
	errFor string
}

func (s *perPairErrSource) TimedVWAPsForPair1m(ctx context.Context, pair canonical.Pair, from, to time.Time) ([]baseline.TimedVWAP, error) {
	if pair.String() == s.errFor {
		return nil, errors.New("transient db error")
	}
	return s.base.TimedVWAPsForPair1m(ctx, pair, from, to)
}

// TestRefresher_RefreshAll_ConcurrencyClamp — concurrency <= 0
// falls back to 1 (serial).
func TestRefresher_RefreshAll_ConcurrencyClamp(t *testing.T) {
	pair := mustPair(t, "native", "fiat:USD")
	src := newStubSource()
	src.set(pair, stableTimedSeries(time.Now().UTC(), 100))
	sink := newStubSink()

	r := baseline.NewRefresher(src, sink, 30*24*time.Hour, nil)
	sum := r.RefreshAll(context.Background(),
		[]canonical.Pair{pair, pair, pair}, 0)
	if sum.OK != 3 {
		t.Errorf("OK = %d, want 3", sum.OK)
	}
}
