package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// GateCounts is the read-back of a ledger range's Tier-1 counts from
// ClickHouse — both the per-ledger STORED counts (the ledgers row the
// extractor wrote) and the ACTUAL row counts of the child tables. The
// completeness gate (ADR-0034 Phase 2 §6 gate 2) asserts these agree with
// each other AND with the decoder-independent census oracle. All reads use
// FINAL so concurrent/duplicate ReplacingMergeTree parts dedup at read time.
type GateCounts struct {
	LedgerRows   uint64 // count() of stellar.ledgers in range (deduped)
	TotalLedgers uint64 // count() of ALL stellar.ledgers (whole DB) — footprint denominator
	MinLedger    uint32
	MaxLedger    uint32

	// Per-ledger counts the extractor stored in stellar.ledgers (summed).
	StoredTx     uint64
	StoredOp     uint64
	StoredEvents uint64
	StoredTrades uint64

	// Actual deduped row counts of the child tables.
	RowTx     uint64
	RowOp     uint64
	RowEvents uint64
}

// TableFootprint is one stellar.* table's on-disk size (compressed) + row
// count, for the Phase 2 §6 gate-1 footprint projection.
type TableFootprint struct {
	Table string
	Bytes uint64
	Rows  uint64
}

// openRead dials ClickHouse for read-only gate queries.
func openRead(ctx context.Context, addr string) (driver.Conn, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr:            []string{addr},
		Auth:            clickhouse.Auth{Database: "stellar"},
		Settings:        clickhouse.Settings{"max_execution_time": 0},
		DialTimeout:     10 * time.Second,
		MaxOpenConns:    2,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open %s: %w", addr, err)
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouse: ping %s: %w", addr, err)
	}
	return conn, nil
}

// ReadGateCounts returns the stored + actual counts for [from, to] inclusive.
func ReadGateCounts(ctx context.Context, addr string, from, to uint32) (GateCounts, error) {
	conn, err := openRead(ctx, addr)
	if err != nil {
		return GateCounts{}, err
	}
	defer func() { _ = conn.Close() }()

	var c GateCounts
	if err := conn.QueryRow(ctx, `
		SELECT count(), min(ledger_seq), max(ledger_seq),
		       sum(tx_count), sum(op_count),
		       sum(soroban_event_count), sum(classic_trade_effect_count)
		FROM stellar.ledgers FINAL
		WHERE ledger_seq BETWEEN ? AND ?`, from, to).
		Scan(&c.LedgerRows, &c.MinLedger, &c.MaxLedger,
			&c.StoredTx, &c.StoredOp, &c.StoredEvents, &c.StoredTrades); err != nil {
		return GateCounts{}, fmt.Errorf("clickhouse: read ledgers gate counts: %w", err)
	}

	rowCount := func(table string) (uint64, error) {
		var n uint64
		q := fmt.Sprintf("SELECT count() FROM stellar.%s FINAL WHERE ledger_seq BETWEEN ? AND ?", table)
		if err := conn.QueryRow(ctx, q, from, to).Scan(&n); err != nil {
			return 0, fmt.Errorf("clickhouse: count %s: %w", table, err)
		}
		return n, nil
	}
	if c.RowTx, err = rowCount("transactions"); err != nil {
		return GateCounts{}, err
	}
	if c.RowOp, err = rowCount("operations"); err != nil {
		return GateCounts{}, err
	}
	if c.RowEvents, err = rowCount("contract_events"); err != nil {
		return GateCounts{}, err
	}
	if err := conn.QueryRow(ctx, "SELECT count() FROM stellar.ledgers FINAL").Scan(&c.TotalLedgers); err != nil {
		return GateCounts{}, fmt.Errorf("clickhouse: count total ledgers: %w", err)
	}
	return c, nil
}

// ReadFootprint returns per-table compressed bytes + row counts for the
// stellar database (active parts only).
func ReadFootprint(ctx context.Context, addr string) ([]TableFootprint, error) {
	conn, err := openRead(ctx, addr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	rows, err := conn.Query(ctx, `
		SELECT table, sum(bytes_on_disk) AS bytes, sum(rows) AS rows
		FROM system.parts
		WHERE database = 'stellar' AND active
		GROUP BY table
		ORDER BY table`)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: read footprint: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []TableFootprint
	for rows.Next() {
		var f TableFootprint
		if err := rows.Scan(&f.Table, &f.Bytes, &f.Rows); err != nil {
			return nil, fmt.Errorf("clickhouse: scan footprint: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
