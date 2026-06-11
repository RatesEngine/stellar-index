// Package clickhouse is the Tier-1 raw-lake write path (ADR-0034 /
// docs/architecture/clickhouse-tier1-decoder.md). It buffers structurally-
// decoded ledger rows and flushes them to the ClickHouse `stellar.*` tables
// in native columnar batches. Rows mirror deploy/clickhouse/tier1_schema.sql
// exactly (excluding the DEFAULT ingested_at column).
package clickhouse

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ---- Row types (1:1 with the Tier-1 schema, ingested_at omitted = DEFAULT) ----

// LedgerRow mirrors stellar.ledgers.
type LedgerRow struct {
	LedgerSeq               uint32
	CloseTime               time.Time
	LedgerHash              string
	PrevHash                string
	ProtocolVersion         uint32
	BucketListHash          string
	TxCount                 uint32
	OpCount                 uint32
	SorobanEventCount       uint32
	ClassicTradeEffectCount uint32
	TotalCoins              int64
	FeePool                 int64
	BaseFee                 uint32
	BaseReserve             uint32
}

// TransactionRow mirrors stellar.transactions.
type TransactionRow struct {
	LedgerSeq      uint32
	CloseTime      time.Time
	TxHash         string
	TxIndex        uint32
	SourceAccount  string
	FeeCharged     int64
	MaxFee         int64
	OperationCount uint16
	Successful     uint8
	ResultCode     int32
	MemoType       string
	Memo           string
}

// OperationRow mirrors stellar.operations.
type OperationRow struct {
	LedgerSeq     uint32
	CloseTime     time.Time
	TxHash        string
	TxIndex       uint32
	OpIndex       uint32
	OpType        string
	SourceAccount string
	BodyXDR       string
}

// OperationResultRow mirrors stellar.operation_results.
type OperationResultRow struct {
	LedgerSeq  uint32
	TxHash     string
	OpIndex    uint32
	ResultCode int32
	ResultXDR  string
}

// ContractEventRow mirrors stellar.contract_events.
type ContractEventRow struct {
	LedgerSeq        uint32
	CloseTime        time.Time
	TxHash           string
	OpIndex          uint32
	EventIndex       uint32
	ContractID       string
	EventType        string
	TopicCount       uint8
	Topic0Sym        string
	TopicsXDR        []string
	DataXDR          string
	OpArgsXDR        []string
	InSuccessfulCall uint8
}

// LedgerEntryChangeRow mirrors stellar.ledger_entry_changes.
type LedgerEntryChangeRow struct {
	LedgerSeq   uint32
	CloseTime   time.Time
	TxHash      string
	OpIndex     int32 // -1 for fee-meta / tx-level
	ChangeIndex uint32
	ChangeType  string
	EntryType   string
	KeyXDR      string
	EntryXDR    string
}

// SupplyFlowRow mirrors stellar.supply_flows: one decoded supply-affecting
// event (CAP-67 classic / SEP-41 mint/burn/clawback). The amount is decoded
// from the event body AT INGEST (DecodeSupplyAmount) — the i128 magnitude as a
// *big.Int (ADR-0003) — so per-token supply is a pure SQL sum over this table
// (Σmint − Σburn − Σclawback) with no XDR decode at read time and no periodic
// rollup refresh. Keyed (in the ReplacingMergeTree ORDER BY) by the event
// identity so the lake's drop→heal / re-backfill re-inserts are idempotent.
type SupplyFlowRow struct {
	ContractID string
	LedgerSeq  uint32
	CloseTime  time.Time
	TxHash     string
	OpIndex    uint32
	EventIndex uint32
	Kind       string // "mint" | "burn" | "clawback"
	Amount     *big.Int
}

