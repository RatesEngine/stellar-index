// Package defindex decodes Soroban contract events from DeFindex
// yield-aggregator vault contracts on Stellar mainnet. DeFindex is
// a paltalabs project (https://github.com/paltalabs/defindex) — its
// vaults wrap underlying yield protocols (currently Blend) and
// expose a single share token (`df_token`) per vault.
//
// We surface vault deposit/withdraw events for flow attribution
// only — they are NOT price-discovery events and never contribute
// to VWAP. The vault contract is registered with `Class: ClassRouter`
// (alongside the Soroswap router) for the same attribution-only
// taxonomy.
//
// See README.md for Phase A vs Phase B scope and the upstream
// source links per event.
package defindex

import (
	"errors"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/scval"
)

// SourceName is the registry key for this source. Used in
// `external.Registry`, `routers.name` (per vault), and trade
// attribution in Phase B.
const SourceName = "defindex"

// MainnetFactory is the factory contract that deploys new vaults.
// Verified against paltalabs/defindex's `mainnet.contracts.json`
// at tag 1.0.0. Factory events (`("DeFindexFactory","create")`)
// announce new-vault deployments — but the new vault's contract
// address lives in the InvokeContract op's return value, NOT in
// the event body. See README.md Phase B follow-up #1.
const MainnetFactory = "CDKFHFJIET3A73A2YN4KV7NSV32S6YGQMUFH3DNJXLBWL4SKEGVRNFKI"

// Phase-A vault inventory — the three known autocompound vaults
// deployed by paltalabs. Sourced from
// web/explorer/src/app/aggregators/page.tsx (last verified
// 2026-05-08). Multi-vault discovery via factory events is a
// Phase-B follow-up; until then, we hand-curate.
const (
	MainnetVaultUSDC = "CDB2WMKQQNVZMEBY7Q7GZ5C7E7IAFSNMZ7GGVD6WKTCEWK7XOIAVZSAP"
	MainnetVaultEURC = "CC5CE6MWISDXT3MLNQ7R3FVILFVFEIH3COWGH45GJKL6BD2ZHF7F7JVI"
	MainnetVaultXLM  = "CDPWNUW7UMCSVO36VAJSQHQECISPJLCVPDASKHRC5SEROAAZDUQ5DG2Z"
)

// MainnetVaults is the set the dispatcher hot-path checks against.
// Map for O(1) Matches() lookup.
var MainnetVaults = map[string]struct{}{
	MainnetVaultUSDC: {},
	MainnetVaultEURC: {},
	MainnetVaultXLM:  {},
}

// VaultName maps a known vault contract id to a human-readable
// name suffix. Stamped into log fields + the eventual
// `routers.name` row (`defindex-vault-{name}`).
var VaultName = map[string]string{
	MainnetVaultUSDC: "usdc-autocompound",
	MainnetVaultEURC: "eurc-autocompound",
	MainnetVaultXLM:  "xlm-autocompound",
}

// MainnetVaultWASMHash is the WASM hash every Phase-A vault was
// deployed from at paltalabs/defindex tag 1.0.0. Useful for
// byte-identification of any new vault deployed by the factory
// pre-flagging via (contract_id, wasm_hash) without needing the
// factory event.
const MainnetVaultWASMHash = "0f3073517cbfacbfd482bc166cff38a0e7abeab9b7ee77334abab45880fb8f3a"

// Topic-prefix string values (topic[0]). Both vault and factory
// emit topic[0] as `ScvString` (NOT Symbol) — the literal strings
// `"DeFindexVault"` (13 chars) and `"DeFindexFactory"` (15 chars)
// exceed `symbol_short!`'s 9-char cap in Rust, so the SDK
// serialises them as ScvString. Mirror the Soroswap topic-prefix
// pattern (also ScvString).
const (
	PrefixVault   = "DeFindexVault"
	PrefixFactory = "DeFindexFactory"
)

// Topic[1] symbols for events we decode. The vault contract
// publishes more events (rescue / paused / nreceiver / nmanager /
// dfees / rebalance) but Phase A only needs deposit + withdraw —
// the user-facing flow we want to attribute.
const (
	EventDeposit  = "deposit"
	EventWithdraw = "withdraw"
)

// Pre-encoded base64 SCVal blobs — byte-identical to what the
// contract emits. Cheap byte-equality classification on the hot
// path; no SCVal parsing for events we don't care about.
//
// Golden wire-format regression covered by
// `internal/scval/scval_test.go::TestGolden_symbolBytes` — if the
// SDK encoder shifts under us, that test fires before this
// package ships.
var (
	TopicPrefixVault    = scval.MustEncodeString(PrefixVault)
	TopicSymbolDeposit  = scval.MustEncodeSymbol(EventDeposit)
	TopicSymbolWithdraw = scval.MustEncodeSymbol(EventWithdraw)
)

// VaultFlow is the canonical wire shape for one vault deposit or
// withdraw. We keep both Deposit and Withdraw on a single struct
// rather than two parallel types — they share every field except
// `Direction`, and downstream consumers (logging now, attribution
// later) want to switch on direction inside one branch.
//
// Amounts is `Vec<i128>` because DeFindex vaults can hold multiple
// assets (multi-asset vaults exist in the upstream protocol). The
// Phase-A trio is single-asset so `len(Amounts) == 1`, but we
// don't hardcode that.
//
// DfTokenDelta is the share-token amount minted (deposit) or burned
// (withdraw). It's the LP-share delta — the underlying-asset
// delta is in `Amounts`. Both are required for any future NAV /
// per-share-price calculation.
type VaultFlow struct {
	Source       string
	Ledger       uint32
	ClosedAt     time.Time
	TxHash       string
	OpIndex      int
	ContractID   string // the vault — one of MainnetVaults
	VaultName    string // human-readable, from VaultName lookup
	Direction    Direction
	Counterparty string             // depositor / withdrawer (G-strkey)
	Amounts      []canonical.Amount // per-asset, in vault asset order
	DfTokenDelta canonical.Amount   // shares minted (deposit) or burned (withdraw)
}

// Direction discriminates the two flow types.
type Direction string

const (
	DirectionDeposit  Direction = "deposit"
	DirectionWithdraw Direction = "withdraw"
)

// Event wraps a VaultFlow so it satisfies consumer.Event for the
// dispatcher / pipeline path. Phase A is log-only; the persist
// hook is a Phase-B add-on.
type Event struct {
	Flow VaultFlow
}

// EventKind implements [consumer.Event].
func (e Event) EventKind() string {
	return "defindex.vault." + string(e.Flow.Direction)
}

// Source implements [consumer.Event].
func (e Event) Source() string { return SourceName }

// Errors returned by the decode path. Callers classify via
// errors.Is.
var (
	// ErrUnknownEvent — topic shape doesn't match any of the
	// vault events we care about. The dispatcher's drop-counter
	// records these; we don't surface them as failures because
	// "vault emits an event we don't decode" is normal (rescue,
	// rebalance, nreceiver, ...).
	ErrUnknownEvent = errors.New("defindex: unknown vault event topic")

	// ErrMalformedPayload — event fields don't match the expected
	// DeFindex schema (arity, types, missing required field).
	ErrMalformedPayload = errors.New("defindex: malformed event payload")

	// ErrUnknownVault — event came from a contract that isn't in
	// MainnetVaults. Defensive check; the dispatcher's contract-id
	// pre-filter (see dispatcher_adapter.Matches) should have
	// caught these first.
	ErrUnknownVault = errors.New("defindex: event from unknown vault contract")
)
