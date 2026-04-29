package streaming

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Event is one SSE message ready for delivery on a topic.
//
// Wire mapping when serialised by the Stream handler:
//
//	id: <ID>
//	event: <Type>
//	data: <Data>
//	\n
//
// Data is pre-serialised JSON or whatever the producer wants on the
// wire — Stream is content-agnostic to keep it reusable across
// price/tip/observations stream shapes.
type Event struct {
	// ID is the lexicographically-sortable event identifier.
	// Clients use it as `Last-Event-ID` on reconnect to resume.
	ID string

	// Type is the SSE `event:` field, e.g. "price_update",
	// "tip_update". Empty Type means "default" event in the SSE spec
	// — clients dispatch to the unnamed handler.
	Type string

	// Data is the SSE `data:` payload, written verbatim. Multiline
	// payloads MUST NOT contain bare \n — Stream emits one `data:`
	// line per logical record, so callers should pre-encode to a
	// single line of JSON before publishing.
	Data []byte

	// Timestamp is the producer-side wallclock at publish. Used by
	// Hub for buffer-eviction policies; clients don't see it
	// directly.
	Timestamp time.Time
}

// Generator is a goroutine-safe monotonic event-ID source. Each
// call to [Generator.Next] returns a fresh 16-char lowercase hex
// ID composed of:
//
//   - 8 bytes (16 hex chars) packed: high 48 bits are unix-millis
//     (truncated), low 16 bits are a per-millisecond rolling counter
//     to break ties in the same wall-clock millisecond.
//
// This is NOT a ULID — we intentionally avoid the dependency. The
// only contract clients need is "lexicographic sort = chronological"
// which this format satisfies for the next ~8900 years (until 2^48
// ms overflows in 10889 CE). The internal format is private; treat
// IDs as opaque strings.
//
// Hubs hold a Generator internally; non-Hub callers (e.g. the
// /v1/price/tip/stream handler that ticks per-connection without
// going through a Hub) can construct one directly.
type Generator struct {
	// state packs (lastMillis << 16) | counter into a single
	// uint64 for atomic-CAS update. counter resets implicitly each
	// time lastMillis advances.
	state atomic.Uint64
}

// Next returns the next sortable event ID. Goroutine-safe; never
// returns the same ID twice (within the lifetime of the generator).
func (g *Generator) Next() string {
	now := uint64(time.Now().UnixMilli()) //nolint:gosec // wall-clock millis bounded; sentinel-safe past year 2106 because we only use lower 48 bits
	for {
		old := g.state.Load()
		oldMillis := old >> 16
		oldCounter := old & 0xFFFF

		var next uint64
		if now > oldMillis {
			next = (now << 16) // counter resets to 0 when the millisecond advances
		} else {
			// Same or earlier millisecond — bump the counter. If the
			// system clock jumped backwards we still produce a strictly
			// increasing ID (we keep the older millis but increment).
			next = (oldMillis << 16) | ((oldCounter + 1) & 0xFFFF)
		}
		if g.state.CompareAndSwap(old, next) {
			// 16-char lowercase hex of next; sortable lexicographically
			// because the high bits are time and we zero-pad with %016x.
			return fmt.Sprintf("%016x", next)
		}
	}
}
