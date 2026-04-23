package aquarius

import (
	"errors"
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
		{TopicSymbolTrade, EventTrade},
		{TopicSymbolDepositLiquidity, EventDepositLiquidity},
		{TopicSymbolWithdrawLiquidity, EventWithdrawLiquidity},
		{TopicSymbolUpdateReserves, EventUpdateReserves},
		{TopicSymbolReservesSync, EventReservesSync},
		{"something-else", ""},
	}
	for _, tc := range cases {
		e := &stellarrpc.Event{Topic: []string{tc.topic0}}
		if got := classify(e); got != tc.want {
			t.Errorf("classify(%q) = %q, want %q", tc.topic0, got, tc.want)
		}
	}
	// Empty topic.
	if got := classify(&stellarrpc.Event{}); got != "" {
		t.Errorf("empty topic: got %q", got)
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

func TestDecodeTrade_twoAssetPool(t *testing.T) {
	// Volatile pool: XLM/USDC. User sold XLM for USDC.
	usdc, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	pool := PoolInfo{
		Type:   PoolVolatile,
		Tokens: []canonical.Asset{canonical.NativeAsset(), usdc},
	}

	prev := decodeTradeAmounts
	defer func() { decodeTradeAmounts = prev }()
	decodeTradeAmounts = func(_ string) ([]canonical.Amount, []canonical.Amount, string, error) {
		return []canonical.Amount{
				canonical.NewAmount(big.NewInt(1_000_000_000)), // 100 XLM in
				canonical.NewAmount(big.NewInt(0)),
			}, []canonical.Amount{
				canonical.NewAmount(big.NewInt(0)),
				canonical.NewAmount(big.NewInt(12_420_000)), // 12.42 USDC out
			}, "GTAKER", nil
	}

	e := &stellarrpc.Event{
		Ledger: 100, TxHash: "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe",
		OperationIndex: 0, LedgerClosedAt: time.Now().UTC().Format(time.RFC3339),
		ContractID: "CPOOL1",
	}
	closedAt, _ := time.Parse(time.RFC3339, e.LedgerClosedAt)

	trades, err := decodeTrade(e, pool, closedAt)
	if err != nil {
		t.Fatalf("decodeTrade: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	tr := trades[0]
	if !tr.Pair.Base.Equal(canonical.NativeAsset()) || !tr.Pair.Quote.Equal(usdc) {
		t.Errorf("wrong direction: %+v", tr.Pair)
	}
	if tr.BaseAmount.Cmp(canonical.NewAmount(big.NewInt(1_000_000_000))) != 0 {
		t.Errorf("base = %s", tr.BaseAmount)
	}
	if tr.Taker != "GTAKER" {
		t.Errorf("taker = %q", tr.Taker)
	}
	if tr.Source != SourceName {
		t.Errorf("source = %q", tr.Source)
	}
}

func TestDecodeTrade_stableswapFourAssetSingleInOut(t *testing.T) {
	// Stableswap pool: [USDC, USDT, BUSD, DAI]. User swaps USDC for USDT.
	usdc, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	usdt, _ := canonical.NewClassicAsset("USDT", "GB5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN") // fake issuer for fixture
	busd, _ := canonical.NewClassicAsset("BUSD", "GC5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	dai, _ := canonical.NewClassicAsset("DAI", "GD5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	pool := PoolInfo{
		Type:   PoolStableswap,
		Tokens: []canonical.Asset{usdc, usdt, busd, dai},
	}

	prev := decodeTradeAmounts
	defer func() { decodeTradeAmounts = prev }()
	decodeTradeAmounts = func(_ string) ([]canonical.Amount, []canonical.Amount, string, error) {
		return []canonical.Amount{
				canonical.NewAmount(big.NewInt(100)),
				canonical.NewAmount(big.NewInt(0)),
				canonical.NewAmount(big.NewInt(0)),
				canonical.NewAmount(big.NewInt(0)),
			}, []canonical.Amount{
				canonical.NewAmount(big.NewInt(0)),
				canonical.NewAmount(big.NewInt(99)),
				canonical.NewAmount(big.NewInt(0)),
				canonical.NewAmount(big.NewInt(0)),
			}, "", nil
	}

	e := &stellarrpc.Event{Ledger: 1, TxHash: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}
	trades, err := decodeTrade(e, pool, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	tr := trades[0]
	if !tr.Pair.Base.Equal(usdc) || !tr.Pair.Quote.Equal(usdt) {
		t.Errorf("wrong pair: %+v", tr.Pair)
	}
}

func TestDecodeTrade_multiTradeFanoutUniqueOpIndex(t *testing.T) {
	// Pathological stableswap event: user put USDC+USDT in, got
	// BUSD+DAI out (atomic multi-asset swap). The decoder emits 4
	// (in_i, out_j) records. Without a fanout scheme they'd all
	// share op_index — and InsertTrade's ON CONFLICT would silently
	// drop all but the first.
	usdc, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	usdt, _ := canonical.NewClassicAsset("USDT", "GB5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	busd, _ := canonical.NewClassicAsset("BUSD", "GC5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	dai, _ := canonical.NewClassicAsset("DAI", "GD5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	pool := PoolInfo{
		Type:   PoolStableswap,
		Tokens: []canonical.Asset{usdc, usdt, busd, dai},
	}

	prev := decodeTradeAmounts
	defer func() { decodeTradeAmounts = prev }()
	decodeTradeAmounts = func(_ string) ([]canonical.Amount, []canonical.Amount, string, error) {
		return []canonical.Amount{
				canonical.NewAmount(big.NewInt(100)),
				canonical.NewAmount(big.NewInt(100)),
				canonical.NewAmount(big.NewInt(0)),
				canonical.NewAmount(big.NewInt(0)),
			}, []canonical.Amount{
				canonical.NewAmount(big.NewInt(0)),
				canonical.NewAmount(big.NewInt(0)),
				canonical.NewAmount(big.NewInt(100)),
				canonical.NewAmount(big.NewInt(100)),
			}, "", nil
	}

	e := &stellarrpc.Event{
		Ledger: 42, OperationIndex: 3,
		TxHash: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	}
	trades, err := decodeTrade(e, pool, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// 2 non-zero in × 2 non-zero out = 4 trades.
	if len(trades) != 4 {
		t.Fatalf("got %d trades, want 4 (2×2 fanout)", len(trades))
	}

	// Every op_index must be unique and distinct from e.OperationIndex
	// alone (proves the synthetic fanout is applied).
	seen := map[uint32]bool{}
	for i, tr := range trades {
		if seen[tr.OpIndex] {
			t.Errorf("trade %d has duplicate OpIndex %d", i, tr.OpIndex)
		}
		seen[tr.OpIndex] = true
	}
	if len(seen) != 4 {
		t.Errorf("op_index uniqueness violated: %d unique in %d trades", len(seen), len(trades))
	}

	// Sanity: trade.ID() (source:ledger:tx:opindex) is what the
	// primary key collides on — every ID must be distinct.
	ids := map[string]bool{}
	for _, tr := range trades {
		ids[tr.ID()] = true
	}
	if len(ids) != 4 {
		t.Errorf("trade ID uniqueness violated: %d unique in %d trades", len(ids), len(trades))
	}
}

func TestDecodeTrade_fanoutDoesNotCollideAcrossOperations(t *testing.T) {
	// Regression: the previous fanout used subIdx = i×stride+j AND
	// outer op×stride with the same stride, so op=0 i=1 j=0 (OpIndex
	// 256) collided with op=1 i=0 j=0 (OpIndex 256). Two operations
	// on the same tx would write rows that shared the trades PK —
	// InsertTrade's ON CONFLICT DO NOTHING silently dropped the
	// second.
	usdc, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	usdt, _ := canonical.NewClassicAsset("USDT", "GB5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	pool := PoolInfo{
		Type:   PoolStableswap,
		Tokens: []canonical.Asset{usdc, usdt},
	}

	prev := decodeTradeAmounts
	defer func() { decodeTradeAmounts = prev }()

	// Two different operations in the same tx, each doing a 2-asset
	// swap but with i=1,j=0 (op A) vs i=0,j=1 (op B). Flat-counter
	// fanout gives each a distinct n=0, but different op multiplies
	// to distinct base → unique OpIndex overall.
	decodeTradeAmounts = func(_ string) ([]canonical.Amount, []canonical.Amount, string, error) {
		return []canonical.Amount{
				canonical.NewAmount(big.NewInt(0)),
				canonical.NewAmount(big.NewInt(100)),
			}, []canonical.Amount{
				canonical.NewAmount(big.NewInt(100)),
				canonical.NewAmount(big.NewInt(0)),
			}, "", nil
	}
	txHash := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	eOp0 := &stellarrpc.Event{Ledger: 42, OperationIndex: 0, TxHash: txHash}
	eOp1 := &stellarrpc.Event{Ledger: 42, OperationIndex: 1, TxHash: txHash}

	t0, err := decodeTrade(eOp0, pool, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t1, err := decodeTrade(eOp1, pool, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	// Each event emits exactly one trade (i=1→j=0 only).
	if len(t0) != 1 || len(t1) != 1 {
		t.Fatalf("expected 1 trade per event, got %d + %d", len(t0), len(t1))
	}

	if t0[0].OpIndex == t1[0].OpIndex {
		t.Errorf("cross-operation OpIndex collision: both %d (old i×stride+j fanout regressed?)",
			t0[0].OpIndex)
	}
	if t0[0].ID() == t1[0].ID() {
		t.Errorf("cross-operation trade ID collision: %q", t0[0].ID())
	}
}

func TestDecodeTrade_concentratedRefused(t *testing.T) {
	pool := PoolInfo{Type: PoolConcentrated, Tokens: []canonical.Asset{canonical.NativeAsset(), canonical.NativeAsset()}}
	_, err := decodeTrade(&stellarrpc.Event{}, pool, time.Now())
	if !errors.Is(err, ErrConcentratedWIP) {
		t.Errorf("expected ErrConcentratedWIP, got %v", err)
	}
}

func TestDecodeTrade_arityMismatch(t *testing.T) {
	pool := PoolInfo{
		Type:   PoolVolatile,
		Tokens: []canonical.Asset{canonical.NativeAsset(), canonical.NativeAsset()},
	}

	prev := decodeTradeAmounts
	defer func() { decodeTradeAmounts = prev }()
	decodeTradeAmounts = func(_ string) ([]canonical.Amount, []canonical.Amount, string, error) {
		// Return mismatched arity — 3 amounts for a 2-asset pool.
		return []canonical.Amount{
				canonical.NewAmount(big.NewInt(1)),
				canonical.NewAmount(big.NewInt(2)),
				canonical.NewAmount(big.NewInt(3)),
			}, []canonical.Amount{
				canonical.NewAmount(big.NewInt(0)),
				canonical.NewAmount(big.NewInt(0)),
				canonical.NewAmount(big.NewInt(0)),
			}, "", nil
	}

	_, err := decodeTrade(&stellarrpc.Event{}, pool, time.Now())
	if !errors.Is(err, ErrMalformedPayload) {
		t.Errorf("expected ErrMalformedPayload for arity mismatch, got %v", err)
	}
}

func TestSource_SeedAndLookupPool(t *testing.T) {
	s := New(nil)
	pool := "CPOOL1"
	info := PoolInfo{Type: PoolVolatile, Tokens: []canonical.Asset{canonical.NativeAsset(), canonical.NativeAsset()}}
	s.SeedPool(pool, info)
	got, ok := s.lookupPool(pool)
	if !ok {
		t.Fatal("pool not found after Seed")
	}
	if got.Type != PoolVolatile {
		t.Errorf("wrong type: %v", got.Type)
	}
}

func TestSource_NameAndHealth(t *testing.T) {
	s := New(nil)
	if s.Name() != SourceName {
		t.Errorf("Name() = %q", s.Name())
	}
	if h := s.Health(); h.Connected || !h.LastEvent.IsZero() || h.LastError != nil {
		t.Errorf("initial health: %+v", h)
	}
}
