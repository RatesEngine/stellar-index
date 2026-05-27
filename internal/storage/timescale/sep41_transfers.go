package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// SEP41TransferKind discriminates the four audit-trail variants
// the sep41_transfers hypertable owns. mint/burn/clawback are NOT
// here — they belong to sep41_supply_events (Algorithm 3).
type SEP41TransferKind string

const (
	SEP41Transfer      SEP41TransferKind = "transfer"
	SEP41Approve       SEP41TransferKind = "approve"
	SEP41SetAdmin      SEP41TransferKind = "set_admin"
	SEP41SetAuthorized SEP41TransferKind = "set_authorized"
)

func (k SEP41TransferKind) IsValid() bool {
	switch k {
	case SEP41Transfer, SEP41Approve, SEP41SetAdmin, SEP41SetAuthorized:
		return true
	}
	return false
}

// SEP41TransferRow mirrors migration 0047's column set. Nil/zero
// values flow to SQL NULL where non-applicable for the kind.
type SEP41TransferRow struct {
	ContractID      string
	Ledger          uint32
	TxHash          string
	OpIndex         uint32
	EventIndex      uint32
	ObservedAt      time.Time
	Kind            SEP41TransferKind
	FromAddr        string
	ToAddr          string
	Amount          *big.Int
	LiveUntilLedger uint32
	Authorized      *bool
}

// InsertSEP41TransferBatch persists rows via a single multi-row
// INSERT. Idempotent on the full PK via ON CONFLICT DO NOTHING.
//
//nolint:gocognit,gocyclo // per-row validation + 12-col placeholder builder; linear.
func (s *Store) InsertSEP41TransferBatch(ctx context.Context, rows []SEP41TransferRow) error {
	if len(rows) == 0 {
		return nil
	}
	for i := range rows {
		r := &rows[i]
		if r.ContractID == "" {
			return fmt.Errorf("timescale: InsertSEP41TransferBatch: row %d empty ContractID", i)
		}
		if r.TxHash == "" {
			return fmt.Errorf("timescale: InsertSEP41TransferBatch: row %d empty TxHash", i)
		}
		if !r.Kind.IsValid() {
			return fmt.Errorf("timescale: InsertSEP41TransferBatch: row %d invalid Kind %q", i, r.Kind)
		}
		switch r.Kind {
		case SEP41Transfer, SEP41Approve:
			if r.Amount == nil {
				return fmt.Errorf("timescale: InsertSEP41TransferBatch: row %d %s missing Amount", i, r.Kind)
			}
			if r.Amount.Sign() < 0 {
				return fmt.Errorf("timescale: InsertSEP41TransferBatch: row %d %s negative Amount %s", i, r.Kind, r.Amount)
			}
		}
		if r.Kind == SEP41SetAuthorized && r.Authorized == nil {
			return fmt.Errorf("timescale: InsertSEP41TransferBatch: row %d set_authorized missing Authorized", i)
		}
	}

	const ncols = 12
	var sb strings.Builder
	sb.WriteString(`
        INSERT INTO sep41_transfers (
            ledger_close_time, ledger, tx_hash, op_index, event_index,
            contract_id, event_kind,
            from_addr, to_addr,
            amount, live_until_ledger, authorized
        ) VALUES `)
	args := make([]any, 0, ncols*len(rows))
	for i := range rows {
		if i > 0 {
			sb.WriteString(", ")
		}
		base := i * ncols
		fmt.Fprintf(&sb,
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6,
			base+7, base+8, base+9, base+10, base+11, base+12,
		)
		r := &rows[i]
		args = append(args,
			r.ObservedAt.UTC(),
			int64(r.Ledger),
			r.TxHash,
			int16(r.OpIndex),
			int16(r.EventIndex),
			r.ContractID,
			string(r.Kind),
			nullStrXfer(r.FromAddr),
			nullStrXfer(r.ToAddr),
			nullNumericFromBigXfer(r.Amount),
			nullU32Xfer(r.LiveUntilLedger, r.Kind == SEP41Approve),
			nullBoolXfer(r.Authorized),
		)
	}
	sb.WriteString(` ON CONFLICT (ledger_close_time, contract_id, ledger, tx_hash, op_index, event_index) DO NOTHING`)

	if _, err := s.db.ExecContext(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("timescale: InsertSEP41TransferBatch (%d rows): %w", len(rows), err)
	}
	return nil
}

