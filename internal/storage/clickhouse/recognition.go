package clickhouse

import (
	"context"
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// TopicShape is one distinct (contract_id, topic_0_sym) event shape in the lake,
// with a representative event for recognition (ADR-0033 Claim 2a). The CH lake
// is the complete authoritative source, so a shape-scan over it sees every event
// shape any contract has ever emitted — no Postgres soroban_events scan needed.
type TopicShape struct {
	ContractID string
	Topic0Sym  string
	Count      uint64
	MinLedger  uint32
	MaxLedger  uint32
	EventType  string
	Topics     []string // base64 SCVal topics of a representative event
	DataXDR    string   // base64 SCVal data of that event
}

// Event reconstructs the representative [events.Event] for this shape — enough
// for a decoder's Matches() (Type, ContractID, Topic, Value). It is NOT a full
// event (no ledger/tx identity); recognition only needs the shape.
func (s TopicShape) Event() events.Event {
	return events.Event{
		Type:                     s.EventType,
		ContractID:               s.ContractID,
		Topic:                    s.Topics,
		Value:                    s.DataXDR,
		InSuccessfulContractCall: true,
	}
}

// MaxLedger returns the highest ledger_seq in the lake's ledgers table.
func MaxLedger(ctx context.Context, addr string) (uint32, error) {
	conn, err := openRead(ctx, addr)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	var hi uint64
	if err := conn.QueryRow(ctx, `SELECT toUInt64(max(ledger_seq)) FROM stellar.ledgers`).Scan(&hi); err != nil {
		return 0, fmt.Errorf("clickhouse: max ledger: %w", err)
	}
	return uint32(hi), nil
}

// DistinctTopicShapes returns one representative event per distinct
// (contract_id, topic_0_sym) in contract_events over [from,to]. Optionally
// excludes topic[0] symbols (e.g. the CAP-67 classic-token firehose, which the
// enabled protocol decoders don't claim — pass ClassicTokenTopic0Syms to focus
// the audit on protocol shapes). The GROUP BY collapses billions of rows to the
// handful-of-thousands of distinct shapes; argMax picks the latest event's bytes
// as the representative (newest WASM's encoding).
func DistinctTopicShapes(ctx context.Context, addr string, from, to uint32, excludeTopic0 []string) ([]TopicShape, error) {
	conn, err := openRead(ctx, addr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	where := "WHERE ledger_seq BETWEEN ? AND ?"
	if len(excludeTopic0) > 0 {
		where += " AND topic_0_sym NOT IN (" + sqlQuoteList(excludeTopic0) + ")"
	}
	q := fmt.Sprintf(`
		SELECT
			contract_id,
			topic_0_sym,
			count() AS cnt,
			min(ledger_seq) AS lo,
			max(ledger_seq) AS hi,
			argMax(event_type, ledger_seq) AS event_type,
			argMax(topics_xdr, ledger_seq) AS topics,
			argMax(data_xdr, ledger_seq)   AS data
		FROM stellar.contract_events
		%s
		GROUP BY contract_id, topic_0_sym
		ORDER BY cnt DESC`, where)
	rows, err := conn.Query(ctx, q, from, to)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: distinct topic shapes [%d,%d]: %w", from, to, err)
	}
	defer func() { _ = rows.Close() }()

	var out []TopicShape
	for rows.Next() {
		var s TopicShape
		if err := rows.Scan(&s.ContractID, &s.Topic0Sym, &s.Count, &s.MinLedger, &s.MaxLedger,
			&s.EventType, &s.Topics, &s.DataXDR); err != nil {
			return nil, fmt.Errorf("clickhouse: scan topic shape: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
