package soroswap

import (
	"math/big"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// ─── consumer.go ──────────────────────────────────────────────────

func TestTradeEvent_implementsConsumerEvent(t *testing.T) {
	te := TradeEvent{}
	if got := te.EventKind(); got != "soroswap.trade" {
		t.Errorf("EventKind() = %q, want \"soroswap.trade\"", got)
	}
	if got := te.Source(); got != SourceName {
		t.Errorf("Source() = %q, want %q", got, SourceName)
	}
	var _ consumer.Event = te
}

// ─── dispatcher_adapter.go ────────────────────────────────────────

func TestDecoder_Name(t *testing.T) {
	if got := NewDecoder().Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
}

func TestDecoder_Matches_pairAndFactoryTopics(t *testing.T) {
	d := NewDecoder()
	for _, tc := range []struct {
		name string
		ev   events.Event
		want bool
	}{
		{"pair swap", events.Event{Topic: []string{TopicPrefixPair, TopicSymbolSwap}}, true},
		{"pair sync", events.Event{Topic: []string{TopicPrefixPair, TopicSymbolSync}}, true},
		{"pair deposit", events.Event{Topic: []string{TopicPrefixPair, TopicSymbolDeposit}}, true},
		{"pair withdraw", events.Event{Topic: []string{TopicPrefixPair, TopicSymbolWithdraw}}, true},
		{"factory new_pair", events.Event{Topic: []string{TopicPrefixFactory, TopicSymbolNewPair}}, true},
		{"unrelated topic", events.Event{Topic: []string{TopicSymbolSwap, TopicPrefixPair}}, false},
		{"empty topic", events.Event{Topic: nil}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := d.Matches(tc.ev); got != tc.want {
				t.Errorf("Matches(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// makeNewPairEvent builds a factory new_pair event whose body
// encodes (token0, token1, pair) — matching the production path
// the registry seeder consumes.
func makeNewPairEvent(t *testing.T, token0, token1, pair string) events.Event {
	t.Helper()
	npL := xdr.Uint32(1)
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("new_pairs_length"), Val: xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &npL}},
		xdr.ScMapEntry{Key: symbol("pair"), Val: contractAddrFromStrkey(t, pair)},
		xdr.ScMapEntry{Key: symbol("token_0"), Val: contractAddrFromStrkey(t, token0)},
		xdr.ScMapEntry{Key: symbol("token_1"), Val: contractAddrFromStrkey(t, token1)},
	))
	return events.Event{
		Topic:          []string{TopicPrefixFactory, TopicSymbolNewPair},
		Value:          body,
		Ledger:         52_000_000,
		TxHash:         "factorytx0",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-23T12:00:00Z",
		ContractID:     pair,
	}
}

// makeSwapEvent builds a pair-contract swap event whose body
// carries a single direction (token0 → token1).
func makeSwapEvent(t *testing.T, pair string, in0, out1 *big.Int) events.Event {
	t.Helper()
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("amount_0_in"), Val: i128(in0)},
		xdr.ScMapEntry{Key: symbol("amount_0_out"), Val: i128(big.NewInt(0))},
		xdr.ScMapEntry{Key: symbol("amount_1_in"), Val: i128(big.NewInt(0))},
		xdr.ScMapEntry{Key: symbol("amount_1_out"), Val: i128(out1)},
		xdr.ScMapEntry{Key: symbol("to"), Val: contractAddrFromStrkey(t, makeContractStrkey(t, 0x99))},
	))
	return events.Event{
		Topic:          []string{TopicPrefixPair, TopicSymbolSwap},
		Value:          body,
		Ledger:         52_000_001,
		TxHash:         "swaptx0",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-23T12:00:01Z",
		ContractID:     pair,
	}
}

// makeSyncEvent builds the sync event paired with makeSwapEvent (same group key).
func makeSyncEvent(t *testing.T, pair string) events.Event {
	t.Helper()
	body := b64(t, scMap(
		xdr.ScMapEntry{Key: symbol("new_reserve_0"), Val: i128(big.NewInt(1_000_000))},
		xdr.ScMapEntry{Key: symbol("new_reserve_1"), Val: i128(big.NewInt(2_000_000))},
	))
	return events.Event{
		Topic:          []string{TopicPrefixPair, TopicSymbolSync},
		Value:          body,
		Ledger:         52_000_001,
		TxHash:         "swaptx0",
		OperationIndex: 0,
		LedgerClosedAt: "2026-04-23T12:00:01Z",
		ContractID:     pair,
	}
}

