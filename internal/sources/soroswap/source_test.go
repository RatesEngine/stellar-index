package soroswap

import (
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		topic0 string
		want   string
	}{
		{TopicSymbolSwap, EventSwap},
		{TopicSymbolSync, EventSync},
		{TopicSymbolDeposit, EventDeposit},
		{TopicSymbolWithdraw, EventWithdraw},
		{TopicSymbolNewPair, EventNewPair},
		{"AAAAANotAThing", ""},
	}
	for _, tc := range cases {
		e := &stellarrpc.Event{Topic: []string{tc.topic0}}
		if got := classify(e); got != tc.want {
			t.Errorf("classify(%q) = %q, want %q", tc.topic0, got, tc.want)
		}
	}

	// Empty topic list.
	if got := classify(&stellarrpc.Event{}); got != "" {
		t.Errorf("empty topic: got %q", got)
	}
}

func TestBufferAbsorbsAndPairs(t *testing.T) {
	buf := newBuffer()
	pair := "CABC...XYZ"
	base := event(EventSwap, pair, 100, "tx1", 0, TopicSymbolSwap)
	companion := event(EventSync, pair, 100, "tx1", 0, TopicSymbolSync)

	// Swap first → nothing complete.
	if got, _ := buf.absorb(base, EventSwap, mustClosed(t, base)); len(got) != 0 {
		t.Fatalf("absorb(swap): expected 0 completes, got %d", len(got))
	}

	// Sync completes the pair.
	completed, _ := buf.absorb(companion, EventSync, mustClosed(t, companion))
	if len(completed) != 1 {
		t.Fatalf("absorb(sync): expected 1 complete, got %d", len(completed))
	}
	if !completed[0].Complete() {
		t.Fatal("returned pair reports itself incomplete")
	}
	if len(buf.m) != 0 {
		t.Fatalf("buffer should be empty after pairing, has %d", len(buf.m))
	}
}

func TestBufferOrphanSyncStaysBuffered(t *testing.T) {
	buf := newBuffer()
	// A sync-only event (no preceding swap in the same group) is
	// held in the buffer as Sync-set-but-Swap-nil. It's NOT emitted
	// as a complete pair; orphans() surfaces it.
	e := event(EventSync, "CXYZ...", 42, "tx2", 0, TopicSymbolSync)
	completed, _ := buf.absorb(e, EventSync, mustClosed(t, e))
	if len(completed) != 0 {
		t.Fatalf("orphan sync should not complete; got %d", len(completed))
	}
	orphans := buf.orphans()
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0].Complete() {
		t.Fatal("orphan should not report Complete")
	}
}

func TestBufferSeparatesByGroupKey(t *testing.T) {
	// Two distinct (ledger, tx_hash, op_index) triples should not
	// collide. This guards against the naive "key on contract_id
	// only" bug.
	buf := newBuffer()
	swapA := event(EventSwap, "CAAA", 1, "txA", 0, TopicSymbolSwap)
	swapB := event(EventSwap, "CBBB", 1, "txB", 0, TopicSymbolSwap)
	syncA := event(EventSync, "CAAA", 1, "txA", 0, TopicSymbolSync)

	buf.absorb(swapA, EventSwap, mustClosed(t, swapA))
	buf.absorb(swapB, EventSwap, mustClosed(t, swapB))
	completed, _ := buf.absorb(syncA, EventSync, mustClosed(t, syncA))
	if len(completed) != 1 {
		t.Fatalf("expected only A to complete, got %d", len(completed))
	}
	if completed[0].TxHash != "txA" {
		t.Fatalf("wrong pair completed: %s", completed[0].TxHash)
	}
	// B still buffered.
	if len(buf.m) != 1 {
		t.Fatalf("B should still be buffered, buffer len %d", len(buf.m))
	}
}

