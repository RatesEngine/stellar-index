package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/RatesEngine/rates-engine/internal/sources/sorobanevents"
)

// InsertSorobanEventsBatch persists a slice of raw Soroban event
// rows into the `soroban_events` hypertable (migration 0041,
// ADR-0029). Idempotent on the (ledger_close_time, ledger, tx_hash, op_index,
// event_index) PK via `ON CONFLICT DO NOTHING` — replays /
// retries / overlapping backfill chunks are a no-op for already-
// captured rows.
//
// Single multi-row INSERT statement. For a typical batch size of
// 1000 rows × 15 columns this is one round-trip to Postgres, which
// is what the [sorobanevents.AsyncSink] depends on for throughput.
//
// An empty batch returns nil — the caller's flush ticker hits an
// idle period; that's not an error.
//
// Defensive bounds-check: every row must carry a non-empty TxHash
// + ContractID + Topic0XDR + BodyXDR. The capture function should
// never produce malformed rows, but a single bad row in a 1000-row
// batch would abort the whole transaction on COMMIT — surfacing
// the validation here keeps one rogue event from sinking 999 good
// ones.
func (s *Store) InsertSorobanEventsBatch(ctx context.Context, rows []sorobanevents.Row) error {
	if len(rows) == 0 {
		return nil
	}

	// Per-row validation. Cheap — keeps a transient malformed row
	// from poisoning the batch.
	for i := range rows {
		r := &rows[i]
		if len(r.TxHash) != 32 {
			return fmt.Errorf("timescale: InsertSorobanEventsBatch: row %d TxHash len %d, want 32", i, len(r.TxHash))
		}
		if r.ContractID == "" {
			return fmt.Errorf("timescale: InsertSorobanEventsBatch: row %d empty ContractID", i)
		}
		if len(r.ContractIDHex) != 32 {
			return fmt.Errorf("timescale: InsertSorobanEventsBatch: row %d ContractIDHex len %d, want 32", i, len(r.ContractIDHex))
		}
		if len(r.Topic0XDR) == 0 {
			return fmt.Errorf("timescale: InsertSorobanEventsBatch: row %d empty Topic0XDR", i)
		}
		if len(r.BodyXDR) == 0 {
			return fmt.Errorf("timescale: InsertSorobanEventsBatch: row %d empty BodyXDR", i)
		}
	}

	// Build the multi-row VALUES clause + arg slice. 15 columns ×
	// N rows.
	const cols = 15
	var sb strings.Builder
	sb.WriteString(`
        INSERT INTO soroban_events (
            ledger, ledger_close_time, tx_hash, op_index, event_index,
            contract_id, contract_id_hex,
            topic_count, topic_0_sym,
            topic_0_xdr, topic_1_xdr, topic_2_xdr, topic_3_xdr,
            body_xdr, op_args_xdr
        ) VALUES `)
	args := make([]any, 0, cols*len(rows))
	for i := range rows {
		if i > 0 {
			sb.WriteString(", ")
		}
		base := i * cols
		fmt.Fprintf(&sb,
			"($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5,
			base+6, base+7,
			base+8, base+9,
			base+10, base+11, base+12, base+13,
			base+14, base+15,
		)
		r := &rows[i]
		args = append(args,
			int64(r.Ledger),
			r.LedgerCloseTime,
			r.TxHash,
			int16(r.OpIndex),
			int16(r.EventIndex),
			r.ContractID,
			r.ContractIDHex,
			int16(r.TopicCount),
			nullString(r.Topic0Sym),
			r.Topic0XDR,
			nullBytes(r.Topic1XDR),
			nullBytes(r.Topic2XDR),
			nullBytes(r.Topic3XDR),
			r.BodyXDR,
			nullBytes(r.OpArgsXDR),
		)
	}
	sb.WriteString(` ON CONFLICT (ledger_close_time, ledger, tx_hash, op_index, event_index) DO NOTHING`)

	if _, err := s.db.ExecContext(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("timescale: InsertSorobanEventsBatch (%d rows): %w", len(rows), err)
	}
	return nil
}

