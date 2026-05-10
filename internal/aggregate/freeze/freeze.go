package freeze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/aggregate/anomaly"
	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// Marker is the JSON shape stored at the `freeze:<asset>:<quote>`
// Redis key. Carries diagnostic context the API doesn't read but
// operators want for log correlation when investigating frozen
// pairs.
type Marker struct {
	// AssetID + QuoteID echo the (asset, quote) the freeze applies
	// to. Lets a Redis dump be self-describing without needing the
	// key to be parsed.
	AssetID string `json:"asset_id"`
	QuoteID string `json:"quote_id"`

	// Action is the anomaly Decision Action — always "freeze" by
	// construction; the field exists so the value type is
	// future-proof if we ever extend the marker to cover
	// ActionWarn-style warnings.
	Action anomaly.Action `json:"action"`

	// Class is the asset class that drove the threshold lookup
	// (stablecoin / volatile / fiat / etc).
	Class anomaly.AssetClass `json:"class"`

	// DeviationPct is the deviation from the previous bucket's VWAP
	// that triggered the freeze.
	DeviationPct float64 `json:"deviation_pct"`

	// Reason is the human-readable explanation from the Decision.
	Reason string `json:"reason,omitempty"`

	// FrozenAt is when the writer wrote this marker. RFC 3339 UTC.
	FrozenAt time.Time `json:"frozen_at"`
}

// RedisCache is the subset of the Redis client both the Writer and
// Looker need. Declared as an interface so tests can substitute
// miniredis without pulling the full UniversalClient surface.
type RedisCache interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
}

// EventSink is the optional durable-mirror seam for freeze events.
// The Writer calls RecordFreeze on every Mark; the implementation
// is responsible for de-duplicating against the still-firing row
// (so refreshing a Redis TTL doesn't create N rows in postgres).
//
// Nil sinks are valid — pre-existing deployments without a sink
// keep their Redis-only behaviour. Production wires
// `internal/storage/timescale.FreezeEventSink` here; tests pass
// either nil or a fake.
//
// Per docs/architecture/explorer-implementation-plan.md
// Phase 2: this is what migrates the Redis-only freeze state into
// a queryable postgres timeline that powers /v1/anomalies.
type EventSink interface {
	// RecordFreeze persists a freeze event. Idempotent against the
	// "currently firing" row for (asset, quote): if a row with
	// recovered_at IS NULL already exists for this pair, the call
	// is a no-op. Otherwise INSERT a new row with frozen_at=now.
	//
	// Implementations must NOT block the Writer's hot path on
	// network failures — log + continue. The Redis marker write
	// is the load-bearing operation; the durable mirror is best-
	// effort.
	RecordFreeze(ctx context.Context, asset, quote canonical.Asset, decision anomaly.Decision) error
}

// Writer marks a (asset, quote) pair as frozen by writing a
// [Marker] to Redis at the `freeze:<asset>:<quote>` key with the
// configured TTL. Constructed by the aggregator orchestrator at
// startup.
//
// When an EventSink is wired, the Writer also records the freeze
// to the durable mirror — postgres-backed in production, used by
// the explorer's /anomalies timeline.
//
// Safe for concurrent Mark calls — fields are read-only after
// construction; the underlying RedisCache is concurrent-safe by
// contract; the EventSink contract requires concurrent-safety.
type Writer struct {
	cache RedisCache
	ttl   time.Duration
	sink  EventSink
}

// NewWriter constructs a Writer. ttl=0 falls back to
// cachekeys.FreezeTTL; operators tune up only when a custom
// deployment needs longer freeze persistence (rare).
//
// sink is optional (nil = legacy Redis-only behaviour); production
// passes the timescale-backed implementation.
func NewWriter(cache RedisCache, ttl time.Duration, opts ...WriterOption) (*Writer, error) {
	if cache == nil {
		return nil, errors.New("freeze: RedisCache is required")
	}
	if ttl <= 0 {
		ttl = cachekeys.FreezeTTL
	}
	w := &Writer{cache: cache, ttl: ttl}
	for _, opt := range opts {
		opt(w)
	}
	return w, nil
}

