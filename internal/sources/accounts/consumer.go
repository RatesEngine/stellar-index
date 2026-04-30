package accounts

import "github.com/RatesEngine/rates-engine/internal/consumer"

// EventKind / Source on Observation implement [consumer.Event] so
// the observer's outputs flow through the standard dispatcher →
// orchestrator → indexer-sink path. The sink type-switches on
// ObservationKind to route into the account_observations
// hypertable.

// EventKind implements [consumer.Event].
func (Observation) EventKind() string { return ObservationKind }

// Source implements [consumer.Event].
func (Observation) Source() string { return SourceName }

var _ consumer.Event = Observation{}
