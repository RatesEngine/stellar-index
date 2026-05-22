package timescale

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// CCTPEventType discriminates the four Circle CCTP v2 event variants.
// String values match the cctp_events.event_type CHECK constraint
// (migration 0038) and internal/sources/cctp's event-name constants.
type CCTPEventType string

const (
	CCTPDepositForBurn  CCTPEventType = "deposit_for_burn"
	CCTPMintAndWithdraw CCTPEventType = "mint_and_withdraw"
	CCTPMessageSent     CCTPEventType = "message_sent"
	CCTPMessageReceived CCTPEventType = "message_received"
)

// IsValid reports whether t is one of the four known CCTP events.
func (t CCTPEventType) IsValid() bool {
	switch t {
	case CCTPDepositForBurn, CCTPMintAndWithdraw, CCTPMessageSent, CCTPMessageReceived:
		return true
	}
	return false
}

// CCTPEvent is one cctp_events row — a single observed Circle CCTP v2
// contract event on Stellar. Mirrors the migration-0038 columns.
//
// Amount / Fee / Token are decimal-or-strkey strings; the empty
// string means "this event type carries no such field" and writes
// SQL NULL. CounterpartyDomain is a *uint32 for the same reason
// (message_sent / mint_and_withdraw carry no domain). Attributes
// holds the event-type-specific remainder as a jsonb blob.
type CCTPEvent struct {
	ContractID         string
	Ledger             uint32
	TxHash             string
	OpIndex            uint32
	ObservedAt         time.Time
	EventType          CCTPEventType
	Amount             string // decimal i128; "" → NULL
	Fee                string // decimal i128; "" → NULL
	Token              string // Stellar Address strkey; "" → NULL
	CounterpartyDomain *uint32
	Attributes         map[string]any
}

// InsertCCTPEvent appends one CCTP event row, idempotent on the
// (contract_id, ledger, tx_hash, op_index, event_type, ts) PK.
// Re-running the indexer or a backfill over the same range writes
// the same rows; ON CONFLICT DO NOTHING makes the replay a no-op.
//
// Defensive: rejects empty ContractID / TxHash and an invalid
// EventType before touching the DB. Amount / Fee are passed straight
// to the NUMERIC columns as decimal strings — a malformed value (the
// decoder should never produce one) surfaces as a DB error here
// rather than being silently coerced.
func (s *Store) InsertCCTPEvent(ctx context.Context, e CCTPEvent) error {
	if e.ContractID == "" {
		return errors.New("timescale: InsertCCTPEvent: ContractID is empty")
	}
	if e.TxHash == "" {
		return errors.New("timescale: InsertCCTPEvent: TxHash is empty")
	}
	if !e.EventType.IsValid() {
		return fmt.Errorf("timescale: InsertCCTPEvent: invalid EventType %q", e.EventType)
	}

	attrs := []byte("{}")
	if len(e.Attributes) > 0 {
		marshaled, err := json.Marshal(e.Attributes)
		if err != nil {
			return fmt.Errorf("timescale: InsertCCTPEvent: marshal attributes: %w", err)
		}
		attrs = marshaled
	}

	const q = `
        INSERT INTO cctp_events (
            contract_id, ledger, tx_hash, op_index, ts,
            event_type, amount, fee, token, counterparty_domain,
            attributes
        ) VALUES (
            $1, $2, $3, $4, $5,
            $6, $7, $8, $9, $10,
            $11
        )
        ON CONFLICT (contract_id, ledger, tx_hash, op_index, event_type, ts) DO NOTHING
    `
	var (
		domain sql.NullInt64
		token  sql.NullString
	)
	if e.CounterpartyDomain != nil {
		domain = sql.NullInt64{Int64: int64(*e.CounterpartyDomain), Valid: true}
	}
	if e.Token != "" {
		token = sql.NullString{String: e.Token, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, q,
		e.ContractID, int(e.Ledger), e.TxHash, int(e.OpIndex), e.ObservedAt.UTC(),
		string(e.EventType), nullNumeric(e.Amount), nullNumeric(e.Fee), token, domain,
		attrs,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertCCTPEvent %s@%d: %w", e.ContractID, e.Ledger, err)
	}
	return nil
}

// nullNumeric maps an empty string to SQL NULL and any other value to
// a string the postgres driver hands to a NUMERIC column verbatim.
func nullNumeric(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}
