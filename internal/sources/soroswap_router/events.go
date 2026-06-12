// Package soroswap_router decodes Soroban InvokeContract calls
// against the Soroswap Router contract. The router emits no events
// itself (its work is calling down to per-pair contracts that
// emit `SoroswapPair("swap")`); this package observes the router's
// invocation directly via dispatcher.ContractCallDecoder so we
// capture user-level intent (path, amount_in, amount_out_min)
// distinct from the per-pair leg-level swaps.
//
// Sister package: internal/sources/soroswap (pair + factory event
// decoder). Same upstream protocol; different vantage point.
package soroswap_router

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// SourceName is the registry key for this source. Used in
// `external.Registry`, `routers.name`, and trade attribution.
const SourceName = "soroswap-router"

// MainnetRouter is the contract ID of the Soroswap router on
// Stellar pubnet. Verified in
// docs/discovery/dexes-amms/soroswap.md against the
// `public/router.json` config in the soroswap-core repo.
const MainnetRouter = "CAG5LRYQ5JVEUI5TEID72EYOVX44TTUJT5BQR2J6J77FH65PCCFAJDDH"

// Function names the router exposes. We track only the swap
// entry points; admin / read-only methods (set_pair_fee,
// router_pairs, init, …) don't move tokens and aren't useful
// for attribution.
const (
	FnSwapExactTokensForTokens = "swap_exact_tokens_for_tokens"
	FnSwapTokensForExactTokens = "swap_tokens_for_exact_tokens"
)

// RouterSwap is the canonical wire shape one router invocation
// projects to. One `RouterSwap` corresponds to ONE call to
// `swap_exact_tokens_for_tokens` / `swap_tokens_for_exact_tokens`,
// which in turn emits N per-pair `Trade` events (one per hop) from
// the existing soroswap pair decoder.
//
// Path is the hop sequence the user requested (or that the router
// computed). Length 2 = direct swap (single pair); length 3+ =
// multi-hop. Each adjacent pair (Path[i], Path[i+1]) maps to one
// pair contract that emits the underlying swap event.
//
// Function discriminates the two router entry points:
//   - `swap_exact_tokens_for_tokens`: user fixes input, accepts
//     any output ≥ AmountOutMin.
//   - `swap_tokens_for_exact_tokens`: user fixes output, accepts
//     any input ≤ AmountInMax.
//
// AmountIn / AmountOut both populate; the "min" / "max" semantics
// depend on Function. Slippage analysis happens at the aggregator
// level by comparing requested vs realized amounts (the realized
// amount comes from the per-pair swap events with matching tx_hash).
type RouterSwap struct {
	Source     string // always SourceName
	Ledger     uint32
	ClosedAt   time.Time
	TxHash     string
	OpIndex    int
	OpSource   string // operation source (G-strkey / muxed)
	TxSource   string // tx source (G-strkey)
	ContractID string // always MainnetRouter (mainnet)

	Function  string // FnSwap*
	Recipient string // `to` arg — where output lands
	// Path is the hop sequence of token contract C-strkeys
	// the router walked. Stored as raw C-strkeys so the
	// downstream SAC-wrapper resolver (cfg.Supply.SacWrappers)
	// can map to canonical.Asset on its own schedule. Length
	// ≥ 2 by router contract precondition.
	Path       []string
	AmountIn   canonical.Amount // exact-input or upper-bound
	AmountOut  canonical.Amount // exact-output or lower-bound
	DeadlineTs time.Time        // user-supplied expiry
}

// Event wraps a RouterSwap so it satisfies consumer.Event for
// the dispatcher / pipeline path. Kept minimal — the persist
// layer is a Phase-B add-on (will tag matching same-tx Trade
// rows via the trades.routed_via column).
type Event struct {
	Swap RouterSwap
}

// EventKind implements [consumer.Event].
func (e Event) EventKind() string { return "soroswap-router.swap" }

// Source implements [consumer.Event].
func (e Event) Source() string { return SourceName }

// CallSig is the per-call discriminator in the soroswap_router_swaps PK. A
// single InvokeContract op can carry MULTIPLE distinct router swaps — an
// aggregator splitting a trade, or a batch distributing to several recipients —
// which all share (ledger, tx_hash, op_index). Without a discriminator the
// served PK collapses them to one row (verified: 106 genuinely-distinct swaps
// across pubnet history were being lost). CallSig is a deterministic 128-bit
// content hash over the swap's economic identity (function + recipient + path +
// requested amounts), so distinct swaps get distinct PKs (all stored), while
// auth-tree DUPLICATES of the same call — a multi-entry (co-signed) tx surfaces
// the identical call at several CallPaths — hash equal and dedup via ON
// CONFLICT. deadline is excluded: it's a user sentinel (often garbage; see the
// deadline_ts NULL-clamp) that doesn't distinguish economic intent.
func (s RouterSwap) CallSig() string {
	parts := make([]string, 0, len(s.Path)+4)
	parts = append(parts, s.Function, s.Recipient)
	parts = append(parts, s.Path...)
	parts = append(parts, s.AmountIn.String(), s.AmountOut.String())
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:16])
}
