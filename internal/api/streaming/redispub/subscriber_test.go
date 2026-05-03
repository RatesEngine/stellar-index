package redispub_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/api/streaming/redispub"
	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// fakeHub captures Hub.Publish calls.
type fakeHub struct {
	mu    sync.Mutex
	calls []hubCall
}

type hubCall struct {
	topic     string
	eventType string
	data      []byte
}

func (h *fakeHub) Publish(topic, eventType string, data []byte) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, hubCall{topic: topic, eventType: eventType, data: data})
	return "fake-id"
}

func (h *fakeHub) Calls() []hubCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]hubCall, len(h.calls))
	copy(out, h.calls)
	return out
}

// newRedis spins up an in-memory miniredis + a *redis.Client.
// miniredis supports SUBSCRIBE/PUBLISH out of the box.
func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

// TestNewSubscriber_RequiresInputs — operator misconfig must fail
// at construction.
func TestNewSubscriber_RequiresInputs(t *testing.T) {
	if _, err := redispub.NewSubscriber(nil, "", &fakeHub{}, nil); err == nil {
		t.Error("expected error for nil cache")
	}
	_, rdb := newRedis(t)
	if _, err := redispub.NewSubscriber(rdb, "", nil, nil); err == nil {
		t.Error("expected error for nil hub")
	}
}

// TestSubscriber_RoundTrip — the canonical happy path: a Publisher
// writes one event; the Subscriber decodes it and republishes on
// the Hub with the canonical topic key.
func TestSubscriber_RoundTrip(t *testing.T) {
	_, rdb := newRedis(t)
	hub := &fakeHub{}
	sub, err := redispub.NewSubscriber(rdb, "test:closed", hub, nil)
	if err != nil {
		t.Fatalf("NewSubscriber: %v", err)
	}

	pub, err := redispub.NewPublisher(rdb, "test:closed")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- sub.Run(ctx) }()

	// miniredis SUBSCRIBE registration is racy with PUBLISH —
	// give the subscriber a beat to bind before we publish.
	time.Sleep(50 * time.Millisecond)

	usd, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("ParseAsset: %v", err)
	}
	pair, err := canonical.NewPair(canonical.NativeAsset(), usd)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}
	observedAt := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)

	if err := pub.PublishClosedBucket(ctx, pair, 5*time.Minute, "0.123456789012", observedAt); err != nil {
		t.Fatalf("PublishClosedBucket: %v", err)
	}

	// Wait briefly for the message to flow through.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(hub.Calls()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}

	calls := hub.Calls()
	if len(calls) != 1 {
		t.Fatalf("Hub.Publish called %d times, want 1", len(calls))
	}
	c := calls[0]
	wantTopic := v1.PriceStreamTopic(pair.Base, pair.Quote)
	if c.topic != wantTopic {
		t.Errorf("topic = %q, want %q", c.topic, wantTopic)
	}
	if c.eventType != "price_update" {
		t.Errorf("eventType = %q, want price_update", c.eventType)
	}

	// Payload round-trip — Subscriber forwards the published
	// JSON bytes verbatim, so re-decode should match.
	var got redispub.ClosedBucketEvent
	if err := json.Unmarshal(c.data, &got); err != nil {
		t.Fatalf("decode forwarded payload: %v", err)
	}
	if got.Asset != pair.Base.String() || got.Quote != pair.Quote.String() {
		t.Errorf("payload identity = %s/%s, want %s/%s",
			got.Asset, got.Quote, pair.Base.String(), pair.Quote.String())
	}

	cancel()
	if err := <-runDone; !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v, want context.Canceled", err)
	}
}

// TestSubscriber_TopicFormatStaysInSync — sentinel test: Subscriber's
// topic format must match v1.PriceStreamTopic since the Hub layer
// expects the exact string.
func TestSubscriber_TopicFormatStaysInSync(t *testing.T) {
	usd, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("ParseAsset: %v", err)
	}
	want := v1.PriceStreamTopic(canonical.NativeAsset(), usd)
	if want != "closed:"+canonical.NativeAsset().String()+"/"+usd.String() {
		t.Fatalf("v1.PriceStreamTopic format changed; redispub Subscriber must update too. Got %q", want)
	}
}
