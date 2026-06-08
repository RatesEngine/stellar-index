package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/RatesEngine/rates-engine/internal/events"
)

// ClassicTokenTopic0Syms are the CAP-67 / SEP-41 token-event topic[0] symbols.
// Under the r1 archive's uniform V4 meta these classic-asset transfer/mint/burn
// events are synthesized for ALL history and utterly dominate contract_events
// (446.91 M of 446.92 M rows in partition 50 — 99.9988 %). No protocol DEX /
// lending decoder (soroswap/aquarius/phoenix/comet/blend/cctp/rozo/defindex)
// consumes them, so a re-derivation pass for those sources can exclude them via
// StreamContractEvents' excludeTopic0 arg, turning a 447 M-row partition scan
// into a ~5 k-row one. (sep41_supply/sep41_transfers DO use these topics, so
// never exclude them when re-deriving those sources.)
var ClassicTokenTopic0Syms = []string{
	"transfer", "mint", "burn", "clawback", "approve", "set_admin", "set_authorized",
}

// sqlQuoteList renders a string slice as a SQL IN list: 'a','b',... The inputs
// are compile-time constants (topic symbols), so inlining carries no injection
// risk and avoids driver-specific slice-binding for IN (?).
func sqlQuoteList(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = "'" + s + "'"
	}
	return strings.Join(q, ",")
}

// StreamContractEvents is the Phase-4 input adapter (ADR-0034): it reads
// stellar.contract_events for [from,to] inclusive, ordered by
// (ledger_seq, tx_hash, op_index, event_index) — the dispatcher's natural
// emission order — and invokes fn for each row reconstructed as an
// events.Event.
//
// The CH columns are a byte-identical serialization of events.Event: topics,
// value, and op-args are all base64(scval.MarshalBinary), exactly as the
// production dispatcher writes them (internal/dispatcher.contractEventToEventsEvent
// at dispatcher.go:881/:907 vs the extractor's eventRow at extract.go:181/:206).
// So the existing protocol decoders consume these events verbatim — no
// re-encoding, no galexie re-touch.
//
// FINAL dedups concurrent/duplicate ReplacingMergeTree parts at read time.
// Callers re-projecting all history should window [from,to] (e.g. per 1M-ledger
// partition) so the streamed result set stays bounded in memory.
//
// Note: ID and TransactionIndex are left zero — the CH lake keys events by
// (ledger, tx_hash, op_index, event_index) and decoders use TxHash, not the
// RPC-shape ID/tx-index. If a future decoder needs tx_index, add it to the
// contract_events schema + extractor first.
// StreamContractEventsFiltered is the projector's forward-read source (ADR-0034
// #10 feed-switch): it streams contract_events for [from,to] narrowed by a
// per-source prefilter (contract_id IN / topic_0_sym IN — mirrors the Postgres
// soroban_events path's prefilter), reconstructing each as an events.Event for
// the source's decoder. NO FINAL: the projector reads small forward windows and
// its downstream writes are idempotent (ON CONFLICT DO NOTHING), so a duplicate
// event decodes to the same row and is absorbed — FINAL's full-partition merge
// would be pure overhead here. Empty filters → match-by-Decoder.Matches alone
// (coarser, but the window is BatchLimit-bounded).
func StreamContractEventsFiltered(ctx context.Context, addr string, from, to uint32, contractIDs, topic0Syms []string, fn func(events.Event) error) error {
	conn, err := openRead(ctx, addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	where := "WHERE ledger_seq BETWEEN ? AND ?"
	if len(contractIDs) > 0 {
		where += " AND contract_id IN (" + sqlQuoteList(contractIDs) + ")"
	}
	if len(topic0Syms) > 0 {
		where += " AND topic_0_sym IN (" + sqlQuoteList(topic0Syms) + ")"
	}
	rows, err := conn.Query(ctx, fmt.Sprintf(`
		SELECT ledger_seq, close_time, tx_hash, op_index, event_index,
		       contract_id, event_type, topics_xdr, data_xdr, op_args_xdr,
		       in_successful_call
		FROM stellar.contract_events
		%s
		ORDER BY ledger_seq, tx_hash, op_index, event_index`, where), from, to)
	if err != nil {
		return fmt.Errorf("clickhouse: query contract_events filtered [%d,%d]: %w", from, to, err)
	}
	defer func() { _ = rows.Close() }()
	return scanContractEvents(rows, fn)
}

// excludeTopic0 (nil = no filter) drops events whose topic[0] symbol is in the
// list — used to skip the CAP-67 classic-token firehose when re-deriving
// protocol sources that don't consume it (see ClassicTokenTopic0Syms).
func StreamContractEvents(ctx context.Context, addr string, from, to uint32, excludeTopic0 []string, fn func(events.Event) error) error {
	conn, err := openRead(ctx, addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	where := "WHERE ledger_seq BETWEEN ? AND ?"
	if len(excludeTopic0) > 0 {
		where += " AND topic_0_sym NOT IN (" + sqlQuoteList(excludeTopic0) + ")"
	}
	rows, err := conn.Query(ctx, fmt.Sprintf(`
		SELECT ledger_seq, close_time, tx_hash, op_index, event_index,
		       contract_id, event_type, topics_xdr, data_xdr, op_args_xdr,
		       in_successful_call
		FROM stellar.contract_events FINAL
		%s
		ORDER BY ledger_seq, tx_hash, op_index, event_index`, where), from, to)
	if err != nil {
		return fmt.Errorf("clickhouse: query contract_events [%d,%d]: %w", from, to, err)
	}
	defer func() { _ = rows.Close() }()
	return scanContractEvents(rows, fn)
}

// scanContractEvents maps contract_events result rows to events.Event and
// invokes fn for each. Shared by StreamContractEvents (FINAL, exclude-filter)
// and StreamContractEventsFiltered (no-FINAL, per-source prefilter).
func scanContractEvents(rows driver.Rows, fn func(events.Event) error) error {
	for rows.Next() {
		var (
			ledger     uint32
			closeTime  time.Time
			txHash     string
			opIndex    uint32
			eventIndex uint32
			contractID string
			eventType  string
			topics     []string
			dataXDR    string
			opArgs     []string
			inSucc     uint8
		)
		if err := rows.Scan(&ledger, &closeTime, &txHash, &opIndex, &eventIndex,
			&contractID, &eventType, &topics, &dataXDR, &opArgs, &inSucc); err != nil {
			return fmt.Errorf("clickhouse: scan contract_event: %w", err)
		}
		if err := fn(events.Event{
			Type:                     eventType,
			Ledger:                   ledger,
			LedgerClosedAt:           closeTime.UTC().Format(time.RFC3339),
			ContractID:               contractID,
			OperationIndex:           int(opIndex),
			EventIndex:               int(eventIndex),
			TxHash:                   txHash,
			InSuccessfulContractCall: inSucc != 0,
			Topic:                    topics,
			Value:                    dataXDR,
			OpArgs:                   opArgs,
		}); err != nil {
			return err
		}
	}
	return rows.Err()
}
