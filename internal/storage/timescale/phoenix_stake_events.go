package timescale

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// PhoenixStakeAction discriminates the two stake-contract actions.
// String values match the phoenix_stake_events.action CHECK
// constraint (migration 0044) and internal/sources/phoenix's
// EventActionBond / EventActionUnbond constants.
type PhoenixStakeAction string

const (
	PhoenixBond   PhoenixStakeAction = "bond"
	PhoenixUnbond PhoenixStakeAction = "unbond"
)

// IsValid reports whether a is one of the two known actions.
func (a PhoenixStakeAction) IsValid() bool {
	switch a {
	case PhoenixBond, PhoenixUnbond:
		return true
	}
	return false
}

// PhoenixStakeEvent is one phoenix_stake_events row — a single
// observed bond / unbond event from a Phoenix per-pool stake
// contract. Mirrors the migration-0044 columns.
//
// Amount is a decimal-string numeric (per ADR-0003 i128 ->
// *big.Int -> string), always positive — the action discriminator
// carries the direction.
type PhoenixStakeEvent struct {
	StakeContract string
	Ledger        uint32
	ObservedAt    time.Time
	TxHash        string
	OpIndex       uint32
	Action        PhoenixStakeAction
	User          string
	LPToken       string
	Amount        string // decimal i128
}

// InsertPhoenixStakeEvent appends one phoenix_stake_events row,
// idempotent on the (ledger_close_time, stake_contract, ledger,
// tx_hash, op_index, action) PK. Re-running the indexer over the
// same range or replaying a backfill writes the same rows;
// ON CONFLICT DO NOTHING makes the replay a no-op.
//
// Defensive: rejects empty StakeContract / TxHash / User / LPToken,
// an invalid Action, and an empty Amount before touching the DB.
func (s *Store) InsertPhoenixStakeEvent(ctx context.Context, e PhoenixStakeEvent) error {
	if e.StakeContract == "" {
		return errors.New("timescale: InsertPhoenixStakeEvent: StakeContract is empty")
	}
	if e.TxHash == "" {
		return errors.New("timescale: InsertPhoenixStakeEvent: TxHash is empty")
	}
	if e.User == "" {
		return errors.New("timescale: InsertPhoenixStakeEvent: User is empty")
	}
	if e.LPToken == "" {
		return errors.New("timescale: InsertPhoenixStakeEvent: LPToken is empty")
	}
	if !e.Action.IsValid() {
		return fmt.Errorf("timescale: InsertPhoenixStakeEvent: invalid Action %q", e.Action)
	}
	if e.Amount == "" {
		return fmt.Errorf("timescale: InsertPhoenixStakeEvent: Amount required (contract=%s tx=%s)",
			e.StakeContract, e.TxHash)
	}

	const q = `
        INSERT INTO phoenix_stake_events (
            stake_contract, ledger, ledger_close_time, tx_hash, op_index,
            action, user_addr, lp_token, amount
        ) VALUES (
            $1, $2, $3, $4, $5,
            $6, $7, $8, $9
        )
        ON CONFLICT (ledger_close_time, stake_contract, ledger, tx_hash, op_index, action) DO NOTHING
    `
	_, err := s.db.ExecContext(ctx, q,
		e.StakeContract, int(e.Ledger), e.ObservedAt.UTC(), e.TxHash, int(e.OpIndex),
		string(e.Action), e.User, e.LPToken, e.Amount,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertPhoenixStakeEvent %s@%d: %w",
			e.StakeContract, e.Ledger, err)
	}
	return nil
}
