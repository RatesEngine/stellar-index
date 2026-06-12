package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// CometLiquidityKind discriminates the four Comet liquidity-mutating
// events. String values match the comet_liquidity.event_kind CHECK
// constraint (migration 0042) and the LiquidityKind constants in
// internal/sources/comet/events.go.
type CometLiquidityKind string

const (
	CometLiquidityJoinPool CometLiquidityKind = "join_pool"
	CometLiquidityExitPool CometLiquidityKind = "exit_pool"
	CometLiquidityDeposit  CometLiquidityKind = "deposit"
	CometLiquidityWithdraw CometLiquidityKind = "withdraw"
)

// IsValid reports whether k is one of the four known kinds.
func (k CometLiquidityKind) IsValid() bool {
	switch k {
	case CometLiquidityJoinPool, CometLiquidityExitPool,
		CometLiquidityDeposit, CometLiquidityWithdraw:
		return true
	}
	return false
}

// Direction returns the add/remove polarity for the kind, matching
// the comet_liquidity.direction CHECK constraint. Empty string for
// an invalid kind (caller should reject via IsValid first).
func (k CometLiquidityKind) Direction() string {
	switch k {
	case CometLiquidityJoinPool, CometLiquidityDeposit:
		return "add"
	case CometLiquidityExitPool, CometLiquidityWithdraw:
		return "remove"
	}
	return ""
}

// CometLiquidityEvent is one comet_liquidity row — a single observed
// (POOL, join_pool | exit_pool | deposit | withdraw) Comet event.
// Mirrors the migration-0042 columns.
//
// PoolAmountIn is populated only for withdraw events (the count of
// BPT-share tokens burned in exchange for the underlying); the
// writer translates a zero Amount to SQL NULL on the other three
// kinds.
type CometLiquidityEvent struct {
	ContractID      string
	Ledger          uint32
	LedgerCloseTime time.Time
	TxHash          string
	OpIndex         uint32
	// EventIndex is the contract event's index within its operation —
	// the per-event discriminator added to the comet_liquidity PK by
	// migration 0059 (F-1324) so two same-(kind,token) events from one
	// op don't collide.
	EventIndex   uint32
	Kind         CometLiquidityKind
	Caller       string
	Token        string
	Amount       canonical.Amount
	PoolAmountIn canonical.Amount // withdraw-only; zero on the other kinds
}

// InsertCometLiquidity appends one Comet liquidity event row,
// idempotent on the (ledger_close_time, contract_id, ledger,
// tx_hash, op_index, event_kind, token, event_index) PK (event_index
// added by migration 0059 / F-1324 so two same-(kind,token) events
// from one op don't collide). Re-running the indexer or a backfill
// over the same range writes the same rows; ON CONFLICT DO NOTHING
// makes the replay a no-op.
//
// Defensive: rejects empty ContractID / TxHash / Caller / Token, an
// invalid Kind, and a non-positive Amount before touching the DB —
// the decoder already enforces these but the writer double-checks
// so a malformed Event coming from an integration test or fuzz
// harness can't silently land bad rows.
func (s *Store) InsertCometLiquidity(ctx context.Context, e CometLiquidityEvent) error {
	if e.ContractID == "" {
		return errors.New("timescale: InsertCometLiquidity: ContractID is empty")
	}
	if e.TxHash == "" {
		return errors.New("timescale: InsertCometLiquidity: TxHash is empty")
	}
	if e.Caller == "" {
		return errors.New("timescale: InsertCometLiquidity: Caller is empty")
	}
	if e.Token == "" {
		return errors.New("timescale: InsertCometLiquidity: Token is empty")
	}
	if !e.Kind.IsValid() {
		return fmt.Errorf("timescale: InsertCometLiquidity: invalid Kind %q", e.Kind)
	}
	if e.Amount.Sign() <= 0 {
		return fmt.Errorf("timescale: InsertCometLiquidity: Amount must be > 0 (got %s)", e.Amount)
	}

	// pool_amount_in is withdraw-only. For withdraw it must be > 0
	// (the contract burns BPT shares — a zero burn would be a bug);
	// for the other kinds the column writes NULL.
	var poolAmountIn sql.NullString
	if e.Kind == CometLiquidityWithdraw {
		if e.PoolAmountIn.Sign() <= 0 {
			return fmt.Errorf("timescale: InsertCometLiquidity: withdraw PoolAmountIn must be > 0 (got %s)", e.PoolAmountIn)
		}
		poolAmountIn = sql.NullString{String: e.PoolAmountIn.String(), Valid: true}
	}

	const q = `
        INSERT INTO comet_liquidity (
            contract_id, ledger, ledger_close_time, tx_hash, op_index,
            event_kind, event_index, direction, caller, token, amount, pool_amount_in
        ) VALUES (
            $1, $2, $3, $4, $5,
            $6, $7, $8, $9, $10, $11, $12
        )
        ON CONFLICT (ledger_close_time, contract_id, ledger, tx_hash,
                     op_index, event_kind, token, event_index) DO NOTHING
    `
	_, err := s.db.ExecContext(ctx, q,
		e.ContractID, int(e.Ledger), e.LedgerCloseTime.UTC(),
		e.TxHash, int(e.OpIndex),
		string(e.Kind), int(e.EventIndex), e.Kind.Direction(),
		e.Caller, e.Token,
		e.Amount.String(), poolAmountIn,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertCometLiquidity %s@%d: %w", e.ContractID, e.Ledger, err)
	}
	return nil
}
