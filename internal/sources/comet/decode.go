package comet

import (
	"fmt"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// classify reports the Comet event kind on a (POOL, <kind>) tuple,
// or empty string if the topic isn't a Comet POOL event we
// recognise. Returns one of EventSwap / EventJoinPool / EventExitPool
// / EventDeposit / EventWithdraw on success.
//
// Topic[0] == POOL is the namespace; topic[1] is the event name. The
// decoder runs this in the hot path of every Soroban event the
// dispatcher routes, so it's pure byte-equality against the
// pre-encoded TopicSymbol* blobs — no SCVal parsing.
func classify(e *events.Event) string {
	if len(e.Topic) < cometTopicArity {
		return ""
	}
	if e.Topic[0] != TopicSymbolPool {
		return ""
	}
	switch e.Topic[1] {
	case TopicSymbolSwap:
		return EventSwap
	case TopicSymbolJoinPool:
		return EventJoinPool
	case TopicSymbolExitPool:
		return EventExitPool
	case TopicSymbolDeposit:
		return EventDeposit
	case TopicSymbolWithdraw:
		return EventWithdraw
	}
	return ""
}

// classifySwap is the legacy swap-only predicate. Retained for the
// existing test pin set; new code uses `classify` and dispatches on
// the returned kind.
func classifySwap(e *events.Event) bool {
	return classify(e) == EventSwap
}

// decodeSwap converts one (POOL, swap) Comet event into a
// canonical.Trade. Unlike Soroswap (where token identities come from
// a factory's new_pair event), Comet's SwapEvent carries token_in
// and token_out as Addresses in the body itself — the decoder needs
// no pool registry.
//
// SwapEvent body (verified against
// comet-contracts-v1/contracts/src/c_pool/event.rs:6-13 +
// call_logic/pool.rs:184-191):
//
//	Map {
//	  "caller":           Address,
//	  "token_in":         Address,
//	  "token_out":        Address,
//	  "token_amount_in":  i128,
//	  "token_amount_out": i128,
//	}
//
// Trade direction: the trader sold token_in (into the pool) and
// bought token_out (out of the pool). So base = token_in, quote =
// token_out — mirrors the Aquarius convention where the "sold"
// side is the base.
func decodeSwap(e *events.Event, closedAt time.Time) (canonical.Trade, error) {
	if !classifySwap(e) {
		return canonical.Trade{}, ErrNotCometSwap
	}

	fields, err := decodeSwapBody(e.Value)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}
	if fields.AmountIn.Sign() <= 0 || fields.AmountOut.Sign() <= 0 {
		return canonical.Trade{}, fmt.Errorf("%w: in=%s out=%s",
			ErrNonPositiveAmounts, fields.AmountIn, fields.AmountOut)
	}

	baseAsset, err := canonical.NewSorobanAsset(fields.TokenIn)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: token_in: %w", ErrMalformedPayload, err)
	}
	quoteAsset, err := canonical.NewSorobanAsset(fields.TokenOut)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("%w: token_out: %w", ErrMalformedPayload, err)
	}
	pair, err := canonical.NewPair(baseAsset, quoteAsset)
	if err != nil {
		return canonical.Trade{}, fmt.Errorf("pair: %w", err)
	}

	return canonical.Trade{
		Source: SourceName,
		Ledger: e.Ledger,
		TxHash: e.TxHash,
		// Fan out by event index so multiple swaps in one op don't
		// collide on the trades PK (ADR-0033, same as aquarius).
		OpIndex:     canonical.FanoutOpIndex(e.OperationIndex, e.EventIndex),
		Timestamp:   closedAt,
		Pair:        pair,
		BaseAmount:  fields.AmountIn,
		QuoteAmount: fields.AmountOut,
		Taker:       fields.Caller,
	}, nil
}

// swapFields holds the five addressable fields from a Comet
// SwapEvent body.
type swapFields struct {
	Caller    string
	TokenIn   string // strkey
	TokenOut  string // strkey
	AmountIn  canonical.Amount
	AmountOut canonical.Amount
}

// liquidityFields holds the body fields shared by the four
// liquidity-mutating events. Not every field is present on every
// kind — see the per-kind decode functions for which fields each
// body actually carries. `PoolAmountIn` is withdraw-only; it stays
// zero (an empty Amount) on the other three kinds.
type liquidityFields struct {
	Caller       string
	Token        string // strkey — token_in for join/deposit, token_out for exit/withdraw
	Amount       canonical.Amount
	PoolAmountIn canonical.Amount // withdraw-only; BPT-share count burned
}