func TestDecodeSwap_withFakeDecoder(t *testing.T) {
	// Install a fake decoder that yields a deterministic trade for
	// the swap's Value blob. This exercises the direction-
	// determination branch without the real XDR codec.
	prev1, prev2 := decodeSwapAmounts, decodeSwapOutAmounts
	defer func() { decodeSwapAmounts, decodeSwapOutAmounts = prev1, prev2 }()

	decodeSwapAmounts = func(_ string) (canonical.Amount, canonical.Amount, error) {
		return canonical.NewAmount(big.NewInt(100)), canonical.NewAmount(big.NewInt(0)), nil
	}
	decodeSwapOutAmounts = func(_ string) (canonical.Amount, canonical.Amount, error) {
		return canonical.NewAmount(big.NewInt(0)), canonical.NewAmount(big.NewInt(42)), nil
	}

	xlm := canonical.NativeAsset()
	usdc, err := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatal(err)
	}

	r := RawPair{
		Ledger: 100, TxHash: "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe", OpIndex: 0,
		ClosedAt: time.Now().UTC().Truncate(time.Second),
		Swap:     &stellarrpc.Event{Value: "stub"},
		Sync:     &stellarrpc.Event{Value: "stub"},
	}

	trade, err := decodeSwap(r, xlm, usdc)
	if err != nil {
		t.Fatalf("decodeSwap: %v", err)
	}
	if trade.Source != SourceName {
		t.Errorf("source = %q", trade.Source)
	}
	// amount0_in=100 > 0 and amount1_out=42 > 0 → base=token0 (xlm), quote=token1 (usdc).
	if !trade.Pair.Base.Equal(xlm) || !trade.Pair.Quote.Equal(usdc) {
		t.Errorf("pair direction wrong: %+v", trade.Pair)
	}
	if trade.BaseAmount.Cmp(canonical.NewAmount(big.NewInt(100))) != 0 {
		t.Errorf("base amount = %s", trade.BaseAmount)
	}
	if trade.QuoteAmount.Cmp(canonical.NewAmount(big.NewInt(42))) != 0 {
		t.Errorf("quote amount = %s", trade.QuoteAmount)
	}
}

func TestDecodeSwap_incompleteErrors(t *testing.T) {
	_, err := decodeSwap(RawPair{Swap: &stellarrpc.Event{}, Sync: nil}, canonical.NativeAsset(), canonical.NativeAsset())
	if err == nil {
		t.Fatal("expected error for incomplete pair")
	}
}

func TestSource_SeedPairIsConcurrentSafe(t *testing.T) {
	// Race-flag test: many concurrent SeedPair writers + lookupPair
	// readers must not trip -race. Catches regressions if someone
	// later inlines pair-cache writes without taking the lock.
	s := New(nil)
	xlm := canonical.NativeAsset()
	usdc, err := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	done := make(chan struct{}, goroutines*2)
	for i := 0; i < goroutines; i++ {
		pair := "CABC" + string(rune('A'+i)) // distinct-enough
		go func() {
			s.SeedPair(pair, xlm, usdc)
			done <- struct{}{}
		}()
		go func() {
			// We don't care if the pair is there yet; we just care
			// about the lock not racing.
			_, _ = s.lookupPair(pair)
			done <- struct{}{}
		}()
	}
	for i := 0; i < goroutines*2; i++ {
		<-done
	}

	// After all writers land, every pair must be readable.
	for i := 0; i < goroutines; i++ {
		pair := "CABC" + string(rune('A'+i))
		tokens, ok := s.lookupPair(pair)
		if !ok {
			t.Errorf("pair %q not found post-seed", pair)
		}
		if !tokens.Token0.Equal(xlm) || !tokens.Token1.Equal(usdc) {
			t.Errorf("pair %q tokens mismatched: %+v", pair, tokens)
		}
	}
}

func TestSource_NameAndNewBasics(t *testing.T) {
	s := New(nil, WithPollInterval(500*time.Millisecond))
	if s.Name() != SourceName {
		t.Errorf("Name = %q, want %q", s.Name(), SourceName)
	}
	if s.pollInterval != 500*time.Millisecond {
		t.Errorf("pollInterval = %v", s.pollInterval)
	}
	// Health starts zero.
	if h := s.Health(); h.Connected || !h.LastEvent.IsZero() || h.LagLedgers != 0 {
		t.Errorf("initial health: %+v", h)
	}
}

func TestBufferEvictsStaleOrphans(t *testing.T) {
	// StreamLive must not leak memory when a swap never gets its
	// sync. After maxAge, the stale entry is returned as `evicted`
	// and dropped from the map.
	buf := newBuffer()
	buf.maxAge = 100 * time.Millisecond

	// Inject an old orphan swap — ClosedAt = 1s ago (past cutoff).
	oldTS := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	orphan := &stellarrpc.Event{
		Type: "contract", Ledger: 1, LedgerClosedAt: oldTS,
		ContractID: "CORPHAN", TxHash: "txOrphan", OperationIndex: 0,
		Topic: []string{TopicSymbolSwap}, Value: "stub",
	}
	completed, evicted := buf.absorb(orphan, EventSwap, mustClosed(t, orphan))
	if len(completed) != 0 {
		t.Errorf("absorbed orphan should not complete; got %d", len(completed))
	}
	// On this first call there's nothing to evict (the orphan was
	// JUST added). Size should be 1.
	if len(evicted) != 0 {
		t.Errorf("first-insert should evict nothing; got %d", len(evicted))
	}
	if buf.size() != 1 {
		t.Fatalf("buffer size = %d, want 1", buf.size())
	}

	// Second call with a fresh event. sweepStale runs BEFORE the
	// new event is added, so the old orphan is evicted.
	fresh := &stellarrpc.Event{
		Type: "contract", Ledger: 2,
		LedgerClosedAt: time.Now().UTC().Format(time.RFC3339),
		ContractID:     "CFRESH", TxHash: "txFresh", OperationIndex: 0,
		Topic: []string{TopicSymbolSwap}, Value: "stub",
	}
	_, evicted = buf.absorb(fresh, EventSwap, mustClosed(t, fresh))
	if len(evicted) != 1 {
		t.Fatalf("expected 1 eviction, got %d", len(evicted))
	}
	if evicted[0].TxHash != "txOrphan" {
		t.Errorf("wrong orphan evicted: %q", evicted[0].TxHash)
	}
	// The fresh entry stays; the orphan is gone.
	if buf.size() != 1 {
		t.Errorf("buffer size after eviction = %d, want 1 (fresh only)", buf.size())
	}
}