// WriterOption tunes a Writer at construction time.
type WriterOption func(*Writer)

// WithEventSink wires the durable freeze-event mirror. Pass
// `internal/storage/timescale.FreezeEventSink` in production; tests
// can inject a fake or omit the option entirely (nil sink = no
// mirror, same as the pre-Phase-2 behaviour).
func WithEventSink(sink EventSink) WriterOption {
	return func(w *Writer) {
		w.sink = sink
	}
}

// Mark records a freeze for (asset, quote) backed by the supplied
// anomaly Decision. Idempotent — overwriting an existing marker
// refreshes its TTL, which matches the desired semantics ("freeze
// stays in effect as long as the underlying anomaly persists").
//
// Returns the underlying error wrapped when the Redis write fails;
// callers log + continue (the next bucket close retries the write).
func (w *Writer) Mark(ctx context.Context, asset, quote canonical.Asset, decision anomaly.Decision) error {
	marker := Marker{
		AssetID:      asset.String(),
		QuoteID:      quote.String(),
		Action:       decision.Action,
		Class:        decision.Class,
		DeviationPct: decision.DeviationPct,
		Reason:       decision.Reason,
		FrozenAt:     time.Now().UTC(),
	}
	body, err := json.Marshal(marker)
	if err != nil {
		// Unreachable — Marker has no func/chan fields. Wrap for
		// diagnostic completeness.
		return fmt.Errorf("freeze: marshal marker: %w", err)
	}
	key := cachekeys.Freeze(asset, quote)
	if err := w.cache.Set(ctx, key, body, w.ttl).Err(); err != nil {
		return fmt.Errorf("freeze: cache set %s: %w", key, err)
	}

	// Durable mirror. Best-effort: a sink failure must not surface
	// to the caller because the Redis write — the load-bearing
	// operation that drives flags.frozen on the API response — has
	// already succeeded. The sink is for the explorer /anomalies
	// timeline, not for liveness.
	if w.sink != nil {
		if sinkErr := w.sink.RecordFreeze(ctx, asset, quote, decision); sinkErr != nil {
			// Caller logs at DEBUG; we don't want to spam WARN on
			// every transient postgres blip. The sink is expected
			// to log its own failures with full context.
			_ = sinkErr
		}
	}
	return nil
}

// Looker reads the freeze marker for a pair. Implements the
// behaviour of internal/api/v1.FrozenLooker (the API package
// declares its own interface to avoid the import cycle; Looker
// satisfies it structurally).
//
// Safe for concurrent FrozenForPair calls.
type Looker struct {
	cache RedisCache
}

// NewLooker constructs a Looker around a RedisCache.
func NewLooker(cache RedisCache) (*Looker, error) {
	if cache == nil {
		return nil, errors.New("freeze: RedisCache is required")
	}
	return &Looker{cache: cache}, nil
}

// FrozenForPair reports whether (asset, quote) currently has a
// freeze marker in cache. Returns:
//
//   - (true, nil)  — marker present (TTL still alive)
//   - (false, nil) — no marker (clean state OR TTL elapsed; the
//     API can't distinguish the two and shouldn't need to)
//   - (false, err) — Redis read failed; caller (API handler) logs
//   - falls through with frozen=false. Better to publish a price
//     without the warning than 5xx because of a Redis blip.
//
// Implements the contract of [internal/api/v1.FrozenLooker].
func (l *Looker) FrozenForPair(ctx context.Context, asset, quote canonical.Asset) (bool, error) {
	key := cachekeys.Freeze(asset, quote)
	_, err := l.cache.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("freeze: cache get %s: %w", key, err)
	}
	return true, nil
}
