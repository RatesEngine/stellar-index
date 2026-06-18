package v1

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/StellarIndex/stellar-index/internal/storage/clickhouse"
)

// ExplorerReader is the seam the network-explorer endpoints (ADR-0038) read
// through: the certified Tier-1 ClickHouse lake (the full chain to genesis —
// ledgers / transactions / operations / contract events). *clickhouse.ExplorerReader
// satisfies it. Nil disables the explorer endpoints (503). The interface grows
// per ADR-0038 phase (A: ledgers/tx/ops/contracts; B: account history; C: state).
type ExplorerReader interface {
	RecentLedgers(ctx context.Context, limit int, beforeSeq uint32) ([]clickhouse.LedgerHeader, error)
	LedgerBySeq(ctx context.Context, seq uint32) (clickhouse.LedgerHeader, bool, error)
	LedgerTransactions(ctx context.Context, seq uint32, limit int) ([]clickhouse.TxSummary, error)
	OperationsByLedger(ctx context.Context, seq uint32, limit int) ([]clickhouse.OpRow, error)
	RecentOperations(ctx context.Context, limit int, cur clickhouse.ExplorerCursor) ([]clickhouse.OpRow, error)
	OperationTypeStats(ctx context.Context, windowLedgers uint32) ([]clickhouse.OpTypeCount, error)
	NetworkThroughput(ctx context.Context, windowDays int) ([]clickhouse.ThroughputBucket, error)
	TransactionByHash(ctx context.Context, hash string) (clickhouse.TxSummary, bool, error)
	OperationsByTx(ctx context.Context, seq uint32, hash string) ([]clickhouse.OpRow, error)
	OperationResultsByTx(ctx context.Context, seq uint32, hash string) (map[uint32]int32, error)
	EventsByTx(ctx context.Context, seq uint32, hash string) ([]clickhouse.EventSummary, error)
	ContractEventsRecent(ctx context.Context, contractID string, limit int, cur clickhouse.ExplorerCursor) ([]clickhouse.ContractActivityRow, error)
	ContractWasm(ctx context.Context, contractID string) (clickhouse.ContractWasmInfo, error)
	RecentContracts(ctx context.Context, limit int, sinceLedger uint32) ([]clickhouse.ContractDirectoryRow, error)
	ContractInteractions(ctx context.Context, contractID string, limit int, sinceLedger uint32) ([]clickhouse.ContractEdgeRow, error)
	ContractCodeHistory(ctx context.Context, contractID string) ([]clickhouse.ContractCodeVersion, error)
	AccountTransactions(ctx context.Context, account string, limit int, cur clickhouse.ExplorerCursor) ([]clickhouse.TxSummary, error)
	AccountOperations(ctx context.Context, account string, limit int, cur clickhouse.ExplorerCursor) ([]clickhouse.OpRow, error)
	AccountState(ctx context.Context, account string) (clickhouse.AccountState, error)
	AssetHolders(ctx context.Context, asset string, limit int) ([]clickhouse.AssetHolder, int64, error)
	AccountsByWealth(ctx context.Context, assets []string, prices []float64, limit int) ([]clickhouse.AccountWealth, error)
}

// explorerUnavailable writes the standard 503 when no explorer reader is wired
// (deployment without ClickHouse, or ClickHouse unreachable at startup).
func (s *Server) explorerUnavailable(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, r,
		"https://api.stellarindex.io/errors/explorer-unavailable",
		"Explorer unavailable", http.StatusServiceUnavailable,
		"This deployment hasn't wired the ClickHouse explorer reader (ADR-0038).")
}

// parseExplorerLimit parses ?limit= with a default and an inclusive cap.
// ok=false (after writing a problem+json) on parse error / out of range.
func parseExplorerLimit(w http.ResponseWriter, r *http.Request, def, maxN int) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxN {
		writeProblem(w, r, "https://api.stellarindex.io/errors/invalid-limit",
			"Invalid limit", http.StatusBadRequest,
			"limit must be an integer in [1, "+strconv.Itoa(maxN)+"]")
		return 0, false
	}
	return n, true
}

// parseUint32Query parses an optional uint32 query param (e.g. ?before=).
// Returns 0 when absent. ok=false (after a problem+json) on a malformed value.
func parseUint32Query(w http.ResponseWriter, r *http.Request, name string) (uint32, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		writeProblem(w, r, "https://api.stellarindex.io/errors/invalid-parameter",
			"Invalid parameter", http.StatusBadRequest,
			name+" must be a non-negative 32-bit integer")
		return 0, false
	}
	return uint32(n), true
}

// encodeCursor renders a composite keyset position as an opaque dotted-decimal
// cursor string ("63000000.4.7") for ?cursor=. The component count matches the
// listing's ORDER BY arity (2 for account txs, 3 for account ops + contract
// events). Clients treat it as opaque and echo it back verbatim.
func encodeCursor(parts ...uint32) string {
	ss := make([]string, len(parts))
	for i, p := range parts {
		ss[i] = strconv.FormatUint(uint64(p), 10)
	}
	return strings.Join(ss, ".")
}

// parseExplorerCursor reads the optional ?cursor= opaque keyset cursor and
// decodes it into an ExplorerCursor with exactly `parts` components (2 → account
// txs; 3 → account ops + contract events). Absent → zero cursor (first page).
// ok=false (after a problem+json) on a malformed value or a zero ledger (a real
// cursor always points past an actual row).
func parseExplorerCursor(w http.ResponseWriter, r *http.Request, parts int) (clickhouse.ExplorerCursor, bool) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return clickhouse.ExplorerCursor{}, true
	}
	bad := func() (clickhouse.ExplorerCursor, bool) {
		writeProblem(w, r, "https://api.stellarindex.io/errors/invalid-cursor",
			"Invalid cursor", http.StatusBadRequest,
			"cursor must be an opaque value returned in a prior next_cursor")
		return clickhouse.ExplorerCursor{}, false
	}
	segs := strings.Split(raw, ".")
	if len(segs) != parts {
		return bad()
	}
	vals := make([]uint32, parts)
	for i, s := range segs {
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return bad()
		}
		vals[i] = uint32(n)
	}
	if vals[0] == 0 {
		return bad()
	}
	cur := clickhouse.ExplorerCursor{Ledger: vals[0], A: vals[1]}
	if parts == 3 {
		cur.B = vals[2]
	}
	return cur, true
}
