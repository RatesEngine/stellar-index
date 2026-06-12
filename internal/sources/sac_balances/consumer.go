package sac_balances

import "github.com/StellarAtlas/stellar-atlas/internal/consumer"

func (Observation) EventKind() string { return ObservationKind }
func (Observation) Source() string    { return SourceName }

var _ consumer.Event = Observation{}
