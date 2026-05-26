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

// ─── Liquidity actions ──────────────────────────────────────────
//
// Phoenix's pool contract (both volatile `contracts/pool/` and
// stableswap `contracts/pool_stable/`) emits the same N-event-per-
// action shape as `swap` for liquidity management:
//
//	provide_liquidity (5 events): sender, token_a, token_a-amount,
//	                              token_b, token_b-amount
//	withdraw_liquidity (4 events): sender, shares_amount,
//	                               return_amount_a, return_amount_b
//
// The withdraw path also OPTIONALLY emits a 5th
// `("withdraw_liquidity", "auto unbonded")` event with a tuple body
// (stake_amount, stake_timestamp). We classify it but do not require
// it for the withdraw correlation to complete — most withdrawals
// don't auto-unbond.
//
// Stake contract (`contracts/stake/`) emits its own 3-event-per-
// action shape for bond/unbond:
//
//	bond   (3 events): user, token, amount
//	unbond (3 events): user, token, amount
//
// Field strings are the literal contract source — keep spellings
// identical, including the `-amount` hyphens on the liquidity-token
// fields. The contract emits all topics as String (not Symbol):
// soroban-sdk serialises tuple-literal strings as ScVal::String.

const (
	EventActionProvideLiquidity  = "provide_liquidity"
	EventActionWithdrawLiquidity = "withdraw_liquidity"
	EventActionBond              = "bond"
	EventActionUnbond            = "unbond"
)

// Field names for `provide_liquidity` (5 events per call).
// The `token_a-amount` / `token_b-amount` hyphens come from the
// contract source — see contracts/pool/src/contract.rs:346-355.
const (
	FieldPLSender              = "sender"
	FieldPLTokenA              = "token_a"
	FieldPLTokenAAmt           = "token_a-amount"
	FieldPLTokenB              = "token_b"
	FieldPLTokenBAmt           = "token_b-amount"
	ProvideLiquidityFieldCount = 5
)

// Field names for `withdraw_liquidity` (4 events per call, plus the
// optional `auto unbonded` 5th — see [FieldWLAutoUnbonded]).
const (
	FieldWLSender               = "sender"
	FieldWLSharesAmount         = "shares_amount"
	FieldWLReturnAmountA        = "return_amount_a"
	FieldWLReturnAmountB        = "return_amount_b"
	FieldWLAutoUnbonded         = "auto unbonded" // optional — emitted only when withdrawing also unbonds
	WithdrawLiquidityFieldCount = 4
)

// Field names for `bond` / `unbond` (3 events per call, same shape
// for both actions — see contracts/stake/src/contract.rs:165-167
// and 196-198).
const (
	FieldStakeUser   = "user"
	FieldStakeToken  = "token"
	FieldStakeAmount = "amount"
	StakeFieldCount  = 3
)

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

// Liquidity-management topic[0] encodings + topic[1] field names.
// Same ScString-discriminator reasoning as swap above: contracts
// publish via tuple-literals like
// `.publish(("provide_liquidity", "sender"), …)` so both slots
// serialise as ScVal::String.
var (
	TopicSymbolProvideLiquidity  = scval.MustEncodeString(EventActionProvideLiquidity)  // topic[0]
	TopicSymbolWithdrawLiquidity = scval.MustEncodeString(EventActionWithdrawLiquidity) // topic[0]
	TopicSymbolBond              = scval.MustEncodeString(EventActionBond)              // topic[0]
	TopicSymbolUnbond            = scval.MustEncodeString(EventActionUnbond)            // topic[0]

	// provide_liquidity topic[1] variants.
	TopicSymbolPLSender    = scval.MustEncodeString(FieldPLSender)
	TopicSymbolPLTokenA    = scval.MustEncodeString(FieldPLTokenA)
	TopicSymbolPLTokenAAmt = scval.MustEncodeString(FieldPLTokenAAmt)
	TopicSymbolPLTokenB    = scval.MustEncodeString(FieldPLTokenB)
	TopicSymbolPLTokenBAmt = scval.MustEncodeString(FieldPLTokenBAmt)

	// withdraw_liquidity topic[1] variants (4 required + 1 optional).
	TopicSymbolWLSender        = scval.MustEncodeString(FieldWLSender)
	TopicSymbolWLSharesAmount  = scval.MustEncodeString(FieldWLSharesAmount)
	TopicSymbolWLReturnAmountA = scval.MustEncodeString(FieldWLReturnAmountA)
	TopicSymbolWLReturnAmountB = scval.MustEncodeString(FieldWLReturnAmountB)
	TopicSymbolWLAutoUnbonded  = scval.MustEncodeString(FieldWLAutoUnbonded)

	// bond / unbond topic[1] variants (shared field set).
	TopicSymbolStakeUser   = scval.MustEncodeString(FieldStakeUser)
	TopicSymbolStakeToken  = scval.MustEncodeString(FieldStakeToken)
	TopicSymbolStakeAmount = scval.MustEncodeString(FieldStakeAmount)
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

	// ErrIncompleteLiquidity — bubbles up if decodeProvideLiquidity /
	// decodeWithdrawLiquidity is called before every required field
	// has landed. Defence-in-depth: the buffer only returns completed
	// records, so callers shouldn't see this in normal flow.
	ErrIncompleteLiquidity = errors.New("phoenix: incomplete liquidity event")

	// ErrIncompleteStake — same shape as ErrIncompleteLiquidity, for
	// the bond / unbond 3-event reassembly.
	ErrIncompleteStake = errors.New("phoenix: incomplete stake event")
)
