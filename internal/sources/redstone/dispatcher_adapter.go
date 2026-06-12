package redstone

import (
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// Decoder is the dispatcher-facing view of the RedStone adapter.
// Single instance per indexer — Redstone has one adapter contract
// covering every feed (unlike Reflector's 3 variants).
//
// No goroutines, no state, no polling. Attribution comes from the
// InvokeContract op args that the dispatcher now plumbs through
// events.Event.OpArgs; decoded on each event. See
// docs/discovery/oracles/redstone.md for the full wire shape.
type Decoder struct {
	contractID string
}

// NewDecoder constructs a Redstone Decoder bound to the adapter's
// mainnet contract address. Other contracts (per-feed proxies,
// unrelated Redstone deployments) are rejected at Matches time —
// they don't emit REDSTONE events, so routing only the adapter is
// the correct scope.
func NewDecoder(adapterContractID string) *Decoder {
	return &Decoder{contractID: adapterContractID}
}

// Name implements [dispatcher.Decoder].
func (d *Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Byte-equality on
// topic[0] and string equality on the event's contract ID — both
// cheap, no SCVal parsing on the hot path.
func (d *Decoder) Matches(ev events.Event) bool {
	if ev.ContractID != d.contractID {
		return false
	}
	return classify(&ev)
}

// Decode implements [dispatcher.Decoder]. Returns zero or more
// UpdateEvent wrappers — one per (feed_id, price) entry in the
// event's updated_feeds vector after unknown-feed skips.
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	closedAt, err := ev.EventClosedAt()
	if err != nil {
		// Fail closed rather than substituting time.Now(): closedAt
		// is the fallback pickTimestamp uses when a PriceData's
		// PackageTimestamp is 0 / out of its sanity window, so a
		// wall-clock value here would mis-timestamp the row during a
		// backfill replay (cf. the comet sibling).
		return nil, err
	}
	updates, err := decodeWritePrices(&ev, closedAt)
	if err != nil {
		return nil, err
	}
	out := make([]consumer.Event, 0, len(updates))
	for _, u := range updates {
		out = append(out, UpdateEvent{Update: u})
	}
	return out, nil
}
