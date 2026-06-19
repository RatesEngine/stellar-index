package v1_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	v1 "github.com/StellarIndex/stellar-index/internal/api/v1"
	"github.com/StellarIndex/stellar-index/internal/storage/clickhouse"
	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
)

// stubProtocolActivityReader is a fake lake-analytics reader for the
// per-protocol page enrichment test.
type stubProtocolActivityReader struct {
	tip       uint32
	breakdown []clickhouse.ProtocolEventTypeCount
	daily     []clickhouse.ProtocolDailyPoint
	contracts []clickhouse.ProtocolContractActivity
}

func (s *stubProtocolActivityReader) LakeTipLedger(context.Context) (uint32, error) {
	return s.tip, nil
}

func (s *stubProtocolActivityReader) ProtocolEventBreakdown(context.Context, []string, uint32) ([]clickhouse.ProtocolEventTypeCount, error) {
	return s.breakdown, nil
}

func (s *stubProtocolActivityReader) ProtocolDailyActivity(context.Context, []string, uint32) ([]clickhouse.ProtocolDailyPoint, error) {
	return s.daily, nil
}

func (s *stubProtocolActivityReader) ProtocolContractActivity(context.Context, []string, uint32) ([]clickhouse.ProtocolContractActivity, error) {
	return s.contracts, nil
}

type stubProtocolContractsReader struct {
	bySource      map[string][]timescale.ProtocolContract
	projBySource  map[string][]string
	contractIndex map[string]string
	err           error
}

func (s *stubProtocolContractsReader) ListProtocolContracts(_ context.Context, source string) ([]timescale.ProtocolContract, error) {
	return s.bySource[source], s.err
}

func (s *stubProtocolContractsReader) ListSourceContractsFromProjection(_ context.Context, source string) ([]string, error) {
	return s.projBySource[source], nil
}

func (s *stubProtocolContractsReader) ProtocolContractIndex(_ context.Context) (map[string]string, error) {
	return s.contractIndex, s.err
}

type stubProtocolStatsReader struct {
	counts map[string]int64
	err    error
}

func (s *stubProtocolStatsReader) CountRecentEventsBySource(context.Context) (map[string]int64, error) {
	return s.counts, s.err
}

type stubSoroswapPairsReader struct {
	pairs []timescale.SoroswapPair
	err   error
}

func (s *stubSoroswapPairsReader) LoadSoroswapPairRegistry(context.Context) ([]timescale.SoroswapPair, error) {
	return s.pairs, s.err
}

func protocolsTestServer(t *testing.T) *testServer {
	t.Helper()
	srv := v1.New(v1.Options{
		ProtocolContracts: &stubProtocolContractsReader{bySource: map[string][]timescale.ProtocolContract{
			"blend": {
				{Source: "blend", ContractID: "CPOOL1", FactoryID: "CFACTORY1", FirstLedger: 51_500_000},
				{Source: "blend", ContractID: "CPOOL2", FactoryID: "CFACTORY2", FirstLedger: 60_000_123},
			},
		}},
		ProtocolStats: &stubProtocolStatsReader{counts: map[string]int64{
			"blend":    1234,
			"soroswap": 567,
			"sdex":     89_000,
			"binance":  999_999, // off-protocol key — must be ignored
		}},
		SoroswapPairs: &stubSoroswapPairsReader{pairs: []timescale.SoroswapPair{
			{PairStrkey: "CPAIR1", Token0Strkey: "CTOK0", Token1Strkey: "CTOK1"},
			{PairStrkey: "CPAIR2", Token0Strkey: "CTOK2", Token1Strkey: "CTOK3"},
			{PairStrkey: "CPAIR3", Token0Strkey: "CTOK4", Token1Strkey: "CTOK5"},
		}},
		CompletenessReader: &stubCompletenessReader{snaps: []timescale.CompletenessSnapshot{
			{Source: "blend", Complete: true, Watermark: 62_999_000},
			{Source: "soroswap", Complete: false, Watermark: 60_000_000},
		}},
	})
	return httpTestServer(t, srv)
}