// LedgerExtract is one ledger's full structural decode — all rows produced
// from a single LedgerCloseMeta.
type LedgerExtract struct {
	Ledger      LedgerRow
	Txs         []TransactionRow
	Ops         []OperationRow
	Results     []OperationResultRow
	Events      []ContractEventRow
	Changes     []LedgerEntryChangeRow
	SupplyFlows []SupplyFlowRow

	// TxReadErrors / TxEventReadErrors count transactions this extract
	// could NOT fully read (a malformed tx, or a tx whose
	// GetTransactionEvents failed — e.g. an unsupported future
	// TransactionMeta version). They are IN-MEMORY ONLY (not ClickHouse
	// columns): the ledger is still written so the lake stays contiguous
	// (contiguity is the substrate-continuity coverage proof — dropping
	// the ledger would be worse), but a non-zero value means this
	// ledger's Events/SorobanEventCount undercount. Callers surface a
	// climb (a meta-version break drops EVERY tx's events in lock-step,
	// which would otherwise look like a run of clean empty ledgers —
	// G15-06).
	TxReadErrors      int
	TxEventReadErrors int
}

// Sink buffers extracts and flushes them to ClickHouse in batches. Not safe
// for concurrent use by multiple goroutines — give each backfill worker its
// own Sink (ClickHouse handles concurrent connections well).
type Sink struct {
	conn       driver.Conn
	flushEvery int

	ledgers     []LedgerRow
	txs         []TransactionRow
	ops         []OperationRow
	results     []OperationResultRow
	events      []ContractEventRow
	changes     []LedgerEntryChangeRow
	supplyFlows []SupplyFlowRow
}

// Open dials ClickHouse (native protocol) at addr (e.g. "127.0.0.1:9300")
// against the `stellar` database and pings it. flushEvery is the ledger-count
// threshold that triggers an automatic Flush.
func Open(ctx context.Context, addr string, flushEvery int) (*Sink, error) {
	if flushEvery <= 0 {
		flushEvery = 2000
	}
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{Database: "stellar"},
		Settings: clickhouse.Settings{
			// keep memory modest; the server cap is the hard ceiling.
			"max_execution_time": 0,
		},
		DialTimeout:     10 * time.Second,
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open %s: %w", addr, err)
	}
	if err := conn.Ping(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clickhouse: ping %s: %w", addr, err)
	}
	return &Sink{conn: conn, flushEvery: flushEvery}, nil
}

// Add buffers one ledger's extract, auto-flushing when the ledger threshold
// is reached.
func (s *Sink) Add(ctx context.Context, e LedgerExtract) error {
	s.ledgers = append(s.ledgers, e.Ledger)
	s.txs = append(s.txs, e.Txs...)
	s.ops = append(s.ops, e.Ops...)
	s.results = append(s.results, e.Results...)
	s.events = append(s.events, e.Events...)
	s.changes = append(s.changes, e.Changes...)
	s.supplyFlows = append(s.supplyFlows, e.SupplyFlows...)
	if len(s.ledgers) >= s.flushEvery {
		return s.Flush(ctx)
	}
	return nil
}

// Flush sends all buffered rows as one native batch per table, then clears
// the buffers. A partial failure returns the error with buffers intact so the
// caller can retry the same range (idempotent under ReplacingMergeTree).
//
// ORDERING IS LOAD-BEARING: stellar.ledgers is flushed LAST, after every other
// table. The batches are independent INSERTs (no cross-table transaction), so a
// flush can partially succeed. Writing ledgers last makes a ledgers row a
// per-ledger COMMIT MARKER: if a ledger_seq is present in stellar.ledgers, all
// of that ledger's txs/ops/results/events/changes are already durable in CH.
// The real-time projector's completeness watermark (ADR-0034 #10,
// ContiguousWatermark) relies on this invariant to read contract_events only up
// to where the lake is provably complete — never racing ahead of a half-written
// or dropped ledger. (Buffer-full drops in LiveSink.PushLedger drop the whole
// LedgerExtract atomically, so they leave no ledgers row either.)
func (s *Sink) Flush(ctx context.Context) error {
	if len(s.ledgers) == 0 {
		return nil
	}
	if err := s.flushTxs(ctx); err != nil {
		return err
	}
	if err := s.flushOps(ctx); err != nil {
		return err
	}
	if err := s.flushResults(ctx); err != nil {
		return err
	}
	if err := s.flushEvents(ctx); err != nil {
		return err
	}
	if err := s.flushChanges(ctx); err != nil {
		return err
	}
	if err := s.flushSupplyFlows(ctx); err != nil {
		return err
	}
	// ledgers LAST — the commit marker. See the ORDERING note above.
	if err := s.flushLedgers(ctx); err != nil {
		return err
	}
	s.reset()
	return nil
}

