package phoenix

import (
	"math/big"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
)

// mustClosed is the test-side equivalent of the processPage
// `e.EventClosedAt() + bail on error` guard. Test fixtures always
// have well-formed RFC 3339 timestamps, so parse failure here is a
// fixture bug.
func mustClosed(t *testing.T, e *stellarrpc.Event) time.Time {
	t.Helper()
	ts, err := e.EventClosedAt()
	if err != nil {
		t.Fatalf("fixture LedgerClosedAt %q: %v", e.LedgerClosedAt, err)
	}
	return ts
}

const (
	phoenixTxHash = "fadefadefadefadefadefadefadefadefadefadefadefadefadefadefadefade"
	testAddress   = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	xlmSAC        = "CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA"
	usdcContract  = "CAQCFVLOBK5GIULPNZRGATJJMIZL5BSP7X5YJVMGCVZLMIDLVJELAVIF"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name       string
		topics     []string
		wantField  string
		wantIsSwap bool
	}{
		{"swap sender", []string{TopicSymbolSwap, TopicSymbolSender}, TopicSymbolSender, true},
		{"swap buy_token", []string{TopicSymbolSwap, TopicSymbolBuyToken}, TopicSymbolBuyToken, true},
		{"not swap", []string{"something_else", TopicSymbolSender}, "", false},
		{"too few topics", []string{TopicSymbolSwap}, "", false},
		{"empty topics", []string{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			field, isSwap := classify(&stellarrpc.Event{Topic: tc.topics})
			if isSwap != tc.wantIsSwap {
				t.Errorf("isSwap = %v, want %v", isSwap, tc.wantIsSwap)
			}
			if field != tc.wantField {
				t.Errorf("field = %q, want %q", field, tc.wantField)
			}
		})
	}
}

func TestRawSwapCompleteCount(t *testing.T) {
	var r RawSwap
	if r.Complete() || r.fieldsPresent() != 0 {
		t.Fatal("zero value should be empty")
	}
	r.Sender = &stellarrpc.Event{}
	r.BuyToken = &stellarrpc.Event{}
	if r.Complete() {
		t.Fatal("2/8 should not be complete")
	}
	if r.fieldsPresent() != 2 {
		t.Errorf("fieldsPresent = %d, want 2", r.fieldsPresent())
	}
}

func TestBufferCollectsEightFieldsInOrder(t *testing.T) {
	buf := newBuffer()
	events := allEightSwapEvents()

	var completed *RawSwap
	for i, e := range events {
		fieldTopic := e.Topic[1]
		got, _, err := buf.absorb(e, fieldTopic, mustClosed(t, e))
		if err != nil {
			t.Fatalf("event %d absorb: %v", i, err)
		}
		if i < 7 && got != nil {
			t.Fatalf("got complete after %d/8 events", i+1)
		}
		if i == 7 {
			completed = got
		}
	}
	if completed == nil {
		t.Fatal("8th event should have completed the RawSwap")
	}
	if !completed.Complete() {
		t.Fatal("returned RawSwap reports itself incomplete")
	}
	if len(buf.m) != 0 {
		t.Errorf("buffer should be empty after completion, has %d", len(buf.m))
	}
}

func TestBufferHandlesOutOfOrderArrival(t *testing.T) {
	// Arrive in reverse of contract emission order (Q1: we don't
	// rely on order).
	buf := newBuffer()
	events := allEightSwapEvents()
	var completed *RawSwap
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		got, _, _ := buf.absorb(e, e.Topic[1], mustClosed(t, e))
		if i == 0 {
			completed = got
		}
	}
	if completed == nil {
		t.Fatal("out-of-order arrival should still complete on 8th event")
	}
	if !completed.Complete() {
		t.Fatal("completed RawSwap reports itself incomplete")
	}
}

func TestBufferSeparatesSwapsByGroupKey(t *testing.T) {
	// Two independent swaps in the same ledger but different
	// op_index — the 8-field correlation must not collide them.
	buf := newBuffer()
	evsA := allEightSwapEventsKeyed(100, "txA", 0)
	evsB := allEightSwapEventsKeyed(100, "txB", 0)

	// Interleave: one from A, one from B, repeat.
	var completedA, completedB *RawSwap
	for i := 0; i < 8; i++ {
		eA := evsA[i]
		got, _, _ := buf.absorb(eA, eA.Topic[1], mustClosed(t, eA))
		if i == 7 {
			completedA = got
		}
		eB := evsB[i]
		got, _, _ = buf.absorb(eB, eB.Topic[1], mustClosed(t, eB))
		if i == 7 {
			completedB = got
		}
	}
	if completedA == nil || completedB == nil {
		t.Fatalf("both swaps should complete: A=%v B=%v", completedA != nil, completedB != nil)
	}
	if completedA.TxHash != "txA" || completedB.TxHash != "txB" {
		t.Errorf("identity mixed: A.TxHash=%q B.TxHash=%q", completedA.TxHash, completedB.TxHash)
	}
}

