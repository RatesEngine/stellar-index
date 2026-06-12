package sorobanevents

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// Reconstruct projects a soroban_events Row back into an
// [events.Event] suitable for feeding into a per-source decoder.
// This is the inverse of [Capture] for the fields that the
// historical-backfill path needs: contract_id, topics 0-3 (base64-
// re-encoded so the standard scval.Parse pathway works), body, and
// the originating op args when present.
//
// The returned Event has Type="contract", LedgerClosedAt formatted
// as RFC 3339 (matching what the dispatcher would have stamped),
// and OpArgs reconstructed from the stored ScVec when non-nil.
// ID + TransactionIndex are left empty — they're metadata for
// stellar-rpc replays, not used by any decoder.
//
// Used by `ratesengine-ops <source>-backfill` subcommands to walk
// soroban_events for a historical range and re-feed the same Go
// decoder live ingest uses, persisting to the per-source hypertable.
func Reconstruct(row Row) (events.Event, error) {
	if row.ContractID == "" {
		return events.Event{}, fmt.Errorf("sorobanevents.Reconstruct: empty contract_id (ledger=%d)", row.Ledger)
	}
	if len(row.Topic0XDR) == 0 {
		return events.Event{}, fmt.Errorf("sorobanevents.Reconstruct: missing topic_0_xdr (ledger=%d, contract=%s)", row.Ledger, row.ContractID)
	}
	if len(row.TxHash) != 32 {
		return events.Event{}, fmt.Errorf("sorobanevents.Reconstruct: tx_hash is %d bytes, want 32 (ledger=%d)", len(row.TxHash), row.Ledger)
	}

	topics := reconstructTopics(row)

	var opArgs []string
	if len(row.OpArgsXDR) > 0 {
		out, err := scval.DecodeScVecToArgs(row.OpArgsXDR)
		if err != nil {
			return events.Event{}, fmt.Errorf("sorobanevents.Reconstruct: op_args: %w", err)
		}
		opArgs = out
	}

	return events.Event{
		Type:           "contract",
		Ledger:         row.Ledger,
		LedgerClosedAt: row.LedgerCloseTime.UTC().Format(time.RFC3339),
		ContractID:     row.ContractID,
		OperationIndex: int(row.OpIndex),
		EventIndex:     int(row.EventIndex),
		TxHash:         hex.EncodeToString(row.TxHash),
		Topic:          topics,
		Value:          base64.StdEncoding.EncodeToString(row.BodyXDR),
		OpArgs:         opArgs,
	}, nil
}

// reconstructTopics base64-re-encodes the stored topic XDR bytes
// into the slice shape decoders expect. Trims to the row's
// TopicCount so events that legitimately had fewer than 4 topics
// don't carry empty trailing strings.
func reconstructTopics(row Row) []string {
	xdrs := [4][]byte{row.Topic0XDR, row.Topic1XDR, row.Topic2XDR, row.Topic3XDR}
	want := int(row.TopicCount)
	if want > 4 {
		// The migration only stores topics 0-3; reflect that cap
		// rather than the higher count the original event had.
		want = 4
	}
	out := make([]string, 0, want)
	for i := 0; i < want; i++ {
		if len(xdrs[i]) == 0 {
			continue
		}
		out = append(out, base64.StdEncoding.EncodeToString(xdrs[i]))
	}
	return out
}
