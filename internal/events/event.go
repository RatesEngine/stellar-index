// Package events holds transport-neutral Soroban contract-event
// types — identical whether the event was observed via stellar-rpc
// getEvents (development / fixture capture) or extracted from
// ledger-meta via internal/dispatcher (production).
//
// Per docs/architecture/ingest-pipeline.md, decoder packages read
// events.Event and nothing else. They don't import internal/stellarrpc
// (which would pull in the JSON-RPC client surface) and they don't
// import xdr (which would bypass the internal/scval wrapper). The
// one shape, one boundary.
//
// Historically this type lived in internal/stellarrpc; stellarrpc
// now re-exports it via a type alias for the source packages that
// still run RPC-based consumer loops (to be retired in PR 165d).
package events

import (
	"fmt"
	"time"
)

// Event is a single Soroban contract event. JSON tags are preserved
// from the stellar-rpc getEvents response shape — the JSON-RPC
// client still populates this type, and RPC fixture replays in the
// dev scripts hit the same struct.
type Event struct {
	Type                     string `json:"type"` // contract | system | diagnostic
	Ledger                   uint32 `json:"ledger"`
	LedgerClosedAt           string `json:"ledgerClosedAt"` // RFC 3339
	ContractID               string `json:"contractId"`
	ID                       string `json:"id"`
	OperationIndex           int    `json:"operationIndex"`
	TransactionIndex         int    `json:"transactionIndex"`
	TxHash                   string `json:"txHash"`
	InSuccessfulContractCall bool   `json:"inSuccessfulContractCall"`

	// Topic entries are base64-encoded SCVal. Decoders parse via
	// internal/scval.
	Topic []string `json:"topic"`
	// Value is base64-encoded SCVal. Decoders parse via
	// internal/scval.
	Value string `json:"value"`

	// OpArgs carries the base64-encoded SCVal arguments of the
	// InvokeHostFunction operation that produced this event, when
	// that op invoked a contract via HostFunctionTypeInvokeContract.
	// Populated by internal/dispatcher from
	// tx.Envelope.Operations()[OperationIndex].Body.InvokeHostFunctionOp
	// .HostFunction.InvokeContract.Args.
	//
	// Empty for events not produced by an InvokeContract call, for
	// events observed via stellar-rpc getEvents (which doesn't
	// surface the invoking op's args), and for CAP-67 transfer
	// events tied to classic ops.
	//
	// Redstone is the current primary user: the WritePrices event's
	// `updated_feeds` vec carries price+timestamps but no feed_id;
	// the feed_ids live in the op args and need to be zipped in at
	// decode time. See docs/discovery/oracles/redstone.md.
	//
	// NOT serialized in the stellar-rpc JSON shape — `omitempty` so
	// fixture replays from RPC round-trip unchanged.
	OpArgs []string `json:"opArgs,omitempty"`
}

// EventClosedAt parses the RFC 3339 LedgerClosedAt string into a
// time.Time. Centralised so every source package doesn't redo the
// parse+error-handling dance.
//
// An empty LedgerClosedAt returns an error rather than the zero
// time.Time: a zero-valued timestamp flowing through to the trades
// hypertable breaks VWAP windows and time-ordered queries, so
// surfacing the parse failure at the decoder boundary is the fail-
// closed choice.
func (e *Event) EventClosedAt() (time.Time, error) {
	if e.LedgerClosedAt == "" {
		return time.Time{}, fmt.Errorf("events: event %s has empty ledgerClosedAt", e.ID)
	}
	t, err := time.Parse(time.RFC3339, e.LedgerClosedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("events: event %s ledgerClosedAt %q: %w", e.ID, e.LedgerClosedAt, err)
	}
	return t, nil
}
