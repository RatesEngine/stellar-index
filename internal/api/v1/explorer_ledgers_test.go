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
	ledgers        []clickhouse.LedgerHeader
	txs            []clickhouse.TxSummary
	ops            []clickhouse.OpRow
	opTypeStats    []clickhouse.OpTypeCount
	throughput     []clickhouse.ThroughputBucket
	opResults      map[uint32]int32
	events         []clickhouse.EventSummary
	contractEvents []clickhouse.ContractActivityRow
	wasm           clickhouse.ContractWasmInfo
	wasmErr        error
	directory      []clickhouse.ContractDirectoryRow
	interactions   []clickhouse.ContractEdgeRow
	codeHistory    []clickhouse.ContractCodeVersion
	accountState   clickhouse.AccountState
	holders        []clickhouse.AssetHolder
	holderCount    int64
	wealth         []clickhouse.AccountWealth
	err            error
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

func (s *stubExplorerReader) OperationsByLedger(_ context.Context, _ uint32, _ int) ([]clickhouse.OpRow, error) {
	return s.ops, s.err
}

func (s *stubExplorerReader) RecentOperations(_ context.Context, _ int, _ clickhouse.ExplorerCursor) ([]clickhouse.OpRow, error) {
	return s.ops, s.err
}

func (s *stubExplorerReader) OperationTypeStats(_ context.Context, _ uint32) ([]clickhouse.OpTypeCount, error) {
	return s.opTypeStats, s.err
}

func (s *stubExplorerReader) NetworkThroughput(_ context.Context, _ int) ([]clickhouse.ThroughputBucket, error) {
	return s.throughput, s.err
}

func (s *stubExplorerReader) TransactionByHash(_ context.Context, hash string) (clickhouse.TxSummary, bool, error) {
	if s.err != nil {
		return clickhouse.TxSummary{}, false, s.err
	}
	for _, t := range s.txs {
		if t.TxHash == hash {
			return t, true, nil
		}
	}
	return clickhouse.TxSummary{}, false, nil
}

func (s *stubExplorerReader) OperationsByTx(_ context.Context, _ uint32, _ string) ([]clickhouse.OpRow, error) {
	return s.ops, s.err
}

func (s *stubExplorerReader) OperationResultsByTx(_ context.Context, _ uint32, _ string) (map[uint32]int32, error) {
	return s.opResults, s.err
}

func (s *stubExplorerReader) EventsByTx(_ context.Context, _ uint32, _ string) ([]clickhouse.EventSummary, error) {
	return s.events, s.err
}

func (s *stubExplorerReader) ContractEventsRecent(_ context.Context, _ string, _ int, _ clickhouse.ExplorerCursor) ([]clickhouse.ContractActivityRow, error) {
	return s.contractEvents, s.err
}

func (s *stubExplorerReader) ContractWasm(_ context.Context, _ string) (clickhouse.ContractWasmInfo, error) {
	return s.wasm, s.wasmErr
}

func (s *stubExplorerReader) RecentContracts(_ context.Context, _ int, _ uint32) ([]clickhouse.ContractDirectoryRow, error) {
	return s.directory, s.err
}

func (s *stubExplorerReader) ContractInteractions(_ context.Context, _ string, _ int, _ uint32) ([]clickhouse.ContractEdgeRow, error) {
	return s.interactions, s.err
}

func (s *stubExplorerReader) ContractCodeHistory(_ context.Context, _ string) ([]clickhouse.ContractCodeVersion, error) {
	return s.codeHistory, s.err
}

func (s *stubExplorerReader) AccountState(_ context.Context, _ string) (clickhouse.AccountState, error) {
	return s.accountState, s.err
}

func (s *stubExplorerReader) AssetHolders(_ context.Context, _ string, _ int) ([]clickhouse.AssetHolder, int64, error) {
	return s.holders, s.holderCount, s.err
}

func (s *stubExplorerReader) AccountsByWealth(_ context.Context, _ []string, _ []float64, _ int) ([]clickhouse.AccountWealth, error) {
	return s.wealth, s.err
}

func (s *stubExplorerReader) AccountTransactions(_ context.Context, _ string, _ int, _ clickhouse.ExplorerCursor) ([]clickhouse.TxSummary, error) {
	return s.txs, s.err
}