func protocolRow(t *testing.T, rows []v1.ProtocolView, name string) v1.ProtocolView {
	t.Helper()
	for _, row := range rows {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("protocol %q not in directory", name)
	return v1.ProtocolView{}
}

// Directory happy path: every registry entry serves, with contract
// counts, 24h events and verdict summaries joined per source.
func TestHandleProtocolsList_Happy(t *testing.T) {
	ts := protocolsTestServer(t)

	resp := mustGet(t, ts.URL+"/v1/protocols")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=60" {
		t.Errorf("Cache-Control = %q, want public, max-age=60", cc)
	}

	var env struct {
		Data v1.ProtocolsView `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	d := env.Data
	if d.TotalProtocols != 15 || len(d.Protocols) != 15 {
		t.Fatalf("total = %d (len %d), want 15", d.TotalProtocols, len(d.Protocols))
	}

	blend := protocolRow(t, d.Protocols, "blend")
	if blend.Category != "lending" || blend.GenesisLedger != 51_499_546 {
		t.Errorf("blend identity wrong: %+v", blend)
	}
	if len(blend.Factories) == 0 {
		t.Errorf("blend.Factories empty, want the pool-factory set")
	}
	if blend.ContractCount != 2 || blend.Events24h != 1234 {
		t.Errorf("blend joins wrong: count=%d events=%d", blend.ContractCount, blend.Events24h)
	}
	if blend.Completeness == nil || !blend.Completeness.Complete || blend.Completeness.WatermarkLedger != 62_999_000 {
		t.Errorf("blend completeness summary wrong: %+v", blend.Completeness)
	}

	// soroswap's contract count comes from soroswap_pairs, not
	// protocol_contracts, and its failing verdict surfaces as-is.
	soroswap := protocolRow(t, d.Protocols, "soroswap")
	if soroswap.ContractCount != 3 || soroswap.Events24h != 567 {
		t.Errorf("soroswap joins wrong: count=%d events=%d", soroswap.ContractCount, soroswap.Events24h)
	}
	if soroswap.Completeness == nil || soroswap.Completeness.Complete {
		t.Errorf("soroswap completeness summary wrong: %+v", soroswap.Completeness)
	}

	// A source with no snapshot, no registry, no events: zeros, no
	// completeness block, factories present-but-empty (never null).
	band := protocolRow(t, d.Protocols, "band")
	if band.ContractCount != 0 || band.Events24h != 0 || band.Completeness != nil {
		t.Errorf("band should be all-zero/absent: %+v", band)
	}
	if band.Factories == nil {
		t.Errorf("band.Factories = nil, want []")
	}
}

// Detail for a protocol_contracts-gated source: instances carry
// factory + first_ledger; event kinds + verification page surface.
func TestHandleProtocolDetail_Blend(t *testing.T) {
	ts := protocolsTestServer(t)

	resp := mustGet(t, ts.URL+"/v1/protocols/blend")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.ProtocolDetailView `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	d := env.Data
	if d.Name != "blend" || d.ContractCount != 2 || len(d.Contracts) != 2 {
		t.Fatalf("blend detail identity/contracts wrong: %+v", d)
	}
	c0 := d.Contracts[0]
	if c0.ContractID != "CPOOL1" || c0.FactoryID != "CFACTORY1" || c0.FirstLedger != 51_500_000 {
		t.Errorf("blend contract row wrong: %+v", c0)
	}
	if c0.Token0 != "" || c0.Token1 != "" {
		t.Errorf("blend contract row should have no token fields: %+v", c0)
	}
	if len(d.EventKinds) != 6 {
		t.Errorf("blend event_kinds = %v, want 6 kinds", d.EventKinds)
	}
	if d.VerificationPage != "docs/protocols/blend.md" {
		t.Errorf("verification_page = %q", d.VerificationPage)
	}
}

// Detail for soroswap: contracts are the pair registry in the unified
// shape (pair strkey + token identities, no factory column).
func TestHandleProtocolDetail_Soroswap(t *testing.T) {
	ts := protocolsTestServer(t)

	resp := mustGet(t, ts.URL+"/v1/protocols/soroswap")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.ProtocolDetailView `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	d := env.Data
	if len(d.Contracts) != 3 || d.ContractCount != 3 {
		t.Fatalf("soroswap contracts = %d/%d, want 3/3", len(d.Contracts), d.ContractCount)
	}
	c0 := d.Contracts[0]
	if c0.ContractID != "CPAIR1" || c0.Token0 != "CTOK0" || c0.Token1 != "CTOK1" {
		t.Errorf("soroswap pair row wrong: %+v", c0)
	}
	if c0.FactoryID != "" || c0.FirstLedger != 0 {
		t.Errorf("soroswap pair row should have no factory/first_ledger: %+v", c0)
	}
	if d.VerificationPage != "docs/protocols/soroswap.md" {
		t.Errorf("verification_page = %q", d.VerificationPage)
	}
}

// Unknown protocol name → 404 problem.
func TestHandleProtocolDetail_Unknown404(t *testing.T) {
	ts := protocolsTestServer(t)
	resp := mustGet(t, ts.URL+"/v1/protocols/dogeswap")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// Nil readers: the static registry still serves the full directory
// (and detail) with zero counts, empty contract lists and no
// completeness blocks — never a 5xx.
func TestHandleProtocols_NilReaderDegradation(t *testing.T) {
	srv := v1.New(v1.Options{})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/protocols")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("directory status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data v1.ProtocolsView `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.TotalProtocols != 15 {
		t.Fatalf("total = %d, want 15", env.Data.TotalProtocols)
	}
	for _, p := range env.Data.Protocols {
		if p.ContractCount != 0 || p.Events24h != 0 || p.Completeness != nil {
			t.Errorf("%s should degrade to zeros: %+v", p.Name, p)
		}
	}

	resp = mustGet(t, ts.URL+"/v1/protocols/soroswap")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", resp.StatusCode)
	}
	var denv struct {
		Data v1.ProtocolDetailView `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&denv); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if denv.Data.Contracts == nil || len(denv.Data.Contracts) != 0 {
		t.Errorf("nil-reader detail contracts = %v, want []", denv.Data.Contracts)
	}
	if len(denv.Data.EventKinds) == 0 {
		t.Errorf("event_kinds should still serve from the static registry")
	}
}

// TestHandleProtocolDetail_LakeAnalytics verifies the per-protocol page
// enrichment: event-type breakdown, daily activity series, and per-contract
// event counts merged onto the roster (audit follow-up: Dune-surpassing
// protocol pages).
func TestHandleProtocolDetail_LakeAnalytics(t *testing.T) {
	srv := v1.New(v1.Options{
		ProtocolContracts: &stubProtocolContractsReader{bySource: map[string][]timescale.ProtocolContract{
			"blend": {
				{Source: "blend", ContractID: "CPOOL1", FactoryID: "CFAC", FirstLedger: 51_500_000},
				{Source: "blend", ContractID: "CPOOL2", FactoryID: "CFAC", FirstLedger: 60_000_123},
			},
		}},
		ProtocolActivity: &stubProtocolActivityReader{
			tip: 63_000_000,
			// Typed breakdown (topic_0_sym != '') sums to 700; the daily
			// series counts ALL contract events (1000). The 300-event gap
			// is non-Symbol-topic'd events the breakdown should surface as
			// a reconciling "untyped" bucket.
			breakdown: []clickhouse.ProtocolEventTypeCount{
				{EventType: "supply", Count: 600},
				{EventType: "borrow", Count: 100},
			},
			daily: []clickhouse.ProtocolDailyPoint{
				{Date: "2026-06-13", Events: 400},
				{Date: "2026-06-14", Events: 600},
			},
			contracts: []clickhouse.ProtocolContractActivity{
				{ContractID: "CPOOL1", Events: 700, LastSeen: time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)},
				{ContractID: "CPOOL2", Events: 300, LastSeen: time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)},
			},
		},
	})
	ts := httpTestServer(t, srv)

	resp := mustGet(t, ts.URL+"/v1/protocols/blend")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data v1.ProtocolDetailView `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	d := body.Data

	// Typed entries first (desc by count), then the reconciling untyped
	// bucket = EventsTotal(1000) − typedSum(700) = 300.
	if len(d.EventBreakdown) != 3 || d.EventBreakdown[0].EventType != "supply" || d.EventBreakdown[0].Count != 600 {
		t.Errorf("event_breakdown = %+v", d.EventBreakdown)
	}
	if last := d.EventBreakdown[len(d.EventBreakdown)-1]; last.EventType != "untyped" || last.Count != 300 {
		t.Errorf("untyped bucket = %+v, want {untyped 300}", last)
	}
	// EventsTotal is the unfiltered series sum (not the typed-breakdown sum).
	if d.EventsTotal != 1000 {
		t.Errorf("events_total = %d, want 1000", d.EventsTotal)
	}
	// sum(EventBreakdown) must reconcile to EventsTotal.
	var bdSum int64
	for _, b := range d.EventBreakdown {
		bdSum += b.Count
	}
	if bdSum != d.EventsTotal {
		t.Errorf("breakdown sum %d != events_total %d", bdSum, d.EventsTotal)
	}
	if len(d.ActivitySeries) != 2 || d.ActivitySeries[1].Events != 600 {
		t.Errorf("activity_series = %+v", d.ActivitySeries)
	}
	if d.ActivityWindowDays != 90 {
		t.Errorf("activity_window_days = %d, want 90", d.ActivityWindowDays)
	}
	// Per-contract activity merged onto the roster.
	var p1 v1.ProtocolContractView
	for _, c := range d.Contracts {
		if c.ContractID == "CPOOL1" {
			p1 = c
		}
	}
	if p1.Events != 700 {
		t.Errorf("CPOOL1 events = %d, want 700", p1.Events)
	}
	if p1.Kind != "instance" {
		t.Errorf("CPOOL1 kind = %q, want instance", p1.Kind)
	}
	if p1.LastSeen == "" {
		t.Errorf("CPOOL1 last_seen should be set")
	}
}

// TestHandleProtocolDetail_ProjectionFallback verifies that when the
// protocol_contracts registry is empty for a source, the roster falls back to
// the projected-table contracts — so defindex/phoenix/comet/cctp/rozo aren't
// starved of their contract set just because the factory-enumeration registry
// isn't seeded.
func TestHandleProtocolDetail_ProjectionFallback(t *testing.T) {
	srv := v1.New(v1.Options{
		ProtocolContracts: &stubProtocolContractsReader{
			bySource:     map[string][]timescale.ProtocolContract{}, // registry empty for defindex
			projBySource: map[string][]string{"defindex": {"CVAULT1", "CVAULT2", "CVAULT3"}},
		},
	})
	ts := httpTestServer(t, srv)
	resp := mustGet(t, ts.URL+"/v1/protocols/defindex")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body struct {
		Data v1.ProtocolDetailView `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.ContractCount != 3 || len(body.Data.Contracts) != 3 {
		t.Fatalf("defindex fallback roster = %d contracts, want 3", len(body.Data.Contracts))
	}
	if body.Data.Contracts[0].ContractID != "CVAULT1" {
		t.Errorf("contract[0] = %q, want CVAULT1", body.Data.Contracts[0].ContractID)
	}
}
