package v1

import (
	"context"
	"net/http"
	"strconv"

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