// decodeJoinPool converts one (POOL, join_pool) event body into a
// liquidityFields struct. Body shape from event.rs JoinEvent:
//
//	Map { caller: Address, token_in: Address, token_amount_in: i128 }
//
// One event per token on a multi-token join — an N-token pool join
// yields N events. The decoder treats each independently; grouping
// is a downstream concern (the comet_liquidity table includes
// (ledger, tx_hash) so a reserve-tracker can re-aggregate).
func decodeJoinPool(e *events.Event) (liquidityFields, error) {
	return decodeLiquiditySingleToken(e, "token_in", "token_amount_in")
}

// decodeExitPool converts one (POOL, exit_pool) event body. Body
// shape from event.rs ExitEvent:
//
//	Map { caller: Address, token_out: Address, token_amount_out: i128 }
//
// Like join_pool, multi-token exit emits one event per token.
func decodeExitPool(e *events.Event) (liquidityFields, error) {
	return decodeLiquiditySingleToken(e, "token_out", "token_amount_out")
}

// decodeDeposit converts one (POOL, deposit) event body. Body shape
// from event.rs DepositEvent (single-asset LP add):
//
//	Map { caller: Address, token_in: Address, token_amount_in: i128 }
//
// Identical wire shape to JoinEvent; the topic[1] is what
// distinguishes the kind for the row.
func decodeDeposit(e *events.Event) (liquidityFields, error) {
	return decodeLiquiditySingleToken(e, "token_in", "token_amount_in")
}

// decodeWithdraw converts one (POOL, withdraw) event body. Body
// shape from event.rs WithdrawEvent (single-asset LP remove):
//
//	Map { caller: Address, token_out: Address,
//	      token_amount_out: i128, pool_amount_in: i128 }
//
// Distinct from the other three: also carries `pool_amount_in`,
// the count of BPT (pool-share) tokens burned in exchange for the
// withdrawn underlying. Surfaced for downstream reserve-tracking
// + total-supply derivation.
func decodeWithdraw(e *events.Event) (liquidityFields, error) {
	fields, err := decodeLiquiditySingleToken(e, "token_out", "token_amount_out")
	if err != nil {
		return liquidityFields{}, err
	}
	// pool_amount_in is the BPT-share count burned. Parse and stash
	// on the same struct; the writer column is NULL on the other
	// kinds.
	body, err := scval.Parse(e.Value)
	if err != nil {
		return liquidityFields{}, fmt.Errorf("parse body: %w", err)
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return liquidityFields{}, fmt.Errorf("body not a Map: %w", err)
	}
	poolAmtSv, err := scval.MustMapField(entries, "pool_amount_in")
	if err != nil {
		return liquidityFields{}, fmt.Errorf("missing pool_amount_in: %w", err)
	}
	poolAmt, err := scval.AsAmountFromI128(poolAmtSv)
	if err != nil {
		return liquidityFields{}, fmt.Errorf("pool_amount_in: %w", err)
	}
	fields.PoolAmountIn = poolAmt
	return fields, nil
}

// decodeLiquiditySingleToken is the shared body decoder for
// (join_pool / exit_pool / deposit / withdraw) — every variant
// shares the (caller, <token field>, <amount field>) trio; only
// the field names differ. Decode-by-name per
// docs/architecture/contract-schema-evolution.md keeps us resilient
// to future contract upgrades that add new fields.
func decodeLiquiditySingleToken(e *events.Event, tokenField, amountField string) (liquidityFields, error) {
	body, err := scval.Parse(e.Value)
	if err != nil {
		return liquidityFields{}, fmt.Errorf("parse body: %w", err)
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return liquidityFields{}, fmt.Errorf("body not a Map: %w", err)
	}

	callerSv, err := scval.MustMapField(entries, "caller")
	if err != nil {
		return liquidityFields{}, fmt.Errorf("missing caller: %w", err)
	}
	caller, err := scval.AsAddressStrkey(callerSv)
	if err != nil {
		return liquidityFields{}, fmt.Errorf("caller: %w", err)
	}

	tokenSv, err := scval.MustMapField(entries, tokenField)
	if err != nil {
		return liquidityFields{}, fmt.Errorf("missing %s: %w", tokenField, err)
	}
	token, err := scval.AsAddressStrkey(tokenSv)
	if err != nil {
		return liquidityFields{}, fmt.Errorf("%s: %w", tokenField, err)
	}

	amountSv, err := scval.MustMapField(entries, amountField)
	if err != nil {
		return liquidityFields{}, fmt.Errorf("missing %s: %w", amountField, err)
	}
	amount, err := scval.AsAmountFromI128(amountSv)
	if err != nil {
		return liquidityFields{}, fmt.Errorf("%s: %w", amountField, err)
	}

	return liquidityFields{Caller: caller, Token: token, Amount: amount}, nil
}

