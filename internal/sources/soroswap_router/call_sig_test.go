package soroswap_router

import (
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// TestCallSig is the regression guard for the migration-0056 PK discriminator.
// CallSig must: (1) be deterministic, (2) IGNORE deadline (so auth-tree dups of
// the same call dedup even if a sentinel deadline varies), and (3) DIFFER for
// any economically-distinct swap (recipient / path / amount) — the 106 router
// swaps that the coarse PK was collapsing. A regression in any of these silently
// re-introduces sub-op data loss or double-counts dedupable dups.
func TestCallSig(t *testing.T) {
	t.Parallel()
	base := RouterSwap{
		Function:   FnSwapExactTokensForTokens,
		Recipient:  "GRECIPIENT",
		Path:       []string{"CTOKENA", "CTOKENB"},
		AmountIn:   canonical.NewAmount(big.NewInt(1_000_000)),
		AmountOut:  canonical.NewAmount(big.NewInt(2_000_000)),
		DeadlineTs: time.Unix(1_700_000_000, 0).UTC(),
	}

	// Deterministic.
	if base.CallSig() != base.CallSig() {
		t.Fatal("CallSig is not deterministic")
	}

	// Deadline must NOT affect the sig (it's an excluded user sentinel) — and
	// fields that don't enter the identity (ledger/tx/op/sources) must not either.
	dupDiffDeadline := base
	dupDiffDeadline.DeadlineTs = time.Unix(3_100_000_000_000_000_000, 0).UTC()
	dupDiffDeadline.Ledger = 999
	dupDiffDeadline.TxHash = "differenttx"
	dupDiffDeadline.OpIndex = 7
	if dupDiffDeadline.CallSig() != base.CallSig() {
		t.Error("CallSig must be invariant to deadline / non-identity fields (would break auth-tree dedup)")
	}

	// Every economic dimension must change the sig.
	diffRecipient := base
	diffRecipient.Recipient = "GOTHER"
	diffPath := base
	diffPath.Path = []string{"CTOKENA", "CTOKENC"}
	diffAmountIn := base
	diffAmountIn.AmountIn = canonical.NewAmount(big.NewInt(1_000_001))
	diffAmountOut := base
	diffAmountOut.AmountOut = canonical.NewAmount(big.NewInt(2_000_001))
	diffFn := base
	diffFn.Function = FnSwapTokensForExactTokens

	for name, v := range map[string]RouterSwap{
		"recipient": diffRecipient,
		"path":      diffPath,
		"amountIn":  diffAmountIn,
		"amountOut": diffAmountOut,
		"function":  diffFn,
	} {
		if v.CallSig() == base.CallSig() {
			t.Errorf("CallSig collision: distinct %s produced the same sig as base (sub-op swap would be lost)", name)
		}
	}

	// Sanity: 16-byte hash → 32 hex chars.
	if len(base.CallSig()) != 32 {
		t.Errorf("CallSig len = %d, want 32 hex chars", len(base.CallSig()))
	}
}
