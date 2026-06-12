package discovery

import (
	"github.com/StellarAtlas/stellar-atlas/internal/events"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// SEP41EventType identifies which of the four SEP-41 event topics
// fired. Stable string values appear in the discovered_assets table
// and metric labels — renaming is a wire break.
type SEP41EventType string

const (
	// EventTransfer fires on `transfer` events. Topic shape:
	// ("transfer", from, to, sep0011_asset?). The asset topic
	// position-3 was added post-P23 (CAP-67) for unified events.
	EventTransfer SEP41EventType = "transfer"

	// EventMint fires on `mint` events. Topic shape:
	// ("mint", admin, to, sep0011_asset?).
	EventMint SEP41EventType = "mint"

	// EventBurn fires on `burn` events. Topic shape:
	// ("burn", from, sep0011_asset?). Distinct from EventClawback
	// — burn is voluntary, clawback is admin-driven.
	EventBurn SEP41EventType = "burn"

	// EventClawback fires on `clawback` events. Topic shape:
	// ("clawback", admin, from, sep0011_asset?). Compliance-
	// significant: a token with frequent clawbacks reads
	// differently from one with frequent voluntary burns.
	EventClawback SEP41EventType = "clawback"
)

// Hit is the structured result of a successful [Sniff]. ContractID
// is the C-strkey of the emitting contract; EventType identifies
// which SEP-41 topic fired; Ledger + ObservedAt locate the
// observation in time.
//
// The struct is small and copyable; the [Recorder] is responsible
// for de-duplicating on ContractID before writing.
type Hit struct {
	ContractID string
	EventType  SEP41EventType
	Ledger     uint32
	// ObservedAtRFC3339 is event.LedgerClosedAt verbatim — the caller
	// parses to time.Time when needed (typically at recorder
	// boundary). Kept as string so the sniffer is allocation-light
	// in the hot dispatch path.
	ObservedAtRFC3339 string
}

// Sniff inspects an event and reports whether it matches a SEP-41
// topic shape. Returns (hit, true) when the event's topic[0] decodes
// to one of the four SEP-41 event symbols; (zero, false) otherwise.
//
// Sniff is pure: no I/O, no allocations beyond the SCVal parse.
// Designed to run in the dispatcher's hot path on every contract
// event without measurable overhead.
//
// Returns (zero, false) when:
//   - The event is not a contract event (Type != "contract").
//   - Topic is empty or topic[0] doesn't decode to a Symbol.
//   - The symbol value isn't one of the four SEP-41 events.
//   - ContractID is empty (defensive — should never happen for
//     contract events but the guard prevents writing junk to the
//     Recorder).
//
// Specifically does NOT validate topic arity beyond topic[0] —
// SEP-41 went through several revisions across Soroban genesis, and
// older contracts may emit transfer events with three topics rather
// than four. Discovery records the contract-id sighting; downstream
// decoders reject malformed bodies on their own schedule.
func Sniff(ev events.Event) (Hit, bool) {
	if ev.Type != "contract" {
		return Hit{}, false
	}
	if ev.ContractID == "" {
		return Hit{}, false
	}
	if len(ev.Topic) == 0 {
		return Hit{}, false
	}

	sv, err := scval.Parse(ev.Topic[0])
	if err != nil {
		return Hit{}, false
	}
	sym, err := scval.AsSymbol(sv)
	if err != nil {
		return Hit{}, false
	}

	eventType, ok := classifySymbol(sym)
	if !ok {
		return Hit{}, false
	}

	return Hit{
		ContractID:        ev.ContractID,
		EventType:         eventType,
		Ledger:            ev.Ledger,
		ObservedAtRFC3339: ev.LedgerClosedAt,
	}, true
}

// classifySymbol maps an SCVal symbol string to its [SEP41EventType].
// Returns ok=false for symbols that aren't SEP-41 events (e.g. the
// many DEX-specific symbols this repo also routes — `swap`, `sync`,
// `deposit`, `withdraw`, etc.).
func classifySymbol(sym string) (SEP41EventType, bool) {
	switch sym {
	case "transfer":
		return EventTransfer, true
	case "mint":
		return EventMint, true
	case "burn":
		return EventBurn, true
	case "clawback":
		return EventClawback, true
	default:
		return "", false
	}
}
