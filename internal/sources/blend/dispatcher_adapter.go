package blend

import (
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/sources/childgate"
)

// Decoder is the dispatcher-facing view of Blend (ADR-0035
// factory-anchored gating). Blend has a clean factory model: every
// business event (money-market / credit-risk / auction / admin) is
// emitted by a POOL contract, and the only event the Pool Factory V2
// emits is `deploy` — which announces each new pool. We anchor on the
// factory (MainnetPoolFactory) and fan out to the pools it deploys.
//
// The registry of factory-deployed pools is the load-bearing state. It's
// seeded live (every `deploy` we decode calls reg.Seed), DB-warmed at
// boot (the `protocol_contracts` table → childgate.WithSeed), and
// genesis-seeded by walking the factory's `deploy` events from the lake
// (`ratesengine-ops seed-protocol-contracts -source blend`, and the
// ADR-0033 reconcile pre-seed). See docs/discovery/dexes-amms/blend.md
// (Pool Factory V2 = CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU).
type Decoder struct {
	reg *childgate.Registry
}

// NewDecoder constructs a Blend Decoder. The options seed and persist the
// factory-deployed-pool registry (childgate.WithSeed / WithHook); with no
// options the registry is empty and only live `deploy` events populate it
// (correct for a from-genesis stream, insufficient for an incremental
// restart — hence the DB warm in production wiring).
//
// Future per-WASM-hash dispatch (per
// docs/architecture/contract-schema-evolution.md) would add a version
// selector but the event surface is currently covered by a single
// contract version (V2).
func NewDecoder(opts ...childgate.Option) *Decoder {
	// The factory trust-root set is intrinsic to the protocol (verified,
	// hard-coded), so it's always installed first; caller opts (WithSeed /
	// WithHook) layer the discovered children + persistence on top.
	base := []childgate.Option{childgate.WithFactories(MainnetPoolFactories)}
	return &Decoder{reg: childgate.New(append(base, opts...)...)}
}

// Name implements [dispatcher.Decoder].
func (*Decoder) Name() string { return SourceName }

// Matches implements [dispatcher.Decoder]. Gates on CONTRACT IDENTITY,
// not topic symbol (ADR-0035, F-1347): a non-Blend contract that emits a
// `supply`/`claim`/`set_admin`/… topic (SACs and other DeFi do) must NOT
// be attributed to Blend.
//
//   - `deploy` matches ONLY when emitted by one of the canonical Pool
//     Factories (MainnetPoolFactories — Blend has MORE THAN ONE; see that
//     var). This is the trust root — without it a foreign contract could
//     inject a pool into the registry and launder its own events as
//     Blend's; with only ONE of the factories it would silently drop the
//     other factory's pools.
//   - every other event matches ONLY when emitted by a REGISTERED pool
//     (a factory descendant). The registry is seeded from factory deploy
//     events (live + DB warm + genesis walk), so a real pool is always
//     present before its business events are processed.
//
// COVERAGE NOTE (ADR-0035): an un-seeded real pool would have its events
// dropped, so registry completeness is a hard requirement. It is
// guaranteed by the factory `deploy` events themselves living in the lake
// (substrate-continuous per ADR-0033 Claim 1) — a missing pool would mean
// a missing factory event, which continuity already rules out — AND by
// MainnetPoolFactories being the complete factory set (empirically
// verified; an undocumented factory is the only residual risk, mitigated
// by re-running the deploy-graph enumeration).
func (d *Decoder) Matches(ev events.Event) bool {
	kind := classifyAny(&ev)
	if kind == "" {
		return false
	}
	if kind == EventDeploy {
		return d.reg.IsFactory(ev.ContractID)
	}
	return d.reg.Has(ev.ContractID)
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
			// Fan-out (ADR-0035): a factory `deploy` announces a new
			// pool. Register it so its subsequent business events pass
			// the Matches gate. Seed fires the persistence hook, so the
			// mapping survives a restart even after the projector cursor
			// advances past this deploy ledger. Matches() already
			// guaranteed this deploy came from one of MainnetPoolFactories
			// (e.ContractID), so the Target is a genuine Blend pool and
			// e.ContractID is the deploying factory (provenance).
			if e.Kind == EventDeploy && e.Target != "" {
				d.reg.Seed(e.Target, e.ContractID, ev.Ledger)
			}
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
