// Package defindex decodes Soroban contract events emitted by both
// layers of paltalabs' DeFindex protocol on Stellar mainnet:
//
//  1. STRATEGY layer — Blend autocompound *strategy* contracts that
//     hold the underlying lending position. Topic[0] =
//     ScvString("BlendStrategy"). Body { from: Address, amount: i128 }.
//     `from` here is the VAULT contract (a C-strkey), not the end
//     user — useful for capital-flow attribution between layers.
//
//  2. VAULT layer — DeFindex *vault wrapper* contracts that users
//     interact with directly. Topic[0] = ScvString("DeFindexVault").
//     Body has the end-user G-strkey (`depositor` / `withdrawer`),
//     multi-asset amounts (`amounts` / `amounts_withdrawn`,
//     Vec<i128>) and share-token deltas (`df_tokens_minted` /
//     `df_tokens_burned`, i128).
//
// Phase A (2026-05-19) shipped only the strategy layer because the
// initial WASM walk confirmed only the strategy WASM
// (`11329c24…988`) on the 3 named "fixed strategy" vault contracts
// in `mainnet.contracts.json`. That walk MISSED the wrapper
// contracts deployed by the factory (different WASM `ae3409a4…468b`
// or its upgraded `07097f83…84b0`); we now know there are 100+
// such wrappers spawned over the protocol's life (factory
// `CDKFHFJI…NFKI` emits one `create` event per spawn). The vault
// wrappers ARE where end-user attribution lives, and missing them
// is what the 2026-05-21 cross-check vs Soroban RPC revealed
// (~27% coverage in a 12-hour sample; pre-rc.63 walker only 14%).
//
// Phase B (this revision, 2026-05-21) adds the DeFindexVault
// topic-match. Dispatch is still PURELY by topic — we don't
// hardcode any contract addresses — so any current or future
// DeFindex vault wrapper, whether listed in mainnet.contracts.json
// or spawned later, gets decoded automatically. This mirrors the
// comet/aquarius shared-emitter topology elsewhere in the codebase.
//
// We surface vault + strategy deposit/withdraw events for flow
// attribution only — they are NOT price-discovery events and never
// contribute to VWAP. Out of scope here: factory `create`/`n_fee`
// events, strategy `harvest` events, vault `rebalance`/admin events
// — all flagged in docs/operations/wasm-audits/defindex.md as
// Phase-B-or-later follow-ups.
//
// See README.md for scope.
package defindex