// StreamSorobanEvents invokes `fn` once per soroban_events row
// matching the predicate in [from, to] (inclusive), in
// (ledger_close_time, ledger, tx_hash, op_index, event_index) order
// — event_index makes the per-op replay order deterministic so
// multi-event ops (Phoenix's 8-events-per-swap) reconstruct stably
// (ADR-0033). Used by
// per-source `ratesengine-ops <source>-backfill` subcommands to
// re-feed historical rows through the live Go decoders without a
// MinIO walk.
//
// `contractIDs` and `topic0Syms` are inclusive filters: empty means
// "no filter on this dimension". Passing both is the common case
// (per-source backfill scopes by both contract set + emitted topic
// names) and pushes the filter into Postgres so we don't stream
// billions of irrelevant rows over the network.
//
// The callback returning a non-nil error aborts the walk. ctx
// cancellation is checked between rows (the underlying rows.Next()
// blocks on network reads; cancellation propagates through the
// driver).
func (s *Store) StreamSorobanEvents(
	ctx context.Context,
	from, to uint32,
	contractIDs []string,
	topic0Syms []string,
	excludeTopic0Syms []string,
	fn func(sorobanevents.Row) error,
) error {
	if to < from {
		return errors.New("timescale: StreamSorobanEvents: to < from")
	}
	if fn == nil {
		return errors.New("timescale: StreamSorobanEvents: nil callback")
	}

	var sb strings.Builder
	sb.WriteString(`
        SELECT
            ledger, ledger_close_time, tx_hash, op_index, event_index,
            contract_id, contract_id_hex,
            topic_count, topic_0_sym,
            topic_0_xdr, topic_1_xdr, topic_2_xdr, topic_3_xdr,
            body_xdr, op_args_xdr
        FROM soroban_events
        WHERE ledger BETWEEN $1 AND $2
    `)
	args := []any{int64(from), int64(to)}
	if len(contractIDs) > 0 {
		args = append(args, contractIDArgsAny(contractIDs)...)
		fmt.Fprintf(&sb, " AND contract_id IN (%s)", placeholdersFrom(3, len(contractIDs)))
	}
	if len(topic0Syms) > 0 {
		baseIdx := 3 + len(contractIDs)
		args = append(args, topic0Args(topic0Syms)...)
		fmt.Fprintf(&sb, " AND topic_0_sym IN (%s)", placeholdersFrom(baseIdx, len(topic0Syms)))
	}
	// excludeTopic0Syms: drop the CAP-67 classic-token firehose at the SQL
	// layer for the no-prefilter DEX/lending sources, so a far-behind source's
	// wide catch-up window doesn't scan millions of rows it would only discard
	// via Decoder.Matches (see projector.Source.ExcludeTopic0Syms).
	if len(excludeTopic0Syms) > 0 {
		baseIdx := len(args) + 1
		args = append(args, topic0Args(excludeTopic0Syms)...)
		fmt.Fprintf(&sb, " AND (topic_0_sym IS NULL OR topic_0_sym NOT IN (%s))", placeholdersFrom(baseIdx, len(excludeTopic0Syms)))
	}
	// Chunk-pruning (ADR-0033): soroban_events is partitioned by
	// ledger_close_time, so `WHERE ledger BETWEEN` alone scans every
	// chunk. When ledger_ingest_log fully covers [from,to] we can bound
	// ledger_close_time exactly (see SorobanEventsTimeBound) and prune.
	// The fullyCovered guard preserves correctness — a partial bound
	// could exclude in-range rows — so without full coverage we keep the
	// (correct, slower) unpruned scan. Sources that pass a contract /
	// topic prefilter already use those indexes; this helps the
	// match-by-topic sources (soroswap/aquarius/phoenix/comet) that
	// pass no prefilter.
	if lo, hi, covered, terr := s.SorobanEventsTimeBound(ctx, from, to); terr == nil && covered {
		next := len(args) + 1
		args = append(args, lo, hi)
		fmt.Fprintf(&sb, " AND ledger_close_time BETWEEN $%d AND $%d", next, next+1)
	}
	sb.WriteString(" ORDER BY ledger_close_time, ledger, tx_hash, op_index, event_index")

	rows, err := s.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return fmt.Errorf("timescale: StreamSorobanEvents query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			r                      sorobanevents.Row
			ledger                 int64
			opIdx                  int16
			eventIdx               int16
			topicCount             int16
			topic0Sym              sql.NullString
			topic1, topic2, topic3 []byte
			opArgs                 []byte
		)
		if err := rows.Scan(
			&ledger, &r.LedgerCloseTime, &r.TxHash, &opIdx, &eventIdx,
			&r.ContractID, &r.ContractIDHex,
			&topicCount, &topic0Sym,
			&r.Topic0XDR, &topic1, &topic2, &topic3,
			&r.BodyXDR, &opArgs,
		); err != nil {
			return fmt.Errorf("timescale: StreamSorobanEvents scan: %w", err)
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
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

func contractIDArgsAny(ids []string) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}

func topic0Args(syms []string) []any {
	out := make([]any, 0, len(syms))
	for _, s := range syms {
		out = append(out, s)
	}
	return out
}

// placeholdersFrom returns "$N, $N+1, …, $N+count-1" — used to
// embed an IN-clause's placeholders mid-string for parameter-bound
// queries.
func placeholdersFrom(startIdx, count int) string {
	var sb strings.Builder
	for i := 0; i < count; i++ {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "$%d", startIdx+i)
	}
	return sb.String()
}

