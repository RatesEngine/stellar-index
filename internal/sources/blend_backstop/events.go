package blend_backstop

import (
	"time"

	"github.com/StellarIndex/stellar-index/internal/consumer"
	"github.com/StellarIndex/stellar-index/internal/scval"
	"github.com/StellarIndex/stellar-index/internal/sources/blend"
)

// SourceName is the registry key for this source.
const SourceName = "blend_backstop"

// Backstop contracts — the two mainnet Blend Backstop deployments.
// The Backstop is a SEPARATE event surface from the Blend pool /
// pool-factory decoder (internal/sources/blend); it has its own
// contract addresses and its own 10-event vocabulary. Gating on BOTH
// is mandatory: the backstop's symbols (claim / withdraw /
// queue_withdrawal / gulp_emissions) OVERLAP with Blend POOL event
// symbols, so the contract-id gate — not the topic symbol — is what
// disambiguates a backstop event from a pool event. See ADR-0035
// (factory-anchored contract gating) + the "Comet uses a shared
// topic" trap in CLAUDE.md.
//
//   - MainnetBackstopV2 is the Backstop V2 singleton (current).
//   - MainnetBackstopV1 (the V1 const) is read from the sibling
//     blend package — both were live for a span and a backfill range
//     would replay either, so both gate.
const MainnetBackstopV2 = "CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7"

// MainnetBackstopV1 re-exports blend.MainnetBackstopV1 so the
// backstop decoder's contract gate and the blend package agree on the
// V1 address without re-declaring the constant value here.
const MainnetBackstopV1 = blend.MainnetBackstopV1

// BackstopGenesisLedger is the first ledger at which a Blend Backstop
// contract could emit — aligned with the Blend pool factory genesis
// (the backstop deploys alongside the protocol). Used by the
// per-source gap detector to size expected coverage.
const BackstopGenesisLedger uint32 = 56_627_571

// Event names — topic[0] Symbol strings emitted by the backstop
// contract. The 10 events of the Backstop V2 surface.
//
// SCHEMA PROVENANCE: these field layouts were REVERSE-ENGINEERED from
// real mainnet lake samples on 2026-06-15 and validated against the
// promoted-column contract below. They are NOT yet confirmed against
// the Blend team's published contract source. Until that confirmation
// lands, this source is LIVE-CAPTURE ONLY — do NOT flip any
// BackfillSafe flag or run a historical re-derive against these
// schemas (a schema drift in the reverse-engineering would silently
// mis-attribute every backfilled row). See README.md §Provenance.
const (
	EventDeposit           = "deposit"
	EventClaim             = "claim"
	EventDonate            = "donate"
	EventQueueWithdrawal   = "queue_withdrawal"
	EventWithdraw          = "withdraw"
	EventDistribute        = "distribute"
	EventGulpEmissions     = "gulp_emissions"
	EventDequeueWithdrawal = "dequeue_withdrawal"
	EventDraw              = "draw"
	EventRwZoneAdd         = "rw_zone_add"
)

// Topic[0] pre-encoded base64 — package-init constants so Classify()
// does single string-equal comparisons rather than a full SCVal
// decode per event (mirrors cctp / blend).
var (
	TopicSymbolDeposit           = scval.MustEncodeSymbol(EventDeposit)
	TopicSymbolClaim             = scval.MustEncodeSymbol(EventClaim)
	TopicSymbolDonate            = scval.MustEncodeSymbol(EventDonate)
	TopicSymbolQueueWithdrawal   = scval.MustEncodeSymbol(EventQueueWithdrawal)
	TopicSymbolWithdraw          = scval.MustEncodeSymbol(EventWithdraw)
	TopicSymbolDistribute        = scval.MustEncodeSymbol(EventDistribute)
	TopicSymbolGulpEmissions     = scval.MustEncodeSymbol(EventGulpEmissions)
	TopicSymbolDequeueWithdrawal = scval.MustEncodeSymbol(EventDequeueWithdrawal)
	TopicSymbolDraw              = scval.MustEncodeSymbol(EventDraw)
	TopicSymbolRwZoneAdd         = scval.MustEncodeSymbol(EventRwZoneAdd)
)

// Event is the [consumer.Event] the backstop Decoder emits — one per
// decoded contract event. It carries the blend_backstop_events row
// shape (migration 0063): the universal identity fields, the promoted
// typed columns (Pool / UserAddress / Amount / Amount2), and the
// event-type-specific remainder in Attributes.
//
// Pool / UserAddress are strkey strings; an empty value means "this
// event type carries no such field" and the sink writes SQL NULL.
// Amount / Amount2 are decimal i128 strings (ADR-0003 — never
// int64); empty → SQL NULL.
//
// The indexer's event sink type-switches on this at its output
// channel (internal/pipeline/sink.go) and writes via
// Store.InsertBlendBackstopEvent. The projector
// (internal/projector/registry.go) is the sole writer in Phase-4
// sole-writer mode.
type Event struct {
	ContractID  string
	Ledger      uint32
	TxHash      string
	OpIndex     int
	EventIndex  int
	ObservedAt  time.Time
	EventType   string         // one of the Event* constants
	Pool        string         // pool Address strkey; "" → none
	UserAddress string         // user Address strkey; "" → none
	Amount      string         // decimal i128; "" → none
	Amount2     string         // decimal i128; "" → none
	Attributes  map[string]any // event-type-specific remainder
}

// EventKind implements [consumer.Event]. A single kind for the whole
// backstop surface — the per-event discriminator lives in EventType /
// the event_kind column, mirroring how cctp.Event uses one EventKind
// across its four event types.
func (Event) EventKind() string { return "blend_backstop.event" }

// Source implements [consumer.Event] — matches [SourceName].
func (Event) Source() string { return SourceName }

// Compile-time check that Event satisfies consumer.Event.
var _ consumer.Event = Event{}