func (s *Sink) reset() {
	s.ledgers = s.ledgers[:0]
	s.txs = s.txs[:0]
	s.ops = s.ops[:0]
	s.results = s.results[:0]
	s.events = s.events[:0]
	s.changes = s.changes[:0]
	s.supplyFlows = s.supplyFlows[:0]
}

// Close flushes any remaining rows and closes the connection.
func (s *Sink) Close(ctx context.Context) error {
	ferr := s.Flush(ctx)
	cerr := s.conn.Close()
	if ferr != nil {
		return ferr
	}
	return cerr
}

func (s *Sink) flushLedgers(ctx context.Context) error {
	b, err := s.conn.PrepareBatch(ctx, "INSERT INTO stellar.ledgers (ledger_seq, close_time, ledger_hash, prev_hash, protocol_version, bucket_list_hash, tx_count, op_count, soroban_event_count, classic_trade_effect_count, total_coins, fee_pool, base_fee, base_reserve)")
	if err != nil {
		return fmt.Errorf("clickhouse: prepare ledgers: %w", err)
	}
	for _, r := range s.ledgers {
		if err := b.Append(r.LedgerSeq, r.CloseTime, r.LedgerHash, r.PrevHash, r.ProtocolVersion, r.BucketListHash, r.TxCount, r.OpCount, r.SorobanEventCount, r.ClassicTradeEffectCount, r.TotalCoins, r.FeePool, r.BaseFee, r.BaseReserve); err != nil {
			return fmt.Errorf("clickhouse: append ledger %d: %w", r.LedgerSeq, err)
		}
	}
	return wrapSend(b.Send(), "ledgers")
}

func (s *Sink) flushTxs(ctx context.Context) error {
	b, err := s.conn.PrepareBatch(ctx, "INSERT INTO stellar.transactions (ledger_seq, close_time, tx_hash, tx_index, source_account, fee_charged, max_fee, operation_count, successful, result_code, memo_type, memo)")
	if err != nil {
		return fmt.Errorf("clickhouse: prepare transactions: %w", err)
	}
	for _, r := range s.txs {
		if err := b.Append(r.LedgerSeq, r.CloseTime, r.TxHash, r.TxIndex, r.SourceAccount, r.FeeCharged, r.MaxFee, r.OperationCount, r.Successful, r.ResultCode, r.MemoType, r.Memo); err != nil {
			return fmt.Errorf("clickhouse: append tx %s: %w", r.TxHash, err)
		}
	}
	return wrapSend(b.Send(), "transactions")
}

func (s *Sink) flushOps(ctx context.Context) error {
	b, err := s.conn.PrepareBatch(ctx, "INSERT INTO stellar.operations (ledger_seq, close_time, tx_hash, tx_index, op_index, op_type, source_account, body_xdr)")
	if err != nil {
		return fmt.Errorf("clickhouse: prepare operations: %w", err)
	}
	for _, r := range s.ops {
		if err := b.Append(r.LedgerSeq, r.CloseTime, r.TxHash, r.TxIndex, r.OpIndex, r.OpType, r.SourceAccount, r.BodyXDR); err != nil {
			return fmt.Errorf("clickhouse: append op %s/%d: %w", r.TxHash, r.OpIndex, err)
		}
	}
	return wrapSend(b.Send(), "operations")
}

