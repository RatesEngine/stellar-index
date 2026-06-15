package timescale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// BlendBackstopEventType discriminates the ten Blend Backstop event
// variants. String values match the blend_backstop_events.event_kind
// CHECK constraint (migration 0063) and internal/sources/blend_backstop's
// event-name constants.
type BlendBackstopEventType string

const (
	BackstopDeposit           BlendBackstopEventType = "deposit"
	BackstopClaim             BlendBackstopEventType = "claim"
	BackstopDonate            BlendBackstopEventType = "donate"
	BackstopQueueWithdrawal   BlendBackstopEventType = "queue_withdrawal"
	BackstopWithdraw          BlendBackstopEventType = "withdraw"
	BackstopDistribute        BlendBackstopEventType = "distribute"
	BackstopGulpEmissions     BlendBackstopEventType = "gulp_emissions"
	BackstopDequeueWithdrawal BlendBackstopEventType = "dequeue_withdrawal"
	BackstopDraw              BlendBackstopEventType = "draw"
	BackstopRwZoneAdd         BlendBackstopEventType = "rw_zone_add"
)

// IsValid reports whether t is one of the ten known backstop events.
func (t BlendBackstopEventType) IsValid() bool {
	switch t {
	case BackstopDeposit, BackstopClaim, BackstopDonate,
		BackstopQueueWithdrawal, BackstopWithdraw, BackstopDistribute,
		BackstopGulpEmissions, BackstopDequeueWithdrawal, BackstopDraw,
		BackstopRwZoneAdd:
		return true
	}
	return false
}

// BlendBackstopEvent is one blend_backstop_events row — a single
// observed Blend Backstop contract event on Stellar. Mirrors the
// migration-0063 columns.
//
// Pool / UserAddress are strkey strings; the empty string means "this
// event type carries no such field" and writes SQL NULL. Amount /
// Amount2 are decimal i128 strings (ADR-0003 — never int64); empty →
// NULL. Attributes holds the event-type-specific remainder as a jsonb
// blob.
type BlendBackstopEvent struct {
	ContractID  string
	Ledger      uint32
	TxHash      string
	OpIndex     uint32
	EventIndex  uint32
	ObservedAt  time.Time
	EventType   BlendBackstopEventType
	Pool        string // Address strkey; "" → NULL
	UserAddress string // Address strkey; "" → NULL
	Amount      string // decimal i128; "" → NULL
	Amount2     string // decimal i128; "" → NULL
	Attributes  map[string]any
}

// InsertBlendBackstopEvent appends one backstop event row, idempotent
// on the PK (ledger_close_time, ledger, tx_hash, op_index, event_index).
// Re-running the indexer or a replay over the same range writes the
// same rows; ON CONFLICT DO NOTHING makes the replay a no-op.
//
// Defensive: rejects empty ContractID / TxHash and an invalid
// EventType before touching the DB. Amount / Amount2 pass straight to
// the NUMERIC columns as decimal strings — a malformed value (the
// decoder should never produce one) surfaces as a DB error here.
func (s *Store) InsertBlendBackstopEvent(ctx context.Context, e BlendBackstopEvent) error {
	if e.ContractID == "" {
		return errors.New("timescale: InsertBlendBackstopEvent: ContractID is empty")
	}
	if e.TxHash == "" {
		return errors.New("timescale: InsertBlendBackstopEvent: TxHash is empty")
	}
	if !e.EventType.IsValid() {
		return fmt.Errorf("timescale: InsertBlendBackstopEvent: invalid EventType %q", e.EventType)
	}

	attrs := []byte("{}")
	if len(e.Attributes) > 0 {
		marshaled, err := json.Marshal(e.Attributes)
		if err != nil {
			return fmt.Errorf("timescale: InsertBlendBackstopEvent: marshal attributes: %w", err)
		}
		attrs = marshaled
	}

	const q = `
        INSERT INTO blend_backstop_events (
            contract_id, ledger, tx_hash, op_index, event_index, ledger_close_time,
            event_kind, pool, user_address, amount, amount2,
            attributes
        ) VALUES (
            $1, $2, $3, $4, $5, $6,
            $7, $8, $9, $10, $11,
            $12
        )
        ON CONFLICT (ledger_close_time, ledger, tx_hash, op_index, event_index) DO NOTHING
    `
	_, err := s.db.ExecContext(ctx, q,
		e.ContractID, int(e.Ledger), e.TxHash, int(e.OpIndex), int(e.EventIndex), e.ObservedAt.UTC(),
		string(e.EventType), nullString(e.Pool), nullString(e.UserAddress),
		nullNumeric(e.Amount), nullNumeric(e.Amount2),
		attrs,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendBackstopEvent %s@%d: %w", e.ContractID, e.Ledger, err)
	}
	return nil
}
