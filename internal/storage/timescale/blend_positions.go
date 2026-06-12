package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/blend"
)

// InsertBlendPositionEvent appends one money-market position-change
// event (supply / withdraw / supply_collateral / withdraw_collateral
// / borrow / repay / flash_loan) to the blend_positions hypertable.
// Idempotent on the PK (pool, ledger, tx_hash, op_index, event_kind,
// ledger_close_time) — re-running over the same range is a no-op
// rather than producing duplicates.
//
// i128 amounts are written as decimal strings to the NUMERIC
// column (ADR-0003 — full precision preserved through Go's
// *big.Int → decimal-text → NUMERIC chain).
//
// Defensive: rejects empty Pool / TxHash and an invalid Kind
// before touching the DB. A nil amount is treated as "0" and
// surfaces in the row — the decoder should never produce one,
// but stamping zero here is more useful than failing the insert
// on a defaulted struct value.
func (s *Store) InsertBlendPositionEvent(ctx context.Context, e blend.PositionEvent) error {
	if e.Pool == "" {
		return errors.New("timescale: InsertBlendPositionEvent: Pool is empty")
	}
	if e.TxHash == "" {
		return errors.New("timescale: InsertBlendPositionEvent: TxHash is empty")
	}
	if !isBlendPositionKind(e.Kind) {
		return fmt.Errorf("timescale: InsertBlendPositionEvent: invalid Kind %q", e.Kind)
	}

	const q = `
        INSERT INTO blend_positions (
            pool, ledger, tx_hash, op_index, event_index, ledger_close_time,
            event_kind, asset, user_address,
            token_amount, b_or_d_amount,
            counterparty
        ) VALUES (
            $1, $2, $3, $4, $5, $6,
            $7, $8, $9,
            $10::numeric, $11::numeric,
            $12
        )
        ON CONFLICT (pool, ledger, tx_hash, op_index, event_kind, event_index, ledger_close_time) DO NOTHING
    `
	var counterparty sql.NullString
	if e.Counterparty != "" {
		counterparty = sql.NullString{String: e.Counterparty, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, q,
		e.Pool, int(e.Ledger), e.TxHash, int(e.OpIndex), int(e.EventIndex), e.Timestamp.UTC(),
		e.Kind, e.Asset, e.User,
		bigIntToNumericString(e.TokenAmount), bigIntToNumericString(e.BOrDAmount),
		counterparty,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendPositionEvent %s@%d: %w", e.Pool, e.Ledger, err)
	}
	return nil
}

// isBlendPositionKind reports whether kind is one of the seven
// money-market position event kinds. Mirrors the CHECK constraint
// in migration 0042.
func isBlendPositionKind(kind string) bool {
	switch kind {
	case blend.EventSupply,
		blend.EventWithdraw,
		blend.EventSupplyCollateral,
		blend.EventWithdrawCollateral,
		blend.EventBorrow,
		blend.EventRepay,
		blend.EventFlashLoan:
		return true
	}
	return false
}

// bigIntToNumericString converts a *big.Int amount to the decimal
// string the postgres driver hands to a NUMERIC column verbatim.
// Nil becomes "0" — defensive default to keep the insert successful
// rather than producing a NOT NULL violation on a malformed event
// the decoder shouldn't have produced.
func bigIntToNumericString(n *big.Int) string {
	if n == nil {
		return "0"
	}
	return n.String()
}
