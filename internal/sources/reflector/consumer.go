package reflector

import (
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
)

// UpdateEvent is the [consumer.Event] Reflector's Decoder emits
// per oracle update. The indexer's event sink type-switches on
// this and calls store.InsertOracleUpdate.
type UpdateEvent struct {
	Update canonical.OracleUpdate
}

// EventKind implements [consumer.Event].
func (UpdateEvent) EventKind() string { return "reflector.update" }

// Source implements [consumer.Event]. Returns the source-name for
// the contained update so the event-sink can attribute metrics
// per-variant (reflector-dex / reflector-cex / reflector-fx)
// without type-assertion.
func (u UpdateEvent) Source() string { return u.Update.Source }

// Compile-time check that UpdateEvent satisfies consumer.Event.
var _ consumer.Event = UpdateEvent{}