func (s *Sink) flushResults(ctx context.Context) error {
	b, err := s.conn.PrepareBatch(ctx, "INSERT INTO stellar.operation_results (ledger_seq, tx_hash, op_index, result_code, result_xdr)")
	if err != nil {
		return fmt.Errorf("clickhouse: prepare operation_results: %w", err)
	}
	for _, r := range s.results {
		if err := b.Append(r.LedgerSeq, r.TxHash, r.OpIndex, r.ResultCode, r.ResultXDR); err != nil {
			return fmt.Errorf("clickhouse: append result %s/%d: %w", r.TxHash, r.OpIndex, err)
		}
	}
	return wrapSend(b.Send(), "operation_results")
}

func (s *Sink) flushEvents(ctx context.Context) error {
	b, err := s.conn.PrepareBatch(ctx, "INSERT INTO stellar.contract_events (ledger_seq, close_time, tx_hash, op_index, event_index, contract_id, event_type, topic_count, topic_0_sym, topics_xdr, data_xdr, op_args_xdr, in_successful_call)")
	if err != nil {
		return fmt.Errorf("clickhouse: prepare contract_events: %w", err)
	}
	for _, r := range s.events {
		if err := b.Append(r.LedgerSeq, r.CloseTime, r.TxHash, r.OpIndex, r.EventIndex, r.ContractID, r.EventType, r.TopicCount, r.Topic0Sym, r.TopicsXDR, r.DataXDR, r.OpArgsXDR, r.InSuccessfulCall); err != nil {
			return fmt.Errorf("clickhouse: append event %s/%d/%d: %w", r.TxHash, r.OpIndex, r.EventIndex, err)
		}
	}
	return wrapSend(b.Send(), "contract_events")
}

func (s *Sink) flushChanges(ctx context.Context) error {
	b, err := s.conn.PrepareBatch(ctx, "INSERT INTO stellar.ledger_entry_changes (ledger_seq, close_time, tx_hash, op_index, change_index, change_type, entry_type, key_xdr, entry_xdr)")
	if err != nil {
		return fmt.Errorf("clickhouse: prepare ledger_entry_changes: %w", err)
	}
	for _, r := range s.changes {
		if err := b.Append(r.LedgerSeq, r.CloseTime, r.TxHash, r.OpIndex, r.ChangeIndex, r.ChangeType, r.EntryType, r.KeyXDR, r.EntryXDR); err != nil {
			return fmt.Errorf("clickhouse: append change %s/%d/%d: %w", r.TxHash, r.OpIndex, r.ChangeIndex, err)
		}
	}
	return wrapSend(b.Send(), "ledger_entry_changes")
}

func (s *Sink) flushSupplyFlows(ctx context.Context) error {
	if len(s.supplyFlows) == 0 {
		return nil
	}
	b, err := s.conn.PrepareBatch(ctx, "INSERT INTO stellar.supply_flows (contract_id, ledger_seq, close_time, tx_hash, op_index, event_index, kind, amount)")
	if err != nil {
		return fmt.Errorf("clickhouse: prepare supply_flows: %w", err)
	}
	for _, r := range s.supplyFlows {
		amt := r.Amount
		if amt == nil {
			amt = big.NewInt(0)
		}
		if err := b.Append(r.ContractID, r.LedgerSeq, r.CloseTime, r.TxHash, r.OpIndex, r.EventIndex, r.Kind, amt); err != nil {
			return fmt.Errorf("clickhouse: append supply_flow %s/%s/%d/%d: %w", r.ContractID, r.TxHash, r.OpIndex, r.EventIndex, err)
		}
	}
	return wrapSend(b.Send(), "supply_flows")
}

func wrapSend(err error, table string) error {
	if err != nil {
		return fmt.Errorf("clickhouse: send %s batch: %w", table, err)
	}
	return nil
}
