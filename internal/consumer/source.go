package consumer

import (
	"context"
	"time"
)

// Source is the uniform interface every ingestion source implements.
//
// Implementations live under internal/sources/<name>/. The
// orchestrator in cmd/ratesengine-indexer holds a registry of
// configured Source values and drives them via BackfillRange +
// StreamLive.
//
// Event is the empty interface on purpose — actual shapes
// (canonical.Trade, canonical.PriceObservation, canonical.LPState)
// come from internal/canonical. We use a tagged sum there rather
// than a type-switch here, keeping this interface generic across
// the many source flavours.
//
// Errors from these methods are fatal for the specific source
// invocation; the orchestrator logs + restarts after a backoff.
// Transient per-event parse failures never bubble up — they
// are counted as metrics and logged with structured fields.
type Source interface {
	// Name is a stable identifier used in metrics, logs, and
	// config. It must be [a-z0-9_-]+, short, lowercase.
	Name() string

	// BackfillRange emits every relevant event in the closed
	// ledger range [from, to]. Returns when the range is
	// exhausted or ctx is cancelled.
	BackfillRange(ctx context.Context, from, to uint32, out chan<- Event) error

	// StreamLive subscribes to the live source and emits new
	// events as they happen. Returns when ctx is cancelled
	// or the source hits an unrecoverable error.
	StreamLive(ctx context.Context, out chan<- Event) error

	// Health returns a point-in-time health snapshot used by
	// /health endpoints + Prometheus scraping.
	Health() HealthStatus
}

// Event is the sum type of every payload a source can emit.
// Concrete shapes are defined in internal/canonical; consumers
// type-switch on the concrete type, not this interface.
type Event interface {
	// EventKind is a constant string — e.g. "soroswap.trade",
	// "aquarius.trade". Used as the "kind" label on
	// event-classification metrics.
	EventKind() string

	// Source returns the stable source-name (matches [Source.Name])
	// so the event-sink pipeline can attribute metrics + logs
	// without type-switching.
	Source() string
}

// HealthStatus is the point-in-time state of a source. Exposed
// via /health and scraped into Prometheus as gauges.
type HealthStatus struct {
	// Connected is true if the source's upstream (WebSocket,
	// RPC, Galexie bucket, etc.) is currently responsive.
	Connected bool

	// LastEvent is the timestamp of the most recent event we
	// observed. Zero value means no event yet.
	LastEvent time.Time

	// LagLedgers is how many ledgers behind the current tip we
	// are, if the source reports it. Zero when not applicable
	// (e.g. CEX sources that aren't ledger-indexed).
	LagLedgers uint32

	// LastError, if non-nil, is the most recent error observed.
	// The orchestrator resets this on successful reconnect.
	LastError error
}
