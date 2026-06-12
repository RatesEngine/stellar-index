// Package blend ingests events from the Blend Capital Soroban
// lending protocol on Stellar.
//
// Per docs/discovery/dexes-amms/blend.md: Blend is **not** a spot
// trading venue. We index it for:
//
//  1. Liquidation auctions — directional price signals during
//     stress; collateral sold at a discount to cover bad debt.
//  2. Money-market positions (supply / withdraw / borrow / repay /
//     flash_loan) — supply-side metrics for asset detail pages.
//  3. Credit-risk events (bad_debt, defaulted_debt) — protocol
//     health.
//  4. Admin / status (set_admin, update_pool, set_status) —
//     operational state for degraded-source detection.
//
// **We do NOT emit Blend events as canonical.Trade.** Blend's
// outputs are auctions and position changes, not spot trades. The
// dispatcher routes Blend events through the [Decoder]; the
// indexer-side sink writes them to per-protocol Blend storage
// (auctions, positions, admin) rather than the trades hypertable.
//
// Stellar RFP commitment: Blend is named in the price-aggregation
// scope but only as a "secondary validation source" per the
// proposal — auction stress-prices contribute as reference points
// on the asset detail surface, not into VWAP.
//
// Verified 2026-04-22 against pool/src/events.rs +
// pool-factory/src/events.rs at clone time of
// .discovery-repos/blend-contracts.
package blend

import (
	"errors"

	"github.com/RatesEngine/rates-engine/internal/scval"
)

// SourceName constant — appears in metrics labels, registry keys,
// and storage rows. Stable.
const SourceName = "blend"

// Event names — topic[0] of every Blend pool / pool-factory event,
// as a Soroban Symbol on the wire. Verified 2026-04-22 against
// blend-contracts-v2 commit c19abee5b9be4f49e0cda9057e87d343e5dcc095.
const (
	// Auction events (PRIMARY — directional price signals).
	EventNewAuction    = "new_auction"
	EventFillAuction   = "fill_auction"
	EventDeleteAuction = "delete_auction"

	// Money-market events (SECONDARY — supply/borrow tallies).
	EventSupply             = "supply"
	EventWithdraw           = "withdraw"
	EventSupplyCollateral   = "supply_collateral"
	EventWithdrawCollateral = "withdraw_collateral"
	EventBorrow             = "borrow"
	EventRepay              = "repay"
	EventFlashLoan          = "flash_loan"
	EventGulp               = "gulp"
	EventClaim              = "claim"

	// Credit-risk + emissions events.
	EventBadDebt          = "bad_debt"
	EventDefaultedDebt    = "defaulted_debt"
	EventReserveEmissions = "reserve_emission_update"
	EventGulpEmissions    = "gulp_emissions"

	// Admin / status events.
	EventSetAdmin         = "set_admin"
	EventUpdatePool       = "update_pool"
	EventQueueSetReserve  = "queue_set_reserve"
	EventCancelSetReserve = "cancel_set_reserve"
	EventSetReserve       = "set_reserve"
	EventSetStatus        = "set_status"

	// Pool-factory event — observed at the factory contract, used
	// for runtime pool enumeration.
	EventDeploy = "deploy"
)

// Mainnet V2 contract addresses — verified 2026-04-22 via
// stellar.expert; cross-referenced against
// docs/discovery/dexes-amms/blend.md and the Blend Capital
// blend-contracts-v2 deploy manifest.
const (
	// MainnetPoolFactory is the documented Pool Factory V2. Blend was
	// REDEPLOYED, so this is not the only factory — see MainnetPoolFactories.
	MainnetPoolFactory = "CDSYOAVXFY7SM5S64IZPPPYB4GVGGLMQVFREPSQQEZVIWXX5R23G4QSU"
	// MainnetPoolFactoryV1 is the earlier pool factory. Verified empirically
	// from the r1 lake (2026-06-12): it emits `deploy` events with an
	// ScVal::Address body (the blend deploy shape) and its children include
	// 4 of the 9 known auction-emitting Blend pools (CDVQVKOY, CBP7NO6F,
	// CDE65QK2, CAQF5KNO) — pools the V2-only gate silently dropped. Its
	// first deploy is at ledger 51_499_915.
	MainnetPoolFactoryV1 = "CCZD6ESMOGMPWH2KRO4O7RGTAPGTUPFWFQBELQSS7ZUK63V3TZWETGAG"
	MainnetBackstop      = "CAQQR5SWBXKIGZKPBZDH3KM5GQ5GUTPKB7JAFCINLZBC5WXPJKRG3IM7"
)

