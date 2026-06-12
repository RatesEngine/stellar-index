package band

import (
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

// UpdateEvent is the [consumer.Event] Band's Decoder emits per
// (symbol, rate) pair. The indexer's event sink type-switches on
// this and calls store.InsertOracleUpdate — same shape as
// reflector.UpdateEvent / redstone.UpdateEvent.
type UpdateEvent struct {
	Update canonical.OracleUpdate
}

// EventKind implements [consumer.Event].
func (UpdateEvent) EventKind() string { return "band.update" }

// Source implements [consumer.Event].
func (UpdateEvent) Source() string { return SourceName }

// Compile-time check.
var _ consumer.Event = UpdateEvent{}
