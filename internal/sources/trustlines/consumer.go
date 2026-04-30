package trustlines

import "github.com/RatesEngine/rates-engine/internal/consumer"

func (Observation) EventKind() string { return ObservationKind }
func (Observation) Source() string    { return SourceName }

var _ consumer.Event = Observation{}
