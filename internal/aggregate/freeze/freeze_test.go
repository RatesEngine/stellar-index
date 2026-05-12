package freeze_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/aggregate/anomaly"
	"github.com/RatesEngine/rates-engine/internal/aggregate/freeze"
	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// newRedis spins up an in-memory miniredis + a *redis.Client
// pointed at it. Returns both so tests can assert against the
// underlying store directly.
func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func nativeUSD(t *testing.T) (canonical.Asset, canonical.Asset) {
	t.Helper()
	usd, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("ParseAsset: %v", err)
	}
	return canonical.NativeAsset(), usd
}

// TestNewWriter_RejectsNilCache — operator misconfig must fail at
// construction, not at first write.
func TestNewWriter_RejectsNilCache(t *testing.T) {
	if _, err := freeze.NewWriter(nil, 0); err == nil {
		t.Error("expected error for nil cache")
	}
}

// TestWriter_MarkRoundTrip — Mark writes a JSON Marker to the
// expected key with the expected TTL.
func TestWriter_MarkRoundTrip(t *testing.T) {
	mr, rdb := newRedis(t)
	w, err := freeze.NewWriter(rdb, 0)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	asset, quote := nativeUSD(t)

	decision := anomaly.Decision{
		Action:       anomaly.ActionFreeze,
		Class:        anomaly.ClassStablecoin,
		DeviationPct: 12.5,
		Reason:       "deviation 12.5% exceeds 10% threshold for stablecoin",
	}
	if err := w.Mark(context.Background(), asset, quote, "1.000000000000", decision); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	key := cachekeys.Freeze(asset, quote)
	raw, err := rdb.Get(context.Background(), key).Bytes()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var got freeze.Marker
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.AssetID != asset.String() || got.QuoteID != quote.String() {
		t.Errorf("AssetID/QuoteID mismatch: %s/%s", got.AssetID, got.QuoteID)
	}
	if got.Action != anomaly.ActionFreeze {
		t.Errorf("Action = %q, want %q", got.Action, anomaly.ActionFreeze)
	}
	if got.Class != anomaly.ClassStablecoin {
		t.Errorf("Class = %q", got.Class)
	}
	if got.DeviationPct != 12.5 {
		t.Errorf("DeviationPct = %v, want 12.5", got.DeviationPct)
	}
	if got.FrozenAt.IsZero() {
		t.Error("FrozenAt is zero")
	}

	ttl := mr.TTL(key)
	if ttl == 0 || ttl > cachekeys.FreezeTTL {
		t.Errorf("TTL = %v, want ≤ %v and > 0", ttl, cachekeys.FreezeTTL)
	}
}

// TestWriter_MarkRefreshesTTL — calling Mark twice for the same
// pair refreshes the TTL (anomaly persists ⇒ freeze stays in
// effect). Mirrors the Redis SET ... EX semantics.
func TestWriter_MarkRefreshesTTL(t *testing.T) {
	mr, rdb := newRedis(t)
	w, _ := freeze.NewWriter(rdb, 30*time.Second)
	asset, quote := nativeUSD(t)
	dec := anomaly.Decision{Action: anomaly.ActionFreeze, Class: anomaly.ClassDefault}

	if err := w.Mark(context.Background(), asset, quote, "", dec); err != nil {
		t.Fatalf("Mark (first): %v", err)
	}
	mr.FastForward(20 * time.Second)
	if err := w.Mark(context.Background(), asset, quote, "", dec); err != nil {
		t.Fatalf("Mark (refresh): %v", err)
	}

	key := cachekeys.Freeze(asset, quote)
	if ttl := mr.TTL(key); ttl <= 10*time.Second {
		t.Errorf("TTL after refresh = %v, want > 10s (refresh extended it)", ttl)
	}
}

// TestNewLooker_RejectsNilCache — same loud-misconfig stance.
func TestNewLooker_RejectsNilCache(t *testing.T) {
	if _, err := freeze.NewLooker(nil); err == nil {
		t.Error("expected error for nil cache")
	}
}

// TestLooker_FrozenForPair_AbsentMarker — clean state returns
// (false, nil), NOT an error. The API treats this as "not frozen".
func TestLooker_FrozenForPair_AbsentMarker(t *testing.T) {
	_, rdb := newRedis(t)
	l, _ := freeze.NewLooker(rdb)
	asset, quote := nativeUSD(t)

	frozen, err := l.FrozenForPair(context.Background(), asset, quote)
	if err != nil {
		t.Fatalf("err = %v, want nil for absent marker", err)
	}
	if frozen {
		t.Error("frozen = true for never-marked pair")
	}
}

// TestLooker_FrozenForPair_PresentMarker — marker present →
// (true, nil).
func TestLooker_FrozenForPair_PresentMarker(t *testing.T) {
	_, rdb := newRedis(t)
	w, _ := freeze.NewWriter(rdb, 0)
	l, _ := freeze.NewLooker(rdb)
	asset, quote := nativeUSD(t)

	if err := w.Mark(context.Background(), asset, quote, "",
		anomaly.Decision{Action: anomaly.ActionFreeze}); err != nil {
		t.Fatal(err)
	}

	frozen, err := l.FrozenForPair(context.Background(), asset, quote)
	if err != nil {
		t.Fatalf("FrozenForPair: %v", err)
	}
	if !frozen {
		t.Error("frozen = false; marker should be present")
	}
}

