// Package reflector ingests oracle updates from the three Reflector
// contracts (DEX / CEX / FX) — a SEP-40 oracle network native to
// Stellar / Soroban.
//
// Design reference: internal/sources/reflector/README.md and
// docs/discovery/oracles/reflector.md. Read the Q1–Q5 quirks first
// before changing the decoder.
package reflector

import (
	"errors"

	"github.com/RatesEngine/rates-engine/internal/scval"
)

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
// `ratesengine_oracle_resolution_seconds` gauge by
// [pipeline.BuildDispatcher] at registration time, so the
// oracle-stale alert has a per-source threshold.
const DefaultResolutionSeconds = 300

// Event-topic constants. Re-verified 2026-04-23 against
// `reflector-contract/oracle/src/events.rs:4-10` — soroban-sdk 25.3.0.
//
// The contract definition is:
//
//	#[contractevent(topics = ["REFLECTOR", "update"])]
//	pub struct UpdateEvent {
//	    #[topic] timestamp: u64,           // <-- topic[2], NOT in body
//	    update_data: Vec<(Val, i128)>,     // Val is Address | Symbol
//	}
//
// So the on-wire shape is:
//
//	topic[0] = Symbol("REFLECTOR")
//	topic[1] = Symbol("update")
//	topic[2] = U64(timestamp)
//	body     = Vec<(ScVal, I128)>  (per contractevent macro expansion)
//
// The previous comment here claimed body was
// `Map{"prices": Vec<(Asset, i128)>, "timestamp": u64}` — that is
// wrong; the Phase-1 decoder PR (#164a) must match the shape above
// against real fixtures captured from mainnet. See
// docs/architecture/contract-schema-evolution.md for why.
const (
	EventTopic0 = "REFLECTOR"
	EventTopic1 = "update"
)

// Pre-encoded base64 SCVal::Symbol blobs — produced at init via
// scval.MustEncodeSymbol and used for byte-equality matching against
// Event.Topic entries (and passed directly to stellar-rpc's
// getEvents topic filter). Regenerated from [EventTopic0]/
// [EventTopic1] at init to keep the source of truth in one place.
//
// Golden regression in internal/scval/scval_test.go
// (TestGolden_symbolBytes) pins the exact base64 output of
// EncodeSymbol("REFLECTOR") and EncodeSymbol("update") — if an SDK
// upgrade shifts the wire encoding, that test fires before this
// package ships.
var (
	TopicSymbolReflector = scval.MustEncodeSymbol(EventTopic0) // topic[0]
	TopicSymbolUpdate    = scval.MustEncodeSymbol(EventTopic1) // topic[1]
)

// Errors returned by the decode path.
var (
	// ErrNotReflectorEvent — topic[0..1] doesn't match REFLECTOR +
	// update. Non-Reflector contract event; skip.
	ErrNotReflectorEvent = errors.New("reflector: not a REFLECTOR.update event")

	// ErrMalformedPayload — event body doesn't decode to the
	// expected Map{"update_data": Vec<(Val, i128)>} shape (the
	// timestamp lives in topic[2], not the body).
	ErrMalformedPayload = errors.New("reflector: malformed event payload")

	// ErrEmptyPrices — prices vector was empty. Reflector should
	// never emit this (5-min cadence implies always at least one
	// price), but guard against it defensively.
	ErrEmptyPrices = errors.New("reflector: empty prices vector")

	// ErrUnknownSymbol — the asset slot of an update_data entry
	// was a Symbol (Asset::Other variant) whose string matched
	// neither the ADR-0010 fiat allow-list nor the ADR-0014 crypto
	// allow-list. Operators extend these lists deliberately; the
	// decoder skips unknown symbols per-entry (other prices in the
	// same event still land) and the orchestrator's
	// SourceDecodeErrorsTotal counter surfaces sustained rates.
	//
	// Renamed 2026-04-23 (was ErrUnknownFiatSymbol) — PR 164e
	// added crypto-ticker support, so "unknown-fiat" is no longer
	// the only reason a symbol gets skipped.
	ErrUnknownSymbol = errors.New("reflector: asset symbol not in fiat or crypto allow-list")

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
