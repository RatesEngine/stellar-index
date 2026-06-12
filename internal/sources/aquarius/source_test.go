package aquarius

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
		{"trade", []string{TopicSymbolTrade}, EventTrade},
		{"deposit_liquidity", []string{TopicSymbolDepositLiquidity}, EventDepositLiquidity},
		{"withdraw_liquidity", []string{TopicSymbolWithdrawLiquidity}, EventWithdrawLiquidity},
		{"update_reserves", []string{TopicSymbolUpdateReserves}, EventUpdateReserves},
		{"reserves_sync", []string{TopicSymbolReservesSync}, EventReservesSync},
		{"set_protocol_fee", []string{TopicSymbolSetProtocolFee}, EventSetProtocolFee},
		{"claim_protocol_fee", []string{TopicSymbolClaimProtocolFee}, EventClaimProtocolFee},
		{"kill_deposit", []string{TopicSymbolKillDeposit}, EventKillDeposit},
		{"unkill_deposit", []string{TopicSymbolUnkillDeposit}, EventUnkillDeposit},
		{"kill_swap", []string{TopicSymbolKillSwap}, EventKillSwap},
		{"unkill_swap", []string{TopicSymbolUnkillSwap}, EventUnkillSwap},
		{"kill_claim", []string{TopicSymbolKillClaim}, EventKillClaim},
		{"unkill_claim", []string{TopicSymbolUnkillClaim}, EventUnkillClaim},
		{"kill_gauges_claim", []string{TopicSymbolKillGaugesClaim}, EventKillGaugesClaim},
		{"unkill_gauges_claim", []string{TopicSymbolUnkillGaugesClaim}, EventUnkillGaugesClaim},
		{"unknown", []string{"AAAAsomething-else"}, ""},
		{"empty", nil, ""},
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

// TestClassify_completenessVsUpstream is a forcing function: if a
// future agent adds an Event* constant without also wiring its
// TopicSymbol* into classify(), this test fails. It enumerates every
// exported Event* string and asserts classify() recognises the
// matching TopicSymbol*. The test fixture also acts as documentation
// of the closed set of topics aquarius emits (verified against
// aquarius-amm/liquidity_pool_events/src/lib.rs).
func TestClassify_completenessVsUpstream(t *testing.T) {
	pairs := []struct {
		name   string
		event  string
		symbol string
	}{
		{"trade", EventTrade, TopicSymbolTrade},
		{"deposit_liquidity", EventDepositLiquidity, TopicSymbolDepositLiquidity},
		{"withdraw_liquidity", EventWithdrawLiquidity, TopicSymbolWithdrawLiquidity},
		{"update_reserves", EventUpdateReserves, TopicSymbolUpdateReserves},
		{"reserves_sync", EventReservesSync, TopicSymbolReservesSync},
		{"set_protocol_fee", EventSetProtocolFee, TopicSymbolSetProtocolFee},
		{"claim_protocol_fee", EventClaimProtocolFee, TopicSymbolClaimProtocolFee},
		{"kill_deposit", EventKillDeposit, TopicSymbolKillDeposit},
		{"unkill_deposit", EventUnkillDeposit, TopicSymbolUnkillDeposit},
		{"kill_swap", EventKillSwap, TopicSymbolKillSwap},
		{"unkill_swap", EventUnkillSwap, TopicSymbolUnkillSwap},
		{"kill_claim", EventKillClaim, TopicSymbolKillClaim},
		{"unkill_claim", EventUnkillClaim, TopicSymbolUnkillClaim},
		{"kill_gauges_claim", EventKillGaugesClaim, TopicSymbolKillGaugesClaim},
		{"unkill_gauges_claim", EventUnkillGaugesClaim, TopicSymbolUnkillGaugesClaim},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			got := classify(&events.Event{Topic: []string{p.symbol}})
			if got != p.event {
				t.Fatalf("classify(symbol for %s) = %q, want %q — extend classify() switch", p.name, got, p.event)
			}
		})
	}
}

func TestPoolTypeString(t *testing.T) {
	cases := map[PoolType]string{
		PoolVolatile:     "volatile",
		PoolStableswap:   "stableswap",
		PoolConcentrated: "concentrated",
		PoolUnknown:      "unknown",
		PoolType(99):     "unknown",
	}
	for p, want := range cases {
		if got := p.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", p, got, want)
		}
	}
}

