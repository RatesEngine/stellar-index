package stellarrpc

import (
	"encoding/json"
	"fmt"

	"github.com/RatesEngine/rates-engine/internal/events"
)

// Event is now a deprecated alias for [events.Event]. The type
// moved to internal/events during PR 165b so source decoders
// could stop depending on the JSON-RPC client package.
//
// Deprecated: use events.Event directly. Alias retained so the
// RPC-based consumer loops in the source packages keep compiling
// during the 165b → 165d transition; removed in PR 165d when the
// indexer retires the per-source orchestrator.
type Event = events.Event

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
// github.com/stellar/go-stellar-sdk/xdr (the post-monorepo
// SDK; stellar/go was archived 2025-12-16). The dependency
// scope decision is ADR-0013; the structural guardrail that
// keeps `xdr` out of decoder packages outside of internal/scval
// lives in scripts/ci/lint-imports.sh rule B.
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

// simulateParams is the JSON-RPC params for simulateTransaction.
type simulateParams struct {
	Transaction string `json:"transaction"` // base64-encoded xdr.TransactionEnvelope
}

// SimulationResponse is the simulateTransaction response. Only the
// fields we actually consume are modelled; the full response also
// includes resource-fee estimates, state-entry deltas, and auth
// hints that aren't useful for read-only view calls.
type SimulationResponse struct {
	LatestLedger uint32             `json:"latestLedger"`
	Results      []SimulationResult `json:"results,omitempty"`
	// Error is populated when the contract call failed (host trap,
	// panic, out-of-gas). Non-empty Error means Results is empty.
	Error string `json:"error,omitempty"`
	// MinResourceFee is a string decimal per stellar-rpc schema;
	// unused for simulation-only callers but exposed for any future
	// tx-building path.
	MinResourceFee string `json:"minResourceFee,omitempty"`
}

// SimulationResult is one entry in SimulationResponse.Results. XDR
// holds the base64-encoded SCVal return value of the invoked
// contract function. Auth is the list of required authorization
// entries (empty for public view functions); included so callers
// building a real tx from a simulation see what still needs
// signing.
type SimulationResult struct {
	XDR  string   `json:"xdr"`            // base64 SCVal return value
	Auth []string `json:"auth,omitempty"` // base64 SorobanAuthorizationEntry
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
