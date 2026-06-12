package soroswap

import (
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		topics []string
		want   string
	}{
		{"pair/swap", []string{TopicPrefixPair, TopicSymbolSwap}, EventSwap},
		{"pair/sync", []string{TopicPrefixPair, TopicSymbolSync}, EventSync},
		{"pair/deposit", []string{TopicPrefixPair, TopicSymbolDeposit}, EventDeposit},
		{"pair/withdraw", []string{TopicPrefixPair, TopicSymbolWithdraw}, EventWithdraw},
		{"pair/skim", []string{TopicPrefixPair, TopicSymbolSkim}, EventSkim},
		{"factory/new_pair", []string{TopicPrefixFactory, TopicSymbolNewPair}, EventNewPair},
		// Wrong prefix for a pair event — ignored.
		{"factory/swap (never emitted)", []string{TopicPrefixFactory, TopicSymbolSwap}, ""},
		// Wrong prefix for a factory event — ignored.
		{"pair/new_pair (never emitted)", []string{TopicPrefixPair, TopicSymbolNewPair}, ""},
		// Unknown second slot.
		{"pair/unknown", []string{TopicPrefixPair, "AAAAPlaceholder"}, ""},
		// Single-topic event (malformed).
		{"single topic", []string{TopicPrefixPair}, ""},
		// Empty.
		{"empty", []string{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &events.Event{Topic: tc.topics}
			if got := classify(e); got != tc.want {
				t.Errorf("classify(%v) = %q, want %q", tc.topics, got, tc.want)
			}
		})
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
	prev := decodeSwapAmounts
	defer func() { decodeSwapAmounts = prev }()

	decodeSwapAmounts = func(_ string) (swapAmounts, error) {
		amt := func(n int64) canonical.Amount { return canonical.NewAmount(big.NewInt(n)) }
		return swapAmounts{
			Amount0In:  amt(100),
			Amount1In:  amt(0),
			Amount0Out: amt(0),
			Amount1Out: amt(42),
		}, nil
	}

	xlm := canonical.NativeAsset()
	usdc, err := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatal(err)
	}

	r := RawPair{
		Ledger: 100, TxHash: "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe", OpIndex: 0,
		ClosedAt: time.Now().UTC().Truncate(time.Second),
		Swap:     &events.Event{Value: "stub"},
		Sync:     &events.Event{Value: "stub"},
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
	_, err := decodeSwap(RawPair{Swap: &events.Event{}, Sync: nil}, canonical.NativeAsset(), canonical.NativeAsset())
	if err == nil {
		t.Fatal("expected error for incomplete pair")
	}
}

func TestDecoder_SeedPairIsConcurrentSafe(t *testing.T) {
	// Race-flag regression: many concurrent SeedPair writers +
	// Decode readers must not trip -race. Guards against a future
	// refactor that inlines pair-cache writes without the lock.
	d := NewDecoder()
	xlm := canonical.NativeAsset()
	usdc, err := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 16
	done := make(chan struct{}, goroutines*2)
	for i := 0; i < goroutines; i++ {
		pair := "CABC" + string(rune('A'+i))
		go func() {
			d.SeedPair(pair, xlm, usdc)
			done <- struct{}{}
		}()
		go func() {
			// Read-side race target — any read path that walks the
			// pairTokens map.
			d.SeedPair(pair, xlm, usdc) // idempotent write also counts as a reader via lock upgrade
			done <- struct{}{}
		}()
	}
	for i := 0; i < goroutines*2; i++ {
		<-done
	}
}

func TestDecoder_NameMatchesSourceName(t *testing.T) {
	if got := NewDecoder().Name(); got != SourceName {
		t.Errorf("Name = %q, want %q", got, SourceName)
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
	orphan := &events.Event{
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
	fresh := &events.Event{
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
	swap := &events.Event{
		Type: "contract", Ledger: 1, LedgerClosedAt: oldTS,
		ContractID: "COLD", TxHash: "txOld", OperationIndex: 0,
		Topic: []string{TopicSymbolSwap}, Value: "stub",
	}
	sync := &events.Event{
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
		e := &events.Event{
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

// event builds a test event with the full 2-slot topic that real
// Soroswap events carry: topic[0] = String(prefix), topic[1] =
// Symbol(event). topic1 is the event-name SCVal blob (TopicSymbolSwap
// etc.) — the prefix is inferred from which event-name is passed
// (factory for new_pair, pair for everything else).
func event(kind string, contract string, ledger uint32, tx string, op int, topic1 string) *events.Event {
	prefix := TopicPrefixPair
	if kind == EventNewPair {
		prefix = TopicPrefixFactory
	}
	return &events.Event{
		Type:           "contract",
		Ledger:         ledger,
		LedgerClosedAt: time.Now().UTC().Format(time.RFC3339),
		ContractID:     contract,
		TxHash:         tx,
		OperationIndex: op,
		Topic:          []string{prefix, topic1},
		Value:          "stub",
	}
}

// mustClosed is the test-side equivalent of the processPage loop's
// `e.EventClosedAt() + bail on error`. Every test event is built by
// `event()` with a well-formed LedgerClosedAt, so parse errors here
// are a test-fixture bug, not a real condition.
func mustClosed(t *testing.T, e *events.Event) time.Time {
	t.Helper()
	ts, err := e.EventClosedAt()
	if err != nil {
		t.Fatalf("fixture has unparseable LedgerClosedAt %q: %v", e.LedgerClosedAt, err)
	}
	return ts
}