func (s *stubExplorerReader) AccountOperations(_ context.Context, _ string, _ int, _ clickhouse.ExplorerCursor) ([]clickhouse.OpRow, error) {
	return s.ops, s.err
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

const testTxHash = "88526317d98b1eb5a8040123456789abcdef0123456789abcdef0123456789ab"

func TestExplorer_TxDetail(t *testing.T) {
	reader := &stubExplorerReader{
		txs:       []clickhouse.TxSummary{{Seq: 42, TxHash: testTxHash, SourceAccount: "GABC", FeeCharged: 300, OperationCount: 1, Successful: true, MemoType: "MemoTypeMemoText", Memo: "hello"}},
		ops:       []clickhouse.OpRow{{Seq: 42, TxHash: testTxHash, OpIndex: 0, OpType: "OperationTypePayment", BodyXDR: "not-valid-xdr"}},
		opResults: map[uint32]int32{0: 0},
		events:    []clickhouse.EventSummary{{OpIndex: 0, EventIndex: 1, ContractID: "CABC", EventType: "contract", Topic0Sym: "transfer"}},
	}
	base := explorerTestServer(t, reader)
	resp := mustGet(t, base+"/v1/tx/"+testTxHash)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data v1.TxDetailView `json:"data"`
	}
	mustDecode(t, resp, &body)
	if body.Data.Hash != testTxHash {
		t.Errorf("hash = %q", body.Data.Hash)
	}
	if body.Data.MemoType != "text" { // normalized from MemoTypeMemoText
		t.Errorf("memo_type = %q, want text", body.Data.MemoType)
	}
	if len(body.Data.Operations) != 1 {
		t.Fatalf("ops = %d, want 1", len(body.Data.Operations))
	}
	// op had invalid XDR -> raw_xdr fallback, result_code populated from map.
	if body.Data.Operations[0].ResultCode == nil || *body.Data.Operations[0].ResultCode != 0 {
		t.Errorf("result_code = %v, want 0", body.Data.Operations[0].ResultCode)
	}
	if len(body.Data.Events) != 1 || body.Data.Events[0].Topic0 != "transfer" {
		t.Errorf("events = %+v", body.Data.Events)
	}
}

func TestExplorer_TxDetail_InvalidHashAndNotFound(t *testing.T) {
	base := explorerTestServer(t, &stubExplorerReader{})
	if resp := mustGet(t, base+"/v1/tx/xyz"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("short hash: status = %d, want 400", resp.StatusCode)
	}
	if resp := mustGet(t, base+"/v1/tx/"+testTxHash); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown tx: status = %d, want 404", resp.StatusCode)
	}
}

func TestExplorer_ContractDetail(t *testing.T) {
	const cid = "CAM7DY53G63XA4AJRS24Z6VFYAFSSF76C3RZ45BE5YU3FQS5255OOABP"
	reader := &stubExplorerReader{contractEvents: []clickhouse.ContractActivityRow{
		{Seq: 63000000, TxHash: "abc", OpIndex: 0, EventIndex: 1, EventType: "contract", Topic0Sym: "transfer"},
		{Seq: 62999000, TxHash: "def", OpIndex: 0, EventIndex: 0, EventType: "contract", Topic0Sym: "mint"},
	}}
	base := explorerTestServer(t, reader)
	// limit=2 makes this a FULL page (n==limit) so a next_cursor is emitted —
	// the composite (ledger, op_index, event_index) of the last (oldest) row.
	resp := mustGet(t, base+"/v1/contracts/"+cid+"?limit=2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data v1.ContractDetailView `json:"data"`
	}
	mustDecode(t, resp, &body)
	if body.Data.ContractID != cid || len(body.Data.Events) != 2 {
		t.Fatalf("detail = %+v", body.Data)
	}
	if body.Data.Events[0].Topic0 != "transfer" || body.Data.NextCursor != "62999000.0.0" {
		t.Errorf("events/cursor = %+v next=%q", body.Data.Events, body.Data.NextCursor)
	}
}

func TestExplorer_ContractDetail_InvalidID(t *testing.T) {
	base := explorerTestServer(t, &stubExplorerReader{})
	if resp := mustGet(t, base+"/v1/contracts/notacontract"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestExplorer_AccountActivity(t *testing.T) {
	const g = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	reader := &stubExplorerReader{
		txs: []clickhouse.TxSummary{{Seq: 100, TxHash: "h1", SourceAccount: g, Successful: true}},
		ops: []clickhouse.OpRow{{Seq: 100, TxHash: "h1", OpIndex: 0, OpType: "OperationTypePayment", BodyXDR: "x"}},
	}
	base := explorerTestServer(t, reader)

	resp := mustGet(t, base+"/v1/accounts/"+g+"/transactions")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("txs status = %d", resp.StatusCode)
	}
	var tb struct {
		Data v1.AccountTransactionsView `json:"data"`
	}
	mustDecode(t, resp, &tb)
	if tb.Data.Account != g || len(tb.Data.Transactions) != 1 || tb.Data.Scope != "all" {
		t.Errorf("account txs = %+v", tb.Data)
	}

	resp = mustGet(t, base+"/v1/accounts/"+g+"/operations")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ops status = %d", resp.StatusCode)
	}
	var ob struct {
		Data v1.AccountOperationsView `json:"data"`
	}
	mustDecode(t, resp, &ob)
	if len(ob.Data.Operations) != 1 {
		t.Errorf("account ops = %+v", ob.Data)
	}

	// invalid strkey -> 400
	if r := mustGet(t, base+"/v1/accounts/notanaccount/transactions"); r.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid account: status = %d, want 400", r.StatusCode)
	}
}

func TestExplorer_Unavailable503(t *testing.T) {
	base := explorerTestServer(t, nil)
	for _, path := range []string{"/v1/ledgers", "/v1/ledgers/1", "/v1/ledgers/1/transactions", "/v1/operations", "/v1/tx/" + testTxHash} {
		resp := mustGet(t, base+path)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", path, resp.StatusCode)
		}
	}
}
