package sep41_transfers

import "github.com/StellarAtlas/stellar-atlas/internal/consumer"

func (Event) EventKind() string { return EventKind }
func (Event) Source() string    { return SourceName }

var _ consumer.Event = Event{}
