package sep41_supply

import (
	"math/big"
	"time"

	"github.com/RatesEngine/rates-engine/internal/scval"
)

const (
	SourceName = "sep41_supply"

	// EventKind is the consumer.Event.EventKind value emitted by
	// this observer. The indexer's sink type-switches on it to
	// route to Store.InsertSEP41SupplyEvent.
	EventKind = "sep41_supply.event"

	// Symbol values matching internal/canonical/discovery's
	// classifySymbol — kept duplicated here so this package can
	// stand alone (the discovery package's classifySymbol is
	// unexported by design).
	SymbolMint     = "mint"
	SymbolBurn     = "burn"
	SymbolClawback = "clawback"
)

// Pre-encoded base64 SCVal blobs for cheap byte-equality matching
// against incoming topic[0] strings. Computed once at init via
// scval.MustEncodeSymbol.
var (
	TopicSymbolMint     = scval.MustEncodeSymbol(SymbolMint)
	TopicSymbolBurn     = scval.MustEncodeSymbol(SymbolBurn)
	TopicSymbolClawback = scval.MustEncodeSymbol(SymbolClawback)
)

// Event is one observed mint / burn / clawback event. Routed
// through the dispatcher → consumer pipeline; the indexer-side
// sink writes to `sep41_supply_events` (#309).
//
// Amount is always non-negative — Kind discriminates direction
// for the running sum. Counterparty is the recipient (mint) or
// holder (burn / clawback); empty when not present (no SEP-41
// variant emits without one today, but the field is reserved
// for future-spec robustness).
type Event struct {
	ContractID   string
	Ledger       uint32
	TxHash       string
	OpIndex      uint32
	ObservedAt   time.Time
	Kind         string // SymbolMint | SymbolBurn | SymbolClawback
	Amount       *big.Int
	Counterparty string
}
