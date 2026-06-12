package blend

import (
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder is the dispatcher-facing view of Blend. Per
// docs/discovery/dexes-amms/blend.md: every Blend pool emits all
// its events under the same per-pool contract, so topic[0] is the
// only classifier we need. There's no per-source state.
//
// The pool-factory's `deploy` event (which announces new pool
// instances) is decoded by a separate factory adapter — kept apart
// from this Decoder because it has a different downstream
// consumer (pool registry, not the auction store). That landing
// happens in Task #45 (factory walk + audit).
type Decoder struct{}

// NewDecoder constructs a Blend Decoder. Stateless; takes no
// arguments. Future per-WASM-hash dispatch (per
// docs/architecture/contract-schema-evolution.md) would add a
// version selector but the auction event surface is currently
// covered by a single contract version (V2).
func NewDecoder() *Decoder { return &Decoder{} }

// Name implements [dispatcher.Decoder].
func (*Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Returns true for every
// Blend pool / pool-factory event the decoder handles — the three
// auction events PLUS the 20 money-market / emission / credit-risk /
// admin / pool-factory events covered by #25 (see classifyAny's
// switch in decode_money_market.go). classifyAny() returns the
// canonical Event* name for any matched topic[0], so a non-empty
// classification is the match.
//
// The `deploy` event is included — it's emitted by the pool-factory
// contract (a different ContractID than the pool emitters), but
// the dispatcher's contract-id filtering is done by topic byte-
// equality, not by contract address (consistent with how Comet's
// shared `POOL` topic is handled per CLAUDE.md). Pool-vs-factory
// distinction lands at the storage layer (different table).
func (*Decoder) Matches(ev events.Event) bool {
	return classifyAny(&ev) != ""
}

// Decode implements [dispatcher.Decoder]. Returns one consumer.Event
// per successful decode. Body shape varies per event; the kind is
// preserved in the returned struct's [Event.EventKind] string so
// the sink can demultiplex.
//
// The three auction events return the legacy NewAuctionEvent /
// FillAuctionEvent / DeleteAuctionEvent structs (sink-side
// blend_auctions table unchanged). The 18 money-market / emission
// / admin events return PositionEvent / EmissionEvent / AdminEvent
// — the sink writes them to blend_positions / blend_emissions /
// blend_admin via the migration-0042 schemas.
// Decode routes the event by kind (decodeByKind) and stamps EventIndex onto the
// position/emission/admin outputs — the per-event discriminator that
// distinguishes multiple same-kind events emitted in a single operation. Without
// it those rows collide on the blend_positions / blend_emissions / blend_admin
// primary key and all but one are silently dropped (the coarse-PK data-loss bug;
// emissions/admin fixed in migration 0053, positions in 0054 after (asset,user)
// proved insufficient for same-(asset,user,kind)-per-op events).
//
// The three auction events (new/fill/delete) carry EventIndex too — their
// decode functions set it directly from events.Event.EventIndex (blend_auctions
// PK, migration 0058 / F-1324), so the loop below only needs to fan it onto the
// non-auction structs whose decode helpers don't see the raw event.
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	outs, err := d.decodeByKind(ev)
	if err != nil {
		return nil, err
	}
	ei := uint32(ev.EventIndex) //nolint:gosec // event index is small, non-negative
	for i, o := range outs {
		switch e := o.(type) {
		case PositionEvent:
			e.EventIndex = ei
			outs[i] = e
		case EmissionEvent:
			e.EventIndex = ei
			outs[i] = e
		case AdminEvent:
			e.EventIndex = ei
			outs[i] = e
		}
	}
	return outs, nil
}

func (d *Decoder) decodeByKind(ev events.Event) ([]consumer.Event, error) { //nolint:gocyclo,gocognit,funlen,cyclop // one case per Blend event kind; flattening makes the dispatch table easier to audit against pool/src/events.rs.
	closedAt, err := ev.EventClosedAt()
	if err != nil {
		return nil, err
	}
	kind := classifyAny(&ev)
	switch kind {
	// ─── Auction events (legacy; blend_auctions table) ────────
	case EventNewAuction:
		out, err := decodeNewAuction(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventFillAuction:
		out, err := decodeFillAuction(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventDeleteAuction:
		out, err := decodeDeleteAuction(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil

	// ─── Money-market position events (blend_positions) ───────
	case EventSupply, EventWithdraw,
		EventSupplyCollateral, EventWithdrawCollateral,
		EventBorrow, EventRepay, EventFlashLoan:
		out, err := decodePositionEvent(&ev, kind, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil

	// ─── Emission / credit-risk events (blend_emissions) ──────
	case EventGulp:
		out, err := decodeGulp(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventClaim:
		out, err := decodeClaim(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventReserveEmissions:
		out, err := decodeReserveEmissionUpdate(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventGulpEmissions:
		out, err := decodeGulpEmissions(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventBadDebt:
		out, err := decodeBadDebt(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventDefaultedDebt:
		out, err := decodeDefaultedDebt(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil

	// ─── Admin / status / factory events (blend_admin) ────────
	case EventSetAdmin:
		out, err := decodeSetAdmin(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventUpdatePool:
		out, err := decodeUpdatePool(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventQueueSetReserve:
		out, err := decodeQueueSetReserve(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventCancelSetReserve:
		out, err := decodeCancelSetReserve(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventSetReserve:
		out, err := decodeSetReserve(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventSetStatus:
		out, err := decodeSetStatus(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil
	case EventDeploy:
		out, err := decodeDeploy(&ev, closedAt)
		if err != nil {
			return nil, err
		}
		return []consumer.Event{out}, nil

	default:
		return nil, fmt.Errorf("%w: topic[0]=%q", ErrNotBlendEvent, firstTopicHex(&ev))
	}
}

// firstTopicHex returns a short identifying string for the event's
// topic[0] when no decoder branch matched — used in error messages
// only. Empty topic / non-symbol topic fall through to a
// placeholder rather than failing the error format itself.
func firstTopicHex(e *events.Event) string {
	if len(e.Topic) == 0 {
		return "<empty>"
	}
	return e.Topic[0]
}