func TestBufferOrphansReportIncompletes(t *testing.T) {
	buf := newBuffer()
	events := allEightSwapEvents()
	// Only absorb 5 of the 8 — the other 3 never arrive.
	for _, e := range events[:5] {
		_, _, _ = buf.absorb(e, e.Topic[1], mustClosed(t, e))
	}
	orphans := buf.orphans()
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0].Complete() {
		t.Fatal("orphan should not report Complete")
	}
	if orphans[0].fieldsPresent() != 5 {
		t.Errorf("orphan fieldsPresent = %d, want 5", orphans[0].fieldsPresent())
	}
}

func TestBufferRejectsUnknownField(t *testing.T) {
	buf := newBuffer()
	e := &stellarrpc.Event{
		Ledger: 1, TxHash: phoenixTxHash, OperationIndex: 0,
		LedgerClosedAt: time.Now().UTC().Format(time.RFC3339),
		Topic:          []string{TopicSymbolSwap, "nonexistent_field"},
	}
	_, _, err := buf.absorb(e, "nonexistent_field", mustClosed(t, e))
	if err == nil {
		t.Fatal("expected ErrUnknownField for nonsense topic")
	}
}

func TestBufferEvictsStaleOrphans(t *testing.T) {
	buf := newBuffer()
	buf.maxAge = 100 * time.Millisecond

	// Seed an old partial swap (only 1 field arrived — classic
	// orphan). Its ClosedAt is well past the cutoff.
	oldTS := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	events := allEightSwapEvents()
	old := events[0]
	old.LedgerClosedAt = oldTS

	if _, evicted, err := buf.absorb(old, old.Topic[1], mustClosed(t, old)); err != nil || len(evicted) != 0 {
		t.Fatalf("first insert: err=%v evicted=%d", err, len(evicted))
	}
	if buf.size() != 1 {
		t.Fatalf("buffer size = %d, want 1", buf.size())
	}

	// Fresh event from a different swap triggers sweepStale → evict.
	fresh := allEightSwapEventsKeyed(999, "txFresh", 0)[0]
	_, evicted, _ := buf.absorb(fresh, fresh.Topic[1], mustClosed(t, fresh))
	if len(evicted) != 1 {
		t.Fatalf("expected 1 eviction, got %d", len(evicted))
	}
	if buf.size() != 1 {
		t.Errorf("buffer size after eviction = %d, want 1 (fresh only)", buf.size())
	}
}

func TestDecodeSwap_happyPath(t *testing.T) {
	// Install fakes for the SCVal decoders so we can synthesise a
	// complete swap + decode it without the real XDR codec.
	prevAddr, prevAsset, prevI128 := decodeAddress, decodeAsset, decodeI128
	defer func() {
		decodeAddress, decodeAsset, decodeI128 = prevAddr, prevAsset, prevI128
	}()

	xlm := canonical.NativeAsset()
	usdc, err := canonical.NewClassicAsset("USDC", testAddress)
	if err != nil {
		t.Fatal(err)
	}

	decodeAddress = func(v string) (string, error) { return "GSENDER", nil }
	decodeAsset = func(v string) (canonical.Asset, error) {
		switch v {
		case "sell":
			return xlm, nil
		case "buy":
			return usdc, nil
		}
		return canonical.Asset{}, nil
	}
	decodeI128 = func(v string) (canonical.Amount, error) {
		switch v {
		case "offer":
			return canonical.NewAmount(big.NewInt(1_000_000_000)), nil // 100 XLM
		case "received":
			return canonical.NewAmount(big.NewInt(12_420_000)), nil // 12.42 USDC
		}
		return canonical.NewAmount(big.NewInt(0)), nil
	}

	now := time.Now().UTC().Truncate(time.Second)
	r := &RawSwap{
		Ledger: 52_430_001, TxHash: phoenixTxHash, OpIndex: 0,
		Pool: usdcContract, ClosedAt: now,
		Sender:         &stellarrpc.Event{Value: "sender"},
		SellToken:      &stellarrpc.Event{Value: "sell"},
		OfferAmount:    &stellarrpc.Event{Value: "offer"},
		ActualReceived: &stellarrpc.Event{Value: "received"},
		BuyToken:       &stellarrpc.Event{Value: "buy"},
		ReturnAmount:   &stellarrpc.Event{Value: "return"},
		SpreadAmount:   &stellarrpc.Event{Value: "spread"},
		ReferralFee:    &stellarrpc.Event{Value: "refferral"},
	}

	trade, err := decodeSwap(r)
	if err != nil {
		t.Fatalf("decodeSwap: %v", err)
	}
	if trade.Source != SourceName {
		t.Errorf("source = %q", trade.Source)
	}
	if !trade.Pair.Base.Equal(xlm) || !trade.Pair.Quote.Equal(usdc) {
		t.Errorf("pair direction wrong: %+v", trade.Pair)
	}
	if trade.BaseAmount.Cmp(canonical.NewAmount(big.NewInt(1_000_000_000))) != 0 {
		t.Errorf("base_amount = %s", trade.BaseAmount)
	}
	if trade.QuoteAmount.Cmp(canonical.NewAmount(big.NewInt(12_420_000))) != 0 {
		t.Errorf("quote_amount = %s", trade.QuoteAmount)
	}
	if trade.Taker != "GSENDER" {
		t.Errorf("taker = %q", trade.Taker)
	}
}

