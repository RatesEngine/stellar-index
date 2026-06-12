package redispub

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/obs"
)

// RedisSubscriber is the subset of the Redis client surface
// [Subscriber] needs. Declared as an interface so tests can
// substitute miniredis without pulling the full UniversalClient.
type RedisSubscriber interface {
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
}

// Hub is the subset of [streaming.Hub] the subscriber needs.
// Declared as an interface so tests can substitute a recorder
// without spinning up the full Hub.
type Hub interface {
	Publish(topic, eventType string, data []byte) string
}

// Subscriber listens on the Redis channel the [Publisher] writes
// to (see [DefaultChannel]) and republishes each
// [ClosedBucketEvent] on the supplied Hub. The matching SSE
// topic key is `closed:<asset>/<quote>` — same format as
// `internal/api/v1.PriceStreamTopic`.
//
// Goroutine-safe: fields are read-only after construction.
type Subscriber struct {
	cache   RedisSubscriber
	channel string
	hub     Hub
	logger  *slog.Logger
}

// NewSubscriber constructs a Subscriber bound to the given Redis
// channel + Hub. Empty channel falls back to [DefaultChannel].
// nil logger falls back to [slog.Default].
func NewSubscriber(cache RedisSubscriber, channel string, hub Hub, logger *slog.Logger) (*Subscriber, error) {
	if cache == nil {
		return nil, errors.New("redispub: RedisSubscriber is required")
	}
	if hub == nil {
		return nil, errors.New("redispub: Hub is required")
	}
	if channel == "" {
		channel = DefaultChannel
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Subscriber{cache: cache, channel: channel, hub: hub, logger: logger}, nil
}

// Channel returns the Redis channel this Subscriber listens on.
func (s *Subscriber) Channel() string { return s.channel }

// Run blocks until ctx is cancelled, consuming messages from the
// Redis channel and republishing each one on the Hub. Returns
// ctx.Err() on clean shutdown; any unexpected stream-end is
// surfaced as an error so the caller can log and decide whether
// to retry.
//
// One Subscriber per binary; safe to invoke as a long-lived
// goroutine. The matching `cmd/ratesengine-api/main.go` wiring
// runs Run inside an errgroup alongside the HTTP server.
func (s *Subscriber) Run(ctx context.Context) error {
	pubsub := s.cache.Subscribe(ctx, s.channel)
	defer func() {
		if err := pubsub.Close(); err != nil {
			s.logger.Warn("redispub: pubsub close", "err", err)
		}
	}()

	ch := pubsub.Channel()
	s.logger.Info("redispub: subscriber listening", "channel", s.channel)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				// go-redis closes the channel when the underlying
				// pubsub disconnects irrecoverably. Surface it so the
				// caller can restart.
				return errors.New("redispub: subscribe channel closed unexpectedly")
			}
			s.handleMessage([]byte(msg.Payload))
		}
	}
}

// handleMessage decodes one wire payload and republishes on the
// Hub. JSON-decode failures log + increment the error metric;
// they're never propagated since one bad message must not stop
// the subscriber from processing the next.
func (s *Subscriber) handleMessage(payload []byte) {
	var ev ClosedBucketEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		obs.APIStreamSubscribeTotal.WithLabelValues("decode_error").Inc()
		s.logger.Warn("redispub: decode message", "err", err, "payload_len", len(payload))
		return
	}
	if ev.Asset == "" || ev.Quote == "" {
		obs.APIStreamSubscribeTotal.WithLabelValues("malformed").Inc()
		s.logger.Warn("redispub: malformed event (empty asset or quote)",
			"asset", ev.Asset, "quote", ev.Quote)
		return
	}
	topic := topicForPair(ev.Asset, ev.Quote)
	s.hub.Publish(topic, "price_update", payload)
	obs.APIStreamSubscribeTotal.WithLabelValues("ok").Inc()
}

// topicForPair returns the Hub topic key for a (asset, quote)
// pair. Mirrors `internal/api/v1.PriceStreamTopic` — a sentinel
// test in this package's test suite verifies the format stays
// in sync.
func topicForPair(asset, quote string) string {
	return "closed:" + asset + "/" + quote
}
