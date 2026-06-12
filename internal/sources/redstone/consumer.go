package redstone

import (
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

// UpdateEvent is the [consumer.Event] Redstone's Decoder emits per
// feed update. The indexer's event sink type-switches on this and
// calls store.InsertOracleUpdate — same shape as reflector.UpdateEvent
// so the sink plumbing stays uniform.
type UpdateEvent struct {
	Update canonical.OracleUpdate
}

// EventKind implements [consumer.Event].
func (UpdateEvent) EventKind() string { return "redstone.update" }

// Source implements [consumer.Event].
func (UpdateEvent) Source() string { return SourceName }

// Compile-time check that UpdateEvent satisfies consumer.Event.
var _ consumer.Event = UpdateEvent{}