// TestDecodeTrade_withFakeDecoders uses the package-level hook vars
// to substitute decoders for the topic + body SCVals. Exercises the
// full direction-assignment path without the real XDR codec —
// real_fixture_test.go covers that path against mainnet captures.
func TestDecodeTrade_withFakeDecoders(t *testing.T) {
	prevAmt, prevAsset, prevAddr := decodeTradeAmounts, decodeAssetTopic, decodeAddressTopic
	defer func() {
		decodeTradeAmounts, decodeAssetTopic, decodeAddressTopic = prevAmt, prevAsset, prevAddr
	}()

	usdc, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	xlm := canonical.NativeAsset()

	decodeAssetTopic = func(slot string) (canonical.Asset, error) {
		switch slot {
		case "token_in_slot":
			return xlm, nil
		case "token_out_slot":
			return usdc, nil
		}
		t.Fatalf("unexpected topic slot: %q", slot)
		return canonical.Asset{}, nil
	}
	decodeAddressTopic = func(slot string) (string, error) {
		if slot == "user_slot" {
			return "GTAKER", nil
		}
		return "", nil
	}
	decodeTradeAmounts = func(_ string) (tradeAmounts, error) {
		return tradeAmounts{
			SoldAmount:   canonical.NewAmount(big.NewInt(1_000_000_000)),
			BoughtAmount: canonical.NewAmount(big.NewInt(12_420_000)),
			Fee:          canonical.NewAmount(big.NewInt(0)),
		}, nil
	}

	e := &events.Event{
		Topic:          []string{TopicSymbolTrade, "token_in_slot", "token_out_slot", "user_slot"},
		Value:          "stub",
		Ledger:         100,
		TxHash:         "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe",
		OperationIndex: 7,
		EventIndex:     3,
		LedgerClosedAt: time.Now().UTC().Format(time.RFC3339),
	}
	closedAt, _ := time.Parse(time.RFC3339, e.LedgerClosedAt)
	tr, err := decodeTrade(e, closedAt)
	if err != nil {
		t.Fatalf("decodeTrade: %v", err)
	}
	if !tr.Pair.Base.Equal(xlm) || !tr.Pair.Quote.Equal(usdc) {
		t.Errorf("wrong pair direction: %+v", tr.Pair)
	}
	if tr.BaseAmount.Cmp(canonical.NewAmount(big.NewInt(1_000_000_000))) != 0 {
		t.Errorf("base = %s", tr.BaseAmount)
	}
	if tr.QuoteAmount.Cmp(canonical.NewAmount(big.NewInt(12_420_000))) != 0 {
		t.Errorf("quote = %s", tr.QuoteAmount)
	}
	if tr.Taker != "GTAKER" {
		t.Errorf("taker = %q", tr.Taker)
	}
	if want := canonical.FanoutOpIndex(7, 3); tr.OpIndex != want {
		t.Errorf("op_index = %d, want %d (op 7 fanned out by event index 3)", tr.OpIndex, want)
	}
	if tr.Source != SourceName {
		t.Errorf("source = %q", tr.Source)
	}
}

func TestDecodeTrade_wrongTopicArity(t *testing.T) {
	// Only 3 topics — missing user slot. Surface ErrMalformedPayload.
	e := &events.Event{Topic: []string{TopicSymbolTrade, "t_in", "t_out"}}
	_, err := decodeTrade(e, time.Now())
	if err == nil {
		t.Fatal("expected error on 3-topic event")
	}
}

func TestDecodeTrade_nonPositiveAmount(t *testing.T) {
	prevAmt, prevAsset, prevAddr := decodeTradeAmounts, decodeAssetTopic, decodeAddressTopic
	defer func() {
		decodeTradeAmounts, decodeAssetTopic, decodeAddressTopic = prevAmt, prevAsset, prevAddr
	}()

	usdc, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	decodeAssetTopic = func(_ string) (canonical.Asset, error) { return usdc, nil }
	decodeAddressTopic = func(_ string) (string, error) { return "", nil }
	decodeTradeAmounts = func(_ string) (tradeAmounts, error) {
		return tradeAmounts{
			SoldAmount:   canonical.NewAmount(big.NewInt(0)),
			BoughtAmount: canonical.NewAmount(big.NewInt(42)),
			Fee:          canonical.NewAmount(big.NewInt(0)),
		}, nil
	}

	e := &events.Event{
		Topic: []string{TopicSymbolTrade, "a", "b", "c"},
	}
	_, err := decodeTrade(e, time.Now())
	if err == nil {
		t.Fatal("expected error on zero sold_amount")
	}
}

func TestDecoder_NameMatchesSourceName(t *testing.T) {
	if got := NewDecoder().Name(); got != SourceName {
		t.Errorf("Name() = %q, want %q", got, SourceName)
	}
}
