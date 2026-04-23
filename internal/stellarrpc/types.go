package stellarrpc

import (
	"encoding/json"
	"fmt"
	"time"
)

// EventClosedAt parses the RFC 3339 ledgerClosedAt on an Event.
//
// Every source package used to write `t, _ := time.Parse(...)` and
// silently drop parse errors — unparseable timestamps then flowed
// through as time.Time{} and landed in the trades hypertable with
// a zero observed_at, breaking VWAP windows and time-ordered
// queries. Centralising the parse lets callers treat a bad
// timestamp as a decode error (metric + skip) instead of garbage
// data.
func (e *Event) EventClosedAt() (time.Time, error) {
	if e.LedgerClosedAt == "" {
		return time.Time{}, fmt.Errorf("stellarrpc: event %s has empty ledgerClosedAt", e.ID)
	}
	t, err := time.Parse(time.RFC3339, e.LedgerClosedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("stellarrpc: event %s ledgerClosedAt %q: %w", e.ID, e.LedgerClosedAt, err)
	}
	return t, nil
}

// sanityCheck validates that a getEvents response is internally
// consistent. Caught conditions:
//
//   - OldestLedger > LatestLedger — RPC served from an inconsistent
//     view (node mid-catchup, node forked, node buggy).
//   - Any event's Ledger outside [OldestLedger, LatestLedger] —
//     event slipped in from a different shard or is stale past
//     retention; either way we don't trust it.
//   - Events not in monotonically-ascending Ledger order — source
//     buffers (Soroswap swap+sync, Phoenix 8-field) assume in-order
//     delivery; an out-of-order page could silently correlate the
//     wrong events.
//
// Checks run at zero cost when the response is well-formed.
// Returns a wrapped error pointing at the offending event when it
// isn't — call site turns that into a retry with backoff (the
// source's existing error path) rather than ingesting garbage.
func (r *EventsResponse) sanityCheck() error {
	if r.OldestLedger > 0 && r.LatestLedger > 0 && r.OldestLedger > r.LatestLedger {
		return fmt.Errorf("stellarrpc: response has OldestLedger %d > LatestLedger %d",
			r.OldestLedger, r.LatestLedger)
	}
	var prev uint32
	for i := range r.Events {
		l := r.Events[i].Ledger
		// Stellar genesis is ledger 1; Ledger=0 means the field was
		// either absent from the JSON payload or the node returned a
		// malformed record. Either way, downstream groupKey builders
		// (phoenix/soroswap fanout) would collide on (0, tx, opIdx)
		// for multiple unrelated events. Reject at the boundary.
		if l == 0 {
			return fmt.Errorf("stellarrpc: event %s has zero ledger", r.Events[i].ID)
		}
		if r.LatestLedger > 0 && l > r.LatestLedger {
			return fmt.Errorf("stellarrpc: event %s ledger %d > response LatestLedger %d",
				r.Events[i].ID, l, r.LatestLedger)
		}
		if r.OldestLedger > 0 && l < r.OldestLedger {
			return fmt.Errorf("stellarrpc: event %s ledger %d < response OldestLedger %d",
				r.Events[i].ID, l, r.OldestLedger)
		}
		if l < prev {
			return fmt.Errorf("stellarrpc: events out of order — event %s ledger %d after %d",
				r.Events[i].ID, l, prev)
		}
		prev = l
	}
	return nil
}

// ─── Envelope ──────────────────────────────────────────────────────

type jsonrpcRequest struct {
	Version string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	Version string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError is the standard JSON-RPC 2.0 error payload.
// Callers typically classify via errors.Is / Error.Code.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("stellar-rpc error %d: %s", e.Code, e.Message)
}

// ─── Method response types ─────────────────────────────────────────