func TestBufferBackfillOldEventsPair(t *testing.T) {
	// Regression: when the orchestrator's backfill path replays a
	// 6-hour-old ledger range, swap + sync events share an ancient
	// ClosedAt. Previously `sweepStale` compared against wall-clock
	// — which meant the swap got evicted as an "orphan" the moment
	// the sync arrived, because now-5min > 6h-ago. Now sweepStale
	// uses the incoming event's ClosedAt as the reference, so
	// same-batch correlated pairs complete regardless of age.
	buf := newBuffer()

	oldTS := time.Now().UTC().Add(-6 * time.Hour).Format(time.RFC3339)
	swap := &stellarrpc.Event{
		Type: "contract", Ledger: 1, LedgerClosedAt: oldTS,
		ContractID: "COLD", TxHash: "txOld", OperationIndex: 0,
		Topic: []string{TopicSymbolSwap}, Value: "stub",
	}
	sync := &stellarrpc.Event{
		Type: "contract", Ledger: 1, LedgerClosedAt: oldTS,
		ContractID: "COLD", TxHash: "txOld", OperationIndex: 0,
		Topic: []string{TopicSymbolSync}, Value: "stub",
	}
	if got, evicted := buf.absorb(swap, EventSwap, mustClosed(t, swap)); len(got) != 0 || len(evicted) != 0 {
		t.Fatalf("swap: got completed=%d evicted=%d; want both 0", len(got), len(evicted))
	}
	got, evicted := buf.absorb(sync, EventSync, mustClosed(t, sync))
	if len(evicted) != 0 {
		t.Errorf("sync: evicted %d entries during correlation; want 0", len(evicted))
	}
	if len(got) != 1 {
		t.Fatalf("sync: completed=%d; want 1 (backfill pair must complete)", len(got))
	}
}

func TestBufferNoEvictionWithFreshEntries(t *testing.T) {
	// When every entry is fresh, sweepStale should evict nothing —
	// regardless of how many times absorb runs.
	buf := newBuffer()
	buf.maxAge = time.Hour

	for i := 0; i < 10; i++ {
		e := &stellarrpc.Event{
			Type: "contract", Ledger: uint32(i + 1),
			LedgerClosedAt: time.Now().UTC().Format(time.RFC3339),
			ContractID:     "CFRESH", TxHash: "tx" + string(rune('0'+i)), OperationIndex: 0,
			Topic: []string{TopicSymbolSwap}, Value: "stub",
		}
		_, evicted := buf.absorb(e, EventSwap, mustClosed(t, e))
		if len(evicted) > 0 {
			t.Fatalf("step %d: evicted %d fresh entries", i, len(evicted))
		}
	}
	if buf.size() != 10 {
		t.Errorf("buffer size = %d, want 10 (all fresh)", buf.size())
	}
}

// helpers

func event(kind string, contract string, ledger uint32, tx string, op int, topic0 string) *stellarrpc.Event {
	return &stellarrpc.Event{
		Type:           "contract",
		Ledger:         ledger,
		LedgerClosedAt: time.Now().UTC().Format(time.RFC3339),
		ContractID:     contract,
		TxHash:         tx,
		OperationIndex: op,
		Topic:          []string{topic0},
		Value:          "stub",
	}
}

// mustClosed is the test-side equivalent of the processPage loop's
// `e.EventClosedAt() + bail on error`. Every test event is built by
// `event()` with a well-formed LedgerClosedAt, so parse errors here
// are a test-fixture bug, not a real condition.
func mustClosed(t *testing.T, e *stellarrpc.Event) time.Time {
	t.Helper()
	ts, err := e.EventClosedAt()
	if err != nil {
		t.Fatalf("fixture has unparseable LedgerClosedAt %q: %v", e.LedgerClosedAt, err)
	}
	return ts
}
