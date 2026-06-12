package reflector

import (
	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// Decoder is the dispatcher-facing view of one Reflector oracle
// variant (DEX / CEX / FX). Each binary registers three Decoders
// — one per variant — scoped to their respective mainnet contract
// addresses.
//
// Decoder has no goroutines, no RPC clients, no polling. All state
// needed to decode a Reflector update lives in the event itself
// (per docs/architecture/ingest-pipeline.md). Per-entry decode
// errors (unknown crypto ticker, malformed payload) are surfaced
// to the dispatcher as non-fatal returns; the dispatcher counts
// them and continues.
type Decoder struct {
	variant    Variant
	contractID string
	decimals   uint8
	observer   string
}

// NewDecoder constructs a Reflector Decoder bound to one mainnet
// oracle variant. contractID scopes the matcher so a DEX Decoder
// won't try to decode a CEX event (fiat vs crypto assets), even
// though they share the REFLECTOR:update topic.
func NewDecoder(variant Variant, contractID string, opts ...DecoderOption) *Decoder {
	d := &Decoder{
		variant:    variant,
		contractID: contractID,
		decimals:   DefaultDecimals,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// DecoderOption configures a Decoder at construction.
type DecoderOption func(*Decoder)

// WithDecoderDecimals overrides the default 14 if an operator knows
// a specific contract's scale differs.
func WithDecoderDecimals(d uint8) DecoderOption {
	return func(r *Decoder) { r.decimals = d }
}

// WithDecoderObserver stamps an observer strkey on every emitted
// OracleUpdate. The observer is typically the tx source account
// (the relayer); when left blank the update's Observer column
// stays empty, which is a valid OracleUpdate.
func WithDecoderObserver(obs string) DecoderOption {
	return func(r *Decoder) { r.observer = obs }
}

// Name implements [dispatcher.Decoder].
func (d *Decoder) Name() string { return d.variant.SourceName() }

// Matches implements [dispatcher.Decoder]. Returns true for events
// from this Decoder's contract whose topic[0..1] bytes are
// Symbol("REFLECTOR"), Symbol("update"). Topic[2] (timestamp) is
// not inspected here — classify() handles the arity check once
// we're routing through Decode.
func (d *Decoder) Matches(ev events.Event) bool {
	if ev.ContractID != d.contractID {
		return false
	}
	return classify(&ev)
}

// Decode implements [dispatcher.Decoder]. Returns zero or more
// UpdateEvent wrappers — one per (asset, price) entry in the
// event's update_data vector, after zero-price and unknown-symbol
// entries are skipped. See decodeUpdate for details.
func (d *Decoder) Decode(ev events.Event) ([]consumer.Event, error) {
	closedAt, err := ev.EventClosedAt()
	if err != nil {
		// Fail closed rather than substituting time.Now(): closedAt
		// is the fallback decodeUpdate uses when the topic[2] oracle
		// timestamp is missing / out of its sanity window, so a
		// wall-clock value here would mis-timestamp the row during a
		// backfill replay (cf. the comet sibling).
		return nil, err
	}
	updates, err := decodeUpdate(&ev, d.variant, d.decimals, d.observer, closedAt)
	if err != nil {
		return nil, err
	}
	out := make([]consumer.Event, 0, len(updates))
	for _, u := range updates {
		out = append(out, UpdateEvent{Update: u})
	}
	return out, nil
}
