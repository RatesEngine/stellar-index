package clickhouse

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Protocol-analytics reads (ADR-0038 explorer / per-protocol pages). These
// aggregate a protocol's on-chain footprint generically from the certified
// lake's contract_events, scoped to the protocol's contract-id set (from the
// registry). Because the set comes from OUR registry — never user input — the
// IN-list is safe to bind directly. All reads filter event_type='contract'
// (drop diagnostic/system events) and lean on the contract_id bloom skip-index.
//
// Windowed variants take sinceLedger (0 = all-time): bounding by ledger_seq
// prunes partitions (PARTITION BY intDiv(ledger_seq,1e6)), which is what keeps
// the daily-activity + breakdown queries fast on the 12B-row table.

// ProtocolEventTypeCount is one (event symbol → count) row of a protocol's
// event-type distribution.
type ProtocolEventTypeCount struct {
	EventType string // topic[0] symbol (e.g. "swap", "deposit", "new_pair")
	Count     uint64
}

// ProtocolDailyPoint is one day of a protocol's event-activity series.
type ProtocolDailyPoint struct {
	Date   string // YYYY-MM-DD (UTC)
	Events uint64
}

// ProtocolContractActivity is per-contract rollup for a protocol's roster.
type ProtocolContractActivity struct {
	ContractID string
	Events     uint64
	LastSeen   time.Time
}

// LakeTipLedger returns the highest ledger_seq in the lake (cheap — small
// ledgers table). Used to derive a recent-window cutoff for the windowed
// protocol-analytics reads.
func (r *ExplorerReader) LakeTipLedger(ctx context.Context) (uint32, error) {
	var tip uint32
	if err := r.conn.QueryRow(ctx, `SELECT max(ledger_seq) FROM stellar.ledgers`).Scan(&tip); err != nil {
		return 0, fmt.Errorf("clickhouse: lake tip: %w", err)
	}
	return tip, nil
}

// ProtocolEventBreakdown returns the event-type distribution (topic[0] symbol →
// count) for a protocol's contracts. sinceLedger>0 bounds to a recent window;
// 0 is all-time. Descending by count.
func (r *ExplorerReader) ProtocolEventBreakdown(ctx context.Context, contractIDs []string, sinceLedger uint32) ([]ProtocolEventTypeCount, error) {
	if len(contractIDs) == 0 {
		return nil, nil
	}
	// Group by topic[0]'s denormalized symbol. For events whose topic[0] is
	// NOT a Symbol — the lake leaves topic_0_sym empty — also carry the raw
	// topic[1] so we can recover the real event name from it. Soroswap is the
	// dominant case: its events are [String("SoroswapPair"), Symbol(name)], so
	// the event name (swap/sync/deposit/withdraw/skim) lives in topic[1].
	// The if() keeps Symbol-topic[0] events grouped by their symbol (topic[1]
	// coalesced away) and splits only the empty bucket by topic[1].
	q := `SELECT topic_0_sym, if(topic_0_sym = '', topics_xdr[2], '') AS t1, count() AS c
		FROM stellar.contract_events
		WHERE contract_id IN (?) AND event_type = 'contract'`
	args := []any{contractIDs}
	if sinceLedger > 0 {
		q += ` AND ledger_seq >= ?`
		args = append(args, sinceLedger)
	}
	q += ` GROUP BY topic_0_sym, t1 ORDER BY c DESC LIMIT 200`
	rows, err := r.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: protocol event breakdown: %w", err)
	}
	defer func() { _ = rows.Close() }()
	// Aggregate by effective event name: topic_0_sym when present, else the
	// decoded topic[1] Symbol. Rows whose name can't be recovered (neither
	// topic is a Symbol) are dropped here — protocols.go's reconcile folds
	// them into the "untyped" remainder against the unfiltered total.
	byName := make(map[string]uint64)
	for rows.Next() {
		var sym, t1 string
		var c uint64
		if err := rows.Scan(&sym, &t1, &c); err != nil {
			return nil, fmt.Errorf("clickhouse: scan event breakdown: %w", err)
		}
		name := sym
		if name == "" {
			dec, ok := decodeTopicSymbol(t1)
			if !ok {
				continue
			}
			name = dec
		}
		byName[name] += c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]ProtocolEventTypeCount, 0, len(byName))
	for name, c := range byName {
		out = append(out, ProtocolEventTypeCount{EventType: name, Count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out, nil
}

// decodeTopicSymbol decodes a base64-XDR ScVal and returns its Symbol string,
// ok=false when empty or not a Symbol. Recovers the event name from topic[1]
// for protocols whose topic[0] is a non-Symbol marker (Soroswap).
func decodeTopicSymbol(b64 string) (string, bool) {
	if b64 == "" {
		return "", false
	}
	var v xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(b64, &v); err != nil {
		return "", false
	}
	if s, ok := v.GetSym(); ok {
		return string(s), true
	}
	return "", false
}

// ProtocolDailyActivity returns daily contract-event counts for a protocol's
// contracts from sinceLedger forward (sinceLedger>0 required for performance —
// the caller passes tip − window). Ascending by date.
func (r *ExplorerReader) ProtocolDailyActivity(ctx context.Context, contractIDs []string, sinceLedger uint32) ([]ProtocolDailyPoint, error) {
	if len(contractIDs) == 0 {
		return nil, nil
	}
	const q = `SELECT toString(toDate(close_time)) AS d, count() AS c
		FROM stellar.contract_events
		WHERE contract_id IN (?) AND event_type = 'contract' AND ledger_seq >= ?
		GROUP BY d ORDER BY d ASC`
	rows, err := r.conn.Query(ctx, q, contractIDs, sinceLedger)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: protocol daily activity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ProtocolDailyPoint
	for rows.Next() {
		var p ProtocolDailyPoint
		if err := rows.Scan(&p.Date, &p.Events); err != nil {
			return nil, fmt.Errorf("clickhouse: scan daily activity: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProtocolContractActivity returns per-contract event counts + last-seen for a
// protocol's roster, scoped to sinceLedger forward (>0 required — bounding by
// ledger_seq prunes partitions; an all-time scan over the 12B-row table blows
// the 30s read budget for active protocols). Descending by event count.
func (r *ExplorerReader) ProtocolContractActivity(ctx context.Context, contractIDs []string, sinceLedger uint32) ([]ProtocolContractActivity, error) {
	if len(contractIDs) == 0 {
		return nil, nil
	}
	const q = `SELECT contract_id, count() AS c, max(close_time) AS last_seen
		FROM stellar.contract_events
		WHERE contract_id IN (?) AND event_type = 'contract' AND ledger_seq >= ?
		GROUP BY contract_id ORDER BY c DESC LIMIT 1000`
	rows, err := r.conn.Query(ctx, q, contractIDs, sinceLedger)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: protocol contract activity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ProtocolContractActivity
	for rows.Next() {
		var a ProtocolContractActivity
		if err := rows.Scan(&a.ContractID, &a.Events, &a.LastSeen); err != nil {
			return nil, fmt.Errorf("clickhouse: scan contract activity: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