// Health is the response from getHealth.
//
// Status is "healthy" when the node's last-applied ledger is within
// the configured staleness threshold. When the node is catching up
// or disconnected, this RPC returns an error envelope — callers
// should check for that error AND then this Status field.
type Health struct {
	Status             string `json:"status"`
	LatestLedger       uint32 `json:"latestLedger,omitempty"`
	OldestLedger       uint32 `json:"oldestLedger,omitempty"`
	LedgerRetentionWin uint32 `json:"ledgerRetentionWindow,omitempty"`
}

// LatestLedger is the response from getLatestLedger.
type LatestLedger struct {
	ID              string `json:"id"` // 32-byte hex ledger hash
	ProtocolVersion int    `json:"protocolVersion"`
	Sequence        uint32 `json:"sequence"`
	CloseTime       string `json:"closeTime"`           // Unix seconds as decimal string
	HeaderXdr       string `json:"headerXdr,omitempty"` // base64
}

// Network is the response from getNetwork.
type Network struct {
	Passphrase      string `json:"passphrase"`
	ProtocolVersion int    `json:"protocolVersion"`
}

// VersionInfo is the response from getVersionInfo.
type VersionInfo struct {
	Version            string `json:"version"`
	CommitHash         string `json:"commitHash"`
	BuildTimestamp     string `json:"buildTimestamp"`
	CaptiveCoreVersion string `json:"captiveCoreVersion"`
	ProtocolVersion    int    `json:"protocolVersion"`
}

// Event is a single Soroban contract event from getEvents.
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
	// Topic entries are base64-encoded SCVal. Callers decode.
	Topic []string `json:"topic"`
	// Value is base64-encoded SCVal. Callers decode.
	Value string `json:"value"`
}

// EventsResponse is the response from getEvents.
type EventsResponse struct {
	Events                []Event `json:"events"`
	Cursor                string  `json:"cursor,omitempty"`
	LatestLedger          uint32  `json:"latestLedger"`
	OldestLedger          uint32  `json:"oldestLedger"`
	LatestLedgerCloseTime string  `json:"latestLedgerCloseTime,omitempty"`
	OldestLedgerCloseTime string  `json:"oldestLedgerCloseTime,omitempty"`
}

// EventFilter restricts which events getEvents returns.
//
// Type: "contract" | "system" | "diagnostic" (or "" for all).
// ContractIDs: optional allow-list of C-address strings.
// Topics: optional list of per-position topic patterns (base64 SCVal
// or the literal "*" wildcard per the stellar-rpc wire contract).
type EventFilter struct {
	Type        string     `json:"type,omitempty"`
	ContractIDs []string   `json:"contractIds,omitempty"`
	Topics      [][]string `json:"topics,omitempty"`
}

type eventsParams struct {
	StartLedger uint32        `json:"startLedger,omitempty"`
	EndLedger   uint32        `json:"endLedger,omitempty"`
	Filters     []EventFilter `json:"filters,omitempty"`
	Pagination  *Pagination   `json:"pagination,omitempty"`
}

// Pagination is shared between getEvents / getLedgers / etc.
type Pagination struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// Ledger is one entry from getLedgers.
type Ledger struct {
	Hash            string `json:"hash"`
	Sequence        uint32 `json:"sequence"`
	LedgerCloseTime string `json:"ledgerCloseTime"` // Unix seconds string
	HeaderXdr       string `json:"headerXdr"`       // base64
	MetadataXdr     string `json:"metadataXdr"`     // base64
}

// LedgersResponse is the response from getLedgers.
type LedgersResponse struct {
	Ledgers      []Ledger `json:"ledgers"`
	Cursor       string   `json:"cursor,omitempty"`
	LatestLedger uint32   `json:"latestLedger"`
	OldestLedger uint32   `json:"oldestLedger"`
}

type ledgersParams struct {
	StartLedger uint32      `json:"startLedger,omitempty"`
	Pagination  *Pagination `json:"pagination,omitempty"`
}

// ─── getTransaction / getTransactions ─────────────────────────────

