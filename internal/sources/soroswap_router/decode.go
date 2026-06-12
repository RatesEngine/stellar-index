package soroswap_router

import (
	"errors"
	"fmt"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// ErrMalformedArgs flags an InvokeContract call whose arg slice
// doesn't match the function's published shape (wrong arity, wrong
// SCVal types). Surfaced via the dispatcher's drop-counter; the
// router decoder skips the call rather than aborting the ledger.
var ErrMalformedArgs = errors.New("soroswap_router: malformed args")

// ErrUnknownFunction is returned when the dispatcher routed a call
// to this decoder but the function name isn't one we handle. The
// dispatcher's Matches() should have filtered first; this is a
// defensive double-check.
var ErrUnknownFunction = errors.New("soroswap_router: unknown function")

// decodeRouterArgs converts one Soroswap router InvokeContract call
// into a single RouterSwap. Returns ErrMalformedArgs / ErrUnknownFunction
// for skip-and-count cases; per-arg context wraps the underlying
// scval / xdr error for diagnostic logging.
//
// Soroswap router function signatures (from soroswap-core's
// `contracts/router/src/lib.rs`):
//
//	swap_exact_tokens_for_tokens(
//	    amount_in:        i128,
//	    amount_out_min:   i128,
//	    path:             Vec<Address>,
//	    to:               Address,
//	    deadline:         u64,
//	) -> Vec<i128>   // realized per-hop amounts
//
//	swap_tokens_for_exact_tokens(
//	    amount_out:       i128,
//	    amount_in_max:    i128,
//	    path:             Vec<Address>,
//	    to:               Address,
//	    deadline:         u64,
//	) -> Vec<i128>
//
// Both shapes return Vec<i128> of realized per-hop amounts but we
// don't decode the return value here — the dispatcher only routes
// the call's args, not its result. The realized amounts are
// recoverable from the per-pair `SoroswapPair("swap")` events in
// the same tx (already decoded by the sister soroswap package).
func decodeRouterArgs(
	fnName string,
	args []string,
	contractID string,
	ledger uint32,
	txHash string,
	opIndex int,
	opSource, txSource string,
	closedAt time.Time,
) (*RouterSwap, error) {
	if fnName != FnSwapExactTokensForTokens && fnName != FnSwapTokensForExactTokens {
		return nil, ErrUnknownFunction
	}
	if len(args) < 5 {
		return nil, fmt.Errorf("%w: %s expects 5 args, got %d", ErrMalformedArgs, fnName, len(args))
	}
	// Destructure into named locals so each later access is on a
	// non-indexed binding — keeps the bounds check visible to
	// gosec G602 across the long function body.
	rawAmount0, rawAmount1, rawPath, rawTo, rawDeadline := args[0], args[1], args[2], args[3], args[4]

	// Position 0 + 1 are i128. The semantic of which is amount-in
	// vs amount-out depends on which function — but both are still
	// i128, so the parse is identical.
	a0, err := parseI128(rawAmount0)
	if err != nil {
		return nil, fmt.Errorf("%w: args[0]: %w", ErrMalformedArgs, err)
	}
	a1, err := parseI128(rawAmount1)
	if err != nil {
		return nil, fmt.Errorf("%w: args[1]: %w", ErrMalformedArgs, err)
	}

	// Position 2: Vec<Address> path. Length-2 = direct (single
	// pair); length-3+ = multi-hop. The router refuses len < 2
	// at the contract level, so a malformed-short path means the
	// dispatcher routed something the router itself would reject.
	pathSv, err := scval.Parse(rawPath)
	if err != nil {
		return nil, fmt.Errorf("%w: args[2] path: %w", ErrMalformedArgs, err)
	}
	pathVec, err := scval.AsVec(pathSv)
	if err != nil {
		return nil, fmt.Errorf("%w: args[2] path: %w", ErrMalformedArgs, err)
	}
	if len(pathVec) < 2 {
		return nil, fmt.Errorf("%w: path len=%d (want >= 2)", ErrMalformedArgs, len(pathVec))
	}
	path := make([]string, len(pathVec))
	for i, sv := range pathVec {
		s, err := scval.AsAddressStrkey(sv)
		if err != nil {
			return nil, fmt.Errorf("%w: path[%d]: %w", ErrMalformedArgs, i, err)
		}
		path[i] = s
	}

	// Position 3: Address `to` (recipient).
	toSv, err := scval.Parse(rawTo)
	if err != nil {
		return nil, fmt.Errorf("%w: args[3] to: %w", ErrMalformedArgs, err)
	}
	to, err := scval.AsAddressStrkey(toSv)
	if err != nil {
		return nil, fmt.Errorf("%w: args[3] to: %w", ErrMalformedArgs, err)
	}

	// Position 4: u64 deadline. Unix-seconds.
	dlSv, err := scval.Parse(rawDeadline)
	if err != nil {
		return nil, fmt.Errorf("%w: args[4] deadline: %w", ErrMalformedArgs, err)
	}
	deadline, err := scval.AsU64(dlSv)
	if err != nil {
		return nil, fmt.Errorf("%w: args[4] deadline: %w", ErrMalformedArgs, err)
	}

	// deadline == 0 is a "no deadline" sentinel, not a real
	// 1970-01-01T00:00:00Z expiry. Leave DeadlineTs as the zero
	// time.Time so the sink's IsZero() guard NULLs the column
	// rather than storing the Unix epoch. (Without this, a 0
	// deadline lands as 1970 — distinguishable in neither intent
	// nor the IsZero guard from a missing value.)
	var deadlineTs time.Time
	if deadline != 0 {
		deadlineTs = time.Unix(int64(deadline), 0).UTC()
	}

	// Map (a0, a1) → (AmountIn, AmountOut) per function shape.
	amountIn, amountOut := a0, a1 // exact_tokens_for_tokens default
	if fnName == FnSwapTokensForExactTokens {
		amountIn, amountOut = a1, a0
	}

	return &RouterSwap{
		Source:     SourceName,
		Ledger:     ledger,
		ClosedAt:   closedAt,
		TxHash:     txHash,
		OpIndex:    opIndex,
		OpSource:   opSource,
		TxSource:   txSource,
		ContractID: contractID,
		Function:   fnName,
		Recipient:  to,
		Path:       path,
		AmountIn:   amountIn,
		AmountOut:  amountOut,
		DeadlineTs: deadlineTs,
	}, nil
}

// parseI128 wraps the (Parse → AsAmountFromI128) chain so the
// per-arg error paths in decodeRouterArgs stay one-line.
func parseI128(b64 string) (canonical.Amount, error) {
	sv, err := scval.Parse(b64)
	if err != nil {
		return canonical.Amount{}, err
	}
	return scval.AsAmountFromI128(sv)
}
