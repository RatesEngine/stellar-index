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

// OpRow is one operation from stellar.operations. OpType is the lake's XDR
// enum string ("OperationTypePayment"); BodyXDR is the base64 body for
// read-time decode (internal/xdrjson). SourceAccount may be empty (the op
// inherits the transaction source).
type OpRow struct {
	Seq           uint32
	CloseTime     time.Time
	TxHash        string
	TxIndex       uint32
	OpIndex       uint32
	OpType        string
	SourceAccount string
	BodyXDR       string
}

const opCols = `ledger_seq, close_time, tx_hash, tx_index, op_index, op_type, source_account, body_xdr`

func scanOps(rows driver.Rows) ([]OpRow, error) {
	var out []OpRow
	for rows.Next() {
		var o OpRow
		if err := rows.Scan(&o.Seq, &o.CloseTime, &o.TxHash, &o.TxIndex, &o.OpIndex,
			&o.OpType, &o.SourceAccount, &o.BodyXDR); err != nil {
			return nil, fmt.Errorf("clickhouse: scan op: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// RecentOperations returns the most-recent operations network-wide,
// newest first, keyset-paged by the composite (ledger_seq, tx_index,
// op_index) cursor. A bare reverse scan from the tip of the table's
// sort key — fast, no extra index. Backs the /v1/operations directory.
func (r *ExplorerReader) RecentOperations(ctx context.Context, limit int, cur ExplorerCursor) ([]OpRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + opCols + ` FROM stellar.operations`
	args := []any{}
	if cur.IsSet() {
		q += ` WHERE (ledger_seq, tx_index, op_index) < (?, ?, ?)`
		args = append(args, cur.Ledger, cur.A, cur.B)
	}
	q += ` ORDER BY ledger_seq DESC, tx_index DESC, op_index DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: recent operations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanOps(rows)
}

// OpTypeCount is one op-type's count in the stats window.
type OpTypeCount struct {
	OpType string
	Count  int64
}

// OperationTypeStats returns the per-op-type operation counts over the
// most-recent `windowLedgers` ledgers (default ~24h at 5 s close
// time). Bounded to the table's tip via `ledger_seq > max - window`,
// so partition pruning keeps it to the last chunk(s). Sorted desc.
func (r *ExplorerReader) OperationTypeStats(ctx context.Context, windowLedgers uint32) ([]OpTypeCount, error) {
	if windowLedgers == 0 {
		windowLedgers = 17280 // ~24h at 5s ledger close
	}
	const q = `SELECT op_type, toInt64(count()) AS c
		FROM stellar.operations
		WHERE ledger_seq > (SELECT max(ledger_seq) FROM stellar.operations) - ?
		GROUP BY op_type
		ORDER BY c DESC`
	rows, err := r.conn.Query(ctx, q, windowLedgers)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: operation type stats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []OpTypeCount
	for rows.Next() {
		var c OpTypeCount
		if err := rows.Scan(&c.OpType, &c.Count); err != nil {
			return nil, fmt.Errorf("clickhouse: scan op-type stat: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ThroughputBucket is one day's network throughput from stellar.ledgers.
type ThroughputBucket struct {
	Day     time.Time
	Ledgers int64
	Txs     int64
	Ops     int64
	Events  int64
}

// NetworkThroughput returns daily network throughput (ledger / tx / op
// / Soroban-event counts) over the most-recent `windowDays` days,
// ascending by day. Aggregates stellar.ledgers (which carries the
// per-ledger counts) bounded to the tip via the ledger-window
// predicate → partition-pruned. windowDays defaults to 30, capped 365.
func (r *ExplorerReader) NetworkThroughput(ctx context.Context, windowDays int) ([]ThroughputBucket, error) {
	if windowDays <= 0 || windowDays > 365 {
		windowDays = 30
	}
	windowLedgers := uint32(windowDays) * 17280 // ~17280 ledgers/day at 5s
	const q = `SELECT toStartOfDay(close_time) AS day,
		toInt64(count())                  AS ledgers,
		toInt64(sum(tx_count))            AS txs,
		toInt64(sum(op_count))            AS ops,
		toInt64(sum(soroban_event_count)) AS events
		FROM stellar.ledgers
		WHERE ledger_seq > (SELECT max(ledger_seq) FROM stellar.ledgers) - ?
		GROUP BY day
		ORDER BY day ASC`
	rows, err := r.conn.Query(ctx, q, windowLedgers)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: network throughput: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ThroughputBucket
	for rows.Next() {
		var b ThroughputBucket
		if err := rows.Scan(&b.Day, &b.Ledgers, &b.Txs, &b.Ops, &b.Events); err != nil {
			return nil, fmt.Errorf("clickhouse: scan throughput: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// OperationsByLedger returns the operations in a ledger, ordered by
// (tx_index, op_index). Ledger-scoped → partition-pruned + fast (no tx_hash
// index needed).
func (r *ExplorerReader) OperationsByLedger(ctx context.Context, seq uint32, limit int) ([]OpRow, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	q := `SELECT ` + opCols + ` FROM stellar.operations FINAL
		WHERE ledger_seq = ? ORDER BY tx_index, op_index LIMIT ?`
	rows, err := r.conn.Query(ctx, q, seq, limit)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: ledger %d ops: %w", seq, err)
	}
	defer func() { _ = rows.Close() }()
	return scanOps(rows)
}

const txCols = `ledger_seq, close_time, tx_hash, tx_index, source_account,
	fee_charged, max_fee, operation_count, successful, result_code, memo_type, memo`

// ExplorerCursor is a composite keyset position for the descending explorer
// listings that can hold MANY rows per ledger (contract events, account
// txs/ops). A scalar ledger-only cursor silently drops the remainder of a
// ledger that straddles a page boundary (a busy AMM emits >limit events in one
// ledger; an MM submits >limit txs in one ledger); the full tuple makes paging
// exact. The zero value (Ledger==0) means "from the newest" (no cursor — first
// page). The A/B fields carry the 2nd/3rd ORDER BY columns and are interpreted
// per-listing: txs use (ledger, tx_index); ops use (ledger, tx_index,
// op_index); events use (ledger, op_index, event_index).
type ExplorerCursor struct {
	Ledger uint32 // ledger_seq — primary sort key (DESC)
	A      uint32 // 2nd sort col: tx_index (txs/ops) | op_index (events)
	B      uint32 // 3rd sort col: op_index (ops) | event_index (events); unused for txs
}

// IsSet reports whether the cursor points past the newest row (i.e. this is a
// continuation page, not the first page).
func (c ExplorerCursor) IsSet() bool { return c.Ledger > 0 }

// AccountTransactions returns transactions INVOLVING an account — both those
// it sourced (source/fee-payer) and those where it's a non-source participant
// in any operation (payment destination, trustor, merge target, …) — newest
// first, keyset-paged by the composite (ledger_seq, tx_index) cursor (ADR-0038
// Phase B). Sourced via the source_account skip-index; incoming via an
// account-prefixed lookup of stellar.operation_participants.
//
// Incoming coverage tracks the participant-index capture + backfill: live
// ingest fills operation_participants going forward, so a tx whose only link
// to the account predates participant capture surfaces once the historical
// re-derive lands.
func (r *ExplorerReader) AccountTransactions(ctx context.Context, account string, limit int, cur ExplorerCursor) ([]TxSummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// Two index-friendly arms UNION'd, NOT `source_account = ? OR … IN (…)`:
	// an OR with a subquery defeats the source_account skip-index and
	// full-scans the 23 B-row table. Arm 1 (sourced) uses the
	// source_account index; arm 2 (participant) matches transactions on its
	// PRIMARY KEY (ledger_seq, tx_index) via op-keys from the account-
	// prefixed operation_participants — both stay index-bounded. DISTINCT
	// dedups the rare tx that is BOTH sourced by the account AND has it as a
	// non-source participant of one of its ops.
	cursorClause := ""
	var cursorArgs []any
	if cur.IsSet() {
		// Tuple comparison: strictly older than the (ledger, tx_index) we last
		// served — never re-emits a served row, never skips an unserved one.
		cursorClause = ` AND (ledger_seq, tx_index) < (?, ?)`
		cursorArgs = []any{cur.Ledger, cur.A}
	}
	q := `SELECT DISTINCT ` + txCols + ` FROM (
		(SELECT ` + txCols + ` FROM stellar.transactions
		   WHERE source_account = ?` + cursorClause + `)
		UNION ALL
		(SELECT ` + txCols + ` FROM stellar.transactions
		   WHERE (ledger_seq, tx_index) IN (
		        SELECT DISTINCT ledger_seq, tx_index FROM stellar.operation_participants WHERE account = ?)` + cursorClause + `)
	) ORDER BY ledger_seq DESC, tx_index DESC LIMIT ?`
	args := []any{account}
	args = append(args, cursorArgs...)
	args = append(args, account)
	args = append(args, cursorArgs...)
	args = append(args, limit)
	rows, err := r.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: account %s txs: %w", account, err)
	}
	defer func() { _ = rows.Close() }()
	return scanTxSummaries(rows)
}

// AccountOperations returns operations INVOLVING an account — both those it
// sourced (effective op source) and those where it's a non-source participant
// — newest first, keyset-paged by the composite (ledger_seq, tx_index,
// op_index) cursor (ADR-0038 Phase B). Sourced via the source_account
// skip-index on stellar.operations; incoming via an account-prefixed lookup of
// stellar.operation_participants. Incoming coverage tracks the participant-
// index capture + backfill (see AccountTransactions).
func (r *ExplorerReader) AccountOperations(ctx context.Context, account string, limit int, cur ExplorerCursor) ([]OpRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// UNION of two index-friendly arms (see AccountTransactions for why an
	// `OR … IN (…)` is wrong). Arm 1 (sourced) uses the source_account
	// index; arm 2 (participant) matches operations on its PRIMARY KEY
	// (ledger_seq, tx_index, op_index) via op-keys from the account-prefixed
	// operation_participants. No DISTINCT needed: an op is sourced XOR has
	// the account as a NON-source participant (participants exclude the op's
	// own source), so the arms never overlap.
	cursorClause := ""
	var cursorArgs []any
	if cur.IsSet() {
		cursorClause = ` AND (ledger_seq, tx_index, op_index) < (?, ?, ?)`
		cursorArgs = []any{cur.Ledger, cur.A, cur.B}
	}
	q := `SELECT ` + opCols + ` FROM (
		(SELECT ` + opCols + ` FROM stellar.operations
		   WHERE source_account = ?` + cursorClause + `)
		UNION ALL
		(SELECT ` + opCols + ` FROM stellar.operations
		   WHERE (ledger_seq, tx_index, op_index) IN (
		        SELECT ledger_seq, tx_index, op_index FROM stellar.operation_participants WHERE account = ?)` + cursorClause + `)
	) ORDER BY ledger_seq DESC, tx_index DESC, op_index DESC LIMIT ?`
	args := []any{account}
	args = append(args, cursorArgs...)
	args = append(args, account)
	args = append(args, cursorArgs...)
	args = append(args, limit)
	rows, err := r.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: account %s ops: %w", account, err)
	}
	defer func() { _ = rows.Close() }()
	return scanOps(rows)
}

// TransactionByHash looks up a single transaction by its hex hash. Relies on
// the tx_hash bloom skip-index (the table is ORDER BY (ledger_seq, tx_index),
// so without the index this would full-scan). NOT FINAL — FINAL would defeat
// the skip-index; instead it takes the latest-ingested row. found=false when
// the hash is unknown.
func (r *ExplorerReader) TransactionByHash(ctx context.Context, hash string) (TxSummary, bool, error) {
	q := `SELECT ` + txCols + ` FROM stellar.transactions
		WHERE tx_hash = ? ORDER BY ingested_at DESC LIMIT 1`
	rows, err := r.conn.Query(ctx, q, hash)
	if err != nil {
		return TxSummary{}, false, fmt.Errorf("clickhouse: tx %s: %w", hash, err)
	}
	defer func() { _ = rows.Close() }()
	out, err := scanTxSummaries(rows)
	if err != nil {
		return TxSummary{}, false, err
	}
	if len(out) == 0 {
		return TxSummary{}, false, nil
	}
	return out[0], true, nil
}

// OperationsByTx returns a transaction's operations, ledger-scoped (so
// partition-pruned + fast — the caller passes the ledger from TransactionByHash).
func (r *ExplorerReader) OperationsByTx(ctx context.Context, seq uint32, hash string) ([]OpRow, error) {
	q := `SELECT ` + opCols + ` FROM stellar.operations
		WHERE ledger_seq = ? AND tx_hash = ? ORDER BY op_index`
	rows, err := r.conn.Query(ctx, q, seq, hash)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: tx %s ops: %w", hash, err)
	}
	defer func() { _ = rows.Close() }()
	return scanOps(rows)
}

// OperationResultsByTx returns op_index → result_code for a transaction
// (ledger-scoped; operation_results is ORDER BY (ledger_seq, tx_hash, op_index)
// so this is a primary-key point lookup).
func (r *ExplorerReader) OperationResultsByTx(ctx context.Context, seq uint32, hash string) (map[uint32]int32, error) {
	const q = `SELECT op_index, result_code FROM stellar.operation_results
		WHERE ledger_seq = ? AND tx_hash = ?`
	rows, err := r.conn.Query(ctx, q, seq, hash)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: tx %s op results: %w", hash, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[uint32]int32{}
	for rows.Next() {
		var idx uint32
		var code int32
		if err := rows.Scan(&idx, &code); err != nil {
			return nil, fmt.Errorf("clickhouse: scan op result: %w", err)
		}
		out[idx] = code
	}
	return out, rows.Err()
}

// ContractActivityRow is a contract event for the contract-activity view
// (GET /v1/contracts/{c}). Ordered most-recent-first.
type ContractActivityRow struct {
	Seq        uint32
	CloseTime  time.Time
	TxHash     string
	OpIndex    uint32
	EventIndex uint32
	EventType  string
	Topic0Sym  string
}

// ContractEventsRecent returns a contract's most-recent events, descending.
// Relies on the contract_id bloom skip-index (contract_events is
// ORDER BY (ledger_seq, tx_hash, ...), so a contract_id predicate would
// otherwise full-scan). NOT FINAL — FINAL would defeat the skip-index.
// A set cursor keyset-pages to older events by the composite
// (ledger_seq, op_index, event_index) — a contract can emit many events in one
// ledger, so a ledger-only cursor would drop the rest of a straddled ledger.
func (r *ExplorerReader) ContractEventsRecent(ctx context.Context, contractID string, limit int, cur ExplorerCursor) ([]ContractActivityRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT ledger_seq, close_time, tx_hash, op_index, event_index, event_type, topic_0_sym
		FROM stellar.contract_events WHERE contract_id = ?`
	args := []any{contractID}
	if cur.IsSet() {
		q += ` AND (ledger_seq, op_index, event_index) < (?, ?, ?)`
		args = append(args, cur.Ledger, cur.A, cur.B)
	}
	q += ` ORDER BY ledger_seq DESC, op_index DESC, event_index DESC LIMIT ?`
	args = append(args, limit)

	rows, err := r.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: contract %s events: %w", contractID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []ContractActivityRow
	for rows.Next() {
		var e ContractActivityRow
		if err := rows.Scan(&e.Seq, &e.CloseTime, &e.TxHash, &e.OpIndex, &e.EventIndex, &e.EventType, &e.Topic0Sym); err != nil {
			return nil, fmt.Errorf("clickhouse: scan contract event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ContractDirectoryRow is one row of the contracts directory: a contract
// ranked by recent on-chain event activity.
type ContractDirectoryRow struct {
	ContractID string
	Events     int64
	LastLedger uint32
	LastSeen   time.Time
}

// RecentContracts returns the most active contracts by contract-event count
// within [sinceLedger, tip] — the contracts directory (GET /v1/contracts).
// Window-scoped so the GROUP BY stays bounded (contract_events is billions of
// rows all-time); the caller derives sinceLedger from the tip. NOT FINAL —
// FINAL would defeat the contract_id bloom index, and a slightly stale
// dedup count is fine for a ranking.
func (r *ExplorerReader) RecentContracts(ctx context.Context, limit int, sinceLedger uint32) ([]ContractDirectoryRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const q = `SELECT contract_id, toInt64(count()) AS events, max(ledger_seq) AS last_ledger, max(close_time) AS last_seen
		FROM stellar.contract_events
		WHERE ledger_seq >= ?
		GROUP BY contract_id
		ORDER BY events DESC
		LIMIT ?`
	rows, err := r.conn.Query(ctx, q, sinceLedger, limit)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: recent contracts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ContractDirectoryRow
	for rows.Next() {
		var c ContractDirectoryRow
		if err := rows.Scan(&c.ContractID, &c.Events, &c.LastLedger, &c.LastSeen); err != nil {
			return nil, fmt.Errorf("clickhouse: scan contract directory row: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ContractEdgeRow is one edge of a contract's interaction map: another
// contract that emitted events in the same transactions as the subject.
type ContractEdgeRow struct {
	ContractID string
	SharedTxs  int64
}

// ContractInteractions returns the contracts that co-occur with contractID
// in the same transactions, ranked by shared-tx count — the contract
// interaction map (GET /v1/contracts/{id}/interactions). Co-occurrence in a
// tx is a strong proxy for a cross-contract call (Soroban invokes nest within
// one InvokeHostFunction op, so the callee's events land in the caller's tx).
//
// Implemented as an IN-subquery (the inner query rides the contract_id bloom
// index to collect the subject's (ledger_seq, tx_hash) set; the outer scan
// finds the other contracts in those txs) rather than a self-join, which
// ClickHouse would materialise more expensively. Window-scoped via
// sinceLedger to bound both halves.
func (r *ExplorerReader) ContractInteractions(ctx context.Context, contractID string, limit int, sinceLedger uint32) ([]ContractEdgeRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// Cap the subject's transaction set to its most-recent 50k (ledger,
	// tx) rows. Without this, a mega-contract (a SAC / AMM router with
	// tens of millions of events in the window) builds an enormous IN set
	// and the probe times out. 50k recent txs is a rich, bounded sample —
	// the interaction map reflects current behaviour regardless of how
	// busy the contract is.
	const subjectTxCap = 50_000
	const q = `SELECT contract_id, toInt64(count()) AS shared
		FROM stellar.contract_events
		WHERE ledger_seq >= ?
		  AND contract_id != ?
		  AND (ledger_seq, tx_hash) IN (
		      SELECT ledger_seq, tx_hash FROM stellar.contract_events
		      WHERE contract_id = ? AND ledger_seq >= ?
		      ORDER BY ledger_seq DESC
		      LIMIT ?
		  )
		GROUP BY contract_id
		ORDER BY shared DESC
		LIMIT ?`
	rows, err := r.conn.Query(ctx, q, sinceLedger, contractID, contractID, sinceLedger, subjectTxCap, limit)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: contract %s interactions: %w", contractID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []ContractEdgeRow
	for rows.Next() {
		var e ContractEdgeRow
		if err := rows.Scan(&e.ContractID, &e.SharedTxs); err != nil {
			return nil, fmt.Errorf("clickhouse: scan contract edge: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventSummary is a lightweight contract-event row for the tx-detail view.
type EventSummary struct {
	OpIndex    uint32
	EventIndex uint32
	ContractID string
	EventType  string
	Topic0Sym  string
}

// EventsByTx returns a transaction's contract events (ledger-scoped — fast;
// contract_events is ORDER BY (ledger_seq, tx_hash, op_index, event_index)).
func (r *ExplorerReader) EventsByTx(ctx context.Context, seq uint32, hash string) ([]EventSummary, error) {
	const q = `SELECT op_index, event_index, contract_id, event_type, topic_0_sym
		FROM stellar.contract_events
		WHERE ledger_seq = ? AND tx_hash = ? ORDER BY op_index, event_index`
	rows, err := r.conn.Query(ctx, q, seq, hash)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: tx %s events: %w", hash, err)
	}
	defer func() { _ = rows.Close() }()
	var out []EventSummary
	for rows.Next() {
		var e EventSummary
		if err := rows.Scan(&e.OpIndex, &e.EventIndex, &e.ContractID, &e.EventType, &e.Topic0Sym); err != nil {
			return nil, fmt.Errorf("clickhouse: scan event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
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