// MainnetPoolFactories is the COMPLETE, empirically-verified set of Blend
// pool factories on mainnet (ADR-0035). A protocol can have more than one
// factory (Blend was redeployed); gating the `deploy` event on a single
// factory silently drops the other factory's pools. Derived by decoding
// every `deploy` event in the lake, keeping the emitters whose body is an
// ScVal::Address (the blend deploy shape), and confirming their children
// cover all 9 known Blend pools — both factories together deploy 27 pools,
// all 9 active ones accounted for (verification: decode `deploy` bodies and
// cross-reference the auction-emitting contracts; re-run if a third factory
// appears). Order is irrelevant (set membership).
var MainnetPoolFactories = []string{
	MainnetPoolFactoryV1,
	MainnetPoolFactory,
}

// FactoryGenesisLedger is the first ledger at which ANY Blend pool factory
// could have emitted a `deploy` — the V1 factory's first deploy
// (2025-04-14, ledger 51_499_915, rounded down). The pool-registry genesis
// seed (ADR-0035) walks every factory's deploy events from here, and the
// ADR-0033 reconcile uses it as the blend source genesis. No pool can
// predate its factory, so this is the lower bound for the fan-out.
const FactoryGenesisLedger uint32 = 51_499_546

// Pre-encoded base64 SCVal::Symbol blobs, computed at init via
// scval.MustEncodeSymbol. Used for byte-equality classification
// against incoming event topics (cheaper than re-decoding the
// topic on every event).
var (
	// Auction events.
	TopicSymbolNewAuction    = scval.MustEncodeSymbol(EventNewAuction)
	TopicSymbolFillAuction   = scval.MustEncodeSymbol(EventFillAuction)
	TopicSymbolDeleteAuction = scval.MustEncodeSymbol(EventDeleteAuction)

	// Money-market events.
	TopicSymbolSupply             = scval.MustEncodeSymbol(EventSupply)
	TopicSymbolWithdraw           = scval.MustEncodeSymbol(EventWithdraw)
	TopicSymbolSupplyCollateral   = scval.MustEncodeSymbol(EventSupplyCollateral)
	TopicSymbolWithdrawCollateral = scval.MustEncodeSymbol(EventWithdrawCollateral)
	TopicSymbolBorrow             = scval.MustEncodeSymbol(EventBorrow)
	TopicSymbolRepay              = scval.MustEncodeSymbol(EventRepay)
	TopicSymbolFlashLoan          = scval.MustEncodeSymbol(EventFlashLoan)
	TopicSymbolGulp               = scval.MustEncodeSymbol(EventGulp)
	TopicSymbolClaim              = scval.MustEncodeSymbol(EventClaim)

	// Credit-risk + emissions.
	TopicSymbolBadDebt          = scval.MustEncodeSymbol(EventBadDebt)
	TopicSymbolDefaultedDebt    = scval.MustEncodeSymbol(EventDefaultedDebt)
	TopicSymbolReserveEmissions = scval.MustEncodeSymbol(EventReserveEmissions)
	TopicSymbolGulpEmissions    = scval.MustEncodeSymbol(EventGulpEmissions)

	// Admin / status.
	TopicSymbolSetAdmin         = scval.MustEncodeSymbol(EventSetAdmin)
	TopicSymbolUpdatePool       = scval.MustEncodeSymbol(EventUpdatePool)
	TopicSymbolQueueSetReserve  = scval.MustEncodeSymbol(EventQueueSetReserve)
	TopicSymbolCancelSetReserve = scval.MustEncodeSymbol(EventCancelSetReserve)
	TopicSymbolSetReserve       = scval.MustEncodeSymbol(EventSetReserve)
	TopicSymbolSetStatus        = scval.MustEncodeSymbol(EventSetStatus)

	// Pool factory.
	TopicSymbolDeploy = scval.MustEncodeSymbol(EventDeploy)
)

// AuctionType discriminator — verified against
// pool/src/auctions/auction.rs constants. The contract emits
// `auction_type: u32` as topic[1] on every auction event.
const (
	AuctionTypeUserLiquidation uint32 = 0
	AuctionTypeBadDebt         uint32 = 1
	AuctionTypeInterest        uint32 = 2
)

// Errors returned by the decode path. Callers classify via
// errors.Is.
var (
	// ErrNotBlendEvent — topic[0] doesn't match any of the names we
	// track. Returned by classify(); the dispatcher uses this to
	// skip cheaply rather than retry.
	ErrNotBlendEvent = errors.New("blend: not a tracked Blend event")

	// ErrMalformedPayload — topic arity / body shape / type tags
	// don't match what blend-contracts-v2 emits. Per-event-fail-loud
	// rather than silent skip; surfaces decoder vs WASM drift.
	ErrMalformedPayload = errors.New("blend: malformed event payload")

	// ErrUnknownAuctionType — auction_type in topic[1] is outside
	// {0=UserLiquidation, 1=BadDebt, 2=Interest}. Indicates a
	// contract upgrade introducing a new auction kind we haven't
	// audited.
	ErrUnknownAuctionType = errors.New("blend: unknown auction type")
)
