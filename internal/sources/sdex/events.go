// Package sdex decodes classic-Stellar DEX trades from
// LedgerCloseMeta operation results. Unlike the four Soroban
// source packages, SDEX doesn't emit contract events — trades are
// implicit in the results of ManageSellOffer / ManageBuyOffer /
// CreatePassiveSellOffer / PathPayment* operations. The dispatcher
// hands this package the (op, result) pair via OpContext and we
// iterate `OffersClaimed` to yield one canonical.Trade per
// ClaimAtom.
//
// Reference implementation: go-stellar-sdk/stellar-extract/trades.go
// covers the OrderBook + V0 variants; we add LiquidityPool on top
// (ADR-0001 rules out Horizon-style classic-DEX APIs so we parse
// XDR directly).
package sdex

import "errors"

// SourceName is the canonical identifier for SDEX-produced trades.
// Stamped into canonical.Trade.Source and used for per-source
// metrics labels + lint-allowlist placement.
const SourceName = "sdex"

// Errors returned by the decode path.
var (
	// ErrUnknownClaimAtomType — a ClaimAtom.Type we don't yet
	// decode (V0 on recent ledgers, or a future variant). Surfaced
	// as a per-entry decode error so sustained-rate alerts fire
	// when new claim types ship.
	ErrUnknownClaimAtomType = errors.New("sdex: unknown ClaimAtom type")

	// ErrMalformedClaimAtom — claim atom fields don't match the
	// expected shape (zero amounts, invalid asset, unreachable
	// account). Usually indicates a protocol bump we haven't
	// audited.
	ErrMalformedClaimAtom = errors.New("sdex: malformed ClaimAtom")
)
