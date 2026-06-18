package v1

import (
	"net/http"
	"strings"
	"time"

	"github.com/StellarIndex/stellar-index/internal/storage/clickhouse"
	"github.com/StellarIndex/stellar-index/internal/xdrjson"
)

// OpView is the wire shape for a decoded operation. Type is the snake_case op
// type; Fields holds the decoded, human-readable body (empty for not-yet-decoded
// types, in which case RawXDR carries the original base64 so nothing is lost).
type OpView struct {
	Ledger        uint32         `json:"ledger"`
	CloseTime     string         `json:"close_time"`
	TxHash        string         `json:"tx_hash"`
	TxIndex       uint32         `json:"tx_index"`
	OpIndex       uint32         `json:"op_index"`
	Type          string         `json:"type"`
	SourceAccount string         `json:"source_account,omitempty"`
	Fields        map[string]any `json:"fields,omitempty"`
	RawXDR        string         `json:"raw_xdr,omitempty"`
	// ResultCode is the operation's XDR result code, populated only in the
	// per-transaction view (GET /v1/tx/{hash}); nil in the ledger op list.
	ResultCode *int32 `json:"result_code,omitempty"`
}

// opView decodes an operation row's XDR body into the wire shape. On decode
// failure it degrades to the lake's (normalised) op type + the raw body, so a
// single malformed/unknown op never fails the response.
func opView(o clickhouse.OpRow) OpView {
	v := OpView{
		Ledger:        o.Seq,
		CloseTime:     o.CloseTime.UTC().Format(time.RFC3339),
		TxHash:        o.TxHash,
		TxIndex:       o.TxIndex,
		OpIndex:       o.OpIndex,
		SourceAccount: o.SourceAccount,
	}
	d, err := xdrjson.DecodeOperationBody(o.BodyXDR)
	if err != nil {
		v.Type = normalizeLakeOpType(o.OpType)
		v.RawXDR = o.BodyXDR
		return v
	}
	v.Type = d.Type
	if len(d.Fields) > 0 {
		v.Fields = d.Fields
	}
	if d.RawXDR != "" {
		v.RawXDR = d.RawXDR
	}
	return v
}

// normalizeLakeOpType turns the lake's "OperationTypeManageSellOffer" into a
// best-effort lowercase fallback ("managesselloffer") for the decode-error path
// only — the happy path uses xdrjson's controlled snake_case vocabulary.
func normalizeLakeOpType(s string) string {
	return strings.ToLower(strings.TrimPrefix(s, "OperationType"))
}

// OperationsView is the wire response for GET /v1/operations.
//
// Two shapes on one route: with ?ledger=<seq> it's that ledger's ops
// (Ledger set, no cursor/stats); without it it's the network-wide
// recent-operations directory (Ledger 0, NextCursor for paging, and
// OpTypeStats — the trailing-24h per-type breakdown).
type OperationsView struct {
	Ledger      uint32        `json:"ledger"`
	Operations  []OpView      `json:"operations"`
	NextCursor  string        `json:"next_cursor,omitempty"`
	OpTypeStats []OpTypeStatV `json:"op_type_stats,omitempty"`
}

// OpTypeStatV is one op-type's count in the trailing-24h window.
type OpTypeStatV struct {
	Type  string `json:"type"`
	Count int64  `json:"count"`
}

