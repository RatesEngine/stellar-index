// Package phoenix ingests trade events from the Phoenix Soroban DEX.
//
// Design reference: internal/sources/phoenix/README.md and
// docs/discovery/dexes-amms/phoenix.md. Read the Q1–Q5 quirks
// before modifying the decoder, especially the 8-events-per-swap
// correlation (Q1).
package phoenix

import (
	"errors"

	"github.com/RatesEngine/rates-engine/internal/scval"
)

// SourceName — stable identifier.
const SourceName = "phoenix"

// Phoenix emits a constant-product swap as 8 distinct events, each
// carrying a single field value. These constants name the fields
// exactly as they appear in contracts/pool/src/contract.rs:1172-1185.
// The string spelling MATTERS — "actual received amount" has
// embedded spaces (Q2), which means it CAN'T be encoded as an
// ScvSymbol (identifier-only) — soroban-sdk emits it as ScvString
// instead. Verified 2026-04-23 against mainnet: every Phoenix swap
// topic slot is ScvString, not ScvSymbol.
const (
	FieldSender         = "sender"
	FieldSellToken      = "sell_token"
	FieldOfferAmount    = "offer_amount"
	FieldActualReceived = "actual received amount" // note the spaces (Q2)
	FieldBuyToken       = "buy_token"
	FieldReturnAmount   = "return_amount"
	FieldSpreadAmount   = "spread_amount"
	FieldReferralFee    = "referral_fee_amount"
)

// SwapFieldCount is the number of distinct events per swap (Q1).
// A trade is ready to emit only when all 8 slots of the RawSwap
// are populated.
const SwapFieldCount = 8

// EventActionSwap — the value of topic[0] for every swap-field
// event. topic[1] carries the per-field name.
const EventActionSwap = "swap"

// Mainnet contract addresses — Phase-1 verified against
// Phoenix-Protocol-Group/phoenix-contracts `scripts/*.sh`.
const (
	MainnetFactory  = "CB4SVAWJA6TSRNOJZ7W2AWFW46D5VR4ZMFZKDIKXEINZCZEGZCJZCKMI"
	MainnetMultihop = "CCLZRD4E72T7JCZCN3P7KNPYNXFYKQCL64ECLX7WP5GNVYPYJGU2IO2G"

	// XLM SAC as referenced by Phoenix's scripts. Note this is
	// NOT the same address as Aquarius's XLM SAC — Phoenix uses
	// a different canonical form in its deploy scripts.
	MainnetXLMSAC = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"
)

// Pre-encoded base64 SCVal::String blobs for topic[0] and topic[1],
// computed at init via scval.MustEncodeString. Phoenix emits both
// topic positions as Strings (not Symbols) because the pool contract
// publishes `(str_literal, str_literal)` tuples — soroban-sdk
// serializes string literals as ScvString. Verified against real
// mainnet capture 2026-04-23.
var (
	TopicSymbolSwap = scval.MustEncodeString(EventActionSwap) // topic[0]

	TopicSymbolSender         = scval.MustEncodeString(FieldSender)         // topic[1] variants
	TopicSymbolSellToken      = scval.MustEncodeString(FieldSellToken)      //
	TopicSymbolOfferAmount    = scval.MustEncodeString(FieldOfferAmount)    //
	TopicSymbolActualReceived = scval.MustEncodeString(FieldActualReceived) //
	TopicSymbolBuyToken       = scval.MustEncodeString(FieldBuyToken)       //
	TopicSymbolReturnAmount   = scval.MustEncodeString(FieldReturnAmount)   //
	TopicSymbolSpreadAmount   = scval.MustEncodeString(FieldSpreadAmount)   //
	TopicSymbolReferralFee    = scval.MustEncodeString(FieldReferralFee)    //
)

// Errors returned by the decode path.
var (
	// ErrUnknownField — topic[1] didn't match any of the 8 expected
	// field names. Usually means a non-swap event (e.g. deposit,
	// withdraw) — classified as "not our problem" and skipped.
	ErrUnknownField = errors.New("phoenix: unknown swap field")

	// ErrIncompleteSwap — fewer than 8 fields populated when asked
	// to finalise. Should never bubble up in normal flow; buffer
	// only returns complete RawSwaps.
	ErrIncompleteSwap = errors.New("phoenix: incomplete swap (need 8 fields)")

	// ErrMalformedPayload — field values don't match expected types
	// or produce a nonsense Trade (zero amount, same base/quote).
	ErrMalformedPayload = errors.New("phoenix: malformed swap payload")
)
