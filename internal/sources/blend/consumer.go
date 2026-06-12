package blend

import (
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

// Auction events emitted by the Blend Decoder. These are NOT
// canonical.Trade rows — Blend doesn't generate spot-trade prices.
// Each is a directional / state-change signal the indexer's sink
// routes to per-protocol Blend storage (auctions table, separate
// from the trades hypertable).

// NewAuctionEventKind / FillAuctionEventKind / DeleteAuctionEventKind
// are stable strings used by the sink's type-switch + by metrics
// labels. They follow the `<source>.<event>` convention used by
// every other source package.
const (
	NewAuctionEventKind    = "blend.new_auction"
	FillAuctionEventKind   = "blend.fill_auction"
	DeleteAuctionEventKind = "blend.delete_auction"

	// Money-market event kinds (#25). Each carries the canonical
	// (per-pool, per-user, per-asset) position-changing event.
	PositionEventKind = "blend.position"
	EmissionEventKind = "blend.emission"
	AdminEventKind    = "blend.admin"
)

// EventKind / Source on the per-event types implements
// [consumer.Event]. The dispatcher's output channel has a
// single concrete type — consumer.Event — and the sink picks each
// off via type-switch.

func (NewAuctionEvent) EventKind() string { return NewAuctionEventKind }
func (NewAuctionEvent) Source() string    { return SourceName }

func (FillAuctionEvent) EventKind() string { return FillAuctionEventKind }
func (FillAuctionEvent) Source() string    { return SourceName }

func (DeleteAuctionEvent) EventKind() string { return DeleteAuctionEventKind }
func (DeleteAuctionEvent) Source() string    { return SourceName }

// Money-market event types (#25). The decoder emits one of these
// per money-market / credit-risk / admin event; the sink's
// type-switch routes each to the matching blend_positions /
// blend_emissions / blend_admin writer.
func (PositionEvent) EventKind() string { return PositionEventKind }
func (PositionEvent) Source() string    { return SourceName }

func (EmissionEvent) EventKind() string { return EmissionEventKind }
func (EmissionEvent) Source() string    { return SourceName }

func (AdminEvent) EventKind() string { return AdminEventKind }
func (AdminEvent) Source() string    { return SourceName }

// Compile-time checks that each event type satisfies consumer.Event.
var (
	_ consumer.Event = NewAuctionEvent{}
	_ consumer.Event = FillAuctionEvent{}
	_ consumer.Event = DeleteAuctionEvent{}
	_ consumer.Event = PositionEvent{}
	_ consumer.Event = EmissionEvent{}
	_ consumer.Event = AdminEvent{}
)