func TestDecoder_Decode_newPairSeedsRegistryButEmitsNothing(t *testing.T) {
	d := NewDecoder()
	token0 := makeContractStrkey(t, 0x10)
	token1 := makeContractStrkey(t, 0x11)
	pair := makeContractStrkey(t, 0x20)

	out, err := d.Decode(makeNewPairEvent(t, token0, token1, pair))
	if err != nil {
		t.Fatalf("Decode new_pair: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("new_pair should produce 0 trade events, got %d", len(out))
	}
	// Registry should have absorbed the pair.
	d.mu.RLock()
	tokens, ok := d.pairTokens[pair]
	d.mu.RUnlock()
	if !ok {
		t.Fatal("pair tokens not seeded after new_pair Decode")
	}
	if tokens.Token0.ContractID != token0 || tokens.Token1.ContractID != token1 {
		t.Errorf("tokens = %+v, want token0=%s token1=%s", tokens, token0, token1)
	}
}

func TestDecoder_Decode_swapSyncWithoutRegistryIncrementsSkipped(t *testing.T) {
	// No new_pair seeded for this pair — the swap+sync must be
	// dropped with skippedUnknownPair++.
	d := NewDecoder()
	pair := makeContractStrkey(t, 0x20)

	if _, err := d.Decode(makeSwapEvent(t, pair, big.NewInt(100), big.NewInt(200))); err != nil {
		t.Fatalf("Decode swap: %v", err)
	}
	if _, err := d.Decode(makeSyncEvent(t, pair)); err != nil {
		t.Fatalf("Decode sync: %v", err)
	}
	if got := d.SkippedUnknownPair(); got != 1 {
		t.Errorf("SkippedUnknownPair() = %d, want 1", got)
	}
}

func TestDecoder_Decode_swapSyncWithRegistryEmitsTradeEvent(t *testing.T) {
	token0 := makeContractStrkey(t, 0x10)
	token1 := makeContractStrkey(t, 0x11)
	pair := makeContractStrkey(t, 0x20)
	t0Asset, _ := canonical.NewSorobanAsset(token0)
	t1Asset, _ := canonical.NewSorobanAsset(token1)

	d := NewDecoder(WithSeededPairTokensDecoder(map[string]pairTokens{
		pair: {Token0: t0Asset, Token1: t1Asset},
	}))

	// Swap arrives first — buffer holds, no output.
	out, err := d.Decode(makeSwapEvent(t, pair, big.NewInt(100), big.NewInt(200)))
	if err != nil {
		t.Fatalf("Decode swap: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("got %d events on swap-only, want 0 (still buffering)", len(out))
	}

	// Sync completes the pair — exactly one TradeEvent.
	out, err = d.Decode(makeSyncEvent(t, pair))
	if err != nil {
		t.Fatalf("Decode sync: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d events after sync, want 1", len(out))
	}
	te, ok := out[0].(TradeEvent)
	if !ok {
		t.Fatalf("expected TradeEvent, got %T", out[0])
	}
	if te.Trade.Source != SourceName {
		t.Errorf("Trade.Source = %q, want %q", te.Trade.Source, SourceName)
	}
}

func TestDecoder_Decode_unrelatedTopicReturnsNilNil(t *testing.T) {
	d := NewDecoder()
	out, err := d.Decode(events.Event{
		Topic: []string{"random-topic-0", "random-topic-1"},
	})
	if err != nil {
		t.Fatalf("Decode unrelated: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("got %d events for unrelated topic, want 0", len(out))
	}
}

func TestDecoder_Decode_depositTopicIsNoop(t *testing.T) {
	d := NewDecoder()
	out, err := d.Decode(events.Event{
		Topic:          []string{TopicPrefixPair, TopicSymbolDeposit},
		Value:          "",
		LedgerClosedAt: "2026-04-23T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("Decode deposit: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("got %d events for deposit, want 0 (not a trade event)", len(out))
	}
}

func TestDecoder_Decode_malformedNewPairBodyReturnsError(t *testing.T) {
	d := NewDecoder()
	bad := events.Event{
		Topic: []string{TopicPrefixFactory, TopicSymbolNewPair},
		Value: "not-base64",
	}
	if _, err := d.Decode(bad); err == nil {
		t.Error("expected decode error on malformed new_pair body, got nil")
	}
}

func TestDecoder_SeedPair_addsPair(t *testing.T) {
	d := NewDecoder()
	t0, _ := canonical.NewSorobanAsset(makeContractStrkey(t, 0x10))
	t1, _ := canonical.NewSorobanAsset(makeContractStrkey(t, 0x11))
	pair := makeContractStrkey(t, 0x20)

	d.SeedPair(pair, t0, t1)
	d.mu.RLock()
	got := d.pairTokens[pair]
	d.mu.RUnlock()
	if got.Token0.ContractID != t0.ContractID {
		t.Errorf("Token0 = %s, want %s", got.Token0.ContractID, t0.ContractID)
	}
}

func TestDecoder_EvictedOrphans_initiallyZero(t *testing.T) {
	d := NewDecoder()
	if got := d.EvictedOrphans(); got != 0 {
		t.Errorf("EvictedOrphans() = %d on fresh Decoder, want 0", got)
	}
}
