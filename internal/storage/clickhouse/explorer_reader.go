package clickhouse

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ExplorerReader serves the network-explorer read path (ADR-0038) directly
// from the certified Tier-1 lake (ADR-0034): the full chain to genesis —
// ledgers, transactions, operations, contract events — lives in ClickHouse,
// not Postgres. Construct once at startup, reuse across requests, Close at
// shutdown. All reads are by immutable key (ledger_seq / tx_hash), so results
// are cacheable indefinitely.
//
// Phase A scope: ledger + transaction + operation + contract reads. Account
// state (balances) is Phase C and reads a different (to-be-populated) table.
type ExplorerReader struct {
	conn driver.Conn
}

// NewExplorerReader dials ClickHouse (native protocol) with a request-sized
// pool and pings it.
func NewExplorerReader(ctx context.Context, addr string) (*ExplorerReader, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr:            []string{addr},
		Auth:            clickhouse.Auth{Database: "stellar"},
		Settings:        clickhouse.Settings{"max_execution_time": 30},
		DialTimeout:     10 * time.Second,
		ReadTimeout:     30 * time.Second,
		MaxOpenConns:    8,
		MaxIdleConns:    4,
		ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open explorer reader %s: %w", addr, err)
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouse: ping explorer reader %s: %w", addr, err)
	}
	return &ExplorerReader{conn: conn}, nil
}

// Close releases the connection pool.
func (r *ExplorerReader) Close() error { return r.conn.Close() }

// LedgerHeader is one ledger header from stellar.ledgers. Hash fields are hex
// strings as stored. total_coins / fee_pool are XLM stroops (Int64 in the
// lake) — they exceed 2^53 so the API serialises them as strings (ADR-0003).
type LedgerHeader struct {
	Seq               uint32
	CloseTime         time.Time
	LedgerHash        string
	PrevHash          string
	ProtocolVersion   uint32
	TxCount           uint32
	OpCount           uint32
	SorobanEventCount uint32
	TotalCoins        int64
	FeePool           int64
	BaseFee           uint32
	BaseReserve       uint32
}

// TxSummary is one transaction summary from stellar.transactions. Memo is
// already decoded to a string at ingest; memo_type carries the discriminant.
type TxSummary struct {
	Seq            uint32
	CloseTime      time.Time
	TxHash         string
	TxIndex        uint32
	SourceAccount  string
	FeeCharged     int64
	MaxFee         int64
	OperationCount uint16
	Successful     bool
	ResultCode     int32
	MemoType       string
	Memo           string
}

const ledgerCols = `ledger_seq, close_time, ledger_hash, prev_hash, protocol_version,
	tx_count, op_count, soroban_event_count, total_coins, fee_pool, base_fee, base_reserve`

func scanLedger(rows driver.Rows) (LedgerHeader, error) {
	var l LedgerHeader
	err := rows.Scan(&l.Seq, &l.CloseTime, &l.LedgerHash, &l.PrevHash, &l.ProtocolVersion,
		&l.TxCount, &l.OpCount, &l.SorobanEventCount, &l.TotalCoins, &l.FeePool, &l.BaseFee, &l.BaseReserve)
	return l, err
}

// RecentLedgers returns up to `limit` ledgers in descending sequence order. If
// beforeSeq > 0, only ledgers strictly below it are returned (keyset
// pagination — the next page descends from the previous page's last seq).
func (r *ExplorerReader) RecentLedgers(ctx context.Context, limit int, beforeSeq uint32) ([]LedgerHeader, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + ledgerCols + ` FROM stellar.ledgers FINAL`
	args := []any{}
	if beforeSeq > 0 {
		q += ` WHERE ledger_seq < ?`
		args = append(args, beforeSeq)
	}
	q += ` ORDER BY ledger_seq DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: recent ledgers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]LedgerHeader, 0, limit)
	for rows.Next() {
		l, err := scanLedger(rows)
		if err != nil {
			return nil, fmt.Errorf("clickhouse: scan ledger: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LedgerBySeq returns a single ledger header. found=false (nil error) when the
// sequence is absent (out of range / not yet ingested).
func (r *ExplorerReader) LedgerBySeq(ctx context.Context, seq uint32) (LedgerHeader, bool, error) {
	q := `SELECT ` + ledgerCols + ` FROM stellar.ledgers FINAL WHERE ledger_seq = ? LIMIT 1`
	rows, err := r.conn.Query(ctx, q, seq)
	if err != nil {
		return LedgerHeader{}, false, fmt.Errorf("clickhouse: ledger %d: %w", seq, err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return LedgerHeader{}, false, rows.Err()
	}
	l, err := scanLedger(rows)
	if err != nil {
		return LedgerHeader{}, false, fmt.Errorf("clickhouse: scan ledger %d: %w", seq, err)
	}
	return l, true, nil
}

// LedgerTransactions returns the transactions in a ledger, ordered by tx_index.
func (r *ExplorerReader) LedgerTransactions(ctx context.Context, seq uint32, limit int) ([]TxSummary, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	const q = `SELECT ledger_seq, close_time, tx_hash, tx_index, source_account,
		fee_charged, max_fee, operation_count, successful, result_code, memo_type, memo
		FROM stellar.transactions FINAL WHERE ledger_seq = ? ORDER BY tx_index ASC LIMIT ?`
	rows, err := r.conn.Query(ctx, q, seq, limit)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: ledger %d txs: %w", seq, err)
	}
	defer func() { _ = rows.Close() }()
	return scanTxSummaries(rows)
}

func scanTxSummaries(rows driver.Rows) ([]TxSummary, error) {
	var out []TxSummary
	for rows.Next() {
		var t TxSummary
		var ok uint8
		if err := rows.Scan(&t.Seq, &t.CloseTime, &t.TxHash, &t.TxIndex, &t.SourceAccount,
			&t.FeeCharged, &t.MaxFee, &t.OperationCount, &ok, &t.ResultCode, &t.MemoType, &t.Memo); err != nil {
			return nil, fmt.Errorf("clickhouse: scan tx: %w", err)
		}
		t.Successful = ok != 0
		out = append(out, t)
	}
	return out, rows.Err()
}
