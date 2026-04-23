// Package reflector ingests oracle updates from the three Reflector
// contracts (DEX / CEX / FX) — a SEP-40 oracle network native to
// Stellar / Soroban.
//
// Design reference: internal/sources/reflector/README.md and
// docs/discovery/oracles/reflector.md. Read the Q1–Q5 quirks first
// before changing the decoder.
package reflector

import "errors"

// Source name constants — one per Reflector contract variant.
// Appear in metrics labels + canonical.OracleUpdate.Source.
const (
	SourceDEX = "reflector-dex"
	SourceCEX = "reflector-cex"
	SourceFX  = "reflector-fx"
)

// Variant identifies which of the three Reflector contracts a
// Source instance targets. Controls the SourceName it stamps on
// emitted updates.
type Variant uint8

const (
	VariantDEX Variant = iota + 1
	VariantCEX
	VariantFX
)

func (v Variant) SourceName() string {
	switch v {
	case VariantDEX:
		return SourceDEX
	case VariantCEX:
		return SourceCEX
	case VariantFX:
		return SourceFX
	default:
		return "reflector-unknown"
	}
}

// DefaultDecimals is the canonical Reflector price scale (verified
// from `reflector-contract/pulse-contract/src/lib.rs` during
// Phase-1 audit). Individual contracts technically publish their
// own `decimals()` SEP-40 method; the consumer can override via
// Option but this is the safe default.
const DefaultDecimals uint8 = 14

// DefaultResolutionSeconds is the uniform 5-min cadence every
// Reflector contract updates on (Q3). Emitted as the
// `ratesengine_oracle_resolution_seconds` gauge so the oracle-stale
// alert has a threshold.
const DefaultResolutionSeconds = 300

// Event-topic constants. Verified from
// `reflector-contract/oracle/src/events.rs`:
//
//	topic:  ["REFLECTOR", "update"]     both Symbols
//	body:   Map{"prices": Vec<(Asset, i128)>, "timestamp": u64}
const (
	EventTopic0 = "REFLECTOR"
	EventTopic1 = "update"
)

// Pre-encoded base64 SCVal::Symbol placeholders. Same pattern as
// the DEX connectors until the SDK-backed XDR encoder lands.
// Uniqueness enforced by Go's switch-with-duplicate-case rule.
const (
	TopicSymbolReflector = "PLACEHOLDER_REFLECTOR_TOPIC_REFLECTOR" // topic[0]
	TopicSymbolUpdate    = "PLACEHOLDER_REFLECTOR_TOPIC_UPDATE"    // topic[1]
)

// Errors returned by the decode path.
var (
	// ErrNotReflectorEvent — topic[0..1] doesn't match REFLECTOR +
	// update. Non-Reflector contract event; skip.
	ErrNotReflectorEvent = errors.New("reflector: not a REFLECTOR.update event")

	// ErrMalformedPayload — event body doesn't decode to the
	// expected Map{prices, timestamp} shape.
	ErrMalformedPayload = errors.New("reflector: malformed event payload")

	// ErrEmptyPrices — prices vector was empty. Reflector should
	// never emit this (5-min cadence implies always at least one
	// price), but guard against it defensively.
	ErrEmptyPrices = errors.New("reflector: empty prices vector")

	// ErrPriceVectorOverflow — prices vector size exceeded the
	// op-index fanout stride (opIndexFanoutStride = 1024). If this
	// ever happens the fanned-out OpIndex values would spill into
	// the next operation's synthetic range and collide on the
	// oracle_updates hypertable's (source, ledger, tx_hash,
	// op_index, ts) primary key. Refusing the event loudly is
	// safer than silently writing colliding rows — observed max in
	// the wild is ~50 assets/update, so hitting 1024 means either
	// a feed explosion or a decoder bug.
	ErrPriceVectorOverflow = errors.New("reflector: price vector exceeds OpIndex fanout stride")
)
