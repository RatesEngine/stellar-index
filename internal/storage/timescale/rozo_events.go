package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RozoEventType discriminates the two Rozo v1 Payment event variants.
// String values match the rozo_events.event_type CHECK constraint
// (migration 0039) and internal/sources/rozo's event-name constants.
type RozoEventType string

const (
	RozoPayment RozoEventType = "payment"
	RozoFlush   RozoEventType = "flush"
)

// IsValid reports whether t is one of the two known Rozo v1 events.
func (t RozoEventType) IsValid() bool {
	switch t {
	case RozoPayment, RozoFlush:
		return true
	}
	return false
}

// RozoEvent is one rozo_events row — a single observed Rozo v1
// intent-bridge contract event on Stellar. Mirrors the
// migration-0039 columns.
//
// Amount and Destination are present on both event types. From and
// Memo are payment-only; Token is flush-only — each is a *string so
// a nil pointer writes SQL NULL and distinguishes "not applicable to
// this event type" from an empty-string value (an empty payment memo
// is a real value and round-trips as an empty string, not NULL).
type RozoEvent struct {
	ContractID  string
	Ledger      uint32
	TxHash      string
	OpIndex     uint32
	ObservedAt  time.Time
	EventType   RozoEventType
	Amount      string // decimal i128
	Destination string
	From        *string // payment-only
	Memo        *string // payment-only ('' is a valid tag)
	Token       *string // flush-only
}

// InsertRozoEvent appends one Rozo event row, idempotent on the
// (contract_id, ledger, tx_hash, op_index, event_type, ts) PK.
// Re-running the indexer or a backfill over the same range writes
// the same rows; ON CONFLICT DO NOTHING makes the replay a no-op.
//
// Defensive: rejects empty ContractID / TxHash / Destination, an
// invalid EventType, and an empty Amount (the column is NOT NULL)
// before touching the DB.
func (s *Store) InsertRozoEvent(ctx context.Context, e RozoEvent) error {
	if e.ContractID == "" {
		return errors.New("timescale: InsertRozoEvent: ContractID is empty")
	}
	if e.TxHash == "" {
		return errors.New("timescale: InsertRozoEvent: TxHash is empty")
	}
	if e.Destination == "" {
		return errors.New("timescale: InsertRozoEvent: Destination is empty")
	}
	if !e.EventType.IsValid() {
		return fmt.Errorf("timescale: InsertRozoEvent: invalid EventType %q", e.EventType)
	}
	if e.Amount == "" {
		return fmt.Errorf("timescale: InsertRozoEvent: Amount is empty (contract=%s tx=%s)", e.ContractID, e.TxHash)
	}

	const q = `
        INSERT INTO rozo_events (
            contract_id, ledger, tx_hash, op_index, ts,
            event_type, amount, destination, from_addr, memo, token
        ) VALUES (
            $1, $2, $3, $4, $5,
            $6, $7, $8, $9, $10, $11
        )
        ON CONFLICT (contract_id, ledger, tx_hash, op_index, event_type, ts) DO NOTHING
    `
	_, err := s.db.ExecContext(ctx, q,
		e.ContractID, int(e.Ledger), e.TxHash, int(e.OpIndex), e.ObservedAt.UTC(),
		string(e.EventType), e.Amount, e.Destination,
		ptrToNullString(e.From), ptrToNullString(e.Memo), ptrToNullString(e.Token),
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertRozoEvent %s@%d: %w", e.ContractID, e.Ledger, err)
	}
	return nil
}

// ptrToNullString maps a nil *string to SQL NULL and a non-nil one to
// its value (including the empty string, which is a meaningful value
// — see RozoEvent.Memo).
func ptrToNullString(v *string) sql.NullString {
	if v == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *v, Valid: true}
}
