package v1_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	v1 "github.com/StellarIndex/stellar-index/internal/api/v1"
	"github.com/StellarIndex/stellar-index/internal/storage/clickhouse"
)

type stubExplorerReader struct {
	ledgers []clickhouse.LedgerHeader
	txs     []clickhouse.TxSummary
	err     error
}

func (s *stubExplorerReader) RecentLedgers(_ context.Context, _ int, _ uint32) ([]clickhouse.LedgerHeader, error) {
	return s.ledgers, s.err
}

func (s *stubExplorerReader) LedgerBySeq(_ context.Context, seq uint32) (clickhouse.LedgerHeader, bool, error) {
	if s.err != nil {
		return clickhouse.LedgerHeader{}, false, s.err
	}
	for _, l := range s.ledgers {
		if l.Seq == seq {
			return l, true, nil
		}
	}
	return clickhouse.LedgerHeader{}, false, nil
}

func (s *stubExplorerReader) LedgerTransactions(_ context.Context, _ uint32, _ int) ([]clickhouse.TxSummary, error) {
	return s.txs, s.err
}

func explorerTestServer(t *testing.T, r v1.ExplorerReader) string {
	t.Helper()
	srv := v1.New(v1.Options{Explorer: r})
	return httpTestServer(t, srv).URL
}

func TestExplorer_LedgersList(t *testing.T) {
	now := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	reader := &stubExplorerReader{ledgers: []clickhouse.LedgerHeader{
		{Seq: 100, CloseTime: now, LedgerHash: "ab", PrevHash: "cd", ProtocolVersion: 22, TxCount: 3, OpCount: 5, TotalCoins: 5000000000000000000, FeePool: 12345, BaseFee: 100, BaseReserve: 5000000},
		{Seq: 99, CloseTime: now, LedgerHash: "ef", PrevHash: "gh", TotalCoins: 1, FeePool: 0},
	}}
	base := explorerTestServer(t, reader)

	resp := mustGet(t, base+"/v1/ledgers?limit=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data v1.LedgersListView `json:"data"`
	}
	mustDecode(t, resp, &body)
	if len(body.Data.Ledgers) != 2 {
		t.Fatalf("want 2 ledgers, got %d", len(body.Data.Ledgers))
	}
	// total_coins must be a STRING (ADR-0003 — exceeds 2^53).
	if body.Data.Ledgers[0].TotalCoins != "5000000000000000000" {
		t.Errorf("total_coins = %q, want exact string", body.Data.Ledgers[0].TotalCoins)
	}
	// next_before = last (oldest) ledger's seq for keyset paging.
	if body.Data.NextBefore != 99 {
		t.Errorf("next_before = %d, want 99", body.Data.NextBefore)
	}
}

func TestExplorer_LedgerDetail_FoundAndNotFound(t *testing.T) {
	reader := &stubExplorerReader{ledgers: []clickhouse.LedgerHeader{{Seq: 42, LedgerHash: "deadbeef"}}}
	base := explorerTestServer(t, reader)

	resp := mustGet(t, base+"/v1/ledgers/42")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("found: status = %d", resp.StatusCode)
	}
	var body struct {
		Data v1.LedgerView `json:"data"`
	}
	mustDecode(t, resp, &body)
	if body.Data.Sequence != 42 || body.Data.Hash != "deadbeef" {
		t.Errorf("ledger view = %+v", body.Data)
	}

	resp = mustGet(t, base+"/v1/ledgers/999")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing ledger: status = %d, want 404", resp.StatusCode)
	}
}

func TestExplorer_LedgerDetail_InvalidSeq(t *testing.T) {
	base := explorerTestServer(t, &stubExplorerReader{})
	resp := mustGet(t, base+"/v1/ledgers/notanumber")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestExplorer_LedgerTransactions(t *testing.T) {
	reader := &stubExplorerReader{txs: []clickhouse.TxSummary{
		{Seq: 42, TxHash: "tx1", TxIndex: 0, SourceAccount: "GABC", FeeCharged: 100, OperationCount: 2, Successful: true, MemoType: "text", Memo: "hi"},
	}}
	base := explorerTestServer(t, reader)
	resp := mustGet(t, base+"/v1/ledgers/42/transactions")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data v1.LedgerTransactionsView `json:"data"`
	}
	mustDecode(t, resp, &body)
	if len(body.Data.Transactions) != 1 || body.Data.Transactions[0].Hash != "tx1" {
		t.Errorf("txs = %+v", body.Data.Transactions)
	}
	if !body.Data.Transactions[0].Successful || body.Data.Transactions[0].Memo != "hi" {
		t.Errorf("tx fields = %+v", body.Data.Transactions[0])
	}
}

func TestExplorer_Unavailable503(t *testing.T) {
	base := explorerTestServer(t, nil)
	for _, path := range []string{"/v1/ledgers", "/v1/ledgers/1", "/v1/ledgers/1/transactions"} {
		resp := mustGet(t, base+path)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", path, resp.StatusCode)
		}
	}
}
