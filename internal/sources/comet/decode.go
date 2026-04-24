package comet

import (
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/scval"
)

// cometTopicArity is the topic-count on every Comet event:
// [Symbol("POOL"), Symbol("<event_name>")]. Anything other than 2 is
// a schema change we don't claim.
const cometTopicArity = 2

// classifySwap reports whether this is a (POOL, swap) event. The
// decoder v1 only handles swaps; other Comet events (join_pool,
// exit_pool, deposit, withdraw) would widen this predicate as
// follow-ups add their own decode paths.
func classifySwap(e *events.Event) bool {
	if len(e.Topic) < cometTopicArity {
		return false
	}
	return e.Topic[0] == TopicSymbolPool && e.Topic[1] == TopicSymbolSwap
}

// decodeSwap converts one (POOL, swap) Comet event into a
// canonical.Trade. Unlike Soroswap (where token identities come from
// a factory's new_pair event), Comet's SwapEvent carries token_in
// and token_out as Addresses in the body itself — the decoder needs
// no pool registry.
//
// SwapEvent body (verified against
// comet-contracts/contracts/src/c_pool/event.rs:6-13 +
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
		Source:      SourceName,
		Ledger:      e.Ledger,
		TxHash:      e.TxHash,
		OpIndex:     uint32(e.OperationIndex),
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