// handleOperations serves GET /v1/operations.
//
//   - ?ledger=<seq>: that ledger's operations, decoded (partition-pruned).
//   - no ?ledger: the network-wide recent-operations DIRECTORY — newest
//     first, keyset-paged via ?cursor=<opaque> (echo back next_cursor;
//     composite ledger.tx_index.op_index), plus op_type_stats (per-type
//     counts over the trailing ~24h of ledgers).
func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request) {
	if s.explorer == nil {
		s.explorerUnavailable(w, r)
		return
	}
	seq, ok := parseUint32Query(w, r, "ledger")
	if !ok {
		return
	}
	if seq == 0 {
		s.handleOperationsDirectory(w, r)
		return
	}
	limit, ok := parseExplorerLimit(w, r, 500, 2000)
	if !ok {
		return
	}
	rows, err := s.explorer.OperationsByLedger(r.Context(), seq, limit)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("explorer OperationsByLedger failed", "err", err, "seq", seq)
		writeProblem(w, r, "https://api.stellarindex.io/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	out := OperationsView{Ledger: seq, Operations: make([]OpView, len(rows))}
	for i, o := range rows {
		out.Operations[i] = opView(o)
	}
	writeJSON(w, out, Flags{})
}

// NetworkThroughputView is the wire response for GET
// /v1/network/throughput — a daily time-series of network counts.
type NetworkThroughputView struct {
	WindowDays int                 `json:"window_days"`
	Buckets    []ThroughputBucketV `json:"buckets"`
}

type ThroughputBucketV struct {
	Day     string `json:"day"`
	Ledgers int64  `json:"ledgers"`
	Txs     int64  `json:"txs"`
	Ops     int64  `json:"ops"`
	Events  int64  `json:"events"`
}

// handleNetworkThroughput serves GET /v1/network/throughput — daily
// ledger / transaction / operation / Soroban-event counts over the
// trailing `?window_days=` (default 30, max 365), ascending by day.
// The time-series companion to the /v1/network/stats snapshot.
func (s *Server) handleNetworkThroughput(w http.ResponseWriter, r *http.Request) {
	if s.explorer == nil {
		s.explorerUnavailable(w, r)
		return
	}
	windowDays := parseWindowDays(r, 30)
	buckets, err := s.explorer.NetworkThroughput(r.Context(), windowDays)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("explorer NetworkThroughput failed", "err", err)
		writeProblem(w, r, "https://api.stellarindex.io/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	out := NetworkThroughputView{WindowDays: windowDays, Buckets: make([]ThroughputBucketV, len(buckets))}
	for i, b := range buckets {
		out.Buckets[i] = ThroughputBucketV{
			Day:     b.Day.UTC().Format("2006-01-02"),
			Ledgers: b.Ledgers, Txs: b.Txs, Ops: b.Ops, Events: b.Events,
		}
	}
	writeJSON(w, out, Flags{})
}

// handleOperationsDirectory serves the no-ledger path: network-wide
// recent operations (keyset-paged) + the trailing-24h op-type stats.
func (s *Server) handleOperationsDirectory(w http.ResponseWriter, r *http.Request) {
	limit, ok := parseExplorerLimit(w, r, 50, 200)
	if !ok {
		return
	}
	cur, ok := parseExplorerCursor(w, r, 3) // (ledger, tx_index, op_index)
	if !ok {
		return
	}
	rows, err := s.explorer.RecentOperations(r.Context(), limit, cur)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("explorer RecentOperations failed", "err", err)
		writeProblem(w, r, "https://api.stellarindex.io/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	out := OperationsView{Operations: make([]OpView, len(rows))}
	for i, o := range rows {
		out.Operations[i] = opView(o)
	}
	if n := len(rows); n == limit {
		last := rows[n-1]
		out.NextCursor = encodeCursor(last.Seq, last.TxIndex, last.OpIndex)
	}
	// Op-type stats are best-effort context — a failure here shouldn't
	// fail the listing (only attached on the first page to keep paging
	// responses lean).
	if !cur.IsSet() {
		if stats, serr := s.explorer.OperationTypeStats(r.Context(), 0); serr == nil {
			out.OpTypeStats = make([]OpTypeStatV, len(stats))
			for i, st := range stats {
				out.OpTypeStats[i] = OpTypeStatV{Type: normalizeLakeOpType(st.OpType), Count: st.Count}
			}
		} else if !clientAborted(r, serr) {
			s.logger.Warn("explorer OperationTypeStats failed", "err", serr)
		}
	}
	writeJSON(w, out, Flags{})
}