// InsertSEP41Transfer is the single-row convenience wrapper.
func (s *Store) InsertSEP41Transfer(ctx context.Context, r SEP41TransferRow) error {
	return s.InsertSEP41TransferBatch(ctx, []SEP41TransferRow{r})
}

// ListSEP41Transfers returns the most-recent N rows for a contract,
// optionally filtered by from_addr / to_addr. Powers GET
// /v1/contracts/{id}/transfers.
//
//nolint:gocognit,gocyclo // linear query-build + row-scan loop.
func (s *Store) ListSEP41Transfers(ctx context.Context, contractID, fromAddr, toAddr string, limit int) ([]SEP41TransferRow, error) {
	if contractID == "" {
		return nil, errors.New("timescale: ListSEP41Transfers: empty contractID")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	var sb strings.Builder
	sb.WriteString(`
        SELECT
            ledger_close_time, ledger, tx_hash, op_index, event_index,
            contract_id, event_kind,
            from_addr, to_addr,
            amount::text, live_until_ledger, authorized
        FROM sep41_transfers
        WHERE contract_id = $1
    `)
	args := []any{contractID}
	if fromAddr != "" {
		args = append(args, fromAddr)
		fmt.Fprintf(&sb, " AND from_addr = $%d", len(args))
	}
	if toAddr != "" {
		args = append(args, toAddr)
		fmt.Fprintf(&sb, " AND to_addr = $%d", len(args))
	}
	args = append(args, limit)
	fmt.Fprintf(&sb,
		" ORDER BY ledger_close_time DESC, ledger DESC, op_index DESC LIMIT $%d",
		len(args),
	)

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("timescale: ListSEP41Transfers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []SEP41TransferRow
	for rows.Next() {
		var (
			r          SEP41TransferRow
			ledger     int64
			opIdx      int16
			eventIdx   int16
			kind       string
			fromNull   sql.NullString
			toNull     sql.NullString
			amountStr  sql.NullString
			liveNull   sql.NullInt32
			authorized sql.NullBool
		)
		if err := rows.Scan(
			&r.ObservedAt, &ledger, &r.TxHash, &opIdx, &eventIdx,
			&r.ContractID, &kind,
			&fromNull, &toNull,
			&amountStr, &liveNull, &authorized,
		); err != nil {
			return nil, fmt.Errorf("timescale: ListSEP41Transfers scan: %w", err)
		}
		r.Ledger = uint32(ledger)
		r.OpIndex = uint32(opIdx)
		r.EventIndex = uint32(eventIdx)
		r.Kind = SEP41TransferKind(kind)
		if fromNull.Valid {
			r.FromAddr = fromNull.String
		}
		if toNull.Valid {
			r.ToAddr = toNull.String
		}
		if amountStr.Valid {
			v, ok := new(big.Int).SetString(amountStr.String, 10)
			if !ok {
				return nil, fmt.Errorf("timescale: ListSEP41Transfers: parse amount %q", amountStr.String)
			}
			r.Amount = v
		}
		if liveNull.Valid {
			r.LiveUntilLedger = uint32(liveNull.Int32)
		}
		if authorized.Valid {
			b := authorized.Bool
			r.Authorized = &b
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountSEP41TransfersInRange returns the row count in [from, to].
func (s *Store) CountSEP41TransfersInRange(ctx context.Context, from, to uint32) (int64, error) {
	if to < from {
		return 0, errors.New("timescale: CountSEP41TransfersInRange: to < from")
	}
	const q = `SELECT count(*) FROM sep41_transfers WHERE ledger BETWEEN $1 AND $2`
	var n int64
	if err := s.db.QueryRowContext(ctx, q, int64(from), int64(to)).Scan(&n); err != nil {
		return 0, fmt.Errorf("timescale: CountSEP41TransfersInRange [%d,%d]: %w", from, to, err)
	}
	return n, nil
}

func nullStrXfer(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

func nullNumericFromBigXfer(amt *big.Int) sql.NullString {
	if amt == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: amt.String(), Valid: true}
}

func nullU32Xfer(v uint32, applicable bool) sql.NullInt32 {
	if !applicable {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(v), Valid: true} //nolint:gosec // u32 -> int32 reinterpret.
}

func nullBoolXfer(b *bool) sql.NullBool {
	if b == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *b, Valid: true}
}
