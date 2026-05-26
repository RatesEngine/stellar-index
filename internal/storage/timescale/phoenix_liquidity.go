package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PhoenixLiquidityAction discriminates the two Phoenix pool
// liquidity-management actions. String values match the
// phoenix_liquidity.action CHECK constraint (migration 0044) and
// internal/sources/phoenix's EventActionProvideLiquidity /
// EventActionWithdrawLiquidity constants.
type PhoenixLiquidityAction string

const (
	PhoenixProvideLiquidity  PhoenixLiquidityAction = "provide_liquidity"
	PhoenixWithdrawLiquidity PhoenixLiquidityAction = "withdraw_liquidity"
)

// IsValid reports whether a is one of the two known actions.
func (a PhoenixLiquidityAction) IsValid() bool {
	switch a {
	case PhoenixProvideLiquidity, PhoenixWithdrawLiquidity:
		return true
	}
	return false
}

// PhoenixLiquidityChange is one phoenix_liquidity row — a single
// observed provide_liquidity / withdraw_liquidity event from a
// Phoenix pool (volatile or stableswap). Mirrors the migration-0044
// columns.
//
// Amount fields are decimal-string numerics (per ADR-0003 i128 ->
// *big.Int -> string). TokenA / TokenB / SharesAmount are empty on
// the side that doesn't carry them (withdraw has no token addresses;
// provide has no shares amount) — the writer maps "" → SQL NULL.
type PhoenixLiquidityChange struct {
	Pool         string
	Ledger       uint32
	ObservedAt   time.Time
	TxHash       string
	OpIndex      uint32
	Action       PhoenixLiquidityAction
	Sender       string
	TokenA       string // provide-only; "" → NULL
	TokenB       string // provide-only; "" → NULL
	AmountA      string // decimal i128 — both actions
	AmountB      string // decimal i128 — both actions
	SharesAmount string // withdraw-only; "" → NULL
}

// InsertPhoenixLiquidityChange appends one phoenix_liquidity row,
// idempotent on the (ledger_close_time, pool, ledger, tx_hash,
// op_index, action) PK. Re-running the indexer over the same range
// or replaying a backfill writes the same rows; ON CONFLICT DO
// NOTHING makes the replay a no-op.
//
// Defensive: rejects empty Pool / TxHash / Sender, an invalid
// Action, and empty NUMERIC amounts (AmountA / AmountB are NOT NULL)
// before touching the DB.
func (s *Store) InsertPhoenixLiquidityChange(ctx context.Context, e PhoenixLiquidityChange) error {
	if e.Pool == "" {
		return errors.New("timescale: InsertPhoenixLiquidityChange: Pool is empty")
	}
	if e.TxHash == "" {
		return errors.New("timescale: InsertPhoenixLiquidityChange: TxHash is empty")
	}
	if e.Sender == "" {
		return errors.New("timescale: InsertPhoenixLiquidityChange: Sender is empty")
	}
	if !e.Action.IsValid() {
		return fmt.Errorf("timescale: InsertPhoenixLiquidityChange: invalid Action %q", e.Action)
	}
	if e.AmountA == "" || e.AmountB == "" {
		return fmt.Errorf("timescale: InsertPhoenixLiquidityChange: AmountA/AmountB required (pool=%s tx=%s)",
			e.Pool, e.TxHash)
	}

	// Provide events do not emit token addresses on the withdraw side
	// and do not emit shares on the provide side. The caller is
	// expected to honour this; we surface NULL accordingly.
	var (
		tokenA sql.NullString
		tokenB sql.NullString
		shares sql.NullString
	)
	if e.TokenA != "" {
		tokenA = sql.NullString{String: e.TokenA, Valid: true}
	}
	if e.TokenB != "" {
		tokenB = sql.NullString{String: e.TokenB, Valid: true}
	}
	if e.SharesAmount != "" {
		shares = sql.NullString{String: e.SharesAmount, Valid: true}
	}

	const q = `
        INSERT INTO phoenix_liquidity (
            pool, ledger, ledger_close_time, tx_hash, op_index,
            action, sender, token_a, token_b, amount_a, amount_b,
            shares_amount
        ) VALUES (
            $1, $2, $3, $4, $5,
            $6, $7, $8, $9, $10, $11,
            $12
        )
        ON CONFLICT (ledger_close_time, pool, ledger, tx_hash, op_index, action) DO NOTHING
    `
	_, err := s.db.ExecContext(ctx, q,
		e.Pool, int(e.Ledger), e.ObservedAt.UTC(), e.TxHash, int(e.OpIndex),
		string(e.Action), e.Sender, tokenA, tokenB, e.AmountA, e.AmountB,
		shares,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertPhoenixLiquidityChange %s@%d: %w",
			e.Pool, e.Ledger, err)
	}
	return nil
}
