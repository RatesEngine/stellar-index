package cctp

import (
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
)

// Event is the [consumer.Event] the CCTP Decoder emits — one per
// decoded contract event. It carries the cctp_events row shape
// (migration 0038): the universal identity fields, the promoted
// typed columns (Amount / Fee / Token / CounterpartyDomain), and the
// event-type-specific remainder in Attributes.
//
// Amount / Fee / Token are decimal-or-strkey strings; an empty value
// means "this event type carries no such field" and the sink writes
// SQL NULL. CounterpartyDomain is a *uint32 for the same reason —
// message_sent and mint_and_withdraw carry no domain.
//
// The indexer's event sink type-switches on this at its output
// channel (internal/pipeline/sink.go) and writes via
// Store.InsertCCTPEvent.
type Event struct {
	ContractID         string
	Ledger             uint32
	TxHash             string
	OpIndex            int
	ObservedAt         time.Time
	EventType          string // one of the Event* constants
	Amount             string // decimal i128; "" → none
	Fee                string // decimal i128; "" → none
	Token              string // Stellar Address strkey; "" → none
	CounterpartyDomain *uint32
	Attributes         map[string]any
}

// EventKind implements [consumer.Event].
func (Event) EventKind() string { return "cctp.event" }

// Source implements [consumer.Event] — matches [SourceName].
func (Event) Source() string { return SourceName }

// Compile-time check that Event satisfies consumer.Event.
var _ consumer.Event = Event{}

// eventFromDepositForBurn projects a decoded DepositForBurn into the
// canonical row Event. burn_token / max_fee / destination_domain are
// promoted; the BytesN<32> hex fields + topics land in Attributes.
func eventFromDepositForBurn(d DepositForBurn, observedAt time.Time) Event {
	domain := d.DestinationDomain
	return Event{
		ContractID:         d.ContractID,
		Ledger:             d.Ledger,
		TxHash:             d.TxHash,
		OpIndex:            d.OpIndex,
		ObservedAt:         observedAt,
		EventType:          EventDepositForBurn,
		Amount:             d.Amount,
		Fee:                d.MaxFee,
		Token:              d.BurnToken,
		CounterpartyDomain: &domain,
		Attributes: map[string]any{
			"depositor":                   d.Depositor,
			"mint_recipient":              d.MintRecipient,
			"destination_token_messenger": d.DestinationTokenMessenger,
			"destination_caller":          d.DestinationCaller,
			"min_finality_threshold":      d.MinFinalityThreshold,
			"hook_data":                   d.HookData,
		},
	}
}

// eventFromMintAndWithdraw projects a decoded MintAndWithdraw. It
// carries no destination/source domain, so CounterpartyDomain is nil.
func eventFromMintAndWithdraw(m MintAndWithdraw, observedAt time.Time) Event {
	return Event{
		ContractID: m.ContractID,
		Ledger:     m.Ledger,
		TxHash:     m.TxHash,
		OpIndex:    m.OpIndex,
		ObservedAt: observedAt,
		EventType:  EventMintAndWithdraw,
		Amount:     m.Amount,
		Fee:        m.FeeCollected,
		Token:      m.MintToken,
		Attributes: map[string]any{
			"mint_recipient": m.MintRecipient,
		},
	}
}

// eventFromMessageSent projects a decoded MessageSent — a wire
// envelope with no amount, token or domain.
func eventFromMessageSent(s MessageSent, observedAt time.Time) Event {
	return Event{
		ContractID: s.ContractID,
		Ledger:     s.Ledger,
		TxHash:     s.TxHash,
		OpIndex:    s.OpIndex,
		ObservedAt: observedAt,
		EventType:  EventMessageSent,
		Attributes: map[string]any{
			"message": s.Message,
		},
	}
}

// eventFromMessageReceived projects a decoded MessageReceived. The
// source_domain is promoted to CounterpartyDomain.
func eventFromMessageReceived(r MessageReceived, observedAt time.Time) Event {
	domain := r.SourceDomain
	return Event{
		ContractID:         r.ContractID,
		Ledger:             r.Ledger,
		TxHash:             r.TxHash,
		OpIndex:            r.OpIndex,
		ObservedAt:         observedAt,
		EventType:          EventMessageReceived,
		CounterpartyDomain: &domain,
		Attributes: map[string]any{
			"caller":                      r.Caller,
			"nonce":                       r.Nonce,
			"finality_threshold_executed": r.FinalityThresholdExecuted,
			"sender":                      r.Sender,
			"message_body":                r.MessageBody,
		},
	}
}
