package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/sorobanevents"
)

// TopicSample is one representative soroban_events row for a distinct
// (contract_id, topic_0_sym) shape, plus how many events share that
// shape and over what ledger span. The representative Row carries the
// full topic XDRs + body so it can be Reconstruct()ed and fed to a
// decoder's Matches() — the recognition audit (ADR-0033 Claim 2a).
type TopicSample struct {
	Row       sorobanevents.Row
	Count     int64
	MinLedger uint32
	MaxLedger uint32
}

// DistinctSorobanTopicSamples returns one representative row per
// distinct (contract_id, topic_0_sym) present in soroban_events over
// [from, to], with per-shape count and ledger span. It is the input to
// the recognition audit: every distinct on-chain event shape, ready to
// run through the decoder chain.
//
// This is an aggregate over the (possibly large) soroban_events table
// and is meant for periodic / operator-run audits, not per-request
// serving. Scope it by ledger range to bound cost.
func (s *Store) DistinctSorobanTopicSamples(ctx context.Context, from, to uint32) ([]TopicSample, error) {
	if to < from {
		return nil, errors.New("timescale: DistinctSorobanTopicSamples: to < from")
	}

	// Chunk-pruning: when ledger_ingest_log fully covers [from,to], add a
	// ledger_close_time bound so this aggregate hits only the relevant
	// chunks instead of scanning the whole hypertable (see
	// SorobanEventsTimeBound). Falls back to the unpruned scan otherwise.
	args := []any{int64(from), int64(to)}
	timeFilter := ""
	if lo, hi, covered, terr := s.SorobanEventsTimeBound(ctx, from, to); terr == nil && covered {
		args = append(args, lo, hi)
		timeFilter = " AND ledger_close_time BETWEEN $3 AND $4"
	}
	q := `
        SELECT DISTINCT ON (contract_id, topic_0_sym)
            ledger, ledger_close_time, tx_hash, op_index, event_index,
            contract_id, contract_id_hex, topic_count, topic_0_sym,
            topic_0_xdr, topic_1_xdr, topic_2_xdr, topic_3_xdr,
            body_xdr, op_args_xdr,
            count(*)   OVER (PARTITION BY contract_id, topic_0_sym) AS grp_count,
            min(ledger) OVER (PARTITION BY contract_id, topic_0_sym) AS grp_min,
            max(ledger) OVER (PARTITION BY contract_id, topic_0_sym) AS grp_max
        FROM soroban_events
        WHERE ledger BETWEEN $1 AND $2` + timeFilter + `
        ORDER BY contract_id, topic_0_sym, ledger`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("timescale: DistinctSorobanTopicSamples query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []TopicSample
	for rows.Next() {
		var (
			samp                     TopicSample
			r                        sorobanevents.Row
			ledger                   int64
			opIdx, eventIdx          int16
			topicCount               int16
			topic0Sym                sql.NullString
			topic1, topic2, topic3   []byte
			opArgs                   []byte
			grpCount, grpMin, grpMax int64
		)
		if err := rows.Scan(
			&ledger, &r.LedgerCloseTime, &r.TxHash, &opIdx, &eventIdx,
			&r.ContractID, &r.ContractIDHex, &topicCount, &topic0Sym,
			&r.Topic0XDR, &topic1, &topic2, &topic3,
			&r.BodyXDR, &opArgs,
			&grpCount, &grpMin, &grpMax,
		); err != nil {
			return nil, fmt.Errorf("timescale: DistinctSorobanTopicSamples scan: %w", err)
		}
		r.Ledger = uint32(ledger)
		r.OpIndex = opIdx
		r.EventIndex = eventIdx
		r.TopicCount = topicCount
		if topic0Sym.Valid {
			r.Topic0Sym = topic0Sym.String
		}
		r.Topic1XDR = topic1
		r.Topic2XDR = topic2
		r.Topic3XDR = topic3
		r.OpArgsXDR = opArgs
		samp.Row = r
		samp.Count = grpCount
		samp.MinLedger = uint32(grpMin)
		samp.MaxLedger = uint32(grpMax)
		out = append(out, samp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: DistinctSorobanTopicSamples rows: %w", err)
	}
	return out, nil
}
