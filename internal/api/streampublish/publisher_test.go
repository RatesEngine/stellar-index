package streampublish_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/api/streaming"
	"github.com/StellarAtlas/stellar-atlas/internal/api/streampublish"
	v1 "github.com/StellarAtlas/stellar-atlas/internal/api/v1"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// fakeReader returns canned snapshots keyed by pair string. The
// returned snapshot is mutable between ticks via SetSnapshot —
// tests advance the clock-equivalent (ObservedAt) to drive the
// publisher's bucket-change detection.
type fakeReader struct {
	mu        sync.Mutex
	snapshots map[string]v1.PriceSnapshot
	err       error
}

func (r *fakeReader) LatestPrice(_ context.Context, asset, quote canonical.Asset) (v1.PriceSnapshot, []string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return v1.PriceSnapshot{}, nil, false, r.err
	}
	key := asset.String() + "/" + quote.String()
	snap, ok := r.snapshots[key]
	if !ok {
		return v1.PriceSnapshot{}, nil, false, v1.ErrPriceNotFound
	}
	return snap, []string{"binance"}, false, nil
}

func (r *fakeReader) SetSnapshot(asset, quote canonical.Asset, snap v1.PriceSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snapshots == nil {
		r.snapshots = map[string]v1.PriceSnapshot{}
	}
	r.snapshots[asset.String()+"/"+quote.String()] = snap
}

func mustParse(t *testing.T, s string) canonical.Asset {
	t.Helper()
	a, err := canonical.ParseAsset(s)
	if err != nil {
		t.Fatalf("ParseAsset(%q): %v", s, err)
	}
	return a
}