// LedgerGap is one contiguous block of ledgers with no
// soroban_events row. Produced by [FindSorobanEventsLedgerGaps]
// and consumed by the operator-facing `ratesengine-ops
// find-data-gaps` subcommand + future periodic gap-detection
// metric.
//
// Start and End are inclusive. Size = End - Start + 1. JSON
// tags use snake_case so the subcommand's `--output json` mode
// emits the same shape an operator-facing plan would (consumed
// by shell scripts piping into `ratesengine-ops backfill`).
type LedgerGap struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
	Size  int64 `json:"size"`
}

// FindSorobanEventsLedgerGaps scans the soroban_events.ledger column
// in [from, to] and returns every contiguous gap of size >=
// minGapSize. Used as the **data-derived** alternative to the
// cursor-derived density projection: cursor coverage counts "did
// we walk this ledger" (process measurement); this query counts
// "is the data we should have actually in the table" (reality).
//
// The two diverge under failure modes the cursor inventory can't
// see — the F-0020 cascade-window soroban_events writer halt being
// the canonical example: cursors recorded "advanced past this
// ledger" but the writer's sink had back-pressured to a stop, so
// no rows landed. The honest signal of that failure is the gap in
// distinct-ledger coverage, not the cursor record.
//
// minGapSize filters out the expected event-free gaps (Soroban
// activity is dense but not gap-free — many blocks emit no events).
// Operator-facing usage typically sets minGapSize to ~1000 to
// surface only structurally-significant gaps (a few seconds of
// no-Soroban-activity at the network level is common; ~1.5 h of
// it is the F-0020 cascade signature).
//
// Implementation uses the LAG() window-function pattern: order
// distinct ledgers, compare each row to its predecessor, emit a
// gap when the difference > 1 AND the gap size meets the
// threshold. Cheap on a hypertable with a btree on (ledger_close_time,
// ledger) — postgres uses the index for the ORDER BY scan and
// streams rows through the window function without sorting in
// memory.
func (s *Store) FindSorobanEventsLedgerGaps(ctx context.Context, from, to, minGapSize int64) ([]LedgerGap, error) {
	if to < from {
		return nil, fmt.Errorf("timescale: FindSorobanEventsLedgerGaps: to (%d) < from (%d)", to, from)
	}
	if minGapSize < 1 {
		minGapSize = 1
	}
	const q = `
        WITH ledgers AS (
          SELECT DISTINCT ledger
            FROM soroban_events
           WHERE ledger BETWEEN $1 AND $2
        ),
        ordered AS (
          SELECT ledger, LAG(ledger) OVER (ORDER BY ledger) AS prev_l
            FROM ledgers
        )
        SELECT prev_l + 1 AS gap_start,
               ledger - 1 AS gap_end,
               ledger - prev_l - 1 AS gap_size
          FROM ordered
         WHERE prev_l IS NOT NULL
           AND ledger - prev_l - 1 >= $3
         ORDER BY gap_start
    `
	rows, err := s.db.QueryContext(ctx, q, from, to, minGapSize)
	if err != nil {
		return nil, fmt.Errorf("timescale: FindSorobanEventsLedgerGaps [%d,%d, min %d]: %w", from, to, minGapSize, err)
	}
	defer func() { _ = rows.Close() }()
	var out []LedgerGap
	for rows.Next() {
		var g LedgerGap
		if err := rows.Scan(&g.Start, &g.End, &g.Size); err != nil {
			return nil, fmt.Errorf("timescale: FindSorobanEventsLedgerGaps scan: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: FindSorobanEventsLedgerGaps rows: %w", err)
	}
	return out, nil
}

// CountSorobanEventsInRange returns the row count in the ledger
// range [from, to] inclusive. Test + diagnostic helper — not on
// the hot path. Used by the integration test to assert
// "100 events walked → 100 rows landed".
func (s *Store) CountSorobanEventsInRange(ctx context.Context, from, to uint32) (int64, error) {
	if to < from {
		return 0, errors.New("timescale: CountSorobanEventsInRange: to < from")
	}
	const q = `SELECT count(*) FROM soroban_events WHERE ledger BETWEEN $1 AND $2`
	var n int64
	if err := s.db.QueryRowContext(ctx, q, int64(from), int64(to)).Scan(&n); err != nil {
		return 0, fmt.Errorf("timescale: CountSorobanEventsInRange [%d,%d]: %w", from, to, err)
	}
	return n, nil
}

// nullString maps an empty string to SQL NULL and any other value
// to a populated sql.NullString. Mirrors `nullNumeric` in
// cctp_events.go.
func nullString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

// nullBytes maps a nil byte slice to SQL NULL and any other value
// to the bytes verbatim. Postgres' bytea column accepts the []byte
// form natively via lib/pq's driver.
func nullBytes(v []byte) any {
	if v == nil {
		return nil
	}
	return v
}