// TransactionStatus is the coarse outcome of a single tx per
// stellar-rpc. SUCCESS = included and applied; FAILED = included but
// errored; NOT_FOUND = not in the RPC's retention window (check
// archive for older txs).
type TransactionStatus string

const (
	TxStatusSuccess  TransactionStatus = "SUCCESS"
	TxStatusFailed   TransactionStatus = "FAILED"
	TxStatusNotFound TransactionStatus = "NOT_FOUND"
)

// TransactionResponse is the response from getTransaction.
//
// XDR fields are base64-encoded; callers decode via
// github.com/stellar/go-stellar-sdk/xdr (monorepo archived
// 2025-12-16, see ADR-0001 + CLAUDE.md).
type TransactionResponse struct {
	Status TransactionStatus `json:"status"`

	LatestLedger          uint32 `json:"latestLedger"`
	LatestLedgerCloseTime string `json:"latestLedgerCloseTime,omitempty"`
	OldestLedger          uint32 `json:"oldestLedger"`
	OldestLedgerCloseTime string `json:"oldestLedgerCloseTime,omitempty"`

	// Present only when Status != NOT_FOUND.
	Ledger           uint32 `json:"ledger,omitempty"`
	CreatedAt        string `json:"createdAt,omitempty"` // RFC 3339
	ApplicationOrder int    `json:"applicationOrder,omitempty"`
	FeeBump          bool   `json:"feeBump,omitempty"`
	EnvelopeXdr      string `json:"envelopeXdr,omitempty"`
	ResultXdr        string `json:"resultXdr,omitempty"`
	ResultMetaXdr    string `json:"resultMetaXdr,omitempty"`
	LedgerCloseTime  string `json:"ledgerCloseTime,omitempty"`

	// DiagnosticEventsXdr is populated only on stellar-rpc v23+. On
	// older nodes this field is empty; decoders should treat absence
	// as "unknown" rather than "none". Useful for understanding why a
	// Soroban tx failed — errors are surfaced here as events.
	DiagnosticEventsXdr []string `json:"diagnosticEventsXdr,omitempty"`
}

type transactionParams struct {
	Hash string `json:"hash"`
}

// TransactionsResponse is the response from getTransactions.
//
// stellar-rpc paginates via cursor. Each entry is a full
// TransactionResponse (minus the envelope-level latest/oldest ledger
// fields which live on the outer response).
type TransactionsResponse struct {
	Transactions          []TransactionResponse `json:"transactions"`
	Cursor                string                `json:"cursor,omitempty"`
	LatestLedger          uint32                `json:"latestLedger"`
	LatestLedgerCloseTime string                `json:"latestLedgerCloseTime,omitempty"`
	OldestLedger          uint32                `json:"oldestLedger"`
	OldestLedgerCloseTime string                `json:"oldestLedgerCloseTime,omitempty"`
}

type transactionsParams struct {
	StartLedger uint32      `json:"startLedger,omitempty"`
	Pagination  *Pagination `json:"pagination,omitempty"`
}

// FeeStats is the response from getFeeStats.
type FeeStats struct {
	SorobanInclusionFee FeePercentiles `json:"sorobanInclusionFee"`
	InclusionFee        FeePercentiles `json:"inclusionFee"`
	LatestLedger        uint32         `json:"latestLedger"`
}

// FeePercentiles are p10/p20/…/p99 distributions, as decimal strings
// to preserve i128 safety.
type FeePercentiles struct {
	Max              string `json:"max"`
	Min              string `json:"min"`
	Mode             string `json:"mode"`
	P10              string `json:"p10"`
	P20              string `json:"p20"`
	P30              string `json:"p30"`
	P40              string `json:"p40"`
	P50              string `json:"p50"`
	P60              string `json:"p60"`
	P70              string `json:"p70"`
	P80              string `json:"p80"`
	P90              string `json:"p90"`
	P95              string `json:"p95"`
	P99              string `json:"p99"`
	TransactionCount string `json:"transactionCount"`
	LedgerCount      int    `json:"ledgerCount"`
}