import (
	"errors"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// SourceName is the registry key for this source. Kept as
// "defindex" (rather than renamed to e.g. "blend-strategy") so the
// registry / genesis / status-page keys stay stable; a rename is a
// separate product-taxonomy decision tracked in defindex.md.
const SourceName = "defindex"

// PrefixStrategy is topic[0] for every Blend strategy event. It is
// 13 chars, exceeds `symbol_short!`'s 9-char cap, so the SDK
// serialises it as ScvString (same pattern as Soroswap's
// "SoroswapPair"). Confirmed on-chain via scan-soroban-events.
const PrefixStrategy = "BlendStrategy"

// PrefixVault is topic[0] for every DeFindex vault-wrapper event
// (user-facing layer). Also 13 chars, also ScvString-encoded.
// Confirmed on-chain via Soroban-RPC getEvents on a known wrapper
// (CCA2ZJP5… runs WASM ae3409a4…468b, emits this topic on every
// user deposit/withdraw with a G-strkey `depositor`/`withdrawer`).
const PrefixVault = "DeFindexVault"

// PrefixFactory is topic[0] for the DeFindex *factory* contract's
// events. The factory (`CDKFHFJI…NFKI`) emits `create` once per
// vault spawn and `n_fee` on protocol-fee-recipient governance
// updates. 15 chars → ScvString-encoded (same shape as
// PrefixStrategy / PrefixVault).
//
// We classify these but do NOT decode bodies today — they don't
// drive a Trade or a flow record. Recognising them is what the
// EVERY-event policy requires (project_every_event_principle):
// classify() must enumerate every topic the source can emit so the
// dispatcher's drop-counter doesn't silently file factory events as
// "unmatched topic." Body decode is Phase-C scope (would give us a
// live notification feed for new wrapper deployments).
const PrefixFactory = "DeFindexFactory"

// Topic[1] symbols for the user-facing flow events we decode. The
// strategy contract publishes more (harvest / keeper admin / …);
// Phase A only decodes deposit + withdraw at the strategy layer.
// The vault layer reuses the same two symbols (`deposit`,
// `withdraw`) — they're shared between layers, so Phase B doesn't
// need new symbol constants.
const (
	EventDeposit  = "deposit"
	EventWithdraw = "withdraw"
	// Strategy-layer governance / yield events that don't produce a
	// canonical Trade today but are valid topics to recognise so
	// classify() enumerates the full upstream event surface per the
	// EVERY-event policy (project_every_event_principle).
	EventHarvest = "harvest"
	// Vault-layer governance / admin events. Per Phase-B audit doc:
	//   rescue, paused, unpaused, nreceiver, nmanager, nemanager,
	//   rbmanager, dfees, rebalance
	// Of these only `rebalance` is multiplexed (four bodies share the
	// topic — discriminate by `rebalance_method` Symbol in body).
	EventRescue    = "rescue"
	EventPaused    = "paused"
	EventUnpaused  = "unpaused"
	EventNReceiver = "nreceiver"
	EventNManager  = "nmanager"
	EventNEManager = "nemanager"
	EventRBManager = "rbmanager"
	EventDFees     = "dfees"
	EventRebalance = "rebalance"
	// Factory-layer events. `create` fires once per vault spawn
	// (body holds roles / vault_fee / assets but NOT the new vault
	// address — see audit doc "Surprising gotcha #2"); `n_fee`
	// fires on protocol-fee-recipient governance updates.
	EventCreate = "create"
	EventNFee   = "n_fee"
)

// Pre-encoded base64 SCVal blobs — byte-identical to what the
// contract emits — for cheap byte-equality classification on the
// hot path (no SCVal parsing for events we don't decode).
//
// Golden wire-format regression covered by
// internal/scval/scval_test.go::TestGolden_symbolBytes — if the SDK
// encoder shifts under us, that test fires before this ships.
var (
	TopicPrefixStrategy = scval.MustEncodeString(PrefixStrategy)
	TopicPrefixVault    = scval.MustEncodeString(PrefixVault)
	TopicPrefixFactory  = scval.MustEncodeString(PrefixFactory)
	TopicSymbolDeposit  = scval.MustEncodeSymbol(EventDeposit)
	TopicSymbolWithdraw = scval.MustEncodeSymbol(EventWithdraw)
	// Classification-only topic[1] symbols (no decoder today).
	TopicSymbolHarvest   = scval.MustEncodeSymbol(EventHarvest)
	TopicSymbolRescue    = scval.MustEncodeSymbol(EventRescue)
	TopicSymbolPaused    = scval.MustEncodeSymbol(EventPaused)
	TopicSymbolUnpaused  = scval.MustEncodeSymbol(EventUnpaused)
	TopicSymbolNReceiver = scval.MustEncodeSymbol(EventNReceiver)
	TopicSymbolNManager  = scval.MustEncodeSymbol(EventNManager)
	TopicSymbolNEManager = scval.MustEncodeSymbol(EventNEManager)
	TopicSymbolRBManager = scval.MustEncodeSymbol(EventRBManager)
	TopicSymbolDFees     = scval.MustEncodeSymbol(EventDFees)
	TopicSymbolRebalance = scval.MustEncodeSymbol(EventRebalance)
	TopicSymbolCreate    = scval.MustEncodeSymbol(EventCreate)
	TopicSymbolNFee      = scval.MustEncodeSymbol(EventNFee)
)

// StrategyFlow is the canonical wire shape for one Blend strategy
// deposit or withdraw. Both directions share an identical body
// (`{from, amount}` — verified on-chain), so a single struct with a
// Direction discriminator is the natural shape.
//
// From is the caller moving capital — for these strategies it is
// typically the vault/router *contract* address (a C-strkey), not
// the end-user; end-user attribution requires correlating with the
// same-tx vault event (a Phase-B follow-up). It can also be a
// plain account G-strkey; scval.AsAddressStrkey renders both.
//
// Amount is the underlying-asset delta as a big-int-backed
// canonical.Amount (i128, never truncated — ADR-0003).
type StrategyFlow struct {
	Source     string
	Ledger     uint32
	ClosedAt   time.Time
	TxHash     string
	OpIndex    int
	EventIndex uint32 // in-tx contract event index — per-flow PK discriminator (migration 0055)
	ContractID string // the BlendStrategy contract that emitted
	Direction  Direction
	From       string           // account (G…) or contract (C…) strkey
	Amount     canonical.Amount // underlying-asset delta (i128)
}

// Direction discriminates the two flow types.
type Direction string

const (
	DirectionDeposit  Direction = "deposit"
	DirectionWithdraw Direction = "withdraw"
)

// Event wraps a StrategyFlow so it satisfies consumer.Event for the
// dispatcher / pipeline path. Log-only sink for now; a per-flow
// persist hypertable is a Phase-C follow-up (see audit doc).
type Event struct {
	Flow StrategyFlow
}

// EventKind implements [consumer.Event].
func (e Event) EventKind() string {
	return "defindex.strategy." + string(e.Flow.Direction)
}

// Source implements [consumer.Event].
func (e Event) Source() string { return SourceName }

// VaultFlow is the canonical wire shape for one user-facing
// DeFindex *vault wrapper* deposit or withdraw — what end users
// see when they interact with the protocol. Distinct from
// StrategyFlow (the underlying strategy-layer flow that fires from
// the strategy contract with `from` = vault address); each user
// deposit produces one VaultFlow + one StrategyFlow + one Blend
// Pool supply event in the same tx (correlate by tx_hash +
// op_index).
//
// User is the end user moving capital — a G-strkey for direct
// interactions, occasionally a C-strkey if the user came via
// another aggregator/router. The vault layer is where actual
// end-user attribution lives (the strategy layer's `from` is
// always the vault contract).
//
// Amounts is a Vec because DeFindex supports multi-asset vaults
// (one Vec entry per asset in the vault's basket). The
// `mainnet.contracts.json` Phase-A trio (USDC / EURC / XLM blend
// autocompound) are all single-asset (vec length 1), but the
// etherfuse-strategy variants (cetes, ustry, tesouro) may have
// multiple — the decoder makes no length assumption.
//
// DfTokens is the share-token delta — `df_tokens_minted` (deposit)
// or `df_tokens_burned` (withdraw). i128, ADR-0003 (never
// truncated).
type VaultFlow struct {
	Source     string
	Ledger     uint32
	ClosedAt   time.Time
	TxHash     string
	OpIndex    int
	EventIndex uint32 // in-tx contract event index — per-flow PK discriminator (migration 0055)
	ContractID string // the DeFindex vault-wrapper contract
	Direction  Direction
	User       string             // depositor (G…) or withdrawer; may be C-strkey
	Amounts    []canonical.Amount // underlying-asset delta vec (i128 each)
	DfTokens   canonical.Amount   // share-token delta — mint on deposit, burn on withdraw
}

// VaultEvent wraps a VaultFlow for the dispatcher / pipeline path.
type VaultEvent struct {
	Flow VaultFlow
}

// EventKind implements [consumer.Event].
func (e VaultEvent) EventKind() string {
	return "defindex.vault." + string(e.Flow.Direction)
}

// Source implements [consumer.Event].
func (e VaultEvent) Source() string { return SourceName }

// Errors returned by the decode path. Callers classify via
// errors.Is.
var (
	// ErrUnknownEvent — topic shape doesn't match a deposit/withdraw
	// BlendStrategy event. The dispatcher's drop-counter records
	// these; not a failure ("strategy emits an event we don't
	// decode" — harvest / keeper admin — is normal).
	ErrUnknownEvent = errors.New("defindex: unknown strategy event topic")

	// ErrMalformedPayload — event body doesn't match the expected
	// {from, amount} schema (missing field, wrong type).
	ErrMalformedPayload = errors.New("defindex: malformed event payload")
)