func TestDecodeSwap_incomplete(t *testing.T) {
	r := &RawSwap{Sender: &stellarrpc.Event{}}
	_, err := decodeSwap(r)
	if err == nil {
		t.Fatal("expected error for incomplete swap")
	}
}

func TestSource_Basics(t *testing.T) {
	s := New(nil, WithPollInterval(500*time.Millisecond))
	if s.Name() != SourceName {
		t.Errorf("Name() = %q", s.Name())
	}
	if h := s.Health(); h.Connected {
		t.Errorf("initial Connected should be false, got %+v", h)
	}
}

func TestBufferBackfillOldEventsComplete(t *testing.T) {
	// Regression: the planned backfill path replays ancient events;
	// without using the event's own ClosedAt as the eviction
	// reference, the first-absorbed field would evict immediately
	// when the 2nd through 8th arrived. Verify an 8-field set with
	// a 6-hour-old ClosedAt still completes in one buffer.
	buf := newBuffer()
	events := allEightSwapEventsAt(100, phoenixTxHash, 0, time.Now().UTC().Add(-6*time.Hour))

	var completed *RawSwap
	for i, e := range events {
		got, evicted, err := buf.absorb(e, e.Topic[1], mustClosed(t, e))
		if err != nil {
			t.Fatalf("step %d absorb: %v", i, err)
		}
		if len(evicted) != 0 {
			t.Errorf("step %d: evicted %d backfilled fields during correlation", i, len(evicted))
		}
		if got != nil {
			completed = got
		}
	}
	if completed == nil {
		t.Fatal("backfilled 8-field set failed to complete")
	}
}

// ─── helpers ──────────────────────────────────────────────────────

// allEightSwapEvents returns 8 synthetic events with stable
// (ledger, tx, op) = (100, phoenixTxHash, 0). Order: sender,
// sell_token, offer_amount, actual_received, buy_token,
// return_amount, spread, referral.
func allEightSwapEvents() []*stellarrpc.Event {
	return allEightSwapEventsKeyed(100, phoenixTxHash, 0)
}

func allEightSwapEventsKeyed(ledger uint32, tx string, op int) []*stellarrpc.Event {
	return allEightSwapEventsAt(ledger, tx, op, time.Now().UTC())
}

func allEightSwapEventsAt(ledger uint32, tx string, op int, ts time.Time) []*stellarrpc.Event {
	closedAt := ts.Format(time.RFC3339)
	field := func(topic1 string) *stellarrpc.Event {
		return &stellarrpc.Event{
			Ledger: ledger, TxHash: tx, OperationIndex: op,
			LedgerClosedAt: closedAt,
			ContractID:     usdcContract,
			Topic:          []string{TopicSymbolSwap, topic1},
			Value:          "stub",
		}
	}
	return []*stellarrpc.Event{
		field(TopicSymbolSender),
		field(TopicSymbolSellToken),
		field(TopicSymbolOfferAmount),
		field(TopicSymbolActualReceived),
		field(TopicSymbolBuyToken),
		field(TopicSymbolReturnAmount),
		field(TopicSymbolSpreadAmount),
		field(TopicSymbolReferralFee),
	}
}