// TestLooker_FrozenForPair_TTLExpiry — once the marker's TTL
// elapses, FrozenForPair returns (false, nil) — same as a
// never-marked pair (which is correct: the freeze policy says
// "the anomaly cleared, publish normally").
func TestLooker_FrozenForPair_TTLExpiry(t *testing.T) {
	mr, rdb := newRedis(t)
	w, _ := freeze.NewWriter(rdb, 30*time.Second)
	l, _ := freeze.NewLooker(rdb)
	asset, quote := nativeUSD(t)

	if err := w.Mark(context.Background(), asset, quote, "",
		anomaly.Decision{Action: anomaly.ActionFreeze}); err != nil {
		t.Fatal(err)
	}
	// Roll past TTL.
	mr.FastForward(60 * time.Second)

	frozen, err := l.FrozenForPair(context.Background(), asset, quote)
	if err != nil {
		t.Fatal(err)
	}
	if frozen {
		t.Error("frozen = true after TTL expiry")
	}
}

// TestLooker_DistinctPairsIsolated — two different (asset, quote)
// pairs use different keys; freezing one doesn't bleed into the
// other.
func TestLooker_DistinctPairsIsolated(t *testing.T) {
	_, rdb := newRedis(t)
	w, _ := freeze.NewWriter(rdb, 0)
	l, _ := freeze.NewLooker(rdb)
	xlm, usd := nativeUSD(t)
	eur, _ := canonical.ParseAsset("fiat:EUR")

	// Freeze XLM/USD only.
	if err := w.Mark(context.Background(), xlm, usd, "",
		anomaly.Decision{Action: anomaly.ActionFreeze}); err != nil {
		t.Fatal(err)
	}

	frozen, _ := l.FrozenForPair(context.Background(), xlm, usd)
	if !frozen {
		t.Error("XLM/USD should be frozen")
	}
	frozen, _ = l.FrozenForPair(context.Background(), xlm, eur)
	if frozen {
		t.Error("XLM/EUR should NOT be frozen (distinct pair)")
	}
}

// recordingSink captures every RecordFreeze call so tests can
// assert the Writer wired the sink correctly.
type recordingSink struct {
	calls []recordedFreeze
	err   error
}

type recordedFreeze struct {
	Asset       canonical.Asset
	Quote       canonical.Asset
	FrozenValue string
	Decision    anomaly.Decision
}

func (r *recordingSink) RecordFreeze(_ context.Context, asset, quote canonical.Asset, frozenValue string, decision anomaly.Decision) error {
	r.calls = append(r.calls, recordedFreeze{
		Asset:       asset,
		Quote:       quote,
		FrozenValue: frozenValue,
		Decision:    decision,
	})
	return r.err
}

// TestWriter_Mark_FiresEventSink — Mark must call the wired sink
// in addition to the Redis write. Production wires the timescale-
// backed sink so the freeze_events hypertable mirrors the Redis
// state; this test pins that the Writer respects the WithEventSink
// option.
func TestWriter_Mark_FiresEventSink(t *testing.T) {
	_, rdb := newRedis(t)
	sink := &recordingSink{}
	w, err := freeze.NewWriter(rdb, 0, freeze.WithEventSink(sink))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	asset, quote := nativeUSD(t)
	decision := anomaly.Decision{
		Action:       anomaly.ActionFreeze,
		Class:        anomaly.ClassStablecoin,
		DeviationPct: 8.5,
		Reason:       "test",
	}
	if err := w.Mark(context.Background(), asset, quote, "0.999500000000", decision); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if len(sink.calls) != 1 {
		t.Fatalf("sink fired %d times, want 1", len(sink.calls))
	}
	got := sink.calls[0]
	if got.Asset.String() != asset.String() {
		t.Errorf("asset = %s, want %s", got.Asset.String(), asset.String())
	}
	if got.Quote.String() != quote.String() {
		t.Errorf("quote = %s, want %s", got.Quote.String(), quote.String())
	}
	if got.Decision.DeviationPct != decision.DeviationPct {
		t.Errorf("deviation = %v, want %v", got.Decision.DeviationPct, decision.DeviationPct)
	}
	if got.FrozenValue != "0.999500000000" {
		t.Errorf("frozenValue = %q, want %q", got.FrozenValue, "0.999500000000")
	}
}

// TestWriter_Mark_SinkErrorIsSwallowed — a sink failure must not
// fail the Mark call. The Redis write is the load-bearing operation
// for flags.frozen on the API; the durable mirror is best-effort.
func TestWriter_Mark_SinkErrorIsSwallowed(t *testing.T) {
	_, rdb := newRedis(t)
	sink := &recordingSink{err: errExploded}
	w, err := freeze.NewWriter(rdb, 0, freeze.WithEventSink(sink))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	asset, quote := nativeUSD(t)
	if err := w.Mark(context.Background(), asset, quote, "",
		anomaly.Decision{Action: anomaly.ActionFreeze}); err != nil {
		t.Fatalf("Mark: sink error must not propagate, got: %v", err)
	}
}

// errExploded is a sentinel for the sink-error test.
var errExploded = errSinkExploded("simulated sink failure")

type errSinkExploded string

func (e errSinkExploded) Error() string { return string(e) }

// silence unused-import warnings on platforms where this file
// is only partially read.
var (
	_ = json.Marshal
	_ = time.Now
	_ = miniredis.RunT
	_ = cachekeys.Freeze
)