// decodeLiquidityEvent dispatches one (POOL, <kind>) event into a
// typed LiquidityEvent row. Returns ErrNotCometEvent if the event
// isn't one of the four liquidity variants (swap is handled by
// decodeSwap upstream of this).
func decodeLiquidityEvent(e *events.Event, closedAt time.Time) (LiquidityEvent, error) {
	kind := classify(e)
	if kind == "" {
		return LiquidityEvent{}, ErrNotCometEvent
	}
	var (
		fields liquidityFields
		k      LiquidityKind
		err    error
	)
	switch kind {
	case EventJoinPool:
		k = LiquidityJoinPool
		fields, err = decodeJoinPool(e)
	case EventExitPool:
		k = LiquidityExitPool
		fields, err = decodeExitPool(e)
	case EventDeposit:
		k = LiquidityDeposit
		fields, err = decodeDeposit(e)
	case EventWithdraw:
		k = LiquidityWithdraw
		fields, err = decodeWithdraw(e)
	default:
		// swap goes through decodeSwap; anything else is not a
		// liquidity variant.
		return LiquidityEvent{}, ErrNotCometEvent
	}
	if err != nil {
		return LiquidityEvent{}, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}
	if fields.Amount.Sign() <= 0 {
		return LiquidityEvent{}, fmt.Errorf("%w: amount=%s", ErrNonPositiveAmounts, fields.Amount)
	}
	return LiquidityEvent{
		ContractID:   e.ContractID,
		Ledger:       e.Ledger,
		TxHash:       e.TxHash,
		OpIndex:      uint32(e.OperationIndex),
		EventIndex:   uint32(e.EventIndex), //nolint:gosec // EventIndex is non-negative by Soroban spec.
		ObservedAt:   closedAt,
		Kind:         k,
		Caller:       fields.Caller,
		Token:        fields.Token,
		Amount:       fields.Amount,
		PoolAmountIn: fields.PoolAmountIn,
	}, nil
}

// ─── SCVal decoders ─────────────────────────────────────────────
// Tests swap via the package-level var.

var decodeSwapBody = sdkDecodeSwapBody

// sdkDecodeSwapBody unpacks the SwapEvent map. Decode-by-name per
// docs/architecture/contract-schema-evolution.md — benign field
// additions in future WASM versions won't break us, unknown fields
// are ignored.
func sdkDecodeSwapBody(valueB64 string) (swapFields, error) {
	body, err := scval.Parse(valueB64)
	if err != nil {
		return swapFields{}, fmt.Errorf("parse body: %w", err)
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return swapFields{}, fmt.Errorf("body not a Map: %w", err)
	}

	callerSv, err := scval.MustMapField(entries, "caller")
	if err != nil {
		return swapFields{}, fmt.Errorf("missing caller: %w", err)
	}
	caller, err := scval.AsAddressStrkey(callerSv)
	if err != nil {
		return swapFields{}, fmt.Errorf("caller: %w", err)
	}

	tokenInSv, err := scval.MustMapField(entries, "token_in")
	if err != nil {
		return swapFields{}, fmt.Errorf("missing token_in: %w", err)
	}
	tokenIn, err := scval.AsAddressStrkey(tokenInSv)
	if err != nil {
		return swapFields{}, fmt.Errorf("token_in: %w", err)
	}

	tokenOutSv, err := scval.MustMapField(entries, "token_out")
	if err != nil {
		return swapFields{}, fmt.Errorf("missing token_out: %w", err)
	}
	tokenOut, err := scval.AsAddressStrkey(tokenOutSv)
	if err != nil {
		return swapFields{}, fmt.Errorf("token_out: %w", err)
	}

	inSv, err := scval.MustMapField(entries, "token_amount_in")
	if err != nil {
		return swapFields{}, fmt.Errorf("missing token_amount_in: %w", err)
	}
	amountIn, err := scval.AsAmountFromI128(inSv)
	if err != nil {
		return swapFields{}, fmt.Errorf("token_amount_in: %w", err)
	}

	outSv, err := scval.MustMapField(entries, "token_amount_out")
	if err != nil {
		return swapFields{}, fmt.Errorf("missing token_amount_out: %w", err)
	}
	amountOut, err := scval.AsAmountFromI128(outSv)
	if err != nil {
		return swapFields{}, fmt.Errorf("token_amount_out: %w", err)
	}

	return swapFields{
		Caller:    caller,
		TokenIn:   tokenIn,
		TokenOut:  tokenOut,
		AmountIn:  amountIn,
		AmountOut: amountOut,
	}, nil
}
