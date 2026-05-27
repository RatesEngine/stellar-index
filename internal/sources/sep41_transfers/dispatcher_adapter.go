package sep41_transfers

import (
	"errors"
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// Decoder is the dispatcher-facing audit-trail observer for SEP-41
// (F-0021 closure).
type Decoder struct {
	watched map[string]struct{}
}

var ErrEmptyWatchSet = errors.New("sep41_transfers: cannot construct Decoder with empty watched-contract list")

// NewDecoder constructs a Decoder watching the supplied SEP-41
// contract C-strkey list. Empty input rejected as a config error.
func NewDecoder(watched []string) (*Decoder, error) {
	if len(watched) == 0 {
		return nil, ErrEmptyWatchSet
	}
	set := make(map[string]struct{}, len(watched))
	for _, c := range watched {
		if c == "" {
			return nil, errors.New("sep41_transfers: empty contract id in watched list")
		}
		set[c] = struct{}{}
	}
	return &Decoder{watched: set}, nil
}

func (*Decoder) Name() string { return SourceName }

// Matches returns true when topic[0] is one of transfer / approve
// / set_admin / set_authorized on a watched contract. mint/burn/
// clawback belong to sep41_supply and are skipped here so the
// two observers don't double-process.
func (d *Decoder) Matches(ev events.Event) bool {
	if ev.Type != "contract" {
		return false
	}
	if _, watched := d.watched[ev.ContractID]; !watched {
		return false
	}
	return classify(&ev) != ""
}

//nolint:funlen,gocyclo // dispatch table; one branch per kind.
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	kind := classify(&ev)
	if kind == "" {
		return nil, fmt.Errorf("%w: topic[0]=%q", ErrUnknownSymbol, firstTopic(&ev))
	}
	closedAt, err := ev.EventClosedAt()
	if err != nil {
		return nil, fmt.Errorf("sep41_transfers: parse closed-at: %w", err)
	}
	out := Event{
		ContractID: ev.ContractID,
		Ledger:     ev.Ledger,
		TxHash:     ev.TxHash,
		OpIndex:    uint32(ev.OperationIndex), //nolint:gosec // non-negative by Soroban spec.
		ObservedAt: closedAt,
		Kind:       kind,
	}
	switch kind {
	case SymbolTransfer:
		from, to, amount, derr := decodeTransfer(&ev)
		if derr != nil {
			return nil, derr
		}
		out.FromAddr = from
		out.ToAddr = to
		out.Amount = amount
	case SymbolApprove:
		from, spender, amount, liveUntil, derr := decodeApprove(&ev)
		if derr != nil {
			return nil, derr
		}
		out.FromAddr = from
		out.ToAddr = spender
		out.Amount = amount
		out.LiveUntilLedger = liveUntil
	case SymbolSetAdmin:
		admin, newAdmin, derr := decodeSetAdmin(&ev)
		if derr != nil {
			return nil, derr
		}
		out.FromAddr = admin
		out.ToAddr = newAdmin
	case SymbolSetAuthorized:
		id, authorized, derr := decodeSetAuthorized(&ev)
		if derr != nil {
			return nil, derr
		}
		out.ToAddr = id
		b := authorized
		out.Authorized = &b
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownSymbol, kind)
	}
	return []consumer.Event{out}, nil
}

func firstTopic(e *events.Event) string {
	if len(e.Topic) == 0 {
		return "<empty>"
	}
	return e.Topic[0]
}
