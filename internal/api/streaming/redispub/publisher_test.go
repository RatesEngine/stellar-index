package redispub_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/api/streaming/redispub"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// fakeRedis captures the (channel, message) of every Publish call
// so tests can assert on the JSON the orchestrator sees on the
// wire. Returns the configured err to exercise the error path.
type fakeRedis struct {
	calls []fakeCall
	err   error
}

type fakeCall struct {
	channel string
	body    []byte
}

func (f *fakeRedis) Publish(ctx context.Context, channel string, message any) *redis.IntCmd {
	body, _ := message.([]byte)
	f.calls = append(f.calls, fakeCall{channel: channel, body: body})
	cmd := redis.NewIntCmd(ctx)
	if f.err != nil {
		cmd.SetErr(f.err)
	} else {
		cmd.SetVal(1)
	}
	return cmd
}

func nativeUSD(t *testing.T) canonical.Pair {
	t.Helper()
	quote, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("ParseAsset: %v", err)
	}
	pair, err := canonical.NewPair(canonical.NativeAsset(), quote)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}
	return pair
}

// TestNewPublisher_RejectsNilCache — operator misconfig must fail
// at construction, not at first publish.
func TestNewPublisher_RejectsNilCache(t *testing.T) {
	if _, err := redispub.NewPublisher(nil, ""); err == nil {
		t.Error("expected error for nil cache")
	}
}

// TestNewPublisher_DefaultsChannel — empty channel falls back to
// DefaultChannel.
func TestNewPublisher_DefaultsChannel(t *testing.T) {
	p, err := redispub.NewPublisher(&fakeRedis{}, "")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	if p.Channel() != redispub.DefaultChannel {
		t.Errorf("Channel() = %q, want %q", p.Channel(), redispub.DefaultChannel)
	}
}

// TestPublishClosedBucket_RoundTrip — the canonical happy path:
// the orchestrator hands a (pair, window, value, observed_at) to
// the publisher, the publisher PUBLISHes a JSON-encoded
// ClosedBucketEvent on the configured channel.
func TestPublishClosedBucket_RoundTrip(t *testing.T) {
	pair := nativeUSD(t)
	cache := &fakeRedis{}
	p, err := redispub.NewPublisher(cache, "test:closed")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	observedAt := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)

	if err := p.PublishClosedBucket(context.Background(), pair, 5*time.Minute, "0.123456789012", observedAt); err != nil {
		t.Fatalf("PublishClosedBucket: %v", err)
	}
	if got := len(cache.calls); got != 1 {
		t.Fatalf("Publish called %d times, want 1", got)
	}
	call := cache.calls[0]
	if call.channel != "test:closed" {
		t.Errorf("channel = %q, want %q", call.channel, "test:closed")
	}

	var ev redispub.ClosedBucketEvent
	if err := json.Unmarshal(call.body, &ev); err != nil {
		t.Fatalf("unmarshal published body: %v", err)
	}
	if ev.Asset != pair.Base.String() {
		t.Errorf("Asset = %q, want %q", ev.Asset, pair.Base.String())
	}
	if ev.Quote != pair.Quote.String() {
		t.Errorf("Quote = %q, want %q", ev.Quote, pair.Quote.String())
	}
	if ev.WindowSeconds != 300 {
		t.Errorf("WindowSeconds = %d, want 300", ev.WindowSeconds)
	}
	if ev.ValueDecimal != "0.123456789012" {
		t.Errorf("ValueDecimal = %q, want %q", ev.ValueDecimal, "0.123456789012")
	}
	if !ev.ObservedAt.Equal(observedAt) {
		t.Errorf("ObservedAt = %v, want %v", ev.ObservedAt, observedAt)
	}
}

// TestPublishClosedBucket_PropagatesError — Redis publish failure
// surfaces wrapped error so the orchestrator can log + metric.
func TestPublishClosedBucket_PropagatesError(t *testing.T) {
	pair := nativeUSD(t)
	sentinel := errors.New("redis down")
	cache := &fakeRedis{err: sentinel}
	p, err := redispub.NewPublisher(cache, "")
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}

	err = p.PublishClosedBucket(context.Background(), pair, time.Hour, "1.0", time.Now())
	if err == nil {
		t.Fatal("expected error from PublishClosedBucket")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error chain = %v, want sentinel %v", err, sentinel)
	}
}
