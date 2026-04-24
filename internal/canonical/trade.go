package canonical

import (
	"fmt"
	"math/big"
	"time"
)

// Trade is one executed trade, observed on a specific venue in a
// specific ledger.
//
// Identity of a trade is (Source, Ledger, TxHash, OpIndex). Across
// restarts and reconciliation passes the same trade must produce
// the same ID — it's the primary key into the `trades` hypertable.
//
// Amounts are [Amount] (big.Int-backed). Price is NOT stored; it is
// derived from QuoteAmount / BaseAmount at query time. Storing a
// derived price would force a precision choice here that belongs at
// the display layer.
//
// Invariant: BaseAmount > 0 and QuoteAmount > 0. A trade with zero
// on either side is an ingestion bug.
type Trade struct {
	// Source is the connector name ("sdex", "soroswap", "aquarius",
	// "binance", …). Must be stable — it's part of the trade ID and
	// appears in API responses.
	Source string `json:"source"`

	// Ledger is the pubnet ledger sequence in which this trade closed.
	// Zero is invalid.
	Ledger uint32 `json:"ledger"`

	// TxHash is the 32-byte transaction hash, hex-encoded
	// (64 lowercase hex chars). Zero or malformed is invalid.
	TxHash string `json:"tx_hash"`

	// OpIndex is the 0-based index of the operation within the
	// transaction. For SDEX ManageOffer, this is the op that closed;
	// for Soroban, it's the invoke-host-function op.
	OpIndex uint32 `json:"op_index"`

	// Timestamp is the ledger close time (UTC). Millisecond precision
	// where the upstream event supplies it; otherwise second-precision
	// from ledger headers.
	Timestamp time.Time `json:"ts"`

	// Pair is the (base, quote) the trade executed against.
	// Direction matches the on-chain event — we do not normalise here.
	Pair Pair `json:"pair"`

	// BaseAmount is the quantity of the base asset that changed hands,
	// in the base asset's smallest unit (stroops for XLM,
	// 10^-decimals for SEP-41).
	BaseAmount Amount `json:"base_amount"`

	// QuoteAmount is the quantity of the quote asset paid for the
	// base, in the quote asset's smallest unit.
	QuoteAmount Amount `json:"quote_amount"`

	// Maker is the account that placed the resting offer (SDEX) or
	// the AMM-pool identity (Soroban DEX). Optional — empty string
	// means "unknown / not applicable". Intentionally not part of
	// trade identity.
	Maker string `json:"maker,omitempty"`

	// Taker is the account that consumed the offer. Optional.
	Taker string `json:"taker,omitempty"`
}

// ID is the stable unique identifier used as the primary key in the
// trades hypertable and as the dedup key across region replicas.
//
// Format: `<source>:<ledger>:<tx_hash>:<op_index>`.
func (t Trade) ID() string {
	return fmt.Sprintf("%s:%d:%s:%d", t.Source, t.Ledger, t.TxHash, t.OpIndex)
}

// Validate returns nil iff every invariant holds. Intended to be
// called by the ingestion pipeline before a write is attempted —
// surfacing a violation as ErrInvalidTrade is always an upstream bug.
//
// Ledger is NOT required to be non-zero. On-chain sources stamp the
// real pubnet sequence; off-chain sources (Binance/Kraken/Bitstamp/
// Coinbase — any venue without a ledger concept) stamp 0 and rely on
// Source + TxHash + OpIndex for uniqueness. The zero-ledger check
// that used to live here caught stub decoders at the cost of
// rejecting valid off-chain inserts; TxHash validation (64-char hex,
// synthesised deterministically for off-chain) already catches stubs.
func (t Trade) Validate() error {
	if t.Source == "" {
		return fmt.Errorf("%w: empty source", ErrInvalidTrade)
	}
	if !validTxHash(t.TxHash) {
		return fmt.Errorf("%w: tx_hash %q is not a 64-char hex string", ErrInvalidTrade, t.TxHash)
	}
	if t.Timestamp.IsZero() {
		return fmt.Errorf("%w: zero timestamp", ErrInvalidTrade)
	}
	if err := t.Pair.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTrade, err)
	}
	if t.BaseAmount.Sign() <= 0 {
		return fmt.Errorf("%w: base_amount must be positive, got %s", ErrInvalidTrade, t.BaseAmount)
	}
	if t.QuoteAmount.Sign() <= 0 {
		return fmt.Errorf("%w: quote_amount must be positive, got %s", ErrInvalidTrade, t.QuoteAmount)
	}
	return nil
}

// PriceRatio returns (numerator, denominator) = (QuoteAmount, BaseAmount).
// Callers scale to the quote-asset-decimals of their choice at the
// display layer. Never returns nil big.Ints.
//
// The pair of *big.Int is deliberately a non-precision-losing form —
// converting to float happens only at the edge (API response / UI).
func (t Trade) PriceRatio() (num, den *big.Int) {
	return t.QuoteAmount.BigInt(), t.BaseAmount.BigInt()
}

// Equal compares two trades by identity only. Fields like Maker/Taker
// are observational — two records of the same trade from different
// regions may differ on Maker if only one region's event included it.
func (t Trade) Equal(o Trade) bool {
	return t.Source == o.Source &&
		t.Ledger == o.Ledger &&
		t.TxHash == o.TxHash &&
		t.OpIndex == o.OpIndex
}

// ─── Internal helpers ──────────────────────────────────────────────

// validTxHash enforces Stellar's canonical lowercase 64-hex-char
// tx-hash form. Uppercase hex (or mixed case) would decode
// successfully but produce duplicate trade rows: Postgres treats
// "DEADBEEF..." and "deadbeef..." as distinct primary-key values,
// so the same on-chain tx ingested from two sources with different
// casing would land as two rows.
//
// Sources MUST lowercase before handing to the canonical types.
// stellar-rpc returns lowercase; SDK XDR→hex renderings are
// lowercase; the constraint is always satisfied by well-behaved
// upstreams. A rejected tx_hash here is always an upstream bug.
func validTxHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < 64; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