// TestPublisher_PublishesOnNewBucket — the publisher polls,
// detects a fresh ObservedAt, and fans out a single event per
// bucket close. Pinned to catch a regression that re-publishes the
// same bucket on every tick (every-tick republish would burn
// subscriber budgets in seconds).
func TestPublisher_PublishesOnNewBucket(t *testing.T) {
	hub := streaming.NewHub(0)
	reader := &fakeReader{}
	asset := mustParse(t, "native")
	quote := mustParse(t, "fiat:USD")
	topic := v1.PriceStreamTopic(asset, quote)

	bucket1 := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	reader.SetSnapshot(asset, quote, v1.PriceSnapshot{
		AssetID: "native", Quote: "fiat:USD", Price: "0.07",
		PriceType: "vwap", ObservedAt: bucket1, WindowSeconds: 60,
	})

	pub := streampublish.New(hub, reader, time.Second, nil)

	// Subscribe BEFORE Run starts so we don't miss the immediate
	// poll-once tick.
	ch, cancel := hub.Subscribe([]string{topic}, "")
	defer cancel()

	ctx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	done := make(chan struct{})
	go func() {
		_ = pub.Run(ctx, []canonical.Pair{{Base: asset, Quote: quote}})
		close(done)
	}()

	// Expect exactly one event for bucket1.
	select {
	case ev := <-ch:
		if ev.Type != "price_update" {
			t.Errorf("event type = %q, want price_update", ev.Type)
		}
		var payload struct {
			Snapshot v1.PriceSnapshot `json:"snapshot"`
			Sources  []string         `json:"sources"`
		}
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if !payload.Snapshot.ObservedAt.Equal(bucket1) {
			t.Errorf("payload ObservedAt = %v, want %v", payload.Snapshot.ObservedAt, bucket1)
		}
		if payload.Snapshot.Price != "0.07" {
			t.Errorf("payload Price = %q, want 0.07", payload.Snapshot.Price)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received within 2s of publisher start")
	}

	// Second tick at the same bucket — must NOT republish.
	select {
	case ev := <-ch:
		t.Errorf("unexpected republish at same bucket: %+v", ev)
	case <-time.After(1500 * time.Millisecond):
		// Good — no event.
	}

	// Advance the bucket. Next tick should publish.
	bucket2 := bucket1.Add(time.Minute)
	reader.SetSnapshot(asset, quote, v1.PriceSnapshot{
		AssetID: "native", Quote: "fiat:USD", Price: "0.0712",
		PriceType: "vwap", ObservedAt: bucket2, WindowSeconds: 60,
	})

	select {
	case ev := <-ch:
		var payload struct {
			Snapshot v1.PriceSnapshot `json:"snapshot"`
		}
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("unmarshal payload (bucket2): %v", err)
		}
		if !payload.Snapshot.ObservedAt.Equal(bucket2) {
			t.Errorf("bucket2 ObservedAt = %v, want %v", payload.Snapshot.ObservedAt, bucket2)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received for bucket2 within 2s")
	}

	cancelRun()
	<-done
}

// TestPublisher_TwoSubscribersIdenticalPayload — the byte-identical
// fanout property is the whole point of the Hub-driven surface.
// If two clients on the same topic see different bytes, the
// cross-region consistency contract /v1/price/stream is meant to
// inherit from /v1/price (ADR-0015) is broken.
func TestPublisher_TwoSubscribersIdenticalPayload(t *testing.T) {
	hub := streaming.NewHub(0)
	reader := &fakeReader{}
	asset := mustParse(t, "native")
	quote := mustParse(t, "fiat:USD")
	topic := v1.PriceStreamTopic(asset, quote)

	reader.SetSnapshot(asset, quote, v1.PriceSnapshot{
		AssetID: "native", Quote: "fiat:USD", Price: "0.07",
		PriceType:  "vwap",
		ObservedAt: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
	})

	pub := streampublish.New(hub, reader, time.Second, nil)

	chA, cancelA := hub.Subscribe([]string{topic}, "")
	defer cancelA()
	chB, cancelB := hub.Subscribe([]string{topic}, "")
	defer cancelB()

	ctx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	go func() { _ = pub.Run(ctx, []canonical.Pair{{Base: asset, Quote: quote}}) }()

	var evA, evB streaming.Event
	select {
	case evA = <-chA:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber A timed out")
	}
	select {
	case evB = <-chB:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber B timed out")
	}
	if evA.ID != evB.ID {
		t.Errorf("event IDs differ: A=%q B=%q (Hub fanout must assign one ID per Publish)", evA.ID, evB.ID)
	}
	if string(evA.Data) != string(evB.Data) {
		t.Errorf("payload bytes differ:\n  A=%s\n  B=%s", evA.Data, evB.Data)
	}
}

// TestPublisher_ErrPriceNotFoundIsSilent — pairs without a
// closed bucket (fresh deploy, asset below operator coverage)
// emit no events. The publisher MUST NOT log a stack of errors
// for pairs that legitimately have no data yet.
func TestPublisher_ErrPriceNotFoundIsSilent(t *testing.T) {
	hub := streaming.NewHub(0)
	reader := &fakeReader{} // no snapshots → ErrPriceNotFound
	asset := mustParse(t, "native")
	quote := mustParse(t, "fiat:USD")
	topic := v1.PriceStreamTopic(asset, quote)

	pub := streampublish.New(hub, reader, time.Second, nil)
	ch, cancel := hub.Subscribe([]string{topic}, "")
	defer cancel()

	ctx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	go func() { _ = pub.Run(ctx, []canonical.Pair{{Base: asset, Quote: quote}}) }()

	select {
	case ev := <-ch:
		t.Errorf("unexpected event for unknown pair: %+v", ev)
	case <-time.After(1500 * time.Millisecond):
		// Good — silent.
	}
}

// TestPublisher_ReaderErrorContinues — a non-NotFound reader
// error (postgres unreachable) should not take the publisher
// down. The next tick after the error clears must publish
// normally. Mirrors the divergence-refresh / volume-reader
// best-effort posture.
func TestPublisher_ReaderErrorContinues(t *testing.T) {
	hub := streaming.NewHub(0)
	reader := &fakeReader{err: errors.New("postgres unreachable")}
	asset := mustParse(t, "native")
	quote := mustParse(t, "fiat:USD")
	topic := v1.PriceStreamTopic(asset, quote)

	pub := streampublish.New(hub, reader, time.Second, nil)
	ch, cancel := hub.Subscribe([]string{topic}, "")
	defer cancel()

	ctx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	go func() { _ = pub.Run(ctx, []canonical.Pair{{Base: asset, Quote: quote}}) }()

	// Wait through one error tick.
	time.Sleep(1500 * time.Millisecond)

	// Clear the error and provide a snapshot. Next tick should
	// publish.
	reader.mu.Lock()
	reader.err = nil
	reader.mu.Unlock()
	reader.SetSnapshot(asset, quote, v1.PriceSnapshot{
		AssetID: "native", Quote: "fiat:USD", Price: "0.07",
		ObservedAt: time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC),
	})

	select {
	case <-ch:
		// Good — publisher recovered.
	case <-time.After(2 * time.Second):
		t.Fatal("publisher did not recover from reader error within 2s")
	}
}

// TestPublisher_NoPairsBlocksUntilCancel — empty Pairs must not
// busy-spin or panic; Run blocks on ctx and returns ctx.Err() on
// cancel. Regression against an earlier draft that returned
// immediately on len(pairs)==0.
func TestPublisher_NoPairsBlocksUntilCancel(t *testing.T) {
	hub := streaming.NewHub(0)
	reader := &fakeReader{}
	pub := streampublish.New(hub, reader, time.Second, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- pub.Run(ctx, nil) }()

	// Confirm Run is still blocked.
	select {
	case err := <-done:
		t.Fatalf("Run returned early on empty pairs: %v", err)
	case <-time.After(100 * time.Millisecond):
		// Good.
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
